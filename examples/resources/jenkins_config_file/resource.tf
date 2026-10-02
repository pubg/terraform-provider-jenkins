resource "jenkins_config_file" "application" {
  id      = "application-config"
  name    = "Application configuration"
  comment = "Shared non-secret settings"
  content = <<-EOT
    log_level: info
    region: us-east-1
  EOT
}

resource "jenkins_config_file" "json" {
  id           = "application-json"
  name         = "Application JSON configuration"
  content_type = "json"
  content = jsonencode({
    log_level = "info"
    region    = "us-east-1"
  })
}
