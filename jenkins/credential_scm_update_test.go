package jenkins

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// scmCredentialServer serves the credential update form and records the
// config.xml POST of a GitHub App or GitLab access token update.
type scmCredentialServer struct {
	t           *testing.T
	path        string
	form        string
	formFetched bool
	posted      []byte
}

func (s *scmCredentialServer) handler(w http.ResponseWriter, req *http.Request) {
	switch {
	case strings.Contains(req.URL.Path, "crumbIssuer"):
		_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
	case req.Method == http.MethodGet && req.URL.Path == s.path+"/updateDialog":
		s.formFetched = true
		_, _ = io.WriteString(w, s.form)
	case req.Method == http.MethodPost && req.URL.Path == s.path+"/config.xml":
		s.posted, _ = io.ReadAll(req.Body)
		w.WriteHeader(http.StatusOK)
	default:
		s.t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

const scmRetainedSecret = "{ZW5jcnlwdGVkIGZpeHR1cmU=}"

func TestGitHubAppUpdatePayload(t *testing.T) {
	const credentialPath = "/job/team/credentials/store/folder/domain/_/credential/app"
	keyForm := `<form action="updateSubmit"><textarea name="_.privateKey" class="secretTextarea-redact">` + scmRetainedSecret + `</textarea></form>`

	tests := []struct {
		name        string
		writeOnly   bool
		rotate      bool
		form        string
		wantErr     bool
		wantKey     string
		wantFetched bool
	}{
		{name: "change access strategy keeps the stored key", form: keyForm, wantKey: scmRetainedSecret, wantFetched: true},
		{name: "change access strategy keeps a write-only key", writeOnly: true, form: keyForm, wantKey: scmRetainedSecret, wantFetched: true},
		{name: "replace plain key", rotate: true, wantKey: "next-key"},
		{name: "rotate write-only key", writeOnly: true, rotate: true, wantKey: "next-key"},
		{name: "fail without an encrypted key", form: `<form action="updateSubmit"><textarea name="_.privateKey"></textarea></form>`, wantErr: true, wantFetched: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &scmCredentialServer{t: t, path: credentialPath, form: tt.form}
			ts := httptest.NewServer(http.HandlerFunc(srv.handler))
			defer ts.Close()

			ctx := t.Context()
			r := newCredentialGitHubAppResource().(*credentialGitHubAppResource)
			var err error
			r.client, err = newJenkinsClient(&Config{ServerURL: ts.URL})
			if err != nil {
				t.Fatal(err)
			}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)

			prior := credentialGitHubAppResourceModel{
				ID:                         types.StringValue("/job/team/app"),
				Name:                       types.StringValue("app"),
				Folder:                     types.StringValue("team"),
				Domain:                     types.StringValue("_"),
				Description:                types.StringValue("app"),
				Scope:                      types.StringValue("GLOBAL"),
				AppID:                      types.StringValue("123"),
				RepositoryAccessStrategy:   types.StringValue(gitHubAppAccessSpecificRepositories),
				Repositories:               types.ListValueMust(types.StringType, []attr.Value{}),
				DefaultPermissionsStrategy: types.StringValue("INHERIT_ALL"),
			}
			if tt.writeOnly {
				prior.PrivateKeyWoVersion = types.StringValue("v1")
			} else {
				prior.PrivateKey = types.StringValue("current-key")
			}
			after := prior
			after.Owner = types.StringValue("myorg")
			after.Repositories = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("r1")})
			after.DefaultPermissionsStrategy = types.StringValue("CONTENTS_READ")
			if tt.rotate {
				if tt.writeOnly {
					after.PrivateKeyWoVersion = types.StringValue("v2")
				} else {
					after.PrivateKey = types.StringValue("next-key")
				}
			}
			config := after
			if tt.writeOnly {
				config.PrivateKeyWo = types.StringValue("next-key")
			}

			resp := runCredentialUpdate(t, r.Update, schema, prior, after, config)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("unexpected update diagnostics: %v", resp.Diagnostics)
			}
			if srv.formFetched != tt.wantFetched {
				t.Errorf("update form fetched = %v, want %v", srv.formFetched, tt.wantFetched)
			}
			if tt.wantErr {
				if srv.posted != nil {
					t.Error("must not overwrite the credential if the stored key is unavailable")
				}
				return
			}
			var payload GitHubAppCredentials
			if err := xml.Unmarshal(srv.posted, &payload); err != nil {
				t.Fatalf("decode credential update: %v", err)
			}
			if payload.PrivateKey != tt.wantKey {
				t.Errorf("POST privateKey = %q, want %q", payload.PrivateKey, tt.wantKey)
			}
			s := payload.RepositoryAccessStrategy
			if s == nil || s.Class != gitHubAppAccessStrategyClasses[gitHubAppAccessSpecificRepositories] ||
				s.Owner != "myorg" || s.Repositories == nil || strings.Join(s.Repositories.Names, ",") != "r1" {
				t.Errorf("POST repositoryAccessStrategy = %+v, want specified repositories myorg/r1", s)
			}
			if payload.DefaultPermissionsStrategy != "CONTENTS_READ" {
				t.Errorf("POST defaultPermissionsStrategy = %q, want CONTENTS_READ", payload.DefaultPermissionsStrategy)
			}
		})
	}
}

