variable "dt_url" {
  description = "Dynatrace tenant URL (e.g. https://<tenant>.live.dynatrace.com)"
  type        = string
}

variable "dt_api_token" {
  description = "Dynatrace API token with settings.read and settings.write scopes"
  type        = string
  sensitive   = true
}

variable "environments" {
  description = "Map of environment name to its config"
  type = map(object({
    # Label used in entity selector conditions and MZ name
    label : string
    # Kubernetes namespace patterns that belong to this environment.
    # Supports wildcards, e.g. "*-dev" matches payments-dev, orders-dev, etc.
    namespace_patterns : list(string)
    # Optional: k8s cluster names to scope this MZ to (empty = all clusters)
    cluster_names : list(string)
    # Description shown in the Dynatrace UI
    description : string
  }))
  default = {
    dev = {
      label              = "dev"
      namespace_patterns = ["*-dev", "dev-*", "development"]
      cluster_names      = []
      description        = "All services running in development namespaces"
    }
    staging = {
      label              = "staging"
      namespace_patterns = ["*-staging", "staging-*"]
      cluster_names      = []
      description        = "All services running in staging / pre-production namespaces"
    }
    perf = {
      label              = "perf"
      namespace_patterns = ["*-perf", "perf-*", "*-performance", "performance-*"]
      cluster_names      = []
      description        = "All services running in performance / load-test namespaces"
    }
    prod = {
      label              = "prod"
      namespace_patterns = ["*-prod", "prod-*", "production"]
      cluster_names      = []
      description        = "All services running in production namespaces — highest priority"
    }
  }
}
