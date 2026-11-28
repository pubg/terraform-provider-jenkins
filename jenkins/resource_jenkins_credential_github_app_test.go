package jenkins

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestCredentialGitHubApp_metadata(t *testing.T) {
	r := newCredentialGitHubAppResource()
	resp := &fwresource.MetadataResponse{}
	r.Metadata(context.Background(), fwresource.MetadataRequest{ProviderTypeName: "jenkins"}, resp)
	if resp.TypeName != "jenkins_credential_github_app" {
		t.Errorf("TypeName = %q, want %q", resp.TypeName, "jenkins_credential_github_app")
	}
}

func TestCredentialGitHubApp_schema(t *testing.T) {
	r := newCredentialGitHubAppResource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)

	for _, attr := range []string{"id", "name", "folder", "description", "domain", "scope", "app_id", "private_key",
		"api_uri", "repository_access_strategy", "owner", "repositories", "default_permissions_strategy"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("Schema() missing attribute %q", attr)
		}
	}
}

func TestGitHubAppCredentials_xmlMarshal(t *testing.T) {
	cred := GitHubAppCredentials{
		ID:          "my-app",
		Scope:       "GLOBAL",
		Description: "test app",
		AppID:       "123456",
		PrivateKey:  "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----",
	}

	data, err := xml.Marshal(cred)
	if err != nil {
		t.Fatalf("xml.Marshal returned error: %v", err)
	}

	var got GitHubAppCredentials
	if err := xml.Unmarshal(data, &got); err != nil {
		t.Fatalf("xml.Unmarshal returned error: %v", err)
	}

	if got.ID != cred.ID {
		t.Errorf("ID = %q, want %q", got.ID, cred.ID)
	}
	if got.AppID != cred.AppID {
		t.Errorf("AppID = %q, want %q", got.AppID, cred.AppID)
	}
	if got.PrivateKey != cred.PrivateKey {
		t.Errorf("PrivateKey = %q, want %q", got.PrivateKey, cred.PrivateKey)
	}
	wantLocal := "org.jenkinsci.plugins.github__branch__source.GitHubAppCredentials"
	if got.XMLName.Local != wantLocal {
		t.Errorf("XMLName.Local = %q, want %q", got.XMLName.Local, wantLocal)
	}
}

// generateTestRSAKey returns a PKCS#1 PEM-encoded 2048-bit RSA private key for acceptance tests.
func generateTestRSAKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return string(pem.EncodeToMemory(block))
}

func TestAccJenkinsCredentialGitHubApp_basic(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialGitHubAppDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app"
				  app_id      = "12345"
				  private_key = %q
				}`, privateKey),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "id", "/test-github-app"),
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "app_id", "12345"),
					// Unset access attributes take the plugin's legacy defaults.
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "repository_access_strategy", "specificRepositories"),
					resource.TestCheckNoResourceAttr("jenkins_credential_github_app.foo", "owner"),
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "repositories.#", "0"),
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "default_permissions_strategy", "INHERIT_ALL"),
					resource.TestCheckNoResourceAttr("jenkins_credential_github_app.foo", "api_uri"),
					testAccCheckJenkinsCredentialGitHubAppExists("jenkins_credential_github_app.foo"),
				),
			},
			{
				// Update by adding description
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app"
				  description = "new-description"
				  app_id      = "12345"
				  private_key = %q
				}`, privateKey),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialGitHubAppExists("jenkins_credential_github_app.foo"),
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "description", "new-description"),
					// The key was not part of the change and must survive the update.
					testAccCheckCredentialSecretEquals("", "test-github-app", "privateKey", privateKey),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialGitHubApp_ignoreChanges(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialGitHubAppDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app-ignore"
				  app_id      = "99999"
				  private_key = %q
				}`, privateKey),
				Check: resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "id", "/test-github-app-ignore"),
			},
			{
				// Update description while ignoring private_key; key must not be overwritten.
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app-ignore"
				  description = "updated"
				  app_id      = "99999"
				  private_key = %q

				  lifecycle {
				    ignore_changes = [private_key]
				  }
				}`, privateKey),
				Check: resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "description", "updated"),
			},
		},
	})
}

