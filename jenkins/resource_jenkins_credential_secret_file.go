package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/net/html"
)

type credentialSecretFileResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Folder               types.String `tfsdk:"folder"`
	Description          types.String `tfsdk:"description"`
	Domain               types.String `tfsdk:"domain"`
	Scope                types.String `tfsdk:"scope"`
	Filename             types.String `tfsdk:"filename"`
	SecretBytes          types.String `tfsdk:"secretbytes"`
	SecretBytesWo        types.String `tfsdk:"secretbytes_wo"`
	SecretBytesWoVersion types.String `tfsdk:"secretbytes_wo_version"`
}

type credentialSecretFileResource struct {
	*credentialCRUD[credentialSecretFileResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialSecretFileResource{}
var _ resource.ResourceWithImportState = &credentialSecretFileResource{}
var _ resource.ResourceWithConfigValidators = &credentialSecretFileResource{}

func newCredentialSecretFileResource() resource.Resource {
	return &credentialSecretFileResource{
		credentialCRUD: newCredentialCRUD(secretFileCredentialSpec()),
	}
}

// secretFileCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go).
func secretFileCredentialSpec() credentialSpec[credentialSecretFileResourceModel] {
	return credentialSpec[credentialSecretFileResourceModel]{
		identity: func(m *credentialSecretFileResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialSecretFileResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialSecretFileResourceModel]{{
			name:         "secretbytes",
			hasWriteOnly: true,
			plainValue:   func(m *credentialSecretFileResourceModel) types.String { return m.SecretBytes },
			woVersion:    func(m *credentialSecretFileResourceModel) types.String { return m.SecretBytesWoVersion },
			preserve:     readEncryptedSecretFile,
		}},
		build: func(m *credentialSecretFileResourceModel, secrets map[string]string) interface{} {
			cred := &jenkins.FileCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				Filename:    m.Filename.ValueString(),
			}
			if s, ok := secrets["secretbytes"]; ok {
				cred.SecretBytes = s
			}
			return cred
		},
		newAPIValue: func() interface{} { return &jenkins.FileCredentials{} },
		fromAPI: func(api interface{}, m *credentialSecretFileResourceModel) {
			// NOTE: the secret bytes are intentionally not read back — GetSingle
			// returns a placeholder. Only Create/Update send them.
			cred := api.(*jenkins.FileCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Filename = types.StringValue(cred.Filename)
		},
	}
}

var encryptedSecretBytes = regexp.MustCompile(`^\{[A-Za-z0-9+/]+={0,2}\}$`)

// config.xml replaces the entire credential and its GET redacts the secret.
// The update form carries Jenkins' encrypted SecretBytes for an unchanged file.
func readEncryptedSecretFile(ctx context.Context, cm *jenkins.CredentialsManager, domain, name string) (string, error) {
	base := "/credentials/store/system"
	if cm.Folder != "" {
		base = "/job/" + cm.Folder + "/credentials/store/folder"
	}
	endpoint := base + "/domain/" + url.PathEscape(domain) + "/credential/" + url.PathEscape(name)
	client := jenkinsAdapter{Jenkins: cm.J}
	body, err := client.getConfigFilePage(ctx, endpoint+"/updateDialog", nil)
	if isNotFound(err) {
		body, err = client.getConfigFilePage(ctx, endpoint+"/update", nil)
	}
	if err != nil {
		return "", fmt.Errorf("cannot preserve the existing secret file: %w", err)
	}
	defer func() { _ = body.Close() }()

	doc, err := html.Parse(body)
	if err != nil {
		return "", errors.New("cannot parse the Jenkins secret file update form")
	}
	var count int
	var encrypted string
	walkHTML(doc, func(node *html.Node) {
		action, _ := htmlAttribute(node, "action")
		if node.Type != html.ElementNode || node.Data != "form" || action != "updateSubmit" {
			return
		}
		walkHTML(node, func(input *html.Node) {
			field, _ := htmlAttribute(input, "name")
			if input.Type == html.ElementNode && input.Data == "input" && field == "_.secretBytes" {
				count++
				encrypted, _ = htmlAttribute(input, "value")
			}
		})
	})
	if count != 1 || !encryptedSecretBytes.MatchString(encrypted) {
		return "", errors.New("cannot preserve the existing secret file: expected one encrypted file value in the Jenkins update form")
	}
	return encrypted, nil
}

// Metadata should return the full name of the resource.
func (r *credentialSecretFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_file"
}

// Schema should return the schema for this resource.
func (r *credentialSecretFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a secret file credential within Jenkins. This secret file may then be referenced within jobs that are created.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"filename": schema.StringAttribute{
				MarkdownDescription: "The secret file filename on jenkins server side.",
				Required:            true,
			},
			"secretbytes": schema.StringAttribute{
				MarkdownDescription: "The secret file, base64 encoded content. It can be sourced directly from local file with filebase64(path) TF function or given directly.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "secretbytes", "The secret file, base64 encoded content.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialSecretFileResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("secretbytes")
}
