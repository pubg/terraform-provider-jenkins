package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsCredentialOIDCIDToken_basic(t *testing.T) {
	const name = "jenkins_credential_oidc_id_token.foo"
	var first, second credentialOIDCIDToken

	config := func(audience string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_oidc_id_token foo {
		  name     = "test-oidc-id-token"
		  audience = %q
		}`, audience)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialOIDCIDTokenDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("https://first.example.com"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", "/test-oidc-id-token"),
					resource.TestCheckResourceAttr(name, "scope", "GLOBAL"),
					resource.TestCheckResourceAttr(name, "description", "Managed by Terraform"),
					resource.TestCheckResourceAttr(name, "audience", "https://first.example.com"),
					resource.TestCheckNoResourceAttr(name, "issuer"),
					resource.TestCheckResourceAttr(name, "effective_issuer", testAccOIDCIssuerBase()),
					testAccCheckJenkinsCredentialOIDCIDTokenExists(name, &first),
				),
			},
			{
				// The same configuration must agree with what Jenkins reports.
				Config: config("https://first.example.com"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     "_/test-oidc-id-token",
				ImportStateVerify: true,
			},
			{
				// Jenkins cannot change the credential without a new signing
				// key, so the change has to be planned as a replacement.
				Config: config("https://second.example.com"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialOIDCIDTokenExists(name, &second),
					func(*terraform.State) error {
						if second.Audience != "https://second.example.com" {
							return fmt.Errorf("audience in Jenkins = %q", second.Audience)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccJenkinsCredentialOIDCIDToken_folder(t *testing.T) {
	const name = "jenkins_credential_oidc_id_token.foo"
	var cred credentialOIDCIDToken
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	config := fmt.Sprintf(`
	resource jenkins_folder foo {
	  name = "tf-acc-test-%s"
	}

	resource jenkins_credential_oidc_id_token foo {
	  name        = "test-oidc-id-token"
	  folder      = jenkins_folder.foo.id
	  description = "folder token"
	  issuer      = "https://issuer.example.com/jenkins"
	}`, randString)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialOIDCIDTokenDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", "/job/tf-acc-test-"+randString+"/test-oidc-id-token"),
					resource.TestCheckResourceAttr(name, "description", "folder token"),
					resource.TestCheckResourceAttr(name, "issuer", "https://issuer.example.com/jenkins"),
					resource.TestCheckResourceAttr(name, "effective_issuer", "https://issuer.example.com/jenkins"),
					resource.TestCheckNoResourceAttr(name, "audience"),
					testAccCheckJenkinsCredentialOIDCIDTokenExists(name, &cred),
				),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// Without an issuer of its own, a folder credential uses the folder's issuer,
// which Jenkins derives from its configured root URL.
func TestAccJenkinsCredentialOIDCIDToken_folderIssuer(t *testing.T) {
	const name = "jenkins_credential_oidc_id_token.foo"
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	config := fmt.Sprintf(`
	resource jenkins_folder foo {
	  name = "tf-acc-test-%s"
	}

	resource jenkins_folder sub {
	  name   = "subfolder"
	  folder = jenkins_folder.foo.id
	}

	resource jenkins_credential_oidc_id_token foo {
	  name   = "test-oidc-id-token"
	  folder = jenkins_folder.sub.id
	}`, randString)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialOIDCIDTokenDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckNoResourceAttr(name, "issuer"),
					resource.TestCheckResourceAttr(name, "effective_issuer", testAccOIDCIssuerBase()+"/job/tf-acc-test-"+randString+"/job/subfolder"),
				),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// Jenkins serves config.xml without a charset, which turns text outside
// ISO-8859-1 into question marks. A description read that way never matches
// the configuration, and the difference forces a replacement.
func TestAccJenkinsCredentialOIDCIDToken_unicodeDescription(t *testing.T) {
	const name = "jenkins_credential_oidc_id_token.foo"
	const config = `
	resource jenkins_credential_oidc_id_token foo {
	  name        = "test-oidc-id-token-unicode"
	  description = "인증용 ID 토큰"
	}`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialOIDCIDTokenDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr(name, "description", "인증용 ID 토큰"),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

// The credentials form accepts a duplicate ID without reporting it, which would
// let Terraform adopt a credential it did not create.
func TestAccJenkinsCredentialOIDCIDToken_duplicate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialOIDCIDTokenDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_oidc_id_token first {
				  name = "test-oidc-id-token-dup"
				}

				resource jenkins_credential_oidc_id_token second {
				  name       = "test-oidc-id-token-dup"
				  depends_on = [jenkins_credential_oidc_id_token.first]
				}`,
				ExpectError: regexp.MustCompile(`already exists`),
			},
		},
	})
}

