package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var configFileTestTypes = []struct{ name, class, content string }{
	{"custom", "org.jenkinsci.plugins.configfiles.custom.CustomConfig", "key: value\n"},
	{"json", "org.jenkinsci.plugins.configfiles.json.JsonConfig", " \n{\"key\": \"value\"}\n "},
	{"xml", "org.jenkinsci.plugins.configfiles.xml.XmlConfig", "<config/>\n"},
	{"groovy", "org.jenkinsci.plugins.configfiles.groovy.GroovyScript", "println 'hello'\n"},
	{"properties", "org.jenkinsci.plugins.configfiles.properties.PropertiesConfig", "key=value\n"},
	{"maven_settings", "org.jenkinsci.plugins.configfiles.maven.MavenSettingsConfig", "<settings/>\n"},
	{"global_maven_settings", "org.jenkinsci.plugins.configfiles.maven.GlobalMavenSettingsConfig", "<settings/>\n"},
	{"maven_toolchains", "org.jenkinsci.plugins.configfiles.maven.MavenToolchainsConfig", "<toolchains/>\n"},
}

func TestConfigFileContentTypes(t *testing.T) {
	for _, tt := range configFileTestTypes {
		t.Run(tt.name, func(t *testing.T) {
			page := fmt.Sprintf(`<input name="stapler-class" value="%s">
<input name="config.id" value="test"><input name="config.name" value="Test"><input name="config.comment" value="">
<input type="checkbox" name="_.isReplaceAll">
<textarea name="config.content">%s</textarea>`, tt.class, html.EscapeString(tt.content))
			got, err := parseConfigFile(io.NopCloser(strings.NewReader(page)))
			if err != nil || got.ContentType != tt.name || got.Content != tt.content {
				t.Fatalf("parseConfigFile() = %#v, %v", got, err)
			}
			var posted bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/crumbIssuer/api/jsonapi/json/":
					_, _ = io.WriteString(w, `{}`)
				case "/configfiles/":
					_, _ = io.WriteString(w, `<a href="editConfig?id=test">Test</a>`)
				case "/configfiles/editConfig":
					_, _ = io.WriteString(w, page)
				case "/configfiles/saveConfig":
					posted = true
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						return
					}
					var payload configFileForm
					if err := json.Unmarshal([]byte(r.Form.Get("json")), &payload); err != nil {
						t.Error(err)
						return
					}
					if payload.Config.Class != tt.class || payload.Config.StaplerClass != tt.class {
						t.Errorf("class = %q / %q, want %q", payload.Config.Class, payload.Config.StaplerClass, tt.class)
					}
					want := tt.content
					if tt.name == "json" {
						want = `"` + want + `"`
					}
					if payload.Config.Content != want {
						t.Errorf("content = %q, want %q", payload.Config.Content, want)
					}
					if payload.Config.IsReplaceAll == nil || *payload.Config.IsReplaceAll {
						t.Error("lost existing Replace All=false")
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			client, err := newJenkinsClient(&Config{ServerURL: srv.URL, RetryMax: 0})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.SaveConfigFile(context.Background(), *got); err != nil {
				t.Fatal(err)
			}
			if !posted {
				t.Fatal("file was not saved")
			}
		})
	}
}

