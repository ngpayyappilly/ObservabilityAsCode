# Creates Dynatrace API tokens for the Monaco CMP sidecar and writes them to Vault.
# Tokens are named argocd-monaco-oac-{env} with the minimum required scopes.
#
# Usage:
#   terraform init
#   terraform apply \
#     -var="dt_dev_url=https://<tenant>.live.dynatrace.com" \
#     -var="dt_dev_token=<bootstrap-token>" \
#     ... (repeat for staging, prod) \
#     -var="vault_address=https://vault.internal"

terraform {
  required_version = ">= 1.6"
  required_providers {
    dynatrace = {
      source  = "dynatrace-oss/dynatrace"
      version = "~> 1.0"
    }
    vault = {
      source  = "hashicorp/vault"
      version = "~> 4.0"
    }
  }
}

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

variable "dt_dev_url" {
  description = "Dynatrace dev tenant URL (no trailing slash)"
  type        = string
}

variable "dt_dev_token" {
  description = "Bootstrap token for the dev tenant (needs token management scope)"
  type        = string
  sensitive   = true
}

variable "dt_staging_url" {
  description = "Dynatrace staging tenant URL"
  type        = string
}

variable "dt_staging_token" {
  description = "Bootstrap token for the staging tenant"
  type        = string
  sensitive   = true
}

variable "dt_prod_url" {
  description = "Dynatrace prod tenant URL"
  type        = string
}

variable "dt_prod_token" {
  description = "Bootstrap token for the prod tenant"
  type        = string
  sensitive   = true
}

variable "vault_address" {
  description = "Vault server URL (e.g. https://vault.internal)"
  type        = string
}

# ---------------------------------------------------------------------------
# Providers — one aliased Dynatrace provider per environment
# ---------------------------------------------------------------------------

provider "dynatrace" {
  alias    = "dev"
  dt_env_url   = var.dt_dev_url
  dt_api_token = var.dt_dev_token
}

provider "dynatrace" {
  alias    = "staging"
  dt_env_url   = var.dt_staging_url
  dt_api_token = var.dt_staging_token
}

provider "dynatrace" {
  alias    = "prod"
  dt_env_url   = var.dt_prod_url
  dt_api_token = var.dt_prod_token
}

provider "vault" {
  address = var.vault_address
  # Authentication is via VAULT_TOKEN env var or ambient k8s SA token
}

# ---------------------------------------------------------------------------
# Dynatrace API tokens — one per environment
# ---------------------------------------------------------------------------

locals {
  # Minimum scopes required by Monaco v2 for SLO/settings/dashboard management
  monaco_scopes = [
    "settings.read",
    "settings.write",
    "slo.read",
    "slo.write",
    "DataExport",
  ]
}

resource "dynatrace_api_token" "dev" {
  provider = dynatrace.dev
  name     = "argocd-monaco-oac-dev"
  enabled  = true
  scopes   = local.monaco_scopes
}

resource "dynatrace_api_token" "staging" {
  provider = dynatrace.staging
  name     = "argocd-monaco-oac-staging"
  enabled  = true
  scopes   = local.monaco_scopes
}

resource "dynatrace_api_token" "prod" {
  provider = dynatrace.prod
  name     = "argocd-monaco-oac-prod"
  enabled  = true
  scopes   = local.monaco_scopes
}

# ---------------------------------------------------------------------------
# Write tokens and URLs to Vault
# ExternalSecrets Operator reads from these paths via the vault-backend ClusterSecretStore.
# ---------------------------------------------------------------------------

resource "vault_generic_secret" "dynatrace_tokens" {
  path = "secret/dynatrace/tokens"
  # data_json — values are only stored in Vault state, never in TF state files
  data_json = jsonencode({
    "dev-token"     = dynatrace_api_token.dev.token
    "staging-token" = dynatrace_api_token.staging.token
    "prod-token"    = dynatrace_api_token.prod.token
  })
}

resource "vault_generic_secret" "dynatrace_tenants" {
  path = "secret/dynatrace/tenants"
  data_json = jsonencode({
    "dev-url"     = var.dt_dev_url
    "staging-url" = var.dt_staging_url
    "prod-url"    = var.dt_prod_url
  })
}

# ---------------------------------------------------------------------------
# Outputs — sensitive, only visible via `terraform output -json`
# ---------------------------------------------------------------------------

output "dev_token_id" {
  value       = dynatrace_api_token.dev.id
  description = "DT token ID for dev (not the token value — use Vault for the value)"
}

output "staging_token_id" {
  value       = dynatrace_api_token.staging.id
  description = "DT token ID for staging"
}

output "prod_token_id" {
  value       = dynatrace_api_token.prod.id
  description = "DT token ID for prod"
}

output "vault_tokens_path" {
  value       = vault_generic_secret.dynatrace_tokens.path
  description = "Vault path where token values are stored"
}