// testAccOIDCIssuerBase is the global issuer of the acceptance Jenkins, which
// has no configured root URL and so derives it from the request.
func testAccOIDCIssuerBase() string {
	return strings.TrimRight(os.Getenv("JENKINS_URL"), "/") + "/oidc"
}

func testAccCheckJenkinsCredentialOIDCIDTokenExists(resourceName string, cred *credentialOIDCIDToken) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return errors.New(resourceName + " not found")
		}

		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		*cred = credentialOIDCIDToken{}
		if err := manager.GetSingle(context.Background(), rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], cred); err != nil {
			return fmt.Errorf("Unable to retrieve credentials for %s - %s: %w", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], err)
		}
		return nil
	}
}

func testAccCheckJenkinsCredentialOIDCIDTokenDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_oidc_id_token" {
			continue
		}

		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		err := manager.GetSingle(context.Background(), rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], &credentialOIDCIDToken{})
		if err == nil {
			return fmt.Errorf("Credentials still exists: %s - %s", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"])
		}
	}
	return nil
}

// oidcIDTokenFakeJenkins stands in for the credentials store: config.xml is 404
// until the credentials form is posted, unless it starts out existing.
type oidcIDTokenFakeJenkins struct {
	mu       sync.Mutex
	exists   bool
	postCode int
	// stores controls whether a posted form actually creates the credential.
	stores bool
	posts  []*http.Request
	forms  []url.Values
	// noDiscovery makes the discovery document 404 even when the credential exists.
	noDiscovery    bool
	discoveryPaths []string
	// description is what the JSON API reports; config.xml always says "desc".
	description string
}

func (f *oidcIDTokenFakeJenkins) client(t *testing.T) *jenkinsAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "crumbIssuer"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
		case r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			f.posts = append(f.posts, r)
			f.forms = append(f.forms, form)
			if f.postCode == http.StatusOK && f.stores {
				f.exists = true
			}
			w.WriteHeader(f.postCode)
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			f.discoveryPaths = append(f.discoveryPaths, r.URL.Path)
			if !f.exists || f.noDiscovery {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"issuer":"https://jenkins.example.com`+strings.TrimSuffix(r.URL.Path, "/.well-known/openid-configuration")+`"}`)
		case strings.HasSuffix(r.URL.Path, "/api/json") && strings.Contains(r.URL.Path, "/credential/"):
			if !f.exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			description := f.description
			if description == "" {
				description = "desc"
			}
			w.Header().Set("Content-Type", "application/json;charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]string{"description": description})
		case strings.Contains(r.URL.Path, "config.xml") && f.exists:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<io.jenkins.plugins.oidc__provider.IdTokenStringCredentials plugin="oidc-provider@1">
  <scope>GLOBAL</scope>
  <id>tok</id>
  <description>desc</description>
  <privateKey><secret-redacted/></privateKey>
  <audience>aud</audience>
</io.jenkins.plugins.oidc__provider.IdTokenStringCredentials>`)
		case strings.Contains(r.URL.Path, "config.xml"):
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c
}

func oidcIDTokenTestResource(client *jenkinsAdapter) (*credentialOIDCIDTokenResource, rschema.Schema) {
	r := &credentialOIDCIDTokenResource{resourceHelper: newResourceHelper()}
	r.client = client
	var sr fwresource.SchemaResponse
	r.Schema(context.Background(), fwresource.SchemaRequest{}, &sr)
	return r, sr.Schema
}

func oidcIDTokenTestModel() *credentialOIDCIDTokenResourceModel {
	return &credentialOIDCIDTokenResourceModel{
		ID:          types.StringValue("/tok"),
		Name:        types.StringValue("tok"),
		Folder:      types.StringNull(),
		Description: types.StringValue("desc"),
		Domain:      types.StringValue(defaultCredentialDomain),
		Scope:       types.StringValue("GLOBAL"),
		Issuer:      types.StringNull(),
		Audience:    types.StringValue("aud"),

		EffectiveIssuer: types.StringValue("https://jenkins.example.com/oidc"),
	}
}

