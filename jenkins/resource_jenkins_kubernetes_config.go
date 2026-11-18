package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var kubernetesID = regexp.MustCompile("^[a-zA-Z0-9_-]+$")

type kubernetesFormField struct {
	tfName, formName string
	attribute        schema.Attribute
}

func kubernetesField(tfName, formName, description string) kubernetesFormField {
	return kubernetesFormField{tfName, formName, schema.StringAttribute{
		Optional: true, Computed: true, Default: stringdefault.StaticString(""),
		MarkdownDescription: description,
	}}
}

func kubernetesFlag(tfName, formName, description string, defaultValue bool) kubernetesFormField {
	return kubernetesFormField{tfName, formName, schema.BoolAttribute{
		Optional: true, Computed: true, Default: booldefault.StaticBool(defaultValue),
		MarkdownDescription: description,
	}}
}

func kubernetesName(description string) schema.StringAttribute {
	return schema.StringAttribute{
		Required: true, MarkdownDescription: description,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:    []validator.String{stringvalidator.RegexMatches(kubernetesID, "use letters, numbers, underscores, and hyphens")},
	}
}

type kubernetesConfigResource struct {
	*resourceHelper
	template bool
	fields   []kubernetesFormField
}

func newKubernetesCloudResource() resource.Resource {
	return &kubernetesConfigResource{resourceHelper: newResourceHelper(), fields: []kubernetesFormField{
		kubernetesField("server_url", "serverUrl", "Kubernetes API server URL. Omit for in-cluster discovery."),
		kubernetesField("server_certificate", "serverCertificate", "PEM certificate for the Kubernetes API server."),
		kubernetesField("namespace", "namespace", "Default Kubernetes namespace for agent Pods."),
		kubernetesField("credentials_id", "credentialsId", "Jenkins credential ID used to connect to Kubernetes."),
		kubernetesField("jenkins_url", "jenkinsUrl", "URL agents use to connect to the controller."),
		kubernetesField("agent_registry", "jnlpregistry", "Registry prefix for the default Jenkins agent image."),
		kubernetesField("container_cap", "containerCapStr", "Maximum agent count as a positive integer string. Omit for the plugin default."),
		kubernetesFlag("usage_restricted", "usageRestricted", "Grant Cloud usage only to authorized Folders. Defaults to true.", true),
		kubernetesFlag("websocket", "webSocket", "Connect agents to Jenkins over WebSocket.", false),
	}}
}

func newKubernetesPodTemplateResource() resource.Resource {
	return &kubernetesConfigResource{resourceHelper: newResourceHelper(), template: true, fields: []kubernetesFormField{
		kubernetesField("namespace", "namespace", "Kubernetes namespace for the agent Pod. Omit to use the Cloud namespace."),
		kubernetesField("label", "label", "Jenkins agent label."),
		kubernetesField("inherit_from", "inheritFrom", "Space-separated names of templates to inherit."),
		kubernetesField("service_account", "serviceAccount", "Kubernetes service account for the Pod."),
		kubernetesField("node_selector", "nodeSelector", "Comma-separated Kubernetes node selectors in key=value form."),
		kubernetesField("yaml", "yaml", "Pod YAML for additional Pod settings. Keep credentials in Jenkins or Kubernetes Secrets."),
		kubernetesFlag("show_raw_yaml", "showRawYaml", "Print the generated Pod manifest in build logs.", false),
	}}
}

func (r *kubernetesConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cloud"
	if r.template {
		resp.TypeName = req.ProviderTypeName + "_kubernetes_pod_template"
	}
}

func (r *kubernetesConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, MarkdownDescription: "Jenkins configuration object identifier.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name": kubernetesName("Jenkins configuration object name."),
	}
	description := "Manages a Jenkins Kubernetes Cloud through the Jenkins Cloud configuration endpoints. Requires Overall/Administer and the Kubernetes plugin. Cloud settings outside the resource schema use Jenkins defaults on apply."
	if r.template {
		attributes["cloud"] = kubernetesName("Name of the Kubernetes Cloud that owns the Pod Template.")
		description = "Manages a Pod Template in a Jenkins Kubernetes Cloud. The resource owns the Pod Template configuration. Supply additional Pod settings through yaml."
	}
	for _, field := range r.fields {
		attributes[field.tfName] = field.attribute
	}
	resp.Schema = schema.Schema{MarkdownDescription: description, Attributes: attributes}
}

func (r *kubernetesConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	resp.State.Raw = req.Plan.Raw
	r.save(ctx, req.Plan, &resp.State, &resp.Diagnostics, true)
}

func (r *kubernetesConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.State.Raw = req.Plan.Raw
	r.save(ctx, req.Plan, &resp.State, &resp.Diagnostics, false)
}

