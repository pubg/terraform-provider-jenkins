resource "jenkins_credential_oidc_id_token" "global" {
  name     = "global-oidc-id-token"
  audience = "https://service.example.com"
}

resource "jenkins_credential_oidc_id_token" "folder" {
  name   = "folder-oidc-id-token"
  folder = jenkins_folder.example.id
}