func TestConfigFileRejectsCredentialMappings(t *testing.T) {
	for _, field := range []string{"tokenKey", "serverId", "propertyKey", "credentialsId"} {
		t.Run(field, func(t *testing.T) {
			page := `<input name="stapler-class" value="org.jenkinsci.plugins.configfiles.custom.CustomConfig">` +
				`<input name="_.` + field + `" value="existing">`
			_, err := parseConfigFile(io.NopCloser(strings.NewReader(page)))
			if err == nil || !strings.Contains(err.Error(), "uses credential mappings") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestConfigFileHTML(t *testing.T) {
	t.Run("finds exact ID in index", func(t *testing.T) {
		html := `<a href="editConfig?id=first">First</a><a href="editConfig?id=second">Second</a>`
		found, err := configFileListed(io.NopCloser(strings.NewReader(html)), "second")
		if err != nil || !found {
			t.Fatalf("configFileListed() = %v, %v; want true, nil", found, err)
		}
	})

	t.Run("parses custom file", func(t *testing.T) {
		html := `<form>
<input type="hidden" name="stapler-class" value="org.jenkinsci.plugins.configfiles.custom.CustomConfig">
<input name="config.id" value="app-config">
<input name="config.name" value="Application &amp; shared">
<input name="config.comment" value="Managed">
<textarea name="config.content">key: &lt;value&gt;
</textarea>
</form>`
		got, err := parseConfigFile(io.NopCloser(strings.NewReader(html)))
		if err != nil {
			t.Fatalf("parseConfigFile() error = %v", err)
		}
		want := managedConfigFile{ID: "app-config", Name: "Application & shared", Comment: "Managed", Content: "key: <value>\n", ContentType: "custom"}
		if *got != want {
			t.Errorf("parseConfigFile() = %#v, want %#v", *got, want)
		}
	})

	t.Run("rejects another provider type", func(t *testing.T) {
		html := `<input name="stapler-class" value="unsupported.Config">`
		if _, err := parseConfigFile(io.NopCloser(strings.NewReader(html))); err == nil {
			t.Fatal("parseConfigFile() accepted an unknown config class")
		}
	})

	t.Run("rejects credential mappings", func(t *testing.T) {
		html := `<form>
<input name="stapler-class" value="org.jenkinsci.plugins.configfiles.custom.CustomConfig">
<input name="config.id" value="app-config"><input name="config.name" value="App"><input name="config.comment" value="">
<input name="config.customizedCredentialMappings.tokenKey" value="TOKEN">
<textarea name="config.content">value</textarea>
</form>`
		if _, err := parseConfigFile(io.NopCloser(strings.NewReader(html))); err == nil {
			t.Fatal("parseConfigFile() accepted tokenized credential mappings")
		}
	})
}

func TestJenkinsAdapterConfigFileRequests(t *testing.T) {
	var saved managedConfigFile
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/jsonapi/json/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		case "/configfiles/":
			_, _ = io.WriteString(w, `<html></html>`)
		case "/configfiles/saveConfig":
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm() error = %v", err)
				return
			}
			var payload configFileForm
			if err := json.Unmarshal([]byte(r.Form.Get("json")), &payload); err != nil {
				t.Errorf("invalid json form: %v", err)
				return
			}
			saved = managedConfigFile{
				ID:          payload.Config.ID,
				Name:        payload.Config.Name,
				Comment:     payload.Config.Comment,
				Content:     payload.Config.Content,
				ContentType: "custom",
			}
			http.Redirect(w, r, "/configfiles/", http.StatusFound)
		case "/configfiles/removeConfig":
			deleted = r.URL.Query().Get("id")
			http.Redirect(w, r, "/configfiles/", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := newJenkinsClient(&Config{ServerURL: srv.URL, RetryMax: 0})
	if err != nil {
		t.Fatalf("newJenkinsClient() error = %v", err)
	}
	want := managedConfigFile{ID: "app-config", Name: "App", Comment: "Managed", Content: "key: value\n", ContentType: "custom"}
	if err := client.SaveConfigFile(context.Background(), want); err != nil {
		t.Fatalf("SaveConfigFile() error = %v", err)
	}
	if saved != want {
		t.Errorf("saved payload = %#v, want %#v", saved, want)
	}
	if err := client.DeleteConfigFile(context.Background(), want.ID, ""); err != nil {
		t.Fatalf("DeleteConfigFile() error = %v", err)
	}
	if deleted != want.ID {
		t.Errorf("deleted ID = %q, want %q", deleted, want.ID)
	}
}

func TestJenkinsAdapterGetConfigFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/configfiles/":
			_, _ = io.WriteString(w, `<a href="editConfig?id=app-config">App</a>`)
		case "/configfiles/editConfig":
			_, _ = io.WriteString(w, `<form>
<input name="stapler-class" value="org.jenkinsci.plugins.configfiles.custom.CustomConfig">
<input name="config.id" value="app-config"><input name="config.name" value="App"><input name="config.comment" value="Managed">
<textarea name="config.content">key: value</textarea></form>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := newJenkinsClient(&Config{ServerURL: srv.URL, RetryMax: 0})
	if err != nil {
		t.Fatalf("newJenkinsClient() error = %v", err)
	}
	got, err := client.GetConfigFile(context.Background(), "app-config", "")
	if err != nil {
		t.Fatalf("GetConfigFile() error = %v", err)
	}
	if got == nil || got.Content != "key: value" {
		t.Fatalf("GetConfigFile() = %#v", got)
	}
	missing, err := client.GetConfigFile(context.Background(), "missing", "")
	if err != nil || missing != nil {
		t.Fatalf("GetConfigFile(missing) = %#v, %v; want nil, nil", missing, err)
	}
}

func TestConfigFileFolderRouting(t *testing.T) {
	for _, tt := range []struct{ folder, base string }{
		{"", "/configfiles"},
		{"team/child", "/job/team/job/child/configfiles"},
		{"/job/team/job/child", "/job/team/job/child/configfiles"},
		{"team one/child#two", "/job/team%20one/job/child%23two/configfiles"},
	} {
		t.Run(tt.folder, func(t *testing.T) {
			// The same ID in another scope must survive every operation.
			other := "/job/other/configfiles"
			contents := map[string]string{other: "untouched"}
			if tt.folder != "" {
				contents["/configfiles"] = "global"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.EscapedPath()
				if strings.HasSuffix(path, "/api/json") || strings.Contains(path, "crumbIssuer") {
					_, _ = io.WriteString(w, `{"name":"child","_class":"com.cloudbees.hudson.plugins.folder.Folder"}`)
					return
				}
				if !strings.HasPrefix(path, tt.base+"/") {
					t.Errorf("request escaped its scope: %s", path)
					http.NotFound(w, r)
					return
				}
				switch strings.TrimPrefix(path, tt.base) {
				case "/":
					if _, found := contents[tt.base]; found {
						_, _ = io.WriteString(w, `<a href="editConfig?id=shared">Shared</a>`)
					}
				case "/editConfig":
					_, _ = fmt.Fprintf(w, `<input name="stapler-class" value="org.jenkinsci.plugins.configfiles.custom.CustomConfig">
<input name="config.id" value="shared"><input name="config.name" value="Shared"><input name="config.comment" value="">
<textarea name="config.content">%s</textarea>`, html.EscapeString(contents[tt.base]))
				case "/saveConfig":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
						return
					}
					var payload configFileForm
					if err := json.Unmarshal([]byte(r.Form.Get("json")), &payload); err != nil {
						t.Error(err)
						return
					}
					contents[tt.base] = payload.Config.Content
				case "/removeConfig":
					if r.URL.Query().Get("id") != "shared" {
						t.Error("wrong deletion ID")
					}
					delete(contents, tt.base)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			client, err := newJenkinsClient(&Config{ServerURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for _, content := range []string{"created", "updated"} {
				if err := client.SaveConfigFile(ctx, managedConfigFile{ID: "shared", Folder: tt.folder, ContentType: "custom", Content: content}); err != nil {
					t.Fatal(err)
				}
				got, err := client.GetConfigFile(ctx, "shared", tt.folder)
				if err != nil || got == nil || got.Content != content || got.Folder != tt.folder {
					t.Fatalf("read = %#v, %v", got, err)
				}
			}
			if err := client.DeleteConfigFile(ctx, "shared", tt.folder); err != nil {
				t.Fatal(err)
			}
			if got, err := client.GetConfigFile(ctx, "shared", tt.folder); err != nil || got != nil {
				t.Fatalf("deleted file = %#v, %v", got, err)
			}
			if contents[other] != "untouched" || (tt.folder != "" && contents["/configfiles"] != "global") {
				t.Fatalf("other scope was changed: %#v", contents)
			}
		})
	}
}

func TestConfigFileMissingFolder(t *testing.T) {
	for _, folderExists := range []bool{false, true} {
		t.Run(fmt.Sprint(folderExists), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if folderExists && strings.HasSuffix(r.URL.Path, "/api/json") {
					_, _ = io.WriteString(w, `{"name":"team","_class":"com.cloudbees.hudson.plugins.folder.Folder"}`)
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()
			client, err := newJenkinsClient(&Config{ServerURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.GetConfigFile(context.Background(), "shared", "team")
			if got != nil || (err != nil) != folderExists {
				t.Fatalf("read = %#v, %v", got, err)
			}
			if !folderExists {
				if err := client.DeleteConfigFile(context.Background(), "shared", "team"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestConfigFileInvalidFolder(t *testing.T) {
	for _, folder := range []string{"/", "job", "../team", "team/../other", "team/.", `team\child`} {
		if _, err := configFileBasePath(folder); err == nil {
			t.Errorf("accepted invalid folder %q", folder)
		}
	}
}
