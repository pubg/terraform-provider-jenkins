package jenkins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// preserveCase describes one credential kind for TestCredentialUpdatePreservesSecret.
type preserveCase[M any] struct {
	// newResource returns the resource's Update, its schema, and a setter for its client.
	newResource func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter))
	// base returns a model whose secret is the plain value "current".
	base func() M
	// setDescription and setSecret change the metadata and the plain secret.
	setDescription func(*M, string)
	setSecret      func(*M, string)
	// control is the secret's control in the update form, and xmlTag its config.xml element.
	control string
	xmlTag  string
}

func runPreserveCases[M any](t *testing.T, c preserveCase[M]) {
	t.Helper()
	const path = "/credentials/store/system/domain/_/credential/cred"
	tests := []struct {
		name        string
		rotate      bool
		form        string
		wantErr     bool
		wantSecret  string
		wantFetched bool
	}{
		{name: "metadata change keeps the stored secret", form: `<form action="updateSubmit">` + c.control + `</form>`, wantSecret: scmRetainedSecret, wantFetched: true},
		{name: "replace plain secret", rotate: true, wantSecret: "next"},
		{name: "fail without an encrypted secret", form: `<form action="updateSubmit"></form>`, wantErr: true, wantFetched: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &scmCredentialServer{t: t, path: path, form: tt.form}
			ts := httptest.NewServer(http.HandlerFunc(srv.handler))
			defer ts.Close()

			update, schema, setClient := c.newResource()
			client, err := newJenkinsClient(&Config{ServerURL: ts.URL})
			if err != nil {
				t.Fatal(err)
			}
			setClient(client)

			prior := c.base()
			after := prior
			c.setDescription(&after, "updated")
			if tt.rotate {
				c.setSecret(&after, "next")
			}
			resp := runCredentialUpdate(t, update, schema, prior, after, after)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("unexpected update diagnostics: %v", resp.Diagnostics)
			}
			if srv.formFetched != tt.wantFetched {
				t.Errorf("update form fetched = %v, want %v", srv.formFetched, tt.wantFetched)
			}
			if tt.wantErr {
				if srv.posted != nil {
					t.Error("must not overwrite the credential if the stored secret is unavailable")
				}
				return
			}
			m := regexp.MustCompile(`<` + c.xmlTag + `>([^<]*)</` + c.xmlTag + `>`).FindSubmatch(srv.posted)
			if m == nil || string(m[1]) != tt.wantSecret {
				t.Errorf("POST <%s> = %q, want %q in %s", c.xmlTag, m, tt.wantSecret, srv.posted)
			}
		})
	}
}

func hiddenSecret(name string) string {
	return `<input type="hidden" name="` + name + `" value="` + scmRetainedSecret + `">`
}