func (r *kubernetesConfigResource) save(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics, create bool) {
	var name, cloud types.String
	diags.Append(plan.GetAttribute(ctx, path.Root("name"), &name)...)
	if r.template {
		diags.Append(plan.GetAttribute(ctx, path.Root("cloud"), &cloud)...)
	}
	values := map[string]any{"name": name.ValueString()}
	for _, field := range r.fields {
		if _, isBool := field.attribute.(schema.BoolAttribute); isBool {
			var value types.Bool
			diags.Append(plan.GetAttribute(ctx, path.Root(field.tfName), &value)...)
			values[field.formName] = value.ValueBool()
		} else {
			var value types.String
			diags.Append(plan.GetAttribute(ctx, path.Root(field.tfName), &value)...)
			values[field.formName] = value.ValueString()
		}
	}
	if diags.HasError() {
		return
	}

	id := name.ValueString()
	var query map[string]string
	createPath := "/cloud/doCreate"
	if r.template {
		id = cloud.ValueString() + "/" + id
		createPath = "/cloud/" + cloud.ValueString() + "/create"
		values["id"] = name.ValueString()
	} else {
		query = map[string]string{"cloudDescriptorName": kubernetesCloudDescriptor}
	}
	target := r.target(id)
	client, ok := r.client.(*jenkinsAdapter)
	if !ok {
		diags.AddError("Unexpected Jenkins client", "Cannot configure a Jenkins Kubernetes resource.")
		return
	}
	if create {
		_, err := client.readKubernetesForm(ctx, r.configPath(target))
		if err == nil {
			diags.AddError("Jenkins configuration already exists", fmt.Sprintf("Configuration object %q already exists.", id))
			return
		}
		if !isNotFound(err) {
			diags.AddError("Unable to check Jenkins configuration", err.Error())
			return
		}
	} else {
		createPath, query = target+"/configSubmit", nil
	}
	if err := client.submitKubernetesForm(ctx, createPath, query, values); err != nil {
		diags.AddError("Unable to save Jenkins Kubernetes configuration", err.Error())
		return
	}
	diags.Append(state.SetAttribute(ctx, path.Root("id"), id)...)
	diags.Append(r.refresh(ctx, id, state)...)
}

func (r *kubernetesConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	resp.State = req.State
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(r.refresh(ctx, id.ValueString(), &resp.State)...)
	}
}

func (r *kubernetesConfigResource) refresh(ctx context.Context, id string, state *tfsdk.State) diag.Diagnostics {
	var diags diag.Diagnostics
	client, ok := r.client.(*jenkinsAdapter)
	if !ok {
		diags.AddError("Unexpected Jenkins client", "Cannot read a Jenkins Kubernetes resource.")
		return diags
	}
	config, err := client.readKubernetesForm(ctx, r.configPath(r.target(id)))
	if isNotFound(err) {
		state.RemoveResource(ctx)
		return diags
	}
	if err != nil {
		diags.AddError("Unable to read Jenkins Kubernetes configuration", err.Error())
		return diags
	}
	if config["name"] == "" {
		diags.AddError("Missing Jenkins configuration name", fmt.Sprintf("The form for %q did not include its name.", id))
		return diags
	}
	diags.Append(state.SetAttribute(ctx, path.Root("name"), config["name"])...)
	for _, field := range r.fields {
		value, found := config[field.formName]
		if !found {
			diags.AddError("Jenkins configuration form changed", fmt.Sprintf("The form for %q did not include %s.", id, field.formName))
			continue
		}
		if _, isBool := field.attribute.(schema.BoolAttribute); isBool {
			flag, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				diags.AddError("Invalid Jenkins configuration", fmt.Sprintf("The form field %s for %q is not a boolean.", field.formName, id))
				continue
			}
			diags.Append(state.SetAttribute(ctx, path.Root(field.tfName), flag)...)
		} else {
			diags.Append(state.SetAttribute(ctx, path.Root(field.tfName), value)...)
		}
	}
	return diags
}

func (r *kubernetesConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, ok := r.client.(*jenkinsAdapter)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Jenkins client", "Cannot delete a Jenkins Kubernetes resource.")
		return
	}
	err := client.submitKubernetesForm(ctx, r.target(id.ValueString())+"/doDelete", nil, nil)
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete Jenkins Kubernetes configuration", err.Error())
	}
}

func (r *kubernetesConfigResource) target(id string) string {
	if r.template {
		cloud, template, _ := strings.Cut(id, "/")
		return "/cloud/" + cloud + "/template/" + template
	}
	return "/cloud/" + id
}

func (r *kubernetesConfigResource) configPath(target string) string {
	if r.template {
		return target
	}
	return target + "/configure"
}
