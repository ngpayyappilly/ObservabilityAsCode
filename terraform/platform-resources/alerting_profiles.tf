# Alerting profiles — one per environment.
#
# Profile determines WHICH problem types get routed and after HOW LONG
# a notification fires. Notification resources (alerting_notifications.tf)
# then attach to a profile by ID.
#
# Escalation policy per environment:
#   dev     — notify immediately, INFO+ only, no delay (Slack only)
#   staging — notify immediately, WARNING+ events (Slack)
#   perf    — notify immediately, PERFORMANCE + RESOURCE events (Slack)
#   prod    — notify at 0min for AVAILABILITY/ERROR, 5min for SLOWDOWN (PD + Slack)
#
# Management zone scoping: each profile is bound to its env:* management zone
# so alerts from prod never route to the dev Slack channel.

locals {
  # Severity levels supported by dynatrace_alerting rules block
  severity = {
    availability = "AVAILABILITY"
    error        = "ERROR"
    performance  = "PERFORMANCE"
    resource     = "RESOURCE_CONTENTION"
    custom       = "CUSTOM_ALERT"
    monitoring   = "MONITORING_UNAVAILABLE"
  }
}

resource "dynatrace_alerting" "environment" {
  for_each        = var.environments
  name            = each.value.label                    # matches AlertingProfileId referenced in Monaco env files
  management_zone = dynatrace_management_zone_v2.environment[each.key].id

  rules {
    # ── AVAILABILITY — always notify, no delay ──────────────────────────
    rule {
      severity_level  = local.severity.availability
      delay_in_minutes = 0
      include_mode    = "INCLUDE_ALL"
    }

    # ── ERROR — always notify, no delay ────────────────────────────────
    rule {
      severity_level  = local.severity.error
      delay_in_minutes = 0
      include_mode    = "INCLUDE_ALL"
    }

    # ── PERFORMANCE — delay varies by env (immediate in prod, 5min elsewhere) ──
    rule {
      severity_level  = local.severity.performance
      delay_in_minutes = each.value.label == "prod" ? 5 : 0
      include_mode    = "INCLUDE_ALL"
    }

    # ── RESOURCE — only include in perf and prod ────────────────────────
    rule {
      severity_level  = local.severity.resource
      delay_in_minutes = 0
      include_mode    = contains(["perf", "prod"], each.value.label) ? "INCLUDE_ALL" : "NONE"
    }

    # ── CUSTOM_ALERT — SLO burn rate alerts from Monaco metric events ───
    rule {
      severity_level  = local.severity.custom
      delay_in_minutes = 0
      include_mode    = "INCLUDE_ALL"
    }

    # ── MONITORING_UNAVAILABLE — only prod and staging care ────────────
    rule {
      severity_level  = local.severity.monitoring
      delay_in_minutes = 0
      include_mode    = contains(["staging", "prod"], each.value.label) ? "INCLUDE_ALL" : "NONE"
    }
  }

  # Event type filters: suppress noisy infrastructure events in dev/perf
  filters {
    dynamic "filter" {
      # In dev: suppress resource contention predefined events (noisy in load tests)
      for_each = each.value.label == "dev" ? ["RESOURCE_CONTENTION_EVENT"] : []
      content {
        predefined {
          type   = filter.value
          negate = false
        }
      }
    }
  }
}