func TestGitLabAccessTokenUpdatePayload(t *testing.T) {
	const credentialPath = "/credentials/store/system/domain/_/credential/gl"
	tokenForm := `<form action="updateSubmit"><input type="password" name="_.token" value="` + scmRetainedSecret + `"></form>`

	tests := []struct {
		name        string
		writeOnly   bool
		rotate      bool
		form        string
		wantErr     bool
		wantToken   string
		wantFetched bool
	}{
		{name: "change description keeps the stored token", form: tokenForm, wantToken: scmRetainedSecret, wantFetched: true},
		{name: "change description keeps a write-only token", writeOnly: true, form: tokenForm, wantToken: scmRetainedSecret, wantFetched: true},
		{name: "replace plain token", rotate: true, wantToken: "glpat-next"},
		{name: "rotate write-only token", writeOnly: true, rotate: true, wantToken: "glpat-next"},
		{name: "fail without an encrypted token", form: `<form action="updateSubmit"></form>`, wantErr: true, wantFetched: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &scmCredentialServer{t: t, path: credentialPath, form: tt.form}
			ts := httptest.NewServer(http.HandlerFunc(srv.handler))
			defer ts.Close()

			ctx := t.Context()
			r := newCredentialGitLabAccessTokenResource().(*credentialGitLabAccessTokenResource)
			var err error
			r.client, err = newJenkinsClient(&Config{ServerURL: ts.URL})
			if err != nil {
				t.Fatal(err)
			}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)

			prior := credentialGitLabAccessTokenResourceModel{
				ID:          types.StringValue("/gl"),
				Name:        types.StringValue("gl"),
				Folder:      types.StringNull(),
				Domain:      types.StringValue("_"),
				Description: types.StringValue("original"),
				Scope:       types.StringValue("GLOBAL"),
				TokenType:   types.StringValue("group"),
			}
			if tt.writeOnly {
				prior.TokenWoVersion = types.StringValue("v1")
			} else {
				prior.Token = types.StringValue("glpat-current")
			}
			after := prior
			after.Description = types.StringValue("updated")
			if tt.rotate {
				if tt.writeOnly {
					after.TokenWoVersion = types.StringValue("v2")
				} else {
					after.Token = types.StringValue("glpat-next")
				}
			}
			config := after
			if tt.writeOnly {
				config.TokenWo = types.StringValue("glpat-next")
			}

			resp := runCredentialUpdate(t, r.Update, schema, prior, after, config)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("unexpected update diagnostics: %v", resp.Diagnostics)
			}
			if srv.formFetched != tt.wantFetched {
				t.Errorf("update form fetched = %v, want %v", srv.formFetched, tt.wantFetched)
			}
			if tt.wantErr {
				if srv.posted != nil {
					t.Error("must not overwrite the credential if the stored token is unavailable")
				}
				return
			}
			var payload GitLabAccessTokenCredentials
			if err := xml.Unmarshal(srv.posted, &payload); err != nil {
				t.Fatalf("decode credential update: %v", err)
			}
			if payload.Token != tt.wantToken {
				t.Errorf("POST token = %q, want %q", payload.Token, tt.wantToken)
			}
			if payload.XMLName.Local != gitLabAccessTokenClasses["group"] {
				t.Errorf("POST root element = %q, want the group access token class", payload.XMLName.Local)
			}
			if payload.Description != "updated" {
				t.Errorf("POST description = %q, want %q", payload.Description, "updated")
			}
		})
	}
}

// runCredentialUpdate drives a credential resource's Update with the given
// prior state, plan and configuration.
func runCredentialUpdate[M any](t *testing.T, update func(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse),
	schema resource.SchemaResponse, prior, after, config M) resource.UpdateResponse {
	t.Helper()
	ctx := t.Context()
	state := tfsdk.State{Schema: schema.Schema}
	plan := tfsdk.Plan{Schema: schema.Schema}
	cfg := tfsdk.State{Schema: schema.Schema}
	for _, failed := range []bool{state.Set(ctx, prior).HasError(), plan.Set(ctx, after).HasError(), cfg.Set(ctx, config).HasError()} {
		if failed {
			t.Fatal("failed to set Terraform test fixture")
		}
	}
	resp := resource.UpdateResponse{State: state}
	update(ctx, resource.UpdateRequest{
		State: state, Plan: plan, Config: tfsdk.Config{Schema: schema.Schema, Raw: cfg.Raw},
	}, &resp)
	return resp
}
