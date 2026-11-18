package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const kubernetesCloudDescriptor = "org.csanchez.jenkins.plugins.kubernetes.KubernetesCloud"

// Jenkins exposes Cloud and Pod Template configuration through its normal
// configure forms. Neither object has a JSON or XML read endpoint.
func (j *jenkinsAdapter) readKubernetesForm(ctx context.Context, endpoint string) (map[string]string, error) {
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
	walkHTML(form, func(node *html.Node) {
		name, _ := htmlAttribute(node, "name")
		if node.Type != html.ElementNode || !strings.HasPrefix(name, "_.") {
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
	})
	return fields, nil
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
	resp, err := j.Requester.Post(ctx, endpoint, body, &struct{}{}, query)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		return &statusError{StatusCode: resp.StatusCode, Attempts: 1, Method: http.MethodPost, URL: endpoint}
	}
	return nil
}
