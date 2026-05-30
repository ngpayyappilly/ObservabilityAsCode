# ── Management zones ───────────────────────────────────────────────────────

output "management_zone_ids" {
  description = "Map of environment name → Dynatrace management zone ID"
  value = {
    for k, mz in dynatrace_management_zone_v2.environment : k => mz.id
  }
}

output "management_zone_names" {
  description = "Map of environment name → management zone name (env:<label>)"
  value = {
    for k, mz in dynatrace_management_zone_v2.environment : k => mz.name
  }
}

# ── Alerting profiles ──────────────────────────────────────────────────────

output "alerting_profile_ids" {
  description = "Map of environment name → alerting profile ID"
  value = {
    for k, ap in dynatrace_alerting.environment : k => ap.id
  }
}

# These are the values to drop into Monaco environment variable files as
# AlertingProfileId. After `terraform apply`, update your scaffold env templates:
#
#   dev.yaml.j2:     AlertingProfileId: "<output.alerting_profile_ids.dev>"
#   staging.yaml.j2: AlertingProfileId: "<output.alerting_profile_ids.staging>"
#   perf.yaml.j2:    AlertingProfileId: "<output.alerting_profile_ids.perf>"
#   prod.yaml.j2:    AlertingProfileId: "<output.alerting_profile_ids.prod>"

output "alerting_profile_names" {
  description = "Map of environment name → alerting profile name (for reference)"
  value = {
    for k, ap in dynatrace_alerting.environment : k => ap.name
  }
}

# ── Notification summary (non-sensitive) ──────────────────────────────────

output "slack_notification_ids" {
  description = "Slack notification integration IDs (empty map if Slack disabled)"
  value = {
    for k, n in dynatrace_slack_notification.environment : k => n.id
  }
}

output "pagerduty_notification_ids" {
  description = "PagerDuty notification integration IDs (empty map if PD disabled)"
  value = {
    for k, n in dynatrace_pager_duty_notification.environment : k => n.id
  }
}

output "splunk_oncall_notification_ids" {
  description = "Splunk On-Call notification integration IDs (empty map if disabled)"
  value = {
    for k, n in dynatrace_victor_ops_notification.environment : k => n.id
  }
}

output "msteams_connection_ids" {
  description = "MS Teams connection IDs (empty map if disabled)"
  value = {
    for k, n in dynatrace_msteams_connection.environment : k => n.id
  }
}
