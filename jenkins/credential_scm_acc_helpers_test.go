package jenkins

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testAccJenkinsScript runs a Groovy script on the acceptance Jenkins through
// the script console and returns its output. The REST API redacts every
// secret, so this is the only way to check what Jenkins actually stored.
func testAccJenkinsScript(script string) (string, error) {
	base := strings.TrimRight(os.Getenv("JENKINS_URL"), "/")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	auth := func(req *http.Request) {
		req.SetBasicAuth(os.Getenv("JENKINS_USERNAME"), os.Getenv("JENKINS_PASSWORD"))
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/crumbIssuer/api/json", nil)
	auth(req)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	var crumb struct {
		Field string `json:"crumbRequestField"`
		Value string `json:"crumb"`
	}
	err = json.NewDecoder(res.Body).Decode(&crumb)
	_ = res.Body.Close()
	if err != nil {
		return "", fmt.Errorf("read crumb: %w", err)
	}

	req, _ = http.NewRequest(http.MethodPost, base+"/scriptText", strings.NewReader(url.Values{"script": {script}}.Encode()))
	auth(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(crumb.Field, crumb.Value)
	res, err = client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("script console returned %d: %s", res.StatusCode, body)
	}
	return strings.TrimSpace(string(body)), nil
}

// testAccCheckCredentialSecretEquals asserts that the credential `id` in
// `folder` (empty for the global store) stores `want` in its Secret property
// `property`, e.g. privateKey or token.
func testAccCheckCredentialSecretEquals(folder, id, property, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		script := fmt.Sprintf(`
import com.cloudbees.plugins.credentials.CredentialsProvider
import com.cloudbees.plugins.credentials.common.StandardCredentials
def folder = %q
def group = folder ? Jenkins.instance.getItemByFullName(folder) : Jenkins.instance
def c = CredentialsProvider.lookupCredentialsInItemGroup(StandardCredentials, group, hudson.security.ACL.SYSTEM2, []).find { it.id == %q }
def want = new String(%q.decodeBase64(), "UTF-8")
println(c == null ? "missing" : (c.%s.plainText == want ? "match" : "mismatch length=" + c.%s.plainText.length()))
`, folder, id, base64.StdEncoding.EncodeToString([]byte(want)), property, property)
		out, err := testAccJenkinsScript(script)
		if err != nil {
			return err
		}
		if out != "match" {
			return fmt.Errorf("credential %q in %q: stored %s does not match: %s", id, folder, property, out)
		}
		return nil
	}
}
