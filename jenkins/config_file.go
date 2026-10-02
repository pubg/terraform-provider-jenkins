package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	jenkins "github.com/bndr/gojenkins"
	"golang.org/x/net/html"
)

const configFilesBase = "/configfiles"

// These are Config subclasses, not MIME types or ConfigProvider subclasses.
// ConfigFilesManagement.doSaveConfig binds the submitted class through Stapler.
var configFileClasses = map[string]string{
	"custom":                "org.jenkinsci.plugins.configfiles.custom.CustomConfig",
	"json":                  "org.jenkinsci.plugins.configfiles.json.JsonConfig",
	"xml":                   "org.jenkinsci.plugins.configfiles.xml.XmlConfig",
	"groovy":                "org.jenkinsci.plugins.configfiles.groovy.GroovyScript",
	"properties":            "org.jenkinsci.plugins.configfiles.properties.PropertiesConfig",
	"maven_settings":        "org.jenkinsci.plugins.configfiles.maven.MavenSettingsConfig",
	"global_maven_settings": "org.jenkinsci.plugins.configfiles.maven.GlobalMavenSettingsConfig",
	"maven_toolchains":      "org.jenkinsci.plugins.configfiles.maven.MavenToolchainsConfig",
}

// managedConfigFile is the global file shape exposed by
// the Config File Provider plugin's Stapler forms.
type managedConfigFile struct {
	ID          string
	Name        string
	Comment     string
	Content     string
	ContentType string
	ReplaceAll  *bool
}

type configFileForm struct {
	Config struct {
		StaplerClass                 string        `json:"stapler-class"`
		Class                        string        `json:"$class"`
		ID                           string        `json:"id"`
		Name                         string        `json:"name"`
		Comment                      string        `json:"comment"`
		Content                      string        `json:"content"`
		CustomizedCredentialMappings []interface{} `json:"customizedCredentialMappings"`
		IsReplaceAll                 *bool         `json:"isReplaceAll,omitempty"`
	} `json:"config"`
}

// SaveConfigFile creates or updates a global file. The plugin uses a
// regular Jenkins form endpoint rather than a JSON REST endpoint: the form has
// one "json" field containing the submitted configuration object.
func (j *jenkinsAdapter) SaveConfigFile(ctx context.Context, config managedConfigFile) error {
	class, ok := configFileClasses[config.ContentType]
	if !ok {
		return fmt.Errorf("unsupported config file content_type %q", config.ContentType)
	}
	// Preserve the plugin's credential replacement option, and refuse to erase
	// unsupported credential mappings even if they appeared after refresh.
	existing, err := j.GetConfigFile(ctx, config.ID)
	if err != nil {
		return err
	}
	var payload configFileForm
	payload.Config.StaplerClass = class
	payload.Config.Class = class
	payload.Config.ID = config.ID
	payload.Config.Name = config.Name
	payload.Config.Comment = config.Comment
	payload.Config.Content = config.Content
	if config.ContentType == "json" {
		// JsonConfig.fixJsonContent trims whitespace and removes one pair of
		// surrounding quotes. Supply that pair to preserve the exact content,
		// including whitespace and JSON string values (not just objects).
		payload.Config.Content = `"` + config.Content + `"`
	}
	if existing != nil && existing.ContentType == config.ContentType {
		payload.Config.IsReplaceAll = existing.ReplaceAll
	}
	payload.Config.CustomizedCredentialMappings = []interface{}{}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	form := url.Values{"json": []string{string(encoded)}}
	resp, err := j.Requester.Post(ctx, configFilesBase+"/saveConfig", strings.NewReader(form.Encode()), &struct{}{}, map[string]string{})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response code %d saving config file %q", resp.StatusCode, config.ID)
	}
	return nil
}

// GetConfigFile returns nil when id is absent. It reads the same edit form the
// plugin UI uses because the plugin does not expose managed files through the
// Jenkins JSON API.
func (j *jenkinsAdapter) GetConfigFile(ctx context.Context, id string) (*managedConfigFile, error) {
	index, err := j.getConfigFilePage(ctx, configFilesBase+"/", nil)
	if err != nil {
		return nil, err
	}
	listed, err := configFileListed(index, id)
	if err != nil {
		return nil, err
	}
	if !listed {
		return nil, nil
	}

	query := url.Values{"id": []string{id}}
	detail, err := j.getConfigFilePage(ctx, configFilesBase+"/editConfig", query)
	if err != nil {
		return nil, err
	}
	return parseConfigFile(detail)
}

