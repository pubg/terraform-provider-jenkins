package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const kubernetesKeyValueEnvVar = "org.csanchez.jenkins.plugins.kubernetes.model.KeyValueEnvVar"

const kubernetesCloudDescriptor = "org.csanchez.jenkins.plugins.kubernetes.KubernetesCloud"

// Jenkins exposes Cloud and Pod Template configuration through its normal
// configure forms. Neither object has a JSON or XML read endpoint.
func (j *jenkinsAdapter) readKubernetesForm(ctx context.Context, endpoint string, rows map[string][]map[string]string) (map[string]string, error) {
	body, err := j.getConfigFilePage(ctx, endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	doc, err := html.Parse(body)
	if err != nil {
		return nil, err
	}

	var form *html.Node
	walkHTML(doc, func(node *html.Node) {
		action, _ := htmlAttribute(node, "action")
		if node.Type == html.ElementNode && node.Data == "form" && action == "configSubmit" {
			form = node
		}
	})
	if form == nil {
		return nil, errors.New("missing Kubernetes configuration form in Jenkins response")
	}

	fields := map[string]string{}
	var readFields func(*html.Node)
	readFields = func(node *html.Node) {
		class, _ := htmlAttribute(node, "class")
		classes := strings.Fields(class)
		if slices.Contains(classes, "to-be-removed") || slices.Contains(classes, "prototypes") {
			return
		}
		if slices.Contains(classes, "repeated-chunk") {
			name, _ := htmlAttribute(node, "name")
			descriptor, _ := htmlAttribute(node, "descriptorid")
			if rows != nil && (name == "volumes" || name == "annotations" || (name == "envVars" && descriptor == kubernetesKeyValueEnvVar)) {
				row := map[string]string{"$class": descriptor}
				walkHTML(node, func(input *html.Node) { readKubernetesInput(input, row) })
				rows[name] = append(rows[name], row)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			readFields(child)
		}
		readKubernetesInput(node, fields)
	}
	readFields(form)
	return fields, nil
}

func readKubernetesInput(node *html.Node, fields map[string]string) {
	name, _ := htmlAttribute(node, "name")
	// The Pod Template form renders its usage mode with f:enum, which omits the "_." prefix.
	if node.Type != html.ElementNode || (!strings.HasPrefix(name, "_.") && name != "nodeUsageMode") {
		return
	}
	name = strings.TrimPrefix(name, "_.")
	switch node.Data {
	case "input":
		inputType, _ := htmlAttribute(node, "type")
		if inputType == "checkbox" {
			_, checked := htmlAttribute(node, "checked")
			fields[name] = strconv.FormatBool(checked)
		} else if inputType != "radio" {
			fields[name], _ = htmlAttribute(node, "value")
		}
	case "textarea":
		fields[name] = htmlText(node)
	case "select":
		if value, hasValue := htmlAttribute(node, "value"); hasValue {
			fields[name] = value
		} else {
			fields[name] = selectedOption(node)
		}
	}
}

func (j *jenkinsAdapter) submitKubernetesForm(ctx context.Context, endpoint string, query map[string]string, values map[string]any) error {
	var body *strings.Reader
	if values != nil {
		encoded, err := json.Marshal(values)
		if err != nil {
			return err
		}
		body = strings.NewReader(url.Values{"json": {string(encoded)}}.Encode())
	} else {
		body = strings.NewReader("")
	}
	j.kubernetesConfigMu.Lock()
	defer j.kubernetesConfigMu.Unlock()
	resp, err := j.Requester.Post(ctx, endpoint, body, &struct{}{}, query)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		return &statusError{StatusCode: resp.StatusCode, Attempts: 1, Method: http.MethodPost, URL: endpoint}
	}
	return nil
}
