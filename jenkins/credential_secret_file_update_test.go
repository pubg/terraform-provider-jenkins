package jenkins

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSecretFileUpdatePayload(t *testing.T) {
	const (
		original = "Y3VycmVudCBmaWxl"
		next     = "bmV3IGZpbGU="
		retained = "{ZW5jcnlwdGVkIGZpeHR1cmU=}"
	)
	tests := []struct {
		name            string
		writeOnly       bool
		rotate          bool
		rename          bool
		legacy          bool
		missingFile     bool
		wantSecretBytes string
	}{
		{name: "change description with plain content", wantSecretBytes: retained},
		{name: "change filename with pinned write-only version", writeOnly: true, rename: true, legacy: true, wantSecretBytes: retained},
		{name: "replace plain content", rotate: true, wantSecretBytes: next},
		{name: "rotate write-only content", writeOnly: true, rotate: true, wantSecretBytes: next},
		{name: "fail without an encrypted value", missingFile: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const credentialPath = "/job/outer/job/inner/credentials/store/folder/domain/testing/credential/file"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch {
				case strings.Contains(req.URL.Path, "crumbIssuer"):
					_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
				case req.Method == http.MethodGet && req.URL.Path == credentialPath+"/updateDialog" && tt.legacy:
					w.WriteHeader(http.StatusNotFound)
				case req.Method == http.MethodGet && (req.URL.Path == credentialPath+"/updateDialog" ||
					(tt.legacy && req.URL.Path == credentialPath+"/update")):
					if tt.rotate {
						t.Error("rotating content should not load the existing file")
					}
					if tt.missingFile {
						_, _ = io.WriteString(w, `<form action="updateSubmit"><input name="_.secretBytes" value=""></form>`)
					} else {
						_, _ = io.WriteString(w, `<form action="updateSubmit"><input name="_.secretBytes" value="`+retained+`"></form>`)
					}
				case req.Method == http.MethodPost && req.URL.Path == credentialPath+"/config.xml":
					if tt.missingFile {
						t.Error("must not overwrite the file if its existing value is unavailable")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					var payload jenkins.FileCredentials
					if err := xml.NewDecoder(req.Body).Decode(&payload); err != nil {
						t.Errorf("decode credential update: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if payload.SecretBytes != tt.wantSecretBytes {
						t.Error("POST sent the wrong secretBytes")
					}
					if tt.rename && payload.Filename != "renamed.txt" || !tt.rename && payload.Description != "updated" {
						t.Error("POST omitted the metadata update")
					}
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			ctx := t.Context()
			r := newCredentialSecretFileResource().(*credentialSecretFileResource)
			var err error
			r.client, err = newJenkinsClient(&Config{ServerURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)

			prior := credentialSecretFileResourceModel{
				ID:          types.StringValue("/job/outer/job/inner/file"),
				Name:        types.StringValue("file"),
				Folder:      types.StringValue("outer/inner"),
				Domain:      types.StringValue("testing"),
				Description: types.StringValue("original"),
				Scope:       types.StringValue("GLOBAL"),
				Filename:    types.StringValue("original.txt"),
			}
			if tt.writeOnly {
				prior.SecretBytesWoVersion = types.StringValue("v1")
			} else {
				prior.SecretBytes = types.StringValue(original)
			}
			after := prior
			if tt.rename {
				after.Filename = types.StringValue("renamed.txt")
			} else {
				after.Description = types.StringValue("updated")
			}
			if tt.rotate {
				if tt.writeOnly {
					after.SecretBytesWoVersion = types.StringValue("v2")
				} else {
					after.SecretBytes = types.StringValue(next)
				}
			}
			config := after
			if tt.writeOnly {
				// A different configured value must not rotate until the version changes.
				config.SecretBytesWo = types.StringValue(next)
			}
			state := tfsdk.State{Schema: schema.Schema}
			plan := tfsdk.Plan{Schema: schema.Schema}
			cfg := tfsdk.State{Schema: schema.Schema}
			for _, d := range []bool{state.Set(ctx, prior).HasError(), plan.Set(ctx, after).HasError(), cfg.Set(ctx, config).HasError()} {
				if d {
					t.Fatal("failed to set Terraform test fixture")
				}
			}
			resp := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{
				State: state, Plan: plan, Config: tfsdk.Config{Schema: schema.Schema, Raw: cfg.Raw},
			}, &resp)
			if resp.Diagnostics.HasError() != tt.missingFile {
				t.Fatalf("unexpected update diagnostics: %v", resp.Diagnostics)
			}
			if !tt.missingFile {
				var saved credentialSecretFileResourceModel
				if d := resp.State.Get(ctx, &saved); d.HasError() || !saved.SecretBytesWo.IsNull() || !saved.SecretBytes.Equal(after.SecretBytes) {
					t.Error("update wrote the wrong value to Terraform state")
				}
				if !saved.Description.Equal(after.Description) || !saved.Filename.Equal(after.Filename) {
					t.Error("update did not save the new metadata to state")
				}
			}
		})
	}
}
