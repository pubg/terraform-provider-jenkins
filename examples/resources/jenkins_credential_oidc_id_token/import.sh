# Import a global credential as "<domain>/<name>". The global domain is "_".
terraform import jenkins_credential_oidc_id_token.example _/example-id-token

# Import a folder credential as "<folder>/<domain>/<name>".
terraform import jenkins_credential_oidc_id_token.example /job/team/_/example-id-token
