package jenkins

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// oidcIDTokenCredentialClass is the Java class of the OpenID Connect Provider
// plugin's "OpenID Connect id token" credential.
const oidcIDTokenCredentialClass = "io.jenkins.plugins.oidc_provider.IdTokenStringCredentials"

// credentialOIDCIDToken is the config.xml shape of the credential. It is only
// ever read: the element also carries the signing key (<privateKey>), which
// Jenkins redacts on GET and requires on POST, so config.xml cannot be used to
// create or update this credential type.
type credentialOIDCIDToken struct {
	XMLName     xml.Name `xml:"io.jenkins.plugins.oidc__provider.IdTokenStringCredentials"`
	ID          string   `xml:"id"`
	Scope       string   `xml:"scope"`
	Description string   `xml:"description"`
	Issuer      string   `xml:"issuer"`
	Audience    string   `xml:"audience"`
}

type credentialOIDCIDTokenResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Folder          types.String `tfsdk:"folder"`
	Description     types.String `tfsdk:"description"`
	Domain          types.String `tfsdk:"domain"`
	Scope           types.String `tfsdk:"scope"`
	Issuer          types.String `tfsdk:"issuer"`
	Audience        types.String `tfsdk:"audience"`
	EffectiveIssuer types.String `tfsdk:"effective_issuer"`
}

// credentialOIDCIDTokenResource does not use the shared credentialCRUD flow.
// That flow creates and updates through config.xml, which this credential type
// rejects (see credentialOIDCIDToken); it is created through the credentials
// form instead, which lets Jenkins generate the signing key.
type credentialOIDCIDTokenResource struct {
	*resourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialOIDCIDTokenResource{}
var _ resource.ResourceWithImportState = &credentialOIDCIDTokenResource{}

func newCredentialOIDCIDTokenResource() resource.Resource {
	return &credentialOIDCIDTokenResource{resourceHelper: newResourceHelper()}
}

// Metadata should return the full name of the resource.
func (r *credentialOIDCIDTokenResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_oidc_id_token"
}

// Schema should return the schema for this resource.
func (r *credentialOIDCIDTokenResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages an OpenID Connect id token credential within Jenkins. Binding the credential in a build (for example with ` + "`withCredentials([string(...)])`" + `) yields a short-lived id token signed by Jenkins, which the build can exchange with an external service that trusts Jenkins as an OpenID Connect issuer.

Jenkins generates the RSA signing key when the credential is created, and the key never leaves Jenkins or enters Terraform state.

~> Jenkins generates a new signing key whenever this credential is saved, so every argument forces replacement. A service that reads the JSON Web Key Set from Jenkins picks the new key up by itself. With a custom ` + "`issuer`" + ` you host that document yourself, and must publish it again after each replacement.

~> The Jenkins installation that uses this resource is expected to have the [OpenID Connect Provider Plugin](https://plugins.jenkins.io/oidc-provider/) installed in their system. The plugin only issues tokens for credentials in the global domain.`,
		Attributes: r.schemaCredential(map[string]schema.Attribute{
			"description": schema.StringAttribute{
				MarkdownDescription: "A human readable description of the credentials being stored.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("Managed by Terraform"),
				PlanModifiers:       replace,
			},
			"scope": schema.StringAttribute{
				MarkdownDescription: `The visibility of the credentials to Jenkins agents. This must be set to either "GLOBAL" or "SYSTEM". If not set will default to "GLOBAL".`,
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("GLOBAL"),
				PlanModifiers:       replace,
				Validators: []validator.String{
					stringvalidator.OneOf(supportedCredentialScopes...),
				},
			},
			"issuer": schema.StringAttribute{
				MarkdownDescription: "The issuer (`iss`) claim of the id token. If not set, Jenkins uses its own issuer URI (`<jenkins-url>/oidc`, or `<jenkins-url>/oidc/job/<folder>` for a folder credential) and serves the discovery documents there; `effective_issuer` reports it. Set this only when you host the discovery documents yourself at an `https` URI.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"audience": schema.StringAttribute{
				MarkdownDescription: "The audience (`aud`) claim of the id token, as expected by the service that verifies it.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"effective_issuer": schema.StringAttribute{
				MarkdownDescription: "The issuer (`iss`) claim that tokens from this credential carry: `issuer` when it is set, otherwise the issuer URI Jenkins reports for the credential's store. It is built from the Jenkins URL configured in Jenkins, which may differ from the provider's `server_url`. Register this value with the service that verifies the tokens.",
				Computed:            true,
			},
		}),
	}
}

// oidcIDTokenCredentialEndpoint returns the REST path of the credential's domain.
func oidcIDTokenCredentialEndpoint(folder, domain string) string {
	return credentialStoreBase(folder) + "/domain/" + url.PathEscape(domain)
}

// oidcIDTokenCreateForm renders the body of the credentials form. Stapler binds
// the "credentials" object to the class's data-bound constructor and setters.
func oidcIDTokenCreateForm(m *credentialOIDCIDTokenResourceModel) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"": "0",
		"credentials": map[string]string{
			"stapler-class": oidcIDTokenCredentialClass,
			"$class":        oidcIDTokenCredentialClass,
			"scope":         m.Scope.ValueString(),
			"id":            m.Name.ValueString(),
			"description":   m.Description.ValueString(),
			"issuer":        m.Issuer.ValueString(),
			"audience":      m.Audience.ValueString(),
		},
	})
	if err != nil {
		return "", err
	}
	return url.Values{"json": {string(payload)}}.Encode(), nil
}

func (r *credentialOIDCIDTokenResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m credentialOIDCIDTokenResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
	cm := r.credentialManagerForFolder(ctx, folder, &resp.Diagnostics)
	if cm == nil {
		return
	}

	// The credentials form reports neither a duplicate ID nor a rejected
	// submission, so check before and read back after.
	err := cm.GetSingle(ctx, domain, name, &credentialOIDCIDToken{})
	if err == nil || !isNotFound(err) {
		detail := fmt.Sprintf("a credential named %q already exists", name)
		if err != nil {
			detail = err.Error()
		}
		resp.Diagnostics.AddError(crudCreateErrSummary, crudCreateErrDetail+detail)
		return
	}

	form, err := oidcIDTokenCreateForm(&m)
	if err == nil {
		var httpResp *http.Response
		httpResp, err = r.client.PostRequest(ctx, oidcIDTokenCredentialEndpoint(folder, domain)+"/createCredentials", strings.NewReader(form), &struct{}{}, map[string]string{})
		if err == nil && httpResp.StatusCode != http.StatusOK {
			err = fmt.Errorf("invalid response code %d creating credential %q", httpResp.StatusCode, name)
		}
	}
	if err != nil {
		resp.Diagnostics.AddError(crudCreateErrSummary, crudCreateErrDetail+err.Error())
		return
	}

	var cred credentialOIDCIDToken
	if err := cm.GetSingle(ctx, domain, name, &cred); err != nil {
		resp.Diagnostics.AddError(crudCreateErrSummary, crudCreateErrDetail+"Jenkins did not store the credential: "+err.Error())
		return
	}

	m.ID = types.StringValue(generateCredentialID(folder, name))
	if err := cred.copyTo(ctx, cm, folder, domain, &m); err != nil {
		resp.Diagnostics.AddError(crudCreateErrSummary, crudCreateErrDetail+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *credentialOIDCIDTokenResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m credentialOIDCIDTokenResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folder, domain, name := m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()

	cm := r.credentialManager(folder)
	var cred credentialOIDCIDToken
	if err := cm.GetSingle(ctx, domain, name, &cred); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(crudReadErrSummary, crudReadErrDetail+err.Error())
		return
	}

	m.ID = types.StringValue(generateCredentialID(folder, name))
	if err := cred.copyTo(ctx, cm, folder, domain, &m); err != nil {
		resp.Diagnostics.AddError(crudReadErrSummary, crudReadErrDetail+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ImportState leaves the state in the form a configuration produces, so that an
// imported credential plans as unchanged. Any difference forces a replacement,
// and replacing the credential makes Jenkins generate a new signing key. The
// shared import sets an empty folder for a global credential and keeps the
// folder as written in the import ID; here a global credential has no folder
// and a folder takes the form of a jenkins_folder ID.
func (r *credentialOIDCIDTokenResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.resourceHelper.ImportState(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}

	var name, folder types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("name"), &name)...)
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("folder"), &folder)...)
	if resp.Diagnostics.HasError() {
		return
	}

	formatted := formatFolderName(folder.ValueString())
	folder = types.StringNull()
	if formatted != "" {
		folder = types.StringValue("/job/" + formatted)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), generateCredentialID(folder.ValueString(), name.ValueString()))...)
}

