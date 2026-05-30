terraform {
  required_version = ">= 1.6"
  required_providers {
    dynatrace = {
      source  = "dynatrace-oss/dynatrace"
      version = "~> 1.0"
    }
  }
}

provider "dynatrace" {
  dt_env_url   = var.dt_url
  dt_api_token = var.dt_api_token
}
