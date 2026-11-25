resource "jenkins_credential_oidc_id_token" "example" {
  name     = "example-id-token"
  audience = "https://service.example.com"
}
