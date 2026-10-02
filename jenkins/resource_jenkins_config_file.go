package jenkins

import (
	"context"
	"maps"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

var configFileIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type configFileResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Comment     types.String `tfsdk:"comment"`
	Content     types.String `tfsdk:"content"`
	ContentType types.String `tfsdk:"content_type"`
}

type configFileResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &configFileResource{}
var _ resource.ResourceWithImportState = &configFileResource{}

func newConfigFileResource() resource.Resource {
	return &configFileResource{resourceHelper: newResourceHelper()}
}

func (r *configFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_file"
}

func (r *configFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a global file through the Jenkins [Config File Provider plugin](https://plugins.jenkins.io/config-file-provider/).

The content_type selects the Jenkins file type, not a MIME type. Custom files may contain any text format, including YAML. Credential mappings are not managed; existing files with tokenized, Maven server or Properties credential mappings are rejected to avoid losing them. The existing Replace All option is preserved when updating the same type. Requires ` + "`Overall/Manage`" + `.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique Config File Provider ID used by jobs and pipelines. Changing it forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(configFileIDPattern, `must contain only letters, digits, "_", "." or "-"`),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The human-readable name shown in Jenkins.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"comment": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("Managed by Terraform"),
				MarkdownDescription: "An optional note shown with the managed file.",
			},
			"content": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The file content. It is stored in Terraform state; use Jenkins credentials rather than embedding secrets.",
			},
			"content_type": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("custom"),
				MarkdownDescription: "Jenkins file type: `custom`, `json`, `xml`, `groovy`, `properties`, `maven_settings`, `global_maven_settings` or `maven_toolchains`. Defaults to `custom`. Changing it updates the file in place. Set this to the existing type when importing a non-Custom file.",
				Validators: []validator.String{
					stringvalidator.OneOf(slices.Sorted(maps.Keys(configFileClasses))...),
				},
			},
		},
	}
}

func (r *configFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "configFileResource.Create")
	var data configFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	existing, err := r.client.GetConfigFile(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to Create Resource", "Could not check for an existing config file.\n\nError: "+err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddError("Config File Already Exists", "A config file with ID "+data.ID.ValueString()+" already exists. Import it instead of overwriting it.")
		return
	}

	if !r.saveAndRead(ctx, &data, "create", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "configFileResource.Read")
	var data configFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.populate(ctx, &data, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configFileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "configFileResource.Update")
	var data configFileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.saveAndRead(ctx, &data, "update", &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "configFileResource.Delete")
	var data configFileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteConfigFile(ctx, data.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *configFileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *configFileResource) saveAndRead(ctx context.Context, data *configFileResourceModel, action string, diags *diag.Diagnostics) bool {
	config := managedConfigFile{
		ID:          data.ID.ValueString(),
		Name:        data.Name.ValueString(),
		Comment:     data.Comment.ValueString(),
		Content:     data.Content.ValueString(),
		ContentType: data.ContentType.ValueString(),
	}
	if err := r.client.SaveConfigFile(ctx, config); err != nil {
		diags.AddError("Unable to "+action+" Resource", "Config File Provider rejected the file.\n\nError: "+err.Error())
		return false
	}
	if !r.populate(ctx, data, diags) && !diags.HasError() {
		diags.AddError("Unable to Read Resource", "The config file was saved but could not be read back.")
		return false
	}
	return !diags.HasError()
}

func (r *configFileResource) populate(ctx context.Context, data *configFileResourceModel, diags *diag.Diagnostics) bool {
	config, err := r.client.GetConfigFile(ctx, data.ID.ValueString())
	if err != nil {
		diags.AddError("Unable to Refresh Resource", "Could not read config file "+data.ID.ValueString()+".\n\nError: "+err.Error())
		return false
	}
	if config == nil {
		return false
	}
	data.ID = types.StringValue(config.ID)
	data.Name = types.StringValue(config.Name)
	data.Comment = types.StringValue(config.Comment)
	data.Content = types.StringValue(config.Content)
	data.ContentType = types.StringValue(config.ContentType)
	return true
}
