package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Repository access strategies of a GitHub App credential. The values are the
// strategies' configuration-as-code symbols, so they read the same as the
// plugin's documentation and JCasC files.
const (
	gitHubAppAccessInferOwner           = "inferOwner"
	gitHubAppAccessInferRepository      = "inferRepository"
	gitHubAppAccessSpecificRepositories = "specificRepositories"
)

// gitHubAppAccessStrategyClasses maps each repository access strategy to the
// Java class Jenkins stores. XStream does not encode class attribute values, so
// the package keeps its single underscores here.
var gitHubAppAccessStrategyClasses = map[string]string{
	gitHubAppAccessInferOwner:           "org.jenkinsci.plugins.github_branch_source.app_credentials.AccessInferredOwner",
	gitHubAppAccessInferRepository:      "org.jenkinsci.plugins.github_branch_source.app_credentials.AccessInferredRepository",
	gitHubAppAccessSpecificRepositories: "org.jenkinsci.plugins.github_branch_source.app_credentials.AccessSpecifiedRepositories",
}

// gitHubAppDefaultPermissionsStrategies are the plugin's DefaultPermissionsStrategy enum values.
var gitHubAppDefaultPermissionsStrategies = []string{"INHERIT_ALL", "CONTENTS_READ", "CONTENTS_WRITE"}

// GitHubAppCredentials represents a Jenkins GitHub App credential.
// It requires the GitHub Branch Source plugin.
// Jenkins/XStream encodes underscores in package names as double underscores, hence github__branch__source.
type GitHubAppCredentials struct {
	XMLName                    xml.Name                           `xml:"org.jenkinsci.plugins.github__branch__source.GitHubAppCredentials"`
	ID                         string                             `xml:"id"`
	Scope                      string                             `xml:"scope"`
	Description                string                             `xml:"description"`
	AppID                      string                             `xml:"appID"`
	PrivateKey                 string                             `xml:"privateKey"`
	APIURI                     string                             `xml:"apiUri,omitempty"`
	RepositoryAccessStrategy   *gitHubAppRepositoryAccessStrategy `xml:"repositoryAccessStrategy,omitempty"`
	DefaultPermissionsStrategy string                             `xml:"defaultPermissionsStrategy,omitempty"`
}

// gitHubAppRepositoryAccessStrategy is the polymorphic repositoryAccessStrategy
// field. Only AccessSpecifiedRepositories carries owner and repositories.
type gitHubAppRepositoryAccessStrategy struct {
	Class        string               `xml:"class,attr"`
	Owner        string               `xml:"owner,omitempty"`
	Repositories *gitHubAppRepository `xml:"repositories,omitempty"`
}

type gitHubAppRepository struct {
	Names []string `xml:"string"`
}

type credentialGitHubAppResourceModel struct {
	ID                         types.String `tfsdk:"id"`
	Name                       types.String `tfsdk:"name"`
	Folder                     types.String `tfsdk:"folder"`
	Description                types.String `tfsdk:"description"`
	Domain                     types.String `tfsdk:"domain"`
	Scope                      types.String `tfsdk:"scope"`
	AppID                      types.String `tfsdk:"app_id"`
	PrivateKey                 types.String `tfsdk:"private_key"`
	PrivateKeyWo               types.String `tfsdk:"private_key_wo"`
	PrivateKeyWoVersion        types.String `tfsdk:"private_key_wo_version"`
	APIURI                     types.String `tfsdk:"api_uri"`
	RepositoryAccessStrategy   types.String `tfsdk:"repository_access_strategy"`
	Owner                      types.String `tfsdk:"owner"`
	Repositories               types.List   `tfsdk:"repositories"`
	DefaultPermissionsStrategy types.String `tfsdk:"default_permissions_strategy"`
}