// The credential's config.xml replaces the whole credential and its GET redacts
// every secret, so an update that leaves a secret unchanged must re-send the
// encrypted value from the update form. Sending nothing stores an empty secret.
func TestCredentialUpdatePreservesSecret(t *testing.T) {
	common := func(id string) (types.String, types.String, types.String, types.String, types.String) {
		return types.StringValue("/" + id), types.StringValue(id), types.StringValue("_"), types.StringValue("original"), types.StringValue("GLOBAL")
	}

	t.Run("secret_text", func(t *testing.T) {
		runPreserveCases(t, preserveCase[credentialSecretTextResourceModel]{
			newResource: func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter)) {
				r := newCredentialSecretTextResource().(*credentialSecretTextResource)
				var s resource.SchemaResponse
				r.Schema(context.Background(), resource.SchemaRequest{}, &s)
				return r.Update, s, func(c *jenkinsAdapter) { r.client = c }
			},
			base: func() credentialSecretTextResourceModel {
				m := credentialSecretTextResourceModel{Secret: types.StringValue("current")}
				m.ID, m.Name, m.Domain, m.Description, m.Scope = common("cred")
				return m
			},
			setDescription: func(m *credentialSecretTextResourceModel, v string) { m.Description = types.StringValue(v) },
			setSecret:      func(m *credentialSecretTextResourceModel, v string) { m.Secret = types.StringValue(v) },
			control:        hiddenSecret("_.secret"),
			xmlTag:         "secret",
		})
	})

	t.Run("username", func(t *testing.T) {
		runPreserveCases(t, preserveCase[credentialUsernameResourceModel]{
			newResource: func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter)) {
				r := newCredentialUsernameResource().(*credentialUsernameResource)
				var s resource.SchemaResponse
				r.Schema(context.Background(), resource.SchemaRequest{}, &s)
				return r.Update, s, func(c *jenkinsAdapter) { r.client = c }
			},
			base: func() credentialUsernameResourceModel {
				m := credentialUsernameResourceModel{Username: types.StringValue("u"), Password: types.StringValue("current")}
				m.ID, m.Name, m.Domain, m.Description, m.Scope = common("cred")
				return m
			},
			setDescription: func(m *credentialUsernameResourceModel, v string) { m.Description = types.StringValue(v) },
			setSecret:      func(m *credentialUsernameResourceModel, v string) { m.Password = types.StringValue(v) },
			control:        hiddenSecret("_.password"),
			xmlTag:         "password",
		})
	})

	t.Run("ssh passphrase", func(t *testing.T) {
		runPreserveCases(t, preserveCase[credentialSSHResourceModel]{
			newResource: func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter)) {
				r := newCredentialSSHResource().(*credentialSSHResource)
				var s resource.SchemaResponse
				r.Schema(context.Background(), resource.SchemaRequest{}, &s)
				return r.Update, s, func(c *jenkinsAdapter) { r.client = c }
			},
			base: func() credentialSSHResourceModel {
				m := credentialSSHResourceModel{Username: types.StringValue("u"), PrivateKey: types.StringValue("key"), Passphrase: types.StringValue("current")}
				m.ID, m.Name, m.Domain, m.Description, m.Scope = common("cred")
				return m
			},
			setDescription: func(m *credentialSSHResourceModel, v string) { m.Description = types.StringValue(v) },
			setSecret:      func(m *credentialSSHResourceModel, v string) { m.Passphrase = types.StringValue(v) },
			control:        hiddenSecret("_.passphrase"),
			xmlTag:         "passphrase",
		})
	})

	t.Run("aws", func(t *testing.T) {
		runPreserveCases(t, preserveCase[credentialAwsResourceModel]{
			newResource: func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter)) {
				r := newCredentialAwsResource().(*credentialAwsResource)
				var s resource.SchemaResponse
				r.Schema(context.Background(), resource.SchemaRequest{}, &s)
				return r.Update, s, func(c *jenkinsAdapter) { r.client = c }
			},
			base: func() credentialAwsResourceModel {
				m := credentialAwsResourceModel{AccessKey: types.StringValue("AKIA"), SecretKey: types.StringValue("current")}
				m.ID, m.Name, m.Domain, m.Description, m.Scope = common("cred")
				return m
			},
			setDescription: func(m *credentialAwsResourceModel, v string) { m.Description = types.StringValue(v) },
			setSecret:      func(m *credentialAwsResourceModel, v string) { m.SecretKey = types.StringValue(v) },
			control:        hiddenSecret("_.secretKey"),
			xmlTag:         "secretKey",
		})
	})

	t.Run("azure_service_principal", func(t *testing.T) {
		runPreserveCases(t, preserveCase[credentialAzureServicePrincipalResourceModel]{
			newResource: func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter)) {
				r := newCredentialAzureServicePrincipalResource().(*credentialAzureServicePrincipalResource)
				var s resource.SchemaResponse
				r.Schema(context.Background(), resource.SchemaRequest{}, &s)
				return r.Update, s, func(c *jenkinsAdapter) { r.client = c }
			},
			base: func() credentialAzureServicePrincipalResourceModel {
				m := credentialAzureServicePrincipalResourceModel{
					SubscriptionId: types.StringValue("s"), ClientId: types.StringValue("c"), Tenant: types.StringValue("t"),
					ClientSecret: types.StringValue("current"),
				}
				m.ID, m.Name, m.Domain, m.Description, m.Scope = common("cred")
				return m
			},
			setDescription: func(m *credentialAzureServicePrincipalResourceModel, v string) { m.Description = types.StringValue(v) },
			setSecret:      func(m *credentialAzureServicePrincipalResourceModel, v string) { m.ClientSecret = types.StringValue(v) },
			control:        hiddenSecret("_.clientSecret"),
			xmlTag:         "clientSecret",
		})
	})

	t.Run("vault_approle", func(t *testing.T) {
		runPreserveCases(t, preserveCase[credentialVaultAppRoleResourceModel]{
			newResource: func() (func(context.Context, resource.UpdateRequest, *resource.UpdateResponse), resource.SchemaResponse, func(*jenkinsAdapter)) {
				r := newCredentialVaultAppRoleResource().(*credentialVaultAppRoleResource)
				var s resource.SchemaResponse
				r.Schema(context.Background(), resource.SchemaRequest{}, &s)
				return r.Update, s, func(c *jenkinsAdapter) { r.client = c }
			},
			base: func() credentialVaultAppRoleResourceModel {
				m := credentialVaultAppRoleResourceModel{RoleID: types.StringValue("r"), Path: types.StringValue("approle"), SecretID: types.StringValue("current")}
				m.ID, m.Name, m.Domain, m.Description, m.Scope = common("cred")
				return m
			},
			setDescription: func(m *credentialVaultAppRoleResourceModel, v string) { m.Description = types.StringValue(v) },
			setSecret:      func(m *credentialVaultAppRoleResourceModel, v string) { m.SecretID = types.StringValue(v) },
			// The Vault AppRole form names the control without the "_." prefix.
			control: hiddenSecret("secretId"),
			xmlTag:  "secretId",
		})
	})
}

// An SSH key without a passphrase stores no Secret, and its form renders the
// passphrase as an empty text field; the update keeps it empty instead of
// failing for want of an encrypted value.
func TestCredentialSSHUpdateKeepsEmptyPassphrase(t *testing.T) {
	const path = "/credentials/store/system/domain/_/credential/cred"
	srv := &scmCredentialServer{t: t, path: path, form: `<form action="updateSubmit"><input type="text" name="_.passphrase" value=""></form>`}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	r := newCredentialSSHResource().(*credentialSSHResource)
	var err error
	r.client, err = newJenkinsClient(&Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	var s resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &s)
	prior := credentialSSHResourceModel{
		ID: types.StringValue("/cred"), Name: types.StringValue("cred"), Domain: types.StringValue("_"),
		Description: types.StringValue("original"), Scope: types.StringValue("GLOBAL"),
		Username: types.StringValue("u"), PrivateKey: types.StringValue("key"),
	}
	after := prior
	after.Description = types.StringValue("updated")
	resp := runCredentialUpdate(t, r.Update, s, prior, after, after)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected update diagnostics: %v", resp.Diagnostics)
	}
	if !srv.formFetched {
		t.Error("update did not read the form")
	}
	// gojenkins omits an empty passphrase, which Jenkins stores as no passphrase.
	if m := regexp.MustCompile(`<passphrase>([^<]*)</passphrase>`).FindSubmatch(srv.posted); m != nil && len(m[1]) != 0 {
		t.Errorf("POST passphrase = %q, want it empty or omitted in %s", m[1], srv.posted)
	}
}
