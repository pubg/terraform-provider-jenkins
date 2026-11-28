resource "jenkins_credential_github_app" "example" {
  name        = "my-github-app"
  app_id      = "123456"
  private_key = file("/path/to/github-app.pem")
}

# A folder-scoped credential for GitHub Enterprise Server whose tokens can only
# read two repositories of one organization.
resource "jenkins_credential_github_app" "team" {
  name   = "github-app-team"
  folder = jenkins_folder.team.id

  app_id                 = "123456"
  private_key_wo         = file("/path/to/github-app.pem")
  private_key_wo_version = "1"

  api_uri                      = "https://github.example.com/api/v3"
  repository_access_strategy   = "specificRepositories"
  owner                        = "my-org"
  repositories                 = ["service-a", "service-b"]
  default_permissions_strategy = "CONTENTS_READ"
}
