package jenkins

import (
	"context"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const defaultFolderInheritanceStrategy = "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"

// folderSecurityObjectType is the element type of the "security" set block. It
// must match the block's nested attributes so the set can be (de)serialized.
var folderSecurityObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"inheritance_strategy": types.StringType,
		"permissions":          types.SetType{ElemType: types.StringType},
	},
}

// folderPipelineLibraryObjectType is the element type of the
// "pipeline_libraries" list. It must match the nested attributes so the list
// can be (de)serialized.
var folderPipelineLibraryObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name":                   types.StringType,
		"default_version":        types.StringType,
		"implicit":               types.BoolType,
		"allow_version_override": types.BoolType,
		"include_in_changesets":  types.BoolType,
		"git_remote":             types.StringType,
		"git_credentials_id":     types.StringType,
	},
}

type folderPipelineLibraryModel struct {
	Name                 types.String `tfsdk:"name"`
	DefaultVersion       types.String `tfsdk:"default_version"`
	Implicit             types.Bool   `tfsdk:"implicit"`
	AllowVersionOverride types.Bool   `tfsdk:"allow_version_override"`
	IncludeInChangesets  types.Bool   `tfsdk:"include_in_changesets"`
	GitRemote            types.String `tfsdk:"git_remote"`
	GitCredentialsID     types.String `tfsdk:"git_credentials_id"`
}

type folderResourceModel struct {
	ID                        types.String `tfsdk:"id"`
	Name                      types.String `tfsdk:"name"`
	Folder                    types.String `tfsdk:"folder"`
	DisplayName               types.String `tfsdk:"display_name"`
	Description               types.String `tfsdk:"description"`
	Security                  types.Set    `tfsdk:"security"`
	Template                  types.String `tfsdk:"template"`
	PermittedKubernetesClouds types.Set    `tfsdk:"permitted_kubernetes_clouds"`
	PipelineLibraries         types.List   `tfsdk:"pipeline_libraries"`
}

type folderSecurityBlockModel struct {
	InheritanceStrategy types.String `tfsdk:"inheritance_strategy"`
	Permissions         types.Set    `tfsdk:"permissions"`
}

type folderResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &folderResource{}
var _ resource.ResourceWithImportState = &folderResource{}

func newFolderResource() resource.Resource {
	return &folderResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *folderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_folder"
}