func oidcIDTokenTestCreate(t *testing.T, fake *oidcIDTokenFakeJenkins) (fwresource.CreateResponse, rschema.Schema) {
	t.Helper()
	ctx := context.Background()
	r, s := oidcIDTokenTestResource(fake.client(t))
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, oidcIDTokenTestModel()); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	resp := fwresource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, fwresource.CreateRequest{Plan: plan}, &resp)
	return resp, s
}

func TestCredentialOIDCIDToken_Create(t *testing.T) {
	t.Run("posts the credentials form and reads the credential back", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{postCode: http.StatusOK, stores: true}
		resp, _ := oidcIDTokenTestCreate(t, fake)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Create: %v", resp.Diagnostics)
		}

		if len(fake.posts) != 1 {
			t.Fatalf("POST count = %d, want 1", len(fake.posts))
		}
		if got, want := fake.posts[0].URL.Path, "/credentials/store/system/domain/_/createCredentials"; got != want {
			t.Errorf("POST path = %q, want %q", got, want)
		}
		var submitted struct {
			Credentials map[string]string `json:"credentials"`
		}
		if err := json.Unmarshal([]byte(fake.forms[0].Get("json")), &submitted); err != nil {
			t.Fatalf("json form field: %v", err)
		}
		want := map[string]string{
			"stapler-class": oidcIDTokenCredentialClass,
			"$class":        oidcIDTokenCredentialClass,
			"scope":         "GLOBAL",
			"id":            "tok",
			"description":   "desc",
			"issuer":        "",
			"audience":      "aud",
		}
		for k, v := range want {
			if got, ok := submitted.Credentials[k]; !ok || got != v {
				t.Errorf("credentials[%q] = %q (present %v), want %q", k, got, ok, v)
			}
		}

		var got credentialOIDCIDTokenResourceModel
		if d := resp.State.Get(context.Background(), &got); d.HasError() {
			t.Fatalf("State.Get: %v", d)
		}
		if got.ID.ValueString() != "/tok" || got.Audience.ValueString() != "aud" || !got.Issuer.IsNull() {
			t.Errorf("state = %+v", got)
		}
		if got, want := got.EffectiveIssuer.ValueString(), "https://jenkins.example.com/oidc"; got != want {
			t.Errorf("effective_issuer = %q, want %q", got, want)
		}
	})

	t.Run("reports a missing discovery document", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{postCode: http.StatusOK, stores: true, noDiscovery: true}
		resp, _ := oidcIDTokenTestCreate(t, fake)
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "cannot read the OpenID Connect issuer") {
			t.Fatalf("diagnostics = %v, want an issuer read error", resp.Diagnostics)
		}
	})

	t.Run("refuses an ID that is already taken", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{postCode: http.StatusOK, stores: true, exists: true}
		resp, _ := oidcIDTokenTestCreate(t, fake)
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "already exists") {
			t.Fatalf("diagnostics = %v, want an 'already exists' error", resp.Diagnostics)
		}
		if len(fake.posts) != 0 {
			t.Errorf("POST count = %d, want 0", len(fake.posts))
		}
	})

	t.Run("reports a rejected form", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{postCode: http.StatusInternalServerError}
		resp, _ := oidcIDTokenTestCreate(t, fake)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error")
		}
	})

	t.Run("reports a form Jenkins accepted without storing", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{postCode: http.StatusOK}
		resp, _ := oidcIDTokenTestCreate(t, fake)
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "did not store") {
			t.Fatalf("diagnostics = %v, want a 'did not store' error", resp.Diagnostics)
		}
	})
}

