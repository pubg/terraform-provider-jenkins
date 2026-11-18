package jenkins

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccJenkinsKubernetesConfig(t *testing.T) {
	name := "tf-acc-cloud-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := func(namespace, selector string) string {
		return fmt.Sprintf(`
resource "jenkins_kubernetes_cloud" "test" {
  name             = %q
  namespace        = %q
  jenkins_url      = "https://jenkins.example.test/"
  container_cap    = "1"
  usage_restricted = true
  websocket        = true
}

resource "jenkins_kubernetes_pod_template" "test" {
  cloud           = jenkins_kubernetes_cloud.test.name
  name            = "linux-amd64"
  service_account = "agent"
  node_selector   = %q
  yaml            = "spec:\n  automountServiceAccountToken: false\n"
}`, name, namespace, selector)
	}
	check := func(namespace, selector string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("jenkins_kubernetes_cloud.test", "id", name),
			resource.TestCheckResourceAttr("jenkins_kubernetes_cloud.test", "namespace", namespace),
			resource.TestCheckResourceAttr("jenkins_kubernetes_cloud.test", "usage_restricted", "true"),
			resource.TestCheckResourceAttr("jenkins_kubernetes_cloud.test", "container_cap", "1"),
			resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "id", name+"/linux-amd64"),
			resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "node_selector", selector),
		)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{Config: config("namespace-a", "karpenter.sh/nodepool=pool-a"), Check: check("namespace-a", "karpenter.sh/nodepool=pool-a")},
			{Config: config("namespace-b", "karpenter.sh/nodepool=pool-b"), Check: check("namespace-b", "karpenter.sh/nodepool=pool-b")},
		},
	})
}
