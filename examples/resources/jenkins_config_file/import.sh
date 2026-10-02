# Config files are imported by their Config File Provider ID.
terraform import jenkins_config_file.application application-config

# Folder-scoped files use folder/path:config-id.
terraform import jenkins_config_file.team_json team:application-json
