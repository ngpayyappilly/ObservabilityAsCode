# Creates the ADO variable group used by the bootstrap and propagation pipelines.
# Sensitive variables (ADO_PAT) are marked secret so ADO masks them in logs.
#
# Usage:
#   terraform init
#   terraform apply \
#     -var="ado_org_service_url=https://dev.azure.com/YOUR_ORG" \
#     -var="ado_project=YOUR_PROJECT" \
#     -var="ado_pat=<token>" \
#     -var="pr_reviewer_emails=alice@example.com,bob@example.com"

terraform {
  required_version = ">= 1.6"
  required_providers {
    azuredevops = {
      source  = "microsoft/azuredevops"
      version = "~> 1.0"
    }
  }
}

variable "ado_org_service_url" {
  description = "Azure DevOps organisation URL (e.g. https://dev.azure.com/YOUR_ORG)"
  type        = string
}

variable "ado_project" {
  description = "ADO project name"
  type        = string
}

# PAT used to configure the provider itself (needs variable group write permissions)
variable "ado_pat" {
  description = "ADO Personal Access Token for the Terraform provider"
  type        = string
  sensitive   = true
}

# The PAT that the bootstrap/propagation pipelines will use at runtime
variable "pipeline_pat" {
  description = "ADO PAT for bootstrap/propagation pipelines (Code read/write, PRs)"
  type        = string
  sensitive   = true
}

variable "pr_reviewer_emails" {
  description = "Comma-separated reviewer email addresses for scaffold PRs"
  type        = string
  default     = ""
}

provider "azuredevops" {
  org_service_url       = var.ado_org_service_url
  personal_access_token = var.ado_pat
}

data "azuredevops_project" "this" {
  name = var.ado_project
}

resource "azuredevops_variable_group" "oac_bootstrap_secrets" {
  project_id   = data.azuredevops_project.this.id
  name         = "oac-bootstrap-secrets"
  description  = "Secrets for the OaC bootstrap and propagation pipelines"
  allow_access = false   # restrict to pipelines explicitly linked, not all pipelines

  variable {
    name      = "ADO_PAT"
    value     = var.pipeline_pat
    is_secret = true   # masked in pipeline logs
  }

  variable {
    name  = "PR_REVIEWER_EMAILS"
    value = var.pr_reviewer_emails
  }
}

output "variable_group_id" {
  value       = azuredevops_variable_group.oac_bootstrap_secrets.id
  description = "ADO variable group ID — reference this in pipeline YAML"
}