func (r *folderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a folder within Jenkins, optionally nested inside another folder, with project-based security.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The full canonical folder path, e.g. `/job/folder-name`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique name of the JenkinsCI folder.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[^/]*$`),
						"must not include path characters. Please use the 'folder' property if specifying a folder within a subfolder",
					),
				},
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The folder namespace to create this folder in. Nested folders are separated with `/`, e.g. `parent/child`. Cannot be changed after creation, and every parent folder must already exist.",
				PlanModifiers: []planmodifier.String{
					folderPlanModifier{},
				},
				Validators: []validator.String{
					folderNameValidator{},
				},
			},
			"display_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The name of the folder to display in the UI.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The description of this folder's purpose.",
			},
			"permitted_kubernetes_clouds": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Usage-restricted Kubernetes Clouds granted directly to this folder. " +
					"Omit to preserve the existing grants, or set to an empty set to remove them. " +
					"Jenkins also grants any Clouds inherited from parent folders.",
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"pipeline_libraries": schema.ListNestedAttribute{
				Optional: true,
				MarkdownDescription: "Pipeline libraries configured directly on this folder, each retrieved from a Git repository. " +
					"Omit to leave the folder's libraries unmanaged, or set to an empty list to remove them. " +
					"Pipelines in this folder and its descendants can load these libraries; they run in the Groovy sandbox.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "The name pipelines use to load the library, as in `@Library('name')`.",
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
						"default_version": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "The branch, tag or commit loaded when a pipeline does not select a version.",
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
						"implicit": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Whether pipelines load the library without an `@Library` annotation. Requires `default_version`. Defaults to `false`.",
						},
						"allow_version_override": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Whether a pipeline may select another version, as in `@Library('name@version')`. Defaults to `true`.",
						},
						"include_in_changesets": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Whether library changes are included in the changesets of the builds that load it. Defaults to `true`.",
						},
						"git_remote": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "The URL of the Git repository holding the library.",
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
						"git_credentials_id": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "The ID of the credential used to fetch the repository.",
							Validators: []validator.String{
								stringvalidator.LengthAtLeast(1),
							},
						},
					},
				},
			},
			"template": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The Jenkins-compatible XML template describing the folder, as used to communicate with Jenkins. An existing folder's XML can be retrieved by appending `/config.xml` to its URL.",
			},
		},
		Blocks: map[string]schema.Block{
			"security": schema.SetNestedBlock{
				MarkdownDescription: "The Jenkins project-based security configuration. At most one block may be specified.",
				// config.xml holds a single AuthorizationMatrixProperty, and both
				// securityFromModel and securityToSet are single-valued, so a
				// second block would be written to neither Jenkins nor state —
				// surfacing as "Provider produced inconsistent result after
				// apply" once the folder had already been half-configured.
				// Reject it while it is still a plan.
				Validators: []validator.Set{
					setvalidator.SizeAtMost(1),
				},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"inheritance_strategy": schema.StringAttribute{
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString(defaultFolderInheritanceStrategy),
							MarkdownDescription: "The strategy for applying these permission sets to permissions inherited from parent folders and the global configuration. Defaults to `org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy`.",
						},
						"permissions": schema.SetAttribute{
							Required:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "The Jenkins permission assignments granting access to this folder, each `<permission>:<user-or-group>`. Duplicates are collapsed and ordering is neither significant nor preserved, so elements cannot be referenced by index.",
						},
					},
				},
			},
		},
	}
}

func (r *folderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "folderResource.Create")
	var data folderResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folderName := data.Folder.ValueString()
	if err := folderExists(ctx, r.client, folderName); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Folder",
			"Could not find folder '"+folderName+"'.\n\nError: "+err.Error(),
		)
		return
	}

	f := folder{
		Description: data.Description.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
	}
	f.Properties.Security = securityFromModel(ctx, data.Security, &resp.Diagnostics)
	setPermittedKubernetesClouds(ctx, &f.Properties, data.PermittedKubernetesClouds, &resp.Diagnostics)
	setPipelineLibraries(ctx, &f.Properties, data.PipelineLibraries, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	xml, err := f.Render()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Folder Configuration", err.Error())
		return
	}

	if _, err := r.client.CreateJobInFolder(ctx, string(xml), data.Name.ValueString(), extractFolders(folderName)...); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the folder "+data.Name.ValueString()+".\n\nError: "+err.Error(),
		)
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Created Resource", "the folder was created but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *folderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "folderResource.Read")
	var data folderResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found := r.populate(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *folderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "folderResource.Update")
	var data folderResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not find folder "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	// Read the existing config so unmanaged elements (folderViews, healthMetrics,
	// and any other properties) are preserved across the update.
	config, err := job.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not read folder configuration.\n\nError: "+err.Error())
		return
	}

	f, err := parseFolder(config)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Parse Folder Configuration", err.Error())
		return
	}

	f.Description = data.Description.ValueString()
	f.DisplayName = data.DisplayName.ValueString()
	f.Properties.Security = securityFromModel(ctx, data.Security, &resp.Diagnostics)
	var configuredClouds types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("permitted_kubernetes_clouds"), &configuredClouds)...)
	if !configuredClouds.IsNull() {
		setPermittedKubernetesClouds(ctx, &f.Properties, data.PermittedKubernetesClouds, &resp.Diagnostics)
	}
	setPipelineLibraries(ctx, &f.Properties, data.PipelineLibraries, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	xml, err := f.Render()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Folder Configuration", err.Error())
		return
	}

	if err := job.UpdateConfig(ctx, string(xml)); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the folder configuration.\n\nError: "+err.Error())
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", "the folder was updated but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *folderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "folderResource.Delete")
	var data folderResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.DeleteJobInFolder(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *folderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name, folders := parseCanonicalJobID(req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	if folder := formatFolderID(folders); folder != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// populate fetches the folder and its config.xml and fills data from them. It
// returns false (without an error diagnostic) when the folder no longer exists,
// so Read can drop it from state.
//
// Everything it writes comes from the server's config.xml, never from the
// configured or prior value: this is the refresh path, so reporting back a
// desired value would make drift undetectable. A security block that Jenkins
// does not report is therefore reported as absent, which is what lets
// `terraform plan` see it and put it back.
func (r *folderResource) populate(ctx context.Context, data *folderResourceModel, diags *diag.Diagnostics) bool {
	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		if isNotFound(err) {
			return false
		}
		diags.AddError("Unable to Refresh Resource", "Could not read folder "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return false
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Refresh Resource", "Could not read folder configuration.\n\nError: "+err.Error())
		return false
	}

	f, err := parseFolder(config)
	if err != nil {
		diags.AddError("Unable to Parse Folder Configuration", err.Error())
		return false
	}

	data.ID = types.StringValue(job.Base)
	data.Template = types.StringValue(config)
	data.DisplayName = types.StringValue(f.DisplayName)
	data.Description = types.StringValue(f.Description)
	data.Security = securityToSet(ctx, f.Properties.Security, diags)
	clouds := []string{}
	if f.Properties.Kubernetes != nil {
		clouds = append(clouds, f.Properties.Kubernetes.PermittedClouds...)
		slices.Sort(clouds)
		clouds = slices.Compact(clouds)
	}
	permittedClouds, cloudDiags := types.SetValueFrom(ctx, types.StringType, clouds)
	diags.Append(cloudDiags...)
	data.PermittedKubernetesClouds = permittedClouds
	data.PipelineLibraries = pipelineLibrariesToList(ctx, &f.Properties, data.PipelineLibraries, diags)
	return !diags.HasError()
}

// setPipelineLibraries writes the "pipeline_libraries" list into the folder
// properties. A null list leaves the properties alone, which is how an omitted
// attribute leaves the libraries already on the folder unmanaged.
func setPipelineLibraries(ctx context.Context, properties *folderProperties, list types.List, diags *diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return
	}
	var models []folderPipelineLibraryModel
	diags.Append(list.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return
	}

	libraries := make([]folderLibrary, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		name := m.Name.ValueString()
		if _, ok := seen[name]; ok {
			diags.AddAttributeError(path.Root("pipeline_libraries"), "Duplicate Pipeline Library",
				"The library name "+name+" is configured more than once. Jenkins loads a library by its name, so each name must be unique within the folder.")
			return
		}
		seen[name] = struct{}{}
		implicit := boolOrDefault(m.Implicit, false)
		if implicit && m.DefaultVersion.ValueString() == "" {
			diags.AddAttributeError(path.Root("pipeline_libraries"), "Missing Default Version",
				"The library "+name+" is implicit but has no default_version. Jenkins cannot load an implicit library without one.")
			return
		}
		libraries = append(libraries, folderLibrary{
			Name:                 name,
			Retriever:            newGitFolderLibraryRetriever(m.GitRemote.ValueString(), m.GitCredentialsID.ValueString()),
			DefaultVersion:       m.DefaultVersion.ValueString(),
			Implicit:             implicit,
			AllowVersionOverride: boolOrDefault(m.AllowVersionOverride, true),
			IncludeInChangesets:  boolOrDefault(m.IncludeInChangesets, true),
		})
	}

	if err := properties.setPipelineLibraries(libraries); err != nil {
		diags.AddError("Unable to Render Folder Configuration", err.Error())
	}
}

// boolOrDefault returns the configured value, or fallback when the attribute
// was omitted.
func boolOrDefault(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

// pipelineLibrariesToList reports the libraries Jenkins holds for the folder,
// or null when prior (the planned or previously stored list) is null: the
// attribute is not computed, so an omitted attribute has to stay null in
// state. It is not computed because Terraform plans a change on every run for
// a computed list of nested attributes whose configuration is null.
//
// A library whose retriever is not a Git SCM source is still listed, with null
// Git attributes, so that replacing it shows up in the plan.
//
// Jenkins always reports a value for the three flags, so a flag that prior
// left null stays null while Jenkins holds the default — otherwise an omitted
// flag would not survive the round trip. Any other value is reported as is, so
// drift stays visible.
func pipelineLibrariesToList(ctx context.Context, properties *folderProperties, prior types.List, diags *diag.Diagnostics) types.List {
	if prior.IsNull() || prior.IsUnknown() {
		return types.ListNull(folderPipelineLibraryObjectType)
	}
	libraries, err := properties.pipelineLibraries()
	if err != nil {
		diags.AddError("Unable to Parse Folder Configuration", err.Error())
		return types.ListNull(folderPipelineLibraryObjectType)
	}

	var priorModels []folderPipelineLibraryModel
	diags.Append(prior.ElementsAs(ctx, &priorModels, false)...)
	priorByName := make(map[string]folderPipelineLibraryModel, len(priorModels))
	for _, m := range priorModels {
		priorByName[m.Name.ValueString()] = m
	}
	flag := func(prior types.Bool, known bool, value, fallback bool) types.Bool {
		if known && prior.IsNull() && value == fallback {
			return types.BoolNull()
		}
		return types.BoolValue(value)
	}

	models := make([]folderPipelineLibraryModel, 0, len(libraries))
	for _, library := range libraries {
		p, known := priorByName[library.Name]
		m := folderPipelineLibraryModel{
			Name:                 types.StringValue(library.Name),
			DefaultVersion:       types.StringNull(),
			Implicit:             flag(p.Implicit, known, library.Implicit, false),
			AllowVersionOverride: flag(p.AllowVersionOverride, known, library.AllowVersionOverride, true),
			IncludeInChangesets:  flag(p.IncludeInChangesets, known, library.IncludeInChangesets, true),
			GitRemote:            types.StringNull(),
			GitCredentialsID:     types.StringNull(),
		}
		if library.DefaultVersion != "" {
			m.DefaultVersion = types.StringValue(library.DefaultVersion)
		}
		if library.isGit() {
			m.GitRemote = types.StringValue(library.Retriever.SCM.Remote)
			if id := library.Retriever.SCM.CredentialsID; id != "" {
				m.GitCredentialsID = types.StringValue(id)
			}
		}
		models = append(models, m)
	}

	list, d := types.ListValueFrom(ctx, folderPipelineLibraryObjectType, models)
	diags.Append(d...)
	return list
}

func setPermittedKubernetesClouds(ctx context.Context, properties *folderProperties, clouds types.Set, diags *diag.Diagnostics) {
	if clouds.IsNull() || clouds.IsUnknown() {
		return
	}
	var names []string
	diags.Append(clouds.ElementsAs(ctx, &names, false)...)
	if diags.HasError() {
		return
	}
	properties.Kubernetes = nil
	if len(names) > 0 {
		properties.Kubernetes = &folderKubernetesClouds{PermittedClouds: names}
	}
}

// securityFromModel converts the "security" set block into the internal
// folderSecurity model used to render config.xml. It returns nil when no
// security block is configured.
func securityFromModel(ctx context.Context, set types.Set, diags *diag.Diagnostics) *folderSecurity {
	if set.IsNull() || set.IsUnknown() || len(set.Elements()) == 0 {
		return nil
	}

	var blocks []folderSecurityBlockModel
	diags.Append(set.ElementsAs(ctx, &blocks, false)...)
	if diags.HasError() || len(blocks) == 0 {
		return nil
	}

	b := blocks[0]
	permissions := []string{}
	diags.Append(b.Permissions.ElementsAs(ctx, &permissions, false)...)
	if diags.HasError() {
		return nil
	}

	// The schema Default normally fills this in, but fall back to the default
	// class so an omitted inheritance_strategy never renders an empty class.
	strategy := b.InheritanceStrategy.ValueString()
	if strategy == "" {
		strategy = defaultFolderInheritanceStrategy
	}

	return &folderSecurity{
		InheritanceStrategy: folderPermissionInheritanceStrategy{Class: strategy},
		Permission:          permissions,
	}
}

// securityToSet converts an internal folderSecurity model into the "security"
// set block. A nil model yields an empty set, matching a folder with no
// project-based security configured.
func securityToSet(ctx context.Context, sec *folderSecurity, diags *diag.Diagnostics) types.Set {
	if sec == nil {
		return types.SetValueMust(folderSecurityObjectType, []attr.Value{})
	}

	// sec.Permission comes straight from Jenkins' config.xml, which the
	// provider does not control: it may be a nil slice (encoding/xml leaves it
	// nil when zero <permission> elements were persisted) or contain repeated
	// entries (e.g. a hand-edited config.xml). Both break SetValueFrom: a nil
	// slice becomes a *null* Set, which Terraform's post-apply consistency
	// check treats as non-correlating with the *empty* Set a "permissions = []"
	// config produces, and duplicate elements fail Set validation with a
	// "Duplicate Set Element" error instead of collapsing. Rebuild the slice
	// deduplicated and non-nil so the round trip always yields a valid,
	// empty-not-null Set.
	perms := make([]string, 0, len(sec.Permission))
	seen := make(map[string]struct{}, len(sec.Permission))
	for _, p := range sec.Permission {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		perms = append(perms, p)
	}
	permissions, d := types.SetValueFrom(ctx, types.StringType, perms)
	diags.Append(d...)

	set, d := types.SetValueFrom(ctx, folderSecurityObjectType, []folderSecurityBlockModel{
		{
			InheritanceStrategy: types.StringValue(sec.InheritanceStrategy.Class),
			Permissions:         permissions,
		},
	})
	diags.Append(d...)
	return set
}