func TestCredentialOIDCIDToken_ReadUpdateDelete(t *testing.T) {
	ctx := context.Background()
	model := oidcIDTokenTestModel()

	t.Run("read refreshes from Jenkins", func(t *testing.T) {
		r, s := oidcIDTokenTestResource((&oidcIDTokenFakeJenkins{exists: true}).client(t))
		stale := *model
		stale.Audience = types.StringValue("stale")
		stale.Issuer = types.StringValue("https://stale.example.com")
		st := tfsdk.State{Schema: s}
		st.Set(ctx, &stale)
		resp := fwresource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, fwresource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read: %v", resp.Diagnostics)
		}
		var got credentialOIDCIDTokenResourceModel
		resp.State.Get(ctx, &got)
		if got.Audience.ValueString() != "aud" || !got.Issuer.IsNull() {
			t.Errorf("state = %+v", got)
		}
	})

	t.Run("read asks the folder's issuer for a folder credential", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{exists: true}
		r, s := oidcIDTokenTestResource(fake.client(t))
		inFolder := *model
		inFolder.Folder = types.StringValue("/job/team/job/sub")
		inFolder.EffectiveIssuer = types.StringNull()
		st := tfsdk.State{Schema: s}
		st.Set(ctx, &inFolder)
		resp := fwresource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, fwresource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read: %v", resp.Diagnostics)
		}
		var got credentialOIDCIDTokenResourceModel
		resp.State.Get(ctx, &got)
		if got, want := got.EffectiveIssuer.ValueString(), "https://jenkins.example.com/oidc/job/team/job/sub"; got != want {
			t.Errorf("effective_issuer = %q, want %q", got, want)
		}
	})

	t.Run("read takes the description from the JSON API", func(t *testing.T) {
		r, s := oidcIDTokenTestResource((&oidcIDTokenFakeJenkins{exists: true, description: "인증용 ID 토큰"}).client(t))
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model)
		resp := fwresource.ReadResponse{State: tfsdk.State{Schema: s}}
		r.Read(ctx, fwresource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read: %v", resp.Diagnostics)
		}
		var got credentialOIDCIDTokenResourceModel
		resp.State.Get(ctx, &got)
		if got, want := got.Description.ValueString(), "인증용 ID 토큰"; got != want {
			t.Errorf("description = %q, want %q", got, want)
		}
	})

	t.Run("read drops a credential that is gone", func(t *testing.T) {
		r, s := oidcIDTokenTestResource((&oidcIDTokenFakeJenkins{}).client(t))
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model)
		resp := fwresource.ReadResponse{State: st}
		r.Read(ctx, fwresource.ReadRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Read: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Error("expected the resource to be removed from state")
		}
	})

	t.Run("update never calls Jenkins", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{exists: true, postCode: http.StatusOK}
		r, s := oidcIDTokenTestResource(fake.client(t))
		plan := tfsdk.Plan{Schema: s}
		plan.Set(ctx, model)
		resp := fwresource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, fwresource.UpdateRequest{Plan: plan}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Update: %v", resp.Diagnostics)
		}
		if len(fake.posts) != 0 {
			t.Errorf("POST count = %d, want 0", len(fake.posts))
		}
	})

	t.Run("delete", func(t *testing.T) {
		fake := &oidcIDTokenFakeJenkins{exists: true, postCode: http.StatusOK}
		r, s := oidcIDTokenTestResource(fake.client(t))
		st := tfsdk.State{Schema: s}
		st.Set(ctx, model)
		resp := fwresource.DeleteResponse{State: st}
		r.Delete(ctx, fwresource.DeleteRequest{State: st}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Delete: %v", resp.Diagnostics)
		}
		if len(fake.posts) != 1 {
			t.Errorf("POST count = %d, want 1", len(fake.posts))
		}
	})
}

// An imported credential has to plan as unchanged: any difference forces a
// replacement, which makes Jenkins generate a new signing key.
func TestCredentialOIDCIDToken_ImportState(t *testing.T) {
	ctx := context.Background()
	r, s := oidcIDTokenTestResource(nil)

	tests := []struct {
		id         string
		wantFolder types.String
		wantID     string
	}{
		{"_/tok", types.StringNull(), "/tok"},
		{"team/sub/_/tok", types.StringValue("/job/team/job/sub"), "/job/team/job/sub/tok"},
		{"/job/team/job/sub/_/tok", types.StringValue("/job/team/job/sub"), "/job/team/job/sub/tok"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			resp := fwresource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
			r.ImportState(ctx, fwresource.ImportStateRequest{ID: tt.id}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("ImportState: %v", resp.Diagnostics)
			}
			var got credentialOIDCIDTokenResourceModel
			if d := resp.State.Get(ctx, &got); d.HasError() {
				t.Fatalf("State.Get: %v", d)
			}
			if !got.Folder.Equal(tt.wantFolder) {
				t.Errorf("folder = %v, want %v", got.Folder, tt.wantFolder)
			}
			if got.ID.ValueString() != tt.wantID || got.Name.ValueString() != "tok" || got.Domain.ValueString() != "_" {
				t.Errorf("state = %+v", got)
			}
		})
	}
}
