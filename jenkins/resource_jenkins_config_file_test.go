package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsConfigFile_basic(t *testing.T) {
	id := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	resourceName := "jenkins_config_file.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsConfigFileDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccJenkinsConfigFileConfig(id, "Application config", "first", "key: <value>\n"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", id),
					resource.TestCheckResourceAttr(resourceName, "content_type", "custom"),
					resource.TestCheckResourceAttr(resourceName, "name", "Application config"),
					resource.TestCheckResourceAttr(resourceName, "comment", "first"),
					resource.TestCheckResourceAttr(resourceName, "content", "key: <value>\n"),
				),
			},
			{
				Config: testAccJenkinsConfigFileConfig(id, "Renamed config", "updated", "key: changed\n"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "Renamed config"),
					resource.TestCheckResourceAttr(resourceName, "comment", "updated"),
					resource.TestCheckResourceAttr(resourceName, "content", "key: changed\n"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccJenkinsConfigFileConfig(id, name, comment, content string) string {
	return fmt.Sprintf(`
resource "jenkins_config_file" "test" {
  id      = %q
  name    = %q
  comment = %q
  content = %q
}`, id, name, comment, content)
}

func testAccCheckJenkinsConfigFileDestroy(s *terraform.State) error {
	ctx := context.Background()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_config_file" {
			continue
		}
		config, err := testAccClient.GetConfigFile(ctx, rs.Primary.ID)
		if err != nil {
			return err
		}
		if config != nil {
			return fmt.Errorf("config file %s still exists", rs.Primary.ID)
		}
	}
	return nil
}

func TestConfigFilePopulate(t *testing.T) {
	ctx := context.Background()
	r := &configFileResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetConfigFile: func(_ context.Context, id string) (*managedConfigFile, error) {
			return &managedConfigFile{ID: id, Name: "Application", Comment: "Managed", Content: "key: value", ContentType: "json"}, nil
		},
	}}}
	data := &configFileResourceModel{ID: types.StringValue("app-config")}
	var diags diag.Diagnostics
	if found := r.populate(ctx, data, &diags); !found || diags.HasError() {
		t.Fatalf("populate() failed: found=%v diags=%v", found, diags)
	}
	if data.Name.ValueString() != "Application" || data.Content.ValueString() != "key: value" || data.ContentType.ValueString() != "json" {
		t.Errorf("populate() = %#v", data)
	}
}

func TestAccJenkinsConfigFile_contentTypes(t *testing.T) {
	for _, tt := range configFileTestTypes {
		t.Run(tt.name, func(t *testing.T) {
			id := "tf-acc-type-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
			config := strings.Replace(testAccJenkinsConfigFileConfig(id, "Typed file", "test", tt.content),
				`  id`, fmt.Sprintf("  content_type = %q\n  id", tt.name), 1)
			updated := strings.Replace(config, "Typed file", "Updated file", 1)
			check := resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("jenkins_config_file.test", "content_type", tt.name),
				resource.TestCheckResourceAttr("jenkins_config_file.test", "content", tt.content),
			)
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviders,
				CheckDestroy:             testAccCheckJenkinsConfigFileDestroy,
				Steps: []resource.TestStep{
					{Config: config, Check: check},
					{Config: config, PlanOnly: true},
					{Config: updated, Check: check},
					{ResourceName: "jenkins_config_file.test", ImportState: true, ImportStateVerify: true},
					{Config: testAccJenkinsConfigFileConfig(id, "Converted file", "test", tt.content), Check: resource.TestCheckResourceAttr("jenkins_config_file.test", "content_type", "custom")},
					{Config: config, Check: check},
				},
			})
		})
	}
}

func TestAccJenkinsConfigFile_jsonValues(t *testing.T) {
	for _, content := range []string{`"hello"`, "", "\n"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			id := "tf-acc-json-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
			config := strings.Replace(testAccJenkinsConfigFileConfig(id, "JSON value", "test", content),
				`  id`, "  content_type = \"json\"\n  id", 1)
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviders,
				CheckDestroy:             testAccCheckJenkinsConfigFileDestroy,
				Steps: []resource.TestStep{
					{Config: config, Check: resource.TestCheckResourceAttr("jenkins_config_file.test", "content", content)},
					{Config: config, PlanOnly: true},
				},
			})
		})
	}
}

func TestAccJenkinsConfigFile_invalidContentType(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{{
			Config: `resource "jenkins_config_file" "test" {
  id = "invalid-content-type"
  name = "Invalid type"
  content = "{}"
  content_type = "application/json"
}`,
			ExpectError: regexp.MustCompile(`Attribute content_type value must be one of`),
		}},
	})
}