// Update is never planned: every configurable attribute forces replacement,
// because saving the credential in place would silently replace its signing key.
func (r *credentialOIDCIDTokenResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m credentialOIDCIDTokenResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *credentialOIDCIDTokenResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m credentialOIDCIDTokenResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.deleteCredential(ctx, m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString(), &resp.Diagnostics)
}

// copyTo writes the fields Jenkins reports into the model. Jenkins omits an
// unset issuer or audience, which maps to null rather than an empty string.
func (c *credentialOIDCIDToken) copyTo(ctx context.Context, cm *jenkins.CredentialsManager, folder, domain string, m *credentialOIDCIDTokenResourceModel) error {
	description, err := readCredentialDescription(ctx, cm, folder, domain, c.ID)
	if err != nil {
		return err
	}
	m.Scope = types.StringValue(c.Scope)
	m.Description = types.StringValue(description)
	m.Issuer = stringOrNull(c.Issuer)
	m.Audience = stringOrNull(c.Audience)

	issuer := c.Issuer
	if issuer == "" {
		if issuer, err = readOIDCIssuer(ctx, cm, folder); err != nil {
			return err
		}
	}
	m.EffectiveIssuer = types.StringValue(issuer)
	return nil
}

// readCredentialDescription reads a credential's description from the JSON API.
// Jenkins serves config.xml without a charset, so text outside ISO-8859-1
// arrives there as question marks. The other attributes are not in the JSON
// API; issuer and audience are URIs and names, so config.xml is kept for them.
func readCredentialDescription(ctx context.Context, cm *jenkins.CredentialsManager, folder, domain, name string) (string, error) {
	client := jenkinsAdapter{Jenkins: cm.J}
	endpoint := oidcIDTokenCredentialEndpoint(folder, domain) + "/credential/" + url.PathEscape(name) + "/api/json"
	body, err := client.getConfigFilePage(ctx, endpoint, url.Values{"tree": {"description"}})
	if err != nil {
		return "", fmt.Errorf("cannot read the credential description from Jenkins: %w", err)
	}
	defer func() { _ = body.Close() }()

	var credential struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(body).Decode(&credential); err != nil {
		return "", fmt.Errorf("cannot parse the Jenkins credential description: %w", err)
	}
	return credential.Description, nil
}

// readOIDCIssuer returns the issuer URI Jenkins uses for credentials in the
// given folder's store (the global store when empty) that set no issuer of
// their own. Jenkins derives it from its configured root URL, so it is read
// from the discovery document rather than built from the provider's server URL.
// Jenkins only serves the document while the store holds such a credential.
func readOIDCIssuer(ctx context.Context, cm *jenkins.CredentialsManager, folder string) (string, error) {
	endpoint := "/oidc"
	if name := formatFolderName(folder); name != "" {
		endpoint += "/job/" + name
	}
	client := jenkinsAdapter{Jenkins: cm.J}
	body, err := client.getConfigFilePage(ctx, endpoint+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", fmt.Errorf("cannot read the OpenID Connect issuer from Jenkins: %w", err)
	}
	defer func() { _ = body.Close() }()

	var discovery struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(body).Decode(&discovery); err != nil {
		return "", fmt.Errorf("cannot parse the Jenkins OpenID Connect discovery document: %w", err)
	}
	if discovery.Issuer == "" {
		return "", errors.New("the Jenkins OpenID Connect discovery document has no issuer")
	}
	return discovery.Issuer, nil
}

func stringOrNull(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
}
