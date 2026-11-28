package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	jenkins "github.com/bndr/gojenkins"
	"golang.org/x/net/html"
)

// encryptedSecret matches a value Jenkins encrypted with its instance key, the
// form Secret and SecretBytes fields take in the credential update form.
var encryptedSecret = regexp.MustCompile(`^\{[A-Za-z0-9+/]+={0,2}\}$`)

// readCredentialFormValues returns the values of the named field in the
// credential's update form. A credential's config.xml replaces the whole
// credential and its GET redacts every secret, so an update that does not
// change a secret has to send the encrypted value this form carries; sending
// nothing, an empty element or <secret-redacted/> stores an empty secret.
//
// The value is read from an <input value="..."> or from a <textarea>'s text,
// the two controls Jenkins renders for secrets. Most forms name the control
// "_.<field>"; a few, such as the Vault AppRole credential, use the bare field
// name, so both are accepted.
func readCredentialFormValues(ctx context.Context, cm *jenkins.CredentialsManager, domain, name, field string) ([]string, error) {
	base := "/credentials/store/system"
	if cm.Folder != "" {
		base = "/job/" + cm.Folder + "/credentials/store/folder"
	}
	endpoint := base + "/domain/" + url.PathEscape(domain) + "/credential/" + url.PathEscape(name)
	client := jenkinsAdapter{Jenkins: cm.J}
	body, err := client.getConfigFilePage(ctx, endpoint+"/updateDialog", nil)
	if isNotFound(err) {
		body, err = client.getConfigFilePage(ctx, endpoint+"/update", nil)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	doc, err := html.Parse(body)
	if err != nil {
		return nil, errors.New("cannot parse the Jenkins credential update form")
	}
	var values []string
	walkHTML(doc, func(node *html.Node) {
		action, _ := htmlAttribute(node, "action")
		if node.Type != html.ElementNode || node.Data != "form" || action != "updateSubmit" {
			return
		}
		walkHTML(node, func(control *html.Node) {
			if control.Type != html.ElementNode {
				return
			}
			if n, _ := htmlAttribute(control, "name"); n != "_."+field && n != field {
				return
			}
			switch control.Data {
			case "input":
				v, _ := htmlAttribute(control, "value")
				values = append(values, v)
			case "textarea":
				values = append(values, strings.TrimSpace(htmlText(control)))
			}
		})
	})
	return values, nil
}

// preserveEncryptedSecret returns a credentialSecretField.preserve function that
// re-sends the encrypted value of a Secret field when an update leaves it
// unchanged. what names the secret in error messages.
//
// Jenkins encrypts even an empty Secret, so for most fields the form always
// carries an encrypted value and anything else means the form changed.
func preserveEncryptedSecret(field, what string) func(context.Context, *jenkins.CredentialsManager, string, string) (string, error) {
	return preserveSecret(field, what, false)
}

// preserveOptionalEncryptedSecret is preserveEncryptedSecret for a field Jenkins
// may store as no Secret at all, such as an SSH key's passphrase. Its form then
// renders the control with an empty value, and the update re-sends the empty
// value.
func preserveOptionalEncryptedSecret(field, what string) func(context.Context, *jenkins.CredentialsManager, string, string) (string, error) {
	return preserveSecret(field, what, true)
}

func preserveSecret(field, what string, allowEmpty bool) func(context.Context, *jenkins.CredentialsManager, string, string) (string, error) {
	return func(ctx context.Context, cm *jenkins.CredentialsManager, domain, name string) (string, error) {
		values, err := readCredentialFormValues(ctx, cm, domain, name, field)
		if err != nil {
			return "", fmt.Errorf("cannot preserve the existing %s: %w", what, err)
		}
		if len(values) == 1 && (encryptedSecret.MatchString(values[0]) || allowEmpty && values[0] == "") {
			return values[0], nil
		}
		return "", fmt.Errorf("cannot preserve the existing %s: expected one encrypted value in the Jenkins update form", what)
	}
}
