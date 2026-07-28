# Notification integrations — wired to alerting profiles by ID.
#
# Each block is guarded by an `enabled` flag in var.notifications so you can
# deploy only the channels your org actually uses without deleting resources.
#
# Channel assignment per environment:
#
#   env     │ Slack           │ MS Teams        │ PagerDuty      │ Splunk On-Call
#   ────────┼─────────────────┼─────────────────┼────────────────┼─────────────────
#   dev     │ #alerts-dev     │ Dev Alerts      │ —              │ —
#   staging │ #alerts-staging │ Staging Alerts  │ —              │ —
#   perf    │ #alerts-perf    │ Perf Alerts     │ —              │ —
#   prod    │ #alerts-prod    │ Prod Alerts     │ prod-p1 policy │ prod routing key
#
# All secrets (webhook URLs, API keys) come from var.notifications which is
# marked sensitive — populate it via Vault-backed TF_VAR_notifications or a
# secrets-manager wrapper, never in plaintext tfvars.

# ─────────────────────────────────────────────────────────────────────────────
# Slack
# ─────────────────────────────────────────────────────────────────────────────

resource "dynatrace_slack_notification" "environment" {
  # Only the environment-name keys are exposed here, never the secret values —
  # nonsensitive() is required because Terraform forbids sensitive values in
  # for_each keys, and var.notifications is marked sensitive as a whole.
  for_each = nonsensitive(
    try(var.notifications.slack.enabled, false)
    ? { for k, v in var.environments : k => v
    if try(var.notifications.slack.channels[k], null) != null }
    : {}
  )

  name    = "slack-${each.value.label}"
  active  = true
  profile = dynatrace_alerting.environment[each.key].id

  channel = var.notifications.slack.channels[each.key].channel
  url     = var.notifications.slack.channels[each.key].webhook_url
  message = var.notification_message_template
}

# ─────────────────────────────────────────────────────────────────────────────
# MS Teams  (uses dynatrace_msteams_connection — the newer Settings 2.0 resource)
# ─────────────────────────────────────────────────────────────────────────────

resource "dynatrace_msteams_connection" "environment" {
  for_each = nonsensitive(
    try(var.notifications.msteams.enabled, false)
    ? { for k, v in var.environments : k => v
    if try(var.notifications.msteams.channels[k], null) != null }
    : {}
  )

  name         = "msteams-${each.value.label}"
  webhook      = var.notifications.msteams.channels[each.key].webhook_url
  team_name    = var.notifications.msteams.channels[each.key].team_name
  channel_name = var.notifications.msteams.channels[each.key].channel_name
}

# ─────────────────────────────────────────────────────────────────────────────
# PagerDuty — only environments that have a PD integration configured
# ─────────────────────────────────────────────────────────────────────────────

resource "dynatrace_pager_duty_notification" "environment" {
  for_each = nonsensitive(
    try(var.notifications.pagerduty.enabled, false)
    ? { for k, v in var.environments : k => v
    if try(var.notifications.pagerduty.integrations[k], null) != null }
    : {}
  )

  name    = "pagerduty-${each.value.label}"
  active  = true
  profile = dynatrace_alerting.environment[each.key].id

  account = var.notifications.pagerduty.integrations[each.key].account
  service = var.notifications.pagerduty.integrations[each.key].service_key
  # api_key is the PD v2 Events API integration key
  api_key = var.notifications.pagerduty.integrations[each.key].service_key
}

# ─────────────────────────────────────────────────────────────────────────────
# Splunk On-Call (VictorOps)
# ─────────────────────────────────────────────────────────────────────────────

resource "dynatrace_victor_ops_notification" "environment" {
  for_each = nonsensitive(
    try(var.notifications.splunk_oncall.enabled, false)
    ? { for k, v in var.environments : k => v
    if try(var.notifications.splunk_oncall.integrations[k], null) != null }
    : {}
  )

  name    = "splunk-oncall-${each.value.label}"
  active  = true
  profile = dynatrace_alerting.environment[each.key].id

  routing_key = var.notifications.splunk_oncall.integrations[each.key].routing_key
  api_key     = var.notifications.splunk_oncall.integrations[each.key].api_key
  message     = var.notification_message_template
}
