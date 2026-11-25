package jenkins

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// What Jenkins writes for a folder holding one Git library and one library
// with a retriever this provider does not model.
const folderWithLibraries = `<?xml version='1.1' encoding='UTF-8'?>
<com.cloudbees.hudson.plugins.folder.Folder plugin="cloudbees-folder@6.1000">
  <description>d</description>
  <properties>
    <org.jenkinsci.plugins.workflow.libs.FolderLibraries plugin="pipeline-groovy-lib@752">
      <libraries>
        <org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
          <name>team-steps</name>
          <retriever class="org.jenkinsci.plugins.workflow.libs.SCMSourceRetriever">
            <scm class="jenkins.plugins.git.GitSCMSource" plugin="git@5.7.0">
              <id>3f1c7a52</id>
              <remote>https://example.com/team/steps.git</remote>
              <credentialsId>git-token</credentialsId>
              <traits>
                <jenkins.plugins.git.traits.BranchDiscoveryTrait/>
              </traits>
            </scm>
          </retriever>
          <defaultVersion>main</defaultVersion>
          <implicit>false</implicit>
          <allowVersionOverride>true</allowVersionOverride>
          <includeInChangesets>true</includeInChangesets>
        </org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
        <org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
          <name>legacy</name>
          <retriever class="org.jenkinsci.plugins.workflow.libs.SCMRetriever">
            <scm class="hudson.scm.NullSCM"/>
          </retriever>
          <implicit>true</implicit>
          <allowVersionOverride>false</allowVersionOverride>
          <includeInChangesets>false</includeInChangesets>
        </org.jenkinsci.plugins.workflow.libs.LibraryConfiguration>
      </libraries>
    </org.jenkinsci.plugins.workflow.libs.FolderLibraries>
  </properties>
</com.cloudbees.hudson.plugins.folder.Folder>`

const folderWithoutLibraries = `<?xml version='1.1' encoding='UTF-8'?>
<com.cloudbees.hudson.plugins.folder.Folder>
  <description>d</description>
  <properties/>
</com.cloudbees.hudson.plugins.folder.Folder>`

func mustParseFolder(t *testing.T, config string) *folder {
	t.Helper()
	f, err := parseFolder(config)
	if err != nil {
		t.Fatalf("parseFolder: %v", err)
	}
	return f
}

func TestFolderPipelineLibraries(t *testing.T) {
	t.Run("absent property has no libraries", func(t *testing.T) {
		libraries, err := mustParseFolder(t, folderWithoutLibraries).Properties.pipelineLibraries()
		if err != nil || len(libraries) != 0 {
			t.Fatalf("libraries = %v, err = %v; want none", libraries, err)
		}
	})

	t.Run("reads git and unmodelled retrievers", func(t *testing.T) {
		libraries, err := mustParseFolder(t, folderWithLibraries).Properties.pipelineLibraries()
		if err != nil {
			t.Fatalf("pipelineLibraries: %v", err)
		}
		if len(libraries) != 2 {
			t.Fatalf("libraries = %d, want 2", len(libraries))
		}

		git := libraries[0]
		if !git.isGit() {
			t.Fatalf("%q is not reported as a Git library", git.Name)
		}
		if git.Name != "team-steps" || git.DefaultVersion != "main" || git.Implicit || !git.AllowVersionOverride || !git.IncludeInChangesets {
			t.Errorf("unexpected Git library: %+v", git)
		}
		if scm := git.Retriever.SCM; scm.Remote != "https://example.com/team/steps.git" || scm.CredentialsID != "git-token" || scm.ID != "3f1c7a52" {
			t.Errorf("unexpected Git source: %+v", scm)
		}

		legacy := libraries[1]
		if legacy.isGit() {
			t.Errorf("%q must not be reported as a Git library", legacy.Name)
		}
		if legacy.Name != "legacy" || !legacy.Implicit || legacy.AllowVersionOverride || legacy.IncludeInChangesets {
			t.Errorf("unexpected legacy library: %+v", legacy)
		}
	})
}