type credentialGitHubAppResource struct {
	*credentialCRUD[credentialGitHubAppResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialGitHubAppResource{}
var _ resource.ResourceWithImportState = &credentialGitHubAppResource{}
var _ resource.ResourceWithConfigValidators = &credentialGitHubAppResource{}
var _ resource.ResourceWithValidateConfig = &credentialGitHubAppResource{}

func newCredentialGitHubAppResource() resource.Resource {
	return &credentialGitHubAppResource{
		credentialCRUD: newCredentialCRUD(gitHubAppCredentialSpec()),
	}
}

// gitHubAppCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go). Uses the local
// GitHubAppCredentials XML struct (the GitHub Branch Source plugin type).
func gitHubAppCredentialSpec() credentialSpec[credentialGitHubAppResourceModel] {
	return credentialSpec[credentialGitHubAppResourceModel]{
		identity: func(m *credentialGitHubAppResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialGitHubAppResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialGitHubAppResourceModel]{{
			name:         "private_key",
			hasWriteOnly: true,
			plainValue:   func(m *credentialGitHubAppResourceModel) types.String { return m.PrivateKey },
			woVersion:    func(m *credentialGitHubAppResourceModel) types.String { return m.PrivateKeyWoVersion },
			// An update that leaves the key unchanged must re-send the stored
			// encrypted key, or Jenkins replaces it with an empty one.
			preserve: preserveEncryptedSecret("privateKey", "GitHub App private key"),
		}},
		build: func(m *credentialGitHubAppResourceModel, secrets map[string]string) interface{} {
			cred := &GitHubAppCredentials{
				ID:                         m.Name.ValueString(),
				Scope:                      m.Scope.ValueString(),
				Description:                m.Description.ValueString(),
				AppID:                      m.AppID.ValueString(),
				APIURI:                     m.APIURI.ValueString(),
				RepositoryAccessStrategy:   gitHubAppAccessStrategyToAPI(m),
				DefaultPermissionsStrategy: m.DefaultPermissionsStrategy.ValueString(),
			}
			if k, ok := secrets["private_key"]; ok {
				cred.PrivateKey = k
			}
			return cred
		},
		newAPIValue: func() interface{} { return &GitHubAppCredentials{} },
		fromAPI: func(api interface{}, m *credentialGitHubAppResourceModel) {
			// NOTE: the private key is intentionally not read back — GetSingle returns
			// a placeholder. Only Create/Update send it.
			cred := api.(*GitHubAppCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.AppID = types.StringValue(cred.AppID)
			m.APIURI = optionalString(cred.APIURI)
			gitHubAppAccessStrategyFromAPI(cred.RepositoryAccessStrategy, m)
			if cred.DefaultPermissionsStrategy == "" {
				// The plugin resolves a missing value to INHERIT_ALL.
				m.DefaultPermissionsStrategy = types.StringValue("INHERIT_ALL")
			} else {
				m.DefaultPermissionsStrategy = types.StringValue(cred.DefaultPermissionsStrategy)
			}
		},
	}
}

// gitHubAppAccessStrategyToAPI builds the repositoryAccessStrategy element. It
// is always sent: a credential stored without one is treated by the plugin as a
// migrated legacy credential and listed in an administrative monitor.
func gitHubAppAccessStrategyToAPI(m *credentialGitHubAppResourceModel) *gitHubAppRepositoryAccessStrategy {
	strategy := m.RepositoryAccessStrategy.ValueString()
	if strategy == "" {
		strategy = gitHubAppAccessSpecificRepositories
	}
	out := &gitHubAppRepositoryAccessStrategy{Class: gitHubAppAccessStrategyClasses[strategy]}
	if strategy != gitHubAppAccessSpecificRepositories {
		return out
	}
	out.Owner = m.Owner.ValueString()
	// AccessSpecifiedRepositories requires a non-null list, so send it even when empty.
	out.Repositories = &gitHubAppRepository{Names: []string{}}
	for _, v := range m.Repositories.Elements() {
		if s, ok := v.(types.String); ok && !s.IsNull() && !s.IsUnknown() {
			out.Repositories.Names = append(out.Repositories.Names, s.ValueString())
		}
	}
	return out
}

// gitHubAppAccessStrategyFromAPI copies the stored strategy into the model. A
// missing element means the plugin's legacy default: specified repositories with
// no owner and no repository restriction.
func gitHubAppAccessStrategyFromAPI(api *gitHubAppRepositoryAccessStrategy, m *credentialGitHubAppResourceModel) {
	strategy := gitHubAppAccessSpecificRepositories
	if api != nil {
		for name, class := range gitHubAppAccessStrategyClasses {
			if class == api.Class {
				strategy = name
			}
		}
	}
	m.RepositoryAccessStrategy = types.StringValue(strategy)

	repos := []attr.Value{}
	if strategy == gitHubAppAccessSpecificRepositories && api != nil {
		m.Owner = optionalString(api.Owner)
		if api.Repositories != nil {
			for _, name := range api.Repositories.Names {
				repos = append(repos, types.StringValue(name))
			}
		}
	} else {
		m.Owner = types.StringNull()
	}
	m.Repositories = types.ListValueMust(types.StringType, repos)
}

// optionalString maps an empty XML value to a null attribute so an unset
// optional attribute does not show a diff against "".
func optionalString(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}

// Metadata should return the full name of the resource.
func (r *credentialGitHubAppResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_github_app"
}

// Schema should return the schema for this resource.
func (r *credentialGitHubAppResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a GitHub App credential within Jenkins. This credential may then be referenced within jobs that are created.

~> The "private_key" property may leave a plain-text private key in your state file. If using the property to manage the private key in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [GitHub Branch Source Plugin](https://plugins.jenkins.io/github-branch-source/) installed.

The repository access strategy and the default permissions strategy limit the installation access tokens Jenkins generates in untrusted contexts, such as a Pipeline using the credential through ` + "`withCredentials`" + `. See the plugin's [GitHub App guide](https://github.com/jenkinsci/github-branch-source-plugin/blob/master/docs/github-app.adoc) for how each strategy behaves.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"app_id": schema.StringAttribute{
				MarkdownDescription: "The numeric GitHub App ID.",
				Required:            true,
			},
			"private_key": schema.StringAttribute{
				MarkdownDescription: "The GitHub App private key in PKCS#8 PEM format. GitHub issues keys in PKCS#1; convert one with `openssl pkcs8 -topk8 -nocrypt -in key.pem`.",
				Optional:            true,
				Sensitive:           true,
			},
			"api_uri": schema.StringAttribute{
				MarkdownDescription: "The GitHub API endpoint, e.g. `https://github.example.com/api/v3` for GitHub Enterprise Server. Leave unset for github.com.",
				Optional:            true,
			},
			"repository_access_strategy": schema.StringAttribute{
				MarkdownDescription: "Which repositories the generated tokens can access: " +
					"`inferOwner` (all repositories of the owner inferred from the job), " +
					"`inferRepository` (only the repository inferred from the job), or " +
					"`specificRepositories` (the `owner` and `repositories` set here). " +
					"The inference-based strategies only work in Organization Folders and Multibranch Pipelines. " +
					"Defaults to `specificRepositories`, which with no `owner` and no `repositories` is the plugin's behavior for credentials created before access strategies existed.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(gitHubAppAccessSpecificRepositories),
				Validators: []validator.String{
					stringvalidator.OneOf(gitHubAppAccessInferOwner, gitHubAppAccessInferRepository, gitHubAppAccessSpecificRepositories),
				},
			},
			"owner": schema.StringAttribute{
				MarkdownDescription: "The GitHub organization or user whose repositories the tokens can access. Only valid with `specificRepositories`. " +
					"May be left unset when the app is installed in a single organization.",
				Optional: true,
			},
			"repositories": schema.ListAttribute{
				MarkdownDescription: "The repository names (without the owner) the tokens can access. Only valid with `specificRepositories`. " +
					"An empty list allows every repository of `owner` that the app installation can access.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Default:     listdefault.StaticValue(types.ListValueMust(types.StringType, []attr.Value{})),
			},
			"default_permissions_strategy": schema.StringAttribute{
				MarkdownDescription: "The permissions of the generated tokens: `INHERIT_ALL` (every permission of the app installation), `CONTENTS_READ`, or `CONTENTS_WRITE`. Defaults to `INHERIT_ALL`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("INHERIT_ALL"),
				Validators: []validator.String{
					stringvalidator.OneOf(gitHubAppDefaultPermissionsStrategies...),
				},
			},
		}, "private_key", "The GitHub App private key in PKCS#8 PEM format. GitHub issues keys in PKCS#1; convert one with `openssl pkcs8 -topk8 -nocrypt -in key.pem`.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialGitHubAppResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("private_key")
}

// ValidateConfig rejects owner and repositories with an inference-based
// strategy, where the plugin would silently ignore them.
func (r *credentialGitHubAppResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var strategy, owner types.String
	var repositories types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("repository_access_strategy"), &strategy)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("owner"), &owner)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("repositories"), &repositories)...)
	if resp.Diagnostics.HasError() || strategy.IsNull() || strategy.IsUnknown() ||
		strategy.ValueString() == gitHubAppAccessSpecificRepositories {
		return
	}
	if !owner.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("owner"), "Invalid Attribute Combination",
			"owner is only valid when repository_access_strategy is \"specificRepositories\".")
	}
	if !repositories.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("repositories"), "Invalid Attribute Combination",
			"repositories is only valid when repository_access_strategy is \"specificRepositories\".")
	}
}
