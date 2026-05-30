# Variables for alerting profiles and notification integrations.
# Each environment gets one alerting profile, wired to one or more notification
# channels depending on its severity tier.

variable "notifications" {
  description = "Notification channel configuration per environment"
  type = object({
    slack = optional(object({
      enabled = bool
      # Map of env name → channel config
      channels = map(object({
        webhook_url = string # stored in Vault, passed via TF_VAR or tfvars
        channel     = string # e.g. #alerts-dev
      }))
    }))

    msteams = optional(object({
      enabled = bool
      channels = map(object({
        webhook_url = string
        team_name   = string # display only
        channel_name = string
      }))
    }))

    pagerduty = optional(object({
      enabled = bool
      # Only prod (and optionally staging) get PD
      integrations = map(object({
        account     = string # PD account subdomain
        service_key = string # PD integration key — sensitive, from Vault
        routing_key = string # not used by PD resource but kept for parity
      }))
    }))

    splunk_oncall = optional(object({
      enabled = bool
      # Splunk On-Call (formerly VictorOps)
      integrations = map(object({
        routing_key = string # VictorOps routing key — sensitive
        api_key     = string # VictorOps API key — sensitive
      }))
    }))
  })
  sensitive = true # whole object is sensitive because it contains secrets
}

variable "notification_message_template" {
  description = "Default message template for Slack and Splunk On-Call notifications"
  type        = string
  default     = <<-EOT
    {ProblemTitle}

    Severity:   {ProblemSeverity}
    Impact:     {ProblemImpact}
    Status:     {ProblemStatus}
    Root cause: {ProblemDetailsText}

    Open in Dynatrace: {ProblemURL}
  EOT
}
