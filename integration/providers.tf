terraform {
  required_providers {
    jenkins = {
      source  = "pubg/jenkins"
      version = "~> 1.3"
    }
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 4.4"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}
