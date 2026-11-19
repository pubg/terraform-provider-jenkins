resource "jenkins_kubernetes_pod_template" "linux" {
  cloud           = "project-agents"
  name            = "linux-amd64"
  service_account = "jenkins-agent"
  node_selector   = "kubernetes.io/os=linux,kubernetes.io/arch=amd64"
  env_vars = {
    OS         = "linux"
    OS_VERSION = ""
    ARCH       = "amd64"
  }
  yaml = yamlencode({
    spec = {
      automountServiceAccountToken = false
    }
  })
}