func (j *jenkinsAdapter) DeleteConfigFile(ctx context.Context, id string) error {
	resp, err := j.Requester.Post(ctx, configFilesBase+"/removeConfig", nil, &struct{}{}, map[string]string{"id": id})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response code %d deleting config file %q", resp.StatusCode, id)
	}
	return nil
}

func (j *jenkinsAdapter) getConfigFilePage(ctx context.Context, path string, query url.Values) (io.ReadCloser, error) {
	r, ok := j.Requester.(*jenkins.Requester)
	if !ok {
		return nil, fmt.Errorf("unexpected requester type %T", j.Requester)
	}
	endpoint := strings.TrimRight(r.Base, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if r.BasicAuth != nil {
		req.SetBasicAuth(r.BasicAuth.Username, r.BasicAuth.Password)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("unexpected status %d fetching Config File Provider page %s", resp.StatusCode, path)
	}
	return resp.Body, nil
}

func configFileListed(r io.ReadCloser, id string) (bool, error) {
	defer func() { _ = r.Close() }()
	doc, err := html.Parse(r)
	if err != nil {
		return false, err
	}
	var found bool
	walkHTML(doc, func(n *html.Node) {
		if found || n.Type != html.ElementNode || n.Data != "a" {
			return
		}
		href, ok := htmlAttribute(n, "href")
		if !ok {
			return
		}
		u, err := url.Parse(href)
		if err == nil && strings.HasSuffix(u.Path, "editConfig") && u.Query().Get("id") == id {
			found = true
		}
	})
	return found, nil
}

func parseConfigFile(r io.ReadCloser) (*managedConfigFile, error) {
	defer func() { _ = r.Close() }()
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	values := map[string]string{}
	present := map[string]bool{}
	var class string
	var replaceAll *bool
	var hasCredentialMappings bool
	walkHTML(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		name, _ := htmlAttribute(n, "name")
		switch n.Data {
		case "input":
			value, _ := htmlAttribute(n, "value")
			if name == "stapler-class" {
				class = value
			}
			if strings.HasSuffix(name, "isReplaceAll") {
				checked, _ := htmlAttribute(n, "type")
				if checked == "checkbox" {
					_, value := htmlAttribute(n, "checked")
					replaceAll = &value
				}
			}
			if strings.HasSuffix(name, "tokenKey") || strings.HasSuffix(name, "credentialsId") || strings.HasSuffix(name, "serverId") || strings.HasSuffix(name, "propertyKey") {
				hasCredentialMappings = hasCredentialMappings || value != ""
			}
			if strings.HasPrefix(name, "config.") {
				values[name] = value
				present[name] = true
			}
		case "textarea":
			if strings.HasPrefix(name, "config.") {
				values[name] = htmlText(n)
				present[name] = true
			}
		case "select":
			if strings.HasSuffix(name, "credentialsId") && selectedOption(n) != "" {
				hasCredentialMappings = true
			}
		}
	})

	var contentType string
	for candidate, configClass := range configFileClasses {
		if class == configClass {
			contentType = candidate
			break
		}
	}
	if contentType == "" {
		return nil, fmt.Errorf("unsupported config file class %q", class)
	}
	if hasCredentialMappings {
		return nil, fmt.Errorf("config file uses credential mappings, which jenkins_config_file does not manage")
	}
	for _, field := range []string{"config.id", "config.name", "config.comment", "config.content"} {
		if !present[field] {
			return nil, fmt.Errorf("Config File Provider response is missing %q", field)
		}
	}
	return &managedConfigFile{
		ID:          values["config.id"],
		Name:        values["config.name"],
		Comment:     values["config.comment"],
		Content:     values["config.content"],
		ContentType: contentType,
		ReplaceAll:  replaceAll,
	}, nil
}

func walkHTML(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}

func htmlAttribute(n *html.Node, key string) (string, bool) {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val, true
		}
	}
	return "", false
}

func htmlText(n *html.Node) string {
	var b strings.Builder
	walkHTML(n, func(child *html.Node) {
		if child.Type == html.TextNode {
			b.WriteString(child.Data)
		}
	})
	return b.String()
}

func selectedOption(n *html.Node) string {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.Data != "option" {
			continue
		}
		if _, selected := htmlAttribute(child, "selected"); selected {
			value, _ := htmlAttribute(child, "value")
			return value
		}
	}
	return ""
}