func TestAccJenkinsCredentialGitHubApp_folder(t *testing.T) {
	privateKey := generateTestRSAKey(t)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialGitHubAppDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
					name = "tf-acc-test-%s"
					description = "Terraform acceptance testing"
				}

				resource jenkins_credential_github_app foo {
				  name        = "test-github-app"
				  folder      = jenkins_folder.foo.id
				  app_id      = "12345"
				  private_key = %q
				}`, randString, privateKey),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "id", "/job/tf-acc-test-"+randString+"/test-github-app"),
					testAccCheckJenkinsCredentialGitHubAppExists("jenkins_credential_github_app.foo"),
				),
			},
		},
	})
}

func testAccCheckJenkinsCredentialGitHubAppExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return errors.New(resourceName + " not found")
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		cred := GitHubAppCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		err := manager.GetSingle(ctx, rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], &cred)
		if err != nil {
			return fmt.Errorf("unable to retrieve credentials for %s - %s: %w", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], err)
		}

		return nil
	}
}

func testAccCheckJenkinsCredentialGitHubAppDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_github_app" {
			continue
		} else if _, ok := rs.Primary.Meta["name"]; !ok {
			continue
		}

		cred := GitHubAppCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Meta["folder"].(string))
		err := manager.GetSingle(ctx, rs.Primary.Meta["domain"].(string), rs.Primary.Meta["name"].(string), &cred)
		if err == nil {
			return fmt.Errorf("credentials still exists: %s - %s", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"])
		}
	}

	return nil
}

func TestGitHubAppAccessStrategyMapping(t *testing.T) {
	specified := gitHubAppAccessStrategyClasses[gitHubAppAccessSpecificRepositories]
	tests := []struct {
		name      string
		api       *gitHubAppRepositoryAccessStrategy
		wantName  string
		wantOwner types.String
		wantRepos int
	}{
		{name: "missing element is the legacy default", api: nil, wantName: gitHubAppAccessSpecificRepositories, wantOwner: types.StringNull()},
		{name: "specified owner and repositories", api: &gitHubAppRepositoryAccessStrategy{Class: specified, Owner: "org", Repositories: &gitHubAppRepository{Names: []string{"a", "b"}}},
			wantName: gitHubAppAccessSpecificRepositories, wantOwner: types.StringValue("org"), wantRepos: 2},
		{name: "specified without owner", api: &gitHubAppRepositoryAccessStrategy{Class: specified, Repositories: &gitHubAppRepository{}},
			wantName: gitHubAppAccessSpecificRepositories, wantOwner: types.StringNull()},
		{name: "infer owner", api: &gitHubAppRepositoryAccessStrategy{Class: gitHubAppAccessStrategyClasses[gitHubAppAccessInferOwner]},
			wantName: gitHubAppAccessInferOwner, wantOwner: types.StringNull()},
		{name: "infer repository", api: &gitHubAppRepositoryAccessStrategy{Class: gitHubAppAccessStrategyClasses[gitHubAppAccessInferRepository]},
			wantName: gitHubAppAccessInferRepository, wantOwner: types.StringNull()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m credentialGitHubAppResourceModel
			gitHubAppAccessStrategyFromAPI(tt.api, &m)
			if m.RepositoryAccessStrategy.ValueString() != tt.wantName {
				t.Errorf("strategy = %q, want %q", m.RepositoryAccessStrategy.ValueString(), tt.wantName)
			}
			if !m.Owner.Equal(tt.wantOwner) {
				t.Errorf("owner = %v, want %v", m.Owner, tt.wantOwner)
			}
			if len(m.Repositories.Elements()) != tt.wantRepos {
				t.Errorf("repositories = %v, want %d entries", m.Repositories, tt.wantRepos)
			}

			// Building the payload back from the model must keep the same strategy.
			back := gitHubAppAccessStrategyToAPI(&m)
			if back.Class != gitHubAppAccessStrategyClasses[tt.wantName] {
				t.Errorf("round-trip class = %q, want %q", back.Class, gitHubAppAccessStrategyClasses[tt.wantName])
			}
			if (tt.wantName == gitHubAppAccessSpecificRepositories) != (back.Repositories != nil) {
				t.Errorf("round-trip repositories element = %+v; only specificRepositories sends it", back.Repositories)
			}
		})
	}
}

func TestGitHubAppCredentials_xmlMarshalAccess(t *testing.T) {
	cred := GitHubAppCredentials{
		ID:                         "app",
		AppID:                      "1",
		RepositoryAccessStrategy:   &gitHubAppRepositoryAccessStrategy{Class: gitHubAppAccessStrategyClasses[gitHubAppAccessSpecificRepositories], Repositories: &gitHubAppRepository{Names: []string{}}},
		DefaultPermissionsStrategy: "CONTENTS_READ",
	}
	data, err := xml.Marshal(cred)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`<repositoryAccessStrategy class="org.jenkinsci.plugins.github_branch_source.app_credentials.AccessSpecifiedRepositories">`,
		// AccessSpecifiedRepositories needs a non-null list even when it is empty.
		`<repositories></repositories>`,
		`<defaultPermissionsStrategy>CONTENTS_READ</defaultPermissionsStrategy>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("xml.Marshal = %s, missing %s", got, want)
		}
	}
	if strings.Contains(got, "<apiUri>") || strings.Contains(got, "<owner>") {
		t.Errorf("xml.Marshal = %s, unset apiUri and owner must be omitted", got)
	}
}

