# A folder-scoped GitLab group access token for the folder's GitLab branch sources.
resource "jenkins_credential_gitlab_access_token" "team" {
  name   = "gitlab-team"
  folder = jenkins_folder.team.id

  token_type       = "group"
  token_wo         = var.gitlab_group_access_token
  token_wo_version = "1"
}
