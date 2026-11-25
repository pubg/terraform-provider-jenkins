package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsFolder_basic(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
				  name = "tf-acc-test-%s"
				  description = "Terraform acceptance tests %s"
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "display_name", ""),
				),
			},
			{
				ResourceName:            "jenkins_folder.foo",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"template"},
			},
		},
	})
}

func TestAccJenkinsFolder_permittedKubernetesClouds(t *testing.T) {
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := func(description, allowedClouds string) string {
		return fmt.Sprintf(`
resource "jenkins_folder" "team" {
  name        = %q
  description = %q
  %s
}`, name, description, allowedClouds)
	}
	checkGrant := resource.ComposeTestCheckFunc(
		resource.TestCheckTypeSetElemAttr("jenkins_folder.team", "permitted_kubernetes_clouds.*", "cloud-west"),
		resource.TestCheckTypeSetElemAttr("jenkins_folder.team", "permitted_kubernetes_clouds.*", "cloud-east"),
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("Grant two Clouds", `permitted_kubernetes_clouds = ["cloud-west", "cloud-east"]`),
				Check:  checkGrant,
			},
			{
				Config: config("Preserve the grants", ""),
				Check:  checkGrant,
			},
			{
				Config: config("Remove the direct grants", "permitted_kubernetes_clouds = []"),
				Check:  resource.TestCheckResourceAttr("jenkins_folder.team", "permitted_kubernetes_clouds.#", "0"),
			},
		},
	})
}

func TestAccJenkinsFolder_pipelineLibraries(t *testing.T) {
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := func(description, libraries string) string {
		return fmt.Sprintf(`
resource "jenkins_folder" "team" {
  name        = %q
  description = %q
  %s
}`, name, description, libraries)
	}
	const twoLibraries = `pipeline_libraries = [
    {
      name               = "team-steps"
      default_version    = "main"
      git_remote         = "https://example.com/team/steps.git"
      git_credentials_id = "git-token"
    },
    {
      name                   = "team-defaults"
      default_version        = "stable"
      implicit               = true
      allow_version_override = false
      include_in_changesets  = false
      git_remote             = "https://example.com/team/defaults.git"
    },
  ]`
	checkLibraries := resource.ComposeTestCheckFunc(
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.#", "2"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.0.name", "team-steps"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.0.default_version", "main"),
		// The first library omits the flags, so Jenkins holds the defaults
		// and state keeps them unset.
		resource.TestCheckNoResourceAttr("jenkins_folder.team", "pipeline_libraries.0.implicit"),
		resource.TestCheckNoResourceAttr("jenkins_folder.team", "pipeline_libraries.0.allow_version_override"),
		resource.TestCheckNoResourceAttr("jenkins_folder.team", "pipeline_libraries.0.include_in_changesets"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.0.git_remote", "https://example.com/team/steps.git"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.0.git_credentials_id", "git-token"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.1.name", "team-defaults"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.1.implicit", "true"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.1.allow_version_override", "false"),
		resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.1.include_in_changesets", "false"),
		resource.TestCheckNoResourceAttr("jenkins_folder.team", "pipeline_libraries.1.git_credentials_id"),
		// Jenkins itself has to have accepted the libraries, not just stored
		// the XML it was sent.
		resource.TestMatchResourceAttr("jenkins_folder.team", "template", regexp.MustCompile(`jenkins\.plugins\.git\.GitSCMSource`)),
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("Configure two libraries", twoLibraries),
				Check:  checkLibraries,
			},
			{
				// Re-applying the same configuration must plan no change.
				Config: config("Configure two libraries", twoLibraries),
				Check:  checkLibraries,
			},
			{
				// With the attribute omitted the libraries are unmanaged: they
				// stay on the folder and leave state.
				Config: config("Leave the libraries alone", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckNoResourceAttr("jenkins_folder.team", "pipeline_libraries.#"),
					resource.TestMatchResourceAttr("jenkins_folder.team", "template", regexp.MustCompile(`<name>team-steps</name>[\s\S]*<name>team-defaults</name>`)),
				),
			},
			{
				// Taking the libraries back under management plans cleanly.
				Config: config("Manage the libraries again", twoLibraries),
				Check:  checkLibraries,
			},
			{
				Config: config("Remove the libraries", "pipeline_libraries = []"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.team", "pipeline_libraries.#", "0"),
					resource.TestCheckResourceAttrWith("jenkins_folder.team", "template", func(template string) error {
						if strings.Contains(template, folderLibrariesProperty) {
							return fmt.Errorf("the folder still carries %s:\n%s", folderLibrariesProperty, template)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_withDisplayName(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
				  name = "tf-acc-test-%s"
				  display_name = "TF Acceptance Test %s"
				  description = "Terraform acceptance tests %s"
				}`, randString, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "display_name", "TF Acceptance Test "+randString),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_nested(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
					name = "tf-acc-test-%s"
					description = "Terraform acceptance tests %s"
				}

				resource jenkins_folder sub {
					name = "subfolder"
                    display_name = "TF Acceptance Test %s"
					folder = jenkins_folder.foo.id
					description = "Terraform acceptance tests ${jenkins_folder.foo.name}"
				}`, randString, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "id", "/job/tf-acc-test-"+randString+"/job/subfolder"),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "folder", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "display_name", "TF Acceptance Test "+randString),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_withSecurity(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = "tf-acc-test-%s"
				  description = "Terraform acceptance tests %s"
				  security {
				    inheritance_strategy = "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"
				    permissions = [
				      "hudson.model.Item.Discover:anonymous",
				    ]
				  }
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "security.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("jenkins_folder.foo", "security.*", map[string]string{
						"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
						"permissions.#":        "1",
					}),
					testAccCheckJenkinsFolderHasPermission("jenkins_folder.foo", "hudson.model.Item.Discover:anonymous"),
				),
			},
		},
	})
}

