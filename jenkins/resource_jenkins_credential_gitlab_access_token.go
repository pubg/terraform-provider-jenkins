package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// GitLab access token credential classes of the GitLab Branch Source plugin.
// Both store the same single token field; GitLab's project access tokens use
// the personal kind.
var gitLabAccessTokenClasses = map[string]string{
	"personal": "io.jenkins.plugins.gitlabserverconfig.credentials.PersonalAccessTokenImpl",
	"group":    "io.jenkins.plugins.gitlabserverconfig.credentials.GroupAccessTokenImpl",
}

// GitLabAccessTokenCredentials represents either GitLab access token credential.
// XMLName carries the class, so the root element decides the token type.
type GitLabAccessTokenCredentials struct {
	XMLName     xml.Name
	ID          string `xml:"id"`
	Scope       string `xml:"scope"`
	Description string `xml:"description"`
	Token       string `xml:"token"`
}

type credentialGitLabAccessTokenResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Folder         types.String `tfsdk:"folder"`
	Description    types.String `tfsdk:"description"`
	Domain         types.String `tfsdk:"domain"`
	Scope          types.String `tfsdk:"scope"`
	TokenType      types.String `tfsdk:"token_type"`
	Token          types.String `tfsdk:"token"`
	TokenWo        types.String `tfsdk:"token_wo"`
	TokenWoVersion types.String `tfsdk:"token_wo_version"`
}

type credentialGitLabAccessTokenResource struct {
	*credentialCRUD[credentialGitLabAccessTokenResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialGitLabAccessTokenResource{}
var _ resource.ResourceWithImportState = &credentialGitLabAccessTokenResource{}
var _ resource.ResourceWithConfigValidators = &credentialGitLabAccessTokenResource{}

func newCredentialGitLabAccessTokenResource() resource.Resource {
	return &credentialGitLabAccessTokenResource{
		credentialCRUD: newCredentialCRUD(gitLabAccessTokenCredentialSpec()),
	}
}

// gitLabAccessTokenCredentialSpec supplies the type-specific mapping for the
// shared credential CRUD flow (see credential_crud.go).
func gitLabAccessTokenCredentialSpec() credentialSpec[credentialGitLabAccessTokenResourceModel] {
	return credentialSpec[credentialGitLabAccessTokenResourceModel]{
		identity: func(m *credentialGitLabAccessTokenResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialGitLabAccessTokenResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialGitLabAccessTokenResourceModel]{{
			name:         "token",
			hasWriteOnly: true,
			plainValue:   func(m *credentialGitLabAccessTokenResourceModel) types.String { return m.Token },
			woVersion:    func(m *credentialGitLabAccessTokenResourceModel) types.String { return m.TokenWoVersion },
			// An update that leaves the token unchanged must re-send the stored
			// encrypted token, or Jenkins replaces it with an empty one.
			preserve: preserveEncryptedSecret("token", "GitLab access token"),
		}},
		build: func(m *credentialGitLabAccessTokenResourceModel, secrets map[string]string) interface{} {
			tokenType := m.TokenType.ValueString()
			if tokenType == "" {
				tokenType = "personal"
			}
			cred := &GitLabAccessTokenCredentials{
				XMLName:     xml.Name{Local: gitLabAccessTokenClasses[tokenType]},
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
			}
			if t, ok := secrets["token"]; ok {
				cred.Token = t
			}
			return cred
		},
		newAPIValue: func() interface{} { return &GitLabAccessTokenCredentials{} },
		fromAPI: func(api interface{}, m *credentialGitLabAccessTokenResourceModel) {
			// NOTE: the token is intentionally not read back — GetSingle returns
			// a placeholder. Only Create/Update send it.
			cred := api.(*GitLabAccessTokenCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			for tokenType, class := range gitLabAccessTokenClasses {
				if class == cred.XMLName.Local {
					m.TokenType = types.StringValue(tokenType)
				}
			}
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialGitLabAccessTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_gitlab_access_token"
}

// Schema should return the schema for this resource.
func (r *credentialGitLabAccessTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a GitLab access token credential within Jenkins, the "GitLab Personal Access Token" or "GitLab Group Access Token" kind of the [GitLab Branch Source Plugin](https://plugins.jenkins.io/gitlab-branch-source/).

A GitLab Project or GitLab Group branch source uses one such credential for the GitLab API, for checkout, and for pipeline status notifications. A plain username/password credential does not work for the API calls, because the plugin only reads a token from these kinds or from a secret text credential.

~> The "token" property may leave a plain-text token in your state file. If using the property to manage the token in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [GitLab Branch Source Plugin](https://plugins.jenkins.io/gitlab-branch-source/) installed.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"token_type": schema.StringAttribute{
				MarkdownDescription: "The credential kind: `personal` (GitLab Personal Access Token, also used for project access tokens) or `group` (GitLab Group Access Token). " +
					"Both store the token the same way. Defaults to `personal`. Changing it forces a new credential.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("personal"),
				Validators: []validator.String{
					stringvalidator.OneOf("personal", "group"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "The GitLab access token.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "token", "The GitLab access token.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialGitLabAccessTokenResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("token")
}
