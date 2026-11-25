package jenkins

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccJenkinsKubernetesConfig(t *testing.T) {
	name := "tf-acc-cloud-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := func(namespace, selector, env string) string {
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
  agent_container = "agent"
  %s
  yaml            = "spec:\n  automountServiceAccountToken: false\n"
}`, name, namespace, selector, env)
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
			{Config: config("namespace-a", "karpenter.sh/nodepool=pool-a", ""), Check: check("namespace-a", "karpenter.sh/nodepool=pool-a")},
			{Config: config("namespace-b", "karpenter.sh/nodepool=pool-b", `env_vars = { OS_VERSION = "", ARCH = "amd64" }
  annotations = { "example.org/project" = "test" }
  volumes = {
    "/scratch" = { empty_dir = { memory = true } }
    "/config" = { config_map = { name = "settings", sub_path = "app.yaml", optional = true } }
    "/token" = { secret = { name = "workload", default_mode = "256" } }
    "/data" = { persistent_volume_claim = { claim_name = "cache", read_only = true } }
    "/host" = { host_path = { path = "/example", read_only = true } }
    "/mnt/nfs" = { nfs = { server_address = "nfs.example.test", server_path = "/export" } }
    "/ephemeral" = { generic_ephemeral = { access_modes = "ReadWriteOnce", requests_size = "1Gi" } }
    "/dynamic" = { dynamic_pvc = { access_modes = "ReadWriteMany", storage_class_name = "shared", requests_size = "2Gi" } }
  }`), Check: resource.ComposeTestCheckFunc(
				check("namespace-b", "karpenter.sh/nodepool=pool-b"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "env_vars.OS_VERSION", ""),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "env_vars.ARCH", "amd64"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "agent_container", "agent"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "idle_minutes", ""),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "node_usage_mode", "EXCLUSIVE"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "volumes.%", "8"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "annotations.example.org/project", "test"),
			)},
			{Config: config("namespace-b", "karpenter.sh/nodepool=pool-b", `env_vars = { OS_VERSION = "2022" }
    annotations = { "example.org/project" = "updated" }
    idle_minutes = "5"
    node_usage_mode = "NORMAL"
    volumes = { "/scratch" = { persistent_volume_claim = { claim_name = "replacement" } } }`), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "idle_minutes", "5"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "node_usage_mode", "NORMAL"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "env_vars.%", "1"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "volumes.%", "1"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "volumes./scratch.persistent_volume_claim.claim_name", "replacement"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "annotations.example.org/project", "updated"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "env_vars.OS_VERSION", "2022"),
			)},
			{Config: config("namespace-b", "karpenter.sh/nodepool=pool-b", ""), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "env_vars.%", "0"),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "idle_minutes", ""),
				resource.TestCheckResourceAttr("jenkins_kubernetes_pod_template.test", "node_usage_mode", "EXCLUSIVE"),
			)},
		},
	})
}
