package jenkins

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestCredentialGitLabAccessToken_metadata(t *testing.T) {
	r := newCredentialGitLabAccessTokenResource()
	resp := &fwresource.MetadataResponse{}
	r.Metadata(context.Background(), fwresource.MetadataRequest{ProviderTypeName: "jenkins"}, resp)
	if resp.TypeName != "jenkins_credential_gitlab_access_token" {
		t.Errorf("TypeName = %q, want %q", resp.TypeName, "jenkins_credential_gitlab_access_token")
	}
}

func TestCredentialGitLabAccessToken_schema(t *testing.T) {
	r := newCredentialGitLabAccessTokenResource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)

	for _, attr := range []string{"id", "name", "folder", "description", "domain", "scope", "token_type", "token", "token_wo", "token_wo_version"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("Schema() missing attribute %q", attr)
		}
	}
}

func TestGitLabAccessTokenCredentials_xml(t *testing.T) {
	spec := gitLabAccessTokenCredentialSpec()
	for tokenType, class := range gitLabAccessTokenClasses {
		t.Run(tokenType, func(t *testing.T) {
			// The class is the root element, and reading it back recovers the type.
			data := fmt.Sprintf(`<%s plugin="gitlab-branch-source@1"><scope>GLOBAL</scope><id>gl</id><description>d</description><token><secret-redacted/></token></%s>`, class, class)
			api := spec.newAPIValue()
			if err := xml.Unmarshal([]byte(data), api); err != nil {
				t.Fatal(err)
			}
			var m credentialGitLabAccessTokenResourceModel
			spec.fromAPI(api, &m)
			if m.TokenType.ValueString() != tokenType {
				t.Errorf("token_type = %q, want %q", m.TokenType.ValueString(), tokenType)
			}

			built := spec.build(&m, map[string]string{"token": "glpat-x"})
			out, err := xml.Marshal(built)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(out), "<"+class+">") || !strings.Contains(string(out), "<token>glpat-x</token>") {
				t.Errorf("xml.Marshal = %s, want root %s with the token", out, class)
			}
		})
	}
}

func TestAccJenkinsCredentialGitLabAccessToken_basic(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	folder := "tf-acc-test-" + randString
	name := "jenkins_credential_gitlab_access_token.foo"
	const (
		first  = "glgat-firsttokenvalue0000"
		second = "glgat-secondtokenvalue000"
	)

	config := func(description, tokenType, token string) string {
		return fmt.Sprintf(`
		resource jenkins_folder foo {
		  name = %q
		}

		resource jenkins_credential_gitlab_access_token foo {
		  name        = "test-gitlab-token"
		  folder      = jenkins_folder.foo.id
		  description = %q
		  token_type  = %q
		  token       = %q
		}`, folder, description, tokenType, token)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialGitLabAccessTokenDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config("first", "group", first),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", "/job/"+folder+"/test-gitlab-token"),
					resource.TestCheckResourceAttr(name, "token_type", "group"),
					testAccCheckJenkinsCredentialGitLabAccessTokenExists(name),
					testAccCheckCredentialSecretEquals(folder, "test-gitlab-token", "token", first),
				),
			},
			{
				Config: config("first", "group", first),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     folder + "/_/test-gitlab-token",
				ImportStateVerify: true,
				// Import takes the folder without the "/job/" prefix the folder
				// resource's id carries, so folder and id read back differently.
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"token", "folder", "id"},
			},
			{
				// A description change updates in place and keeps the token.
				Config: config("second", "group", first),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "description", "second"),
					testAccCheckCredentialSecretEquals(folder, "test-gitlab-token", "token", first),
				),
			},
			{
				Config: config("second", "group", second),
				Check:  testAccCheckCredentialSecretEquals(folder, "test-gitlab-token", "token", second),
			},
			{
				// The kind is the stored class, so changing it replaces the credential.
				Config: config("second", "personal", second),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "token_type", "personal"),
					testAccCheckCredentialSecretEquals(folder, "test-gitlab-token", "token", second),
				),
			},
		},
	})
}

func testAccCheckJenkinsCredentialGitLabAccessTokenExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return errors.New(resourceName + " not found")
		}
		cred := GitLabAccessTokenCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		if err := manager.GetSingle(context.Background(), rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], &cred); err != nil {
			return fmt.Errorf("unable to retrieve credentials for %s - %s: %w", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], err)
		}
		return nil
	}
}

func testAccCheckJenkinsCredentialGitLabAccessTokenDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_gitlab_access_token" {
			continue
		}
		cred := GitLabAccessTokenCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		if err := manager.GetSingle(context.Background(), rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], &cred); err == nil {
			return fmt.Errorf("credentials still exists: %s - %s", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"])
		}
	}
	return nil
}