// testAccCheckJenkinsFolderHasPermission asserts that one of the resource's
// "security.*.permissions.*" attributes (a set nested inside a set, so the
// exact flatmap keys are hash-based rather than index-based) equals want.
func testAccCheckJenkinsFolderHasPermission(resourceName, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}

		for k, v := range rs.Primary.Attributes {
			if strings.Contains(k, "permissions.") && v == want {
				return nil
			}
		}
		return fmt.Errorf("no %q attribute matching %q found on %s; attributes: %v", "security.*.permissions.*", want, resourceName, rs.Primary.Attributes)
	}
}

func testAccCheckJenkinsFolderDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_folder" {
			continue
		}

		_, err := testAccClient.GetJob(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("Folder %s still exists", rs.Primary.ID)
		}
	}

	return nil
}

// TestAccJenkinsFolder_withEmptySecurityPermissions covers `permissions = []`
// end to end. Jenkins persists an AuthorizationMatrixProperty with zero
// <permission> children, which encoding/xml decodes into a nil slice —
// reflected naively that becomes a *null* set and Terraform rejects the apply
// with "planned set element ... does not correlate with any element in actual".
// The second step re-applies the same config so a plan/refresh cycle has to
// agree with the server too.
func TestAccJenkinsFolder_withEmptySecurityPermissions(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = "tf-acc-test-%s"
				  description = "Terraform acceptance tests %s"
				  security {
				    inheritance_strategy = "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy"
				    permissions          = []
				  }
				}`, randString, randString)

	check := resource.ComposeTestCheckFunc(
		resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
		resource.TestCheckResourceAttr("jenkins_folder.foo", "security.#", "1"),
		resource.TestCheckTypeSetElemNestedAttrs("jenkins_folder.foo", "security.*", map[string]string{
			"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
			"permissions.#":        "0",
		}),
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{Config: config, Check: check},
			{Config: config, Check: check},
		},
	})
}