func TestAccJenkinsCredentialGitHubApp_access(t *testing.T) {
	privateKey := generateTestRSAKey(t)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	folder := "tf-acc-test-" + randString
	name := "jenkins_credential_github_app.foo"

	config := func(access string) string {
		return fmt.Sprintf(`
		resource jenkins_folder foo {
		  name = %q
		}

		resource jenkins_credential_github_app foo {
		  name        = "test-github-app-access"
		  folder      = jenkins_folder.foo.id
		  app_id      = "12345"
		  private_key = %q
		  %s
		}`, folder, privateKey, access)
	}
	specific := `
		  api_uri                      = "https://github.example.com/api/v3"
		  owner                        = "myorg"
		  repositories                 = ["repo-a", "repo-b"]
		  default_permissions_strategy = "CONTENTS_READ"`
	inferred := `
		  repository_access_strategy   = "inferRepository"
		  default_permissions_strategy = "CONTENTS_WRITE"`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialGitHubAppDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config(`
		  repository_access_strategy = "inferOwner"
		  owner                      = "myorg"`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`owner is only valid when repository_access_strategy is`),
			},
			{
				Config: config(specific),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "api_uri", "https://github.example.com/api/v3"),
					resource.TestCheckResourceAttr(name, "repository_access_strategy", "specificRepositories"),
					resource.TestCheckResourceAttr(name, "owner", "myorg"),
					resource.TestCheckResourceAttr(name, "repositories.#", "2"),
					resource.TestCheckResourceAttr(name, "repositories.1", "repo-b"),
					resource.TestCheckResourceAttr(name, "default_permissions_strategy", "CONTENTS_READ"),
					testAccCheckCredentialSecretEquals(folder, "test-github-app-access", "privateKey", privateKey),
				),
			},
			{
				// The same configuration must agree with what Jenkins reports.
				Config: config(specific),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     folder + "/_/test-github-app-access",
				ImportStateVerify: true,
				// Import takes the folder without the "/job/" prefix the folder
				// resource's id carries, so folder and id read back differently.
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"private_key", "folder", "id"},
			},
			{
				// Switching strategy updates the credential in place and keeps the key.
				Config: config(inferred),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "repository_access_strategy", "inferRepository"),
					resource.TestCheckNoResourceAttr(name, "owner"),
					resource.TestCheckNoResourceAttr(name, "api_uri"),
					resource.TestCheckResourceAttr(name, "repositories.#", "0"),
					resource.TestCheckResourceAttr(name, "default_permissions_strategy", "CONTENTS_WRITE"),
					testAccCheckCredentialSecretEquals(folder, "test-github-app-access", "privateKey", privateKey),
				),
			},
			{
				Config: config(inferred),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
