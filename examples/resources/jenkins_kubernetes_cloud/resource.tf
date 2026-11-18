resource "jenkins_kubernetes_cloud" "project" {
  name             = "project-agents"
  namespace        = "project-agents"
  credentials_id   = "project-provisioner"
  jenkins_url      = "https://jenkins.example.com/"
  usage_restricted = true
  websocket        = true
}

resource "jenkins_folder" "project" {
  name                        = "project"
  permitted_kubernetes_clouds = [jenkins_kubernetes_cloud.project.name]
}
