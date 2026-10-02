terraform {
  required_providers {
    jenkins = {
      source  = "pubg/jenkins"
      version = ">= 1.3.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0.0"
    }
  }
}

variable "port" {
  description = "The port that the Jenkins setup has been published on"
}

provider "jenkins" {
  server_url = "http://localhost:${var.port}"
  username   = "admin"
  password   = "admin"
  ca_cert    = ""
}