func TestFolderSetPipelineLibraries(t *testing.T) {
	render := func(t *testing.T, f *folder) string {
		t.Helper()
		out, err := f.Render()
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return string(out)
	}

	t.Run("an untouched folder keeps its libraries verbatim", func(t *testing.T) {
		out := render(t, mustParseFolder(t, folderWithLibraries))
		for _, want := range []string{
			`<retriever class="org.jenkinsci.plugins.workflow.libs.SCMRetriever">`,
			`<scm class="hudson.scm.NullSCM"/>`,
			`plugin="pipeline-groovy-lib@752"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("rendered folder lost %s:\n%s", want, out)
			}
		}
	})

	t.Run("adds the property and round-trips", func(t *testing.T) {
		f := mustParseFolder(t, folderWithoutLibraries)
		want := folderLibrary{
			Name:                 "team-steps",
			Retriever:            newGitFolderLibraryRetriever("https://example.com/team/steps.git", "git-token"),
			DefaultVersion:       "main",
			AllowVersionOverride: true,
			IncludeInChangesets:  true,
		}
		if err := f.Properties.setPipelineLibraries([]folderLibrary{want}); err != nil {
			t.Fatalf("setPipelineLibraries: %v", err)
		}

		libraries, err := mustParseFolder(t, render(t, f)).Properties.pipelineLibraries()
		if err != nil {
			t.Fatalf("pipelineLibraries: %v", err)
		}
		if len(libraries) != 1 || !libraries[0].isGit() {
			t.Fatalf("libraries = %+v, want one Git library", libraries)
		}
		got := libraries[0]
		if got.Name != want.Name || got.DefaultVersion != want.DefaultVersion || got.Implicit || !got.AllowVersionOverride || !got.IncludeInChangesets {
			t.Errorf("library = %+v", got)
		}
		if scm := got.Retriever.SCM; scm.Remote != "https://example.com/team/steps.git" || scm.CredentialsID != "git-token" {
			t.Errorf("source = %+v", scm)
		}
		if !strings.Contains(got.Retriever.SCM.Traits.Raw, "BranchDiscoveryTrait") {
			t.Errorf("the Git source does not discover branches: %q", got.Retriever.SCM.Traits.Raw)
		}
	})

	t.Run("replaces the property and keeps the source id of a kept library", func(t *testing.T) {
		f := mustParseFolder(t, folderWithLibraries)
		err := f.Properties.setPipelineLibraries([]folderLibrary{
			{Name: "team-steps", Retriever: newGitFolderLibraryRetriever("https://example.com/team/steps.git", "")},
			{Name: "new", Retriever: newGitFolderLibraryRetriever("https://example.com/team/new.git", "")},
		})
		if err != nil {
			t.Fatalf("setPipelineLibraries: %v", err)
		}

		out := render(t, f)
		if strings.Contains(out, "legacy") {
			t.Errorf("the library left out of the list is still rendered:\n%s", out)
		}
		if strings.Count(out, folderLibrariesProperty) != 2 {
			t.Errorf("want exactly one %s element:\n%s", folderLibrariesProperty, out)
		}
		libraries, err := mustParseFolder(t, out).Properties.pipelineLibraries()
		if err != nil || len(libraries) != 2 {
			t.Fatalf("libraries = %+v, err = %v", libraries, err)
		}
		if id := libraries[0].Retriever.SCM.ID; id != "3f1c7a52" {
			t.Errorf("kept library source id = %q, want the id Jenkins assigned", id)
		}
		if id := libraries[1].Retriever.SCM.ID; id != "" {
			t.Errorf("new library source id = %q, want none", id)
		}
		if libraries[0].Retriever.SCM.CredentialsID != "" {
			t.Errorf("credentials id was not cleared: %+v", libraries[0].Retriever.SCM)
		}
	})

	t.Run("an empty list removes the property", func(t *testing.T) {
		f := mustParseFolder(t, folderWithLibraries)
		if err := f.Properties.setPipelineLibraries(nil); err != nil {
			t.Fatalf("setPipelineLibraries: %v", err)
		}
		if out := render(t, f); strings.Contains(out, folderLibrariesProperty) {
			t.Errorf("the property is still rendered:\n%s", out)
		}
	})
}

func pipelineLibraryValue(t *testing.T, name string, flags map[string]attr.Value) attr.Value {
	t.Helper()
	values := map[string]attr.Value{
		"name":                   types.StringValue(name),
		"default_version":        types.StringNull(),
		"implicit":               types.BoolNull(),
		"allow_version_override": types.BoolNull(),
		"include_in_changesets":  types.BoolNull(),
		"git_remote":             types.StringValue("https://example.com/" + name + ".git"),
		"git_credentials_id":     types.StringNull(),
	}
	for k, v := range flags {
		values[k] = v
	}
	object, diags := types.ObjectValue(folderPipelineLibraryObjectType.AttrTypes, values)
	if diags.HasError() {
		t.Fatalf("building library value: %v", diags)
	}
	return object
}

func TestPipelineLibrariesToList(t *testing.T) {
	ctx := context.Background()
	properties := &mustParseFolder(t, folderWithLibraries).Properties

	t.Run("an unmanaged attribute stays null", func(t *testing.T) {
		var diags diag.Diagnostics
		list := pipelineLibrariesToList(ctx, properties, types.ListNull(folderPipelineLibraryObjectType), &diags)
		if diags.HasError() || !list.IsNull() {
			t.Fatalf("list = %v, diags = %v; want null", list, diags)
		}
	})

	t.Run("omitted flags stay null only while Jenkins holds the default", func(t *testing.T) {
		prior := types.ListValueMust(folderPipelineLibraryObjectType, []attr.Value{
			pipelineLibraryValue(t, "team-steps", nil),
			pipelineLibraryValue(t, "legacy", nil),
		})

		var diags diag.Diagnostics
		list := pipelineLibrariesToList(ctx, properties, prior, &diags)
		var models []folderPipelineLibraryModel
		diags.Append(list.ElementsAs(ctx, &models, false)...)
		if diags.HasError() || len(models) != 2 {
			t.Fatalf("models = %v, diags = %v", models, diags)
		}

		steps := models[0]
		if !steps.Implicit.IsNull() || !steps.AllowVersionOverride.IsNull() || !steps.IncludeInChangesets.IsNull() {
			t.Errorf("default flags must stay null: %+v", steps)
		}
		if steps.DefaultVersion.ValueString() != "main" || steps.GitCredentialsID.ValueString() != "git-token" {
			t.Errorf("server values not reported: %+v", steps)
		}

		// The server holds non-default flags the configuration never set:
		// that is drift, and it has to be reported.
		legacy := models[1]
		if legacy.Implicit.IsNull() || !legacy.Implicit.ValueBool() ||
			legacy.AllowVersionOverride.IsNull() || legacy.AllowVersionOverride.ValueBool() ||
			legacy.IncludeInChangesets.IsNull() || legacy.IncludeInChangesets.ValueBool() {
			t.Errorf("non-default flags must be reported: %+v", legacy)
		}
		if !legacy.GitRemote.IsNull() {
			t.Errorf("a non-Git library must report a null git_remote: %+v", legacy)
		}
	})

	t.Run("explicit flags are reported", func(t *testing.T) {
		prior := types.ListValueMust(folderPipelineLibraryObjectType, []attr.Value{
			pipelineLibraryValue(t, "team-steps", map[string]attr.Value{"implicit": types.BoolValue(false)}),
		})

		var diags diag.Diagnostics
		list := pipelineLibrariesToList(ctx, properties, prior, &diags)
		var models []folderPipelineLibraryModel
		diags.Append(list.ElementsAs(ctx, &models, false)...)
		if diags.HasError() || len(models) != 2 {
			t.Fatalf("models = %v, diags = %v", models, diags)
		}
		if models[0].Implicit.IsNull() || models[0].Implicit.ValueBool() {
			t.Errorf("implicit = %v, want false", models[0].Implicit)
		}
	})
}

func TestSetPipelineLibrariesValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		libraries []attr.Value
		wantError string
	}{
		{
			name: "duplicate names",
			libraries: []attr.Value{
				pipelineLibraryValue(t, "steps", nil),
				pipelineLibraryValue(t, "steps", nil),
			},
			wantError: "Duplicate Pipeline Library",
		},
		{
			name: "implicit without a default version",
			libraries: []attr.Value{
				pipelineLibraryValue(t, "steps", map[string]attr.Value{"implicit": types.BoolValue(true)}),
			},
			wantError: "Missing Default Version",
		},
		{
			name: "implicit with a default version",
			libraries: []attr.Value{
				pipelineLibraryValue(t, "steps", map[string]attr.Value{
					"implicit":        types.BoolValue(true),
					"default_version": types.StringValue("main"),
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			properties := &mustParseFolder(t, folderWithoutLibraries).Properties
			var diags diag.Diagnostics
			setPipelineLibraries(ctx, properties, types.ListValueMust(folderPipelineLibraryObjectType, tt.libraries), &diags)

			if tt.wantError == "" {
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if len(diags.Errors()) != 1 || diags.Errors()[0].Summary() != tt.wantError {
				t.Fatalf("diagnostics = %v, want %q", diags, tt.wantError)
			}
			if libraries, _ := properties.pipelineLibraries(); len(libraries) != 0 {
				t.Errorf("a rejected list was still written: %+v", libraries)
			}
		})
	}
}
