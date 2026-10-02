package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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
		config, err := testAccClient.GetConfigFile(ctx, rs.Primary.ID, rs.Primary.Attributes["folder"])
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
		mockGetConfigFile: func(_ context.Context, id, folder string) (*managedConfigFile, error) {
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

func TestAccJenkinsConfigFile_folders(t *testing.T) {
	parent := "tf-acc-config-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	id := parent + "-file"
	config := func(content string) string {
		return fmt.Sprintf(`
resource "jenkins_folder" "parent" { name = %q }
resource "jenkins_folder" "child" {
  name = "nested"
  folder = jenkins_folder.parent.id
}
resource "jenkins_config_file" "global" {
  id = %q
  name = "Global"
  folder = null
  content = "global"
}
resource "jenkins_config_file" "parent" {
  id = %q
  name = "Parent"
  folder = jenkins_folder.parent.id
  content = "parent"
}
resource "jenkins_config_file" "child" {
  id = %q
  name = "Child"
  folder = "${jenkins_folder.parent.name}/${jenkins_folder.child.name}"
  content_type = "json"
  content = %q
}`, parent, id, id, id, content)
	}
	check := func(childContent string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckNoResourceAttr("jenkins_config_file.global", "folder"),
			resource.TestCheckResourceAttr("jenkins_config_file.parent", "folder", "/job/"+parent),
			resource.TestCheckResourceAttr("jenkins_config_file.child", "folder", parent+"/nested"),
			func(_ *terraform.State) error {
				for folder, content := range map[string]string{"": "global", parent: "parent", parent + "/nested": childContent} {
					got, err := testAccClient.GetConfigFile(context.Background(), id, folder)
					if err != nil {
						return err
					}
					if got == nil || got.Content != content {
						return fmt.Errorf("folder %q: got %#v, want content %q", folder, got, content)
					}
				}
				return nil
			},
		)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsConfigFileDestroy,
		Steps: []resource.TestStep{
			{Config: config(`{"version":1}`), Check: check(`{"version":1}`)},
			{Config: config(`{"version":1}`), PlanOnly: true},
			{Config: config(`{"version":2}`), Check: check(`{"version":2}`)},
			// The harness matches resources by ID by default; these intentionally share one.
			{ResourceName: "jenkins_config_file.child", ImportState: true, ImportStateId: parent + "/nested:" + id, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
			{ResourceName: "jenkins_config_file.parent", ImportState: true, ImportStateId: "/job/" + parent + ":" + id, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
			{ResourceName: "jenkins_config_file.global", ImportState: true, ImportStateId: id, ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "name"},
		},
	})
}

func TestAccJenkinsConfigFile_folderMoves(t *testing.T) {
	parent := "tf-acc-move-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	id := parent + "-file"
	config := func(folder string) string {
		return fmt.Sprintf(`
resource "jenkins_folder" "parent" { name = %q }
resource "jenkins_folder" "child" {
  name = "nested"
  folder = jenkins_folder.parent.id
}
resource "jenkins_config_file" "moving" {
  id = %q
  name = "Moving"
  folder = %s
  content = "moving"
}`, parent, id, folder)
	}
	check := func(wantFolder string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			for _, folder := range []string{"", parent, parent + "/nested"} {
				got, err := testAccClient.GetConfigFile(context.Background(), id, folder)
				if err != nil {
					return err
				}
				if (got != nil) != (folder == wantFolder) {
					return fmt.Errorf("unexpected file in folder %q: %#v", folder, got)
				}
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsConfigFileDestroy,
		Steps: []resource.TestStep{
			{Config: config("null"), Check: check("")},
			{Config: config("jenkins_folder.parent.id"), Check: check(parent)},
			{Config: config("jenkins_folder.child.id"), Check: check(parent + "/nested")},
			{Config: config("null"), Check: check("")},
			{Config: config("null"), PlanOnly: true},
		},
	})
}

func TestConfigFileImportScopes(t *testing.T) {
	ctx := context.Background()
	r := newConfigFileResource().(*configFileResource)
	var schema fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schema)
	for _, tt := range []struct {
		input, id, folder string
		invalid           bool
	}{
		{"app", "app", "", false},
		{"team/nested:app", "app", "team/nested", false},
		{"/job/team/job/nested:app", "app", "/job/team/job/nested", false},
		{"", "", "", true},
		{":app", "", "", true},
		{"team:", "", "", true},
		{"team:app:other", "", "", true},
		{"../team:app", "", "", true},
	} {
		t.Run(tt.input, func(t *testing.T) {
			resp := fwresource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema, Raw: tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil)}}
			r.ImportState(ctx, fwresource.ImportStateRequest{ID: tt.input}, &resp)
			if resp.Diagnostics.HasError() != tt.invalid {
				t.Fatalf("diagnostics = %v", resp.Diagnostics)
			}
			if tt.invalid {
				return
			}
			var data configFileResourceModel
			if diags := resp.State.Get(ctx, &data); diags.HasError() {
				t.Fatal(diags)
			}
			if data.ID.ValueString() != tt.id || data.Folder.ValueString() != tt.folder || (tt.folder == "" && !data.Folder.IsNull()) {
				t.Fatalf("state = %#v", data)
			}
		})
	}
}
