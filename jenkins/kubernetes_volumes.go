package jenkins

import (
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/objectvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const volumeClassPrefix = "org.csanchez.jenkins.plugins.kubernetes.volumes."

type podVolumeKind struct {
	name, class string
	fields      []kubernetesFormField
}

func requiredVolumeField(name, field, description string) kubernetesFormField {
	return kubernetesFormField{name, field, schema.StringAttribute{
		Required: true, MarkdownDescription: description,
		Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
	}}
}

var podVolumeKinds = func() []podVolumeKind {
	readOnly := kubernetesFlag("read_only", "readOnly", "Mount read-only.", false)
	optional := kubernetesFlag("optional", "optional", "Allow the referenced object to be absent.", false)
	pvcFields := []kubernetesFormField{
		kubernetesField("storage_class_name", "storageClassName", "StorageClass for the new claim."),
		kubernetesField("requests_size", "requestsSize", "Requested storage quantity."),
		{"access_modes", "accessModes", schema.StringAttribute{
			Optional: true, Computed: true, Default: stringdefault.StaticString("ReadWriteOnce"),
			MarkdownDescription: "Claim access mode.",
			Validators:          []validator.String{stringvalidator.OneOf("ReadWriteOnce", "ReadOnlyMany", "ReadWriteMany")},
		}},
	}
	return []podVolumeKind{
		{"empty_dir", "EmptyDirVolume", []kubernetesFormField{
			kubernetesFlag("memory", "memory", "Back the empty directory with memory.", false),
		}},
		{"config_map", "ConfigMapVolume", []kubernetesFormField{
			requiredVolumeField("name", "configMapName", "ConfigMap name."),
			kubernetesField("sub_path", "subPath", "Optional mount subpath."),
			optional,
		}},
		{"secret", "SecretVolume", []kubernetesFormField{
			requiredVolumeField("name", "secretName", "Secret name."),
			kubernetesField("default_mode", "defaultMode", "Default file mode, expressed as a decimal integer."),
			optional,
		}},
		{"persistent_volume_claim", "PersistentVolumeClaim", []kubernetesFormField{
			requiredVolumeField("claim_name", "claimName", "Existing claim name."), readOnly,
		}},
		{"host_path", "HostPathVolume", []kubernetesFormField{
			requiredVolumeField("path", "hostPath", "Path on the host."), readOnly,
		}},
		{"nfs", "NfsVolume", []kubernetesFormField{
			requiredVolumeField("server_address", "serverAddress", "NFS server address."),
			requiredVolumeField("server_path", "serverPath", "Exported path on the NFS server."),
			readOnly,
		}},
		{"generic_ephemeral", "GenericEphemeralVolume", pvcFields},
		{"dynamic_pvc", "DynamicPVCVolume", pvcFields},
	}
}()

func podVolumesSchema() schema.MapNestedAttribute {
	alternatives := make([]path.Expression, 0, len(podVolumeKinds))
	for _, kind := range podVolumeKinds {
		alternatives = append(alternatives, path.MatchRelative().AtParent().AtName(kind.name))
	}
	kinds := map[string]schema.Attribute{}
	for _, kind := range podVolumeKinds {
		fields := map[string]schema.Attribute{}
		for _, field := range kind.fields {
			fields[field.tfName] = field.attribute
		}
		kinds[kind.name] = schema.SingleNestedAttribute{
			Optional: true, Attributes: fields, Validators: []validator.Object{objectvalidator.ExactlyOneOf(alternatives...)},
			MarkdownDescription: fmt.Sprintf("Jenkins %s volume.", kind.class),
		}
	}
	volumes := schema.MapNestedAttribute{
		Optional: true, Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: kinds},
		Validators:          []validator.Map{mapvalidator.KeysAre(stringvalidator.LengthAtLeast(1))},
		MarkdownDescription: "Agent Pod volumes keyed by mount path. Choose one source per mount. Use generic_ephemeral for new dynamically provisioned claims.",
	}
	volumes.Default = mapdefault.StaticValue(types.MapValueMust(volumes.GetType().(types.MapType).ElemType, map[string]attr.Value{}))
	return volumes
}

func podVolumeForm(volumes types.Map) []map[string]any {
	entries := volumes.Elements()
	rows := make([]map[string]any, 0, len(entries))
	for _, mount := range slices.Sorted(maps.Keys(entries)) {
		sources := entries[mount].(types.Object).Attributes()
		for _, kind := range podVolumeKinds {
			source := sources[kind.name].(types.Object)
			if source.IsNull() {
				continue
			}
			class := volumeClassPrefix + kind.class
			row := map[string]any{"mountPath": mount, "stapler-class": class, "$class": class}
			for _, field := range kind.fields {
				switch v := source.Attributes()[field.tfName].(type) {
				case types.String:
					row[field.formName] = v.ValueString()
				case types.Bool:
					row[field.formName] = v.ValueBool()
				}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func podVolumesState(rows []map[string]string) (types.Map, diag.Diagnostics) {
	element := podVolumesSchema().GetType().(types.MapType).ElemType.(types.ObjectType)
	entries := map[string]attr.Value{}
	var diags diag.Diagnostics
	for _, row := range rows {
		sources := map[string]attr.Value{}
		found := false
		for _, kind := range podVolumeKinds {
			attrs := element.AttrTypes[kind.name].(types.ObjectType).AttrTypes
			sources[kind.name] = types.ObjectNull(attrs)
			if row["$class"] != volumeClassPrefix+kind.class {
				continue
			}
			found = true
			values := map[string]attr.Value{}
			for _, field := range kind.fields {
				value, exists := row[field.formName]
				if !exists {
					diags.AddError("Missing Jenkins volume field", fmt.Sprintf("%s has no %s field.", kind.class, field.formName))
					continue
				}
				if _, ok := field.attribute.(schema.BoolAttribute); ok {
					values[field.tfName] = types.BoolValue(value == "true")
				} else {
					values[field.tfName] = types.StringValue(value)
				}
			}
			source, d := types.ObjectValue(attrs, values)
			diags.Append(d...)
			sources[kind.name] = source
		}
		mount := row["mountPath"]
		if !found || mount == "" || entries[mount] != nil {
			diags.AddError("Invalid Jenkins Pod Template volume", fmt.Sprintf("Unsupported source or missing/duplicate mount path for volume %q.", mount))
			continue
		}
		obj, d := types.ObjectValue(element.AttrTypes, sources)
		diags.Append(d...)
		entries[mount] = obj
	}
	result, d := types.MapValue(element, entries)
	diags.Append(d...)
	return result, diags
}
