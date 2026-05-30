# Management zones — one per environment (dev, staging, perf, prod).
#
# Now that auto_tags.tf creates the `environment` and `k8s.namespace.name` tags
# from Kubernetes/Backstage labels, the MZ rules can match those tags directly
# instead of doing raw namespace string matching.
#
# Matching strategy (in priority order):
#   1. SELECTOR rule using entity selector — most precise, catches everything
#      that carries the `environment:<label>` tag created by auto_tags.tf
#   2. Attribute rules on namespace name — belt-and-suspenders for entities
#      not yet tagged (e.g. on first deploy before auto-tag runs)
#
# Depends on: dynatrace_autotag_v2.backstage["environment"] (auto_tags.tf)

resource "dynatrace_management_zone_v2" "environment" {
  for_each = var.environments

  name        = "env:${each.value.label}"
  description = each.value.description

  # Explicit dependency: MZ tag-matching only works after the auto-tag rule exists
  depends_on = [dynatrace_autotag_v2.backstage, dynatrace_autotag_v2.k8s_namespace]

  rules {
    # ── Rule 1: SELECTOR — match any entity tagged environment:<label> ────
    # This is the primary rule. Works for services, process groups, synthetics,
    # and any future entity type — as long as the `environment` k8s label is set.
    rule {
      type    = "SELECTOR"
      enabled = true
      entity_selector = "tag(\"environment:${each.value.label}\")"
    }

    # ── Rule 2: CLOUD_APPLICATION_NAMESPACE by name pattern ───────────────
    # Belt-and-suspenders: catches namespaces before the auto-tag propagates,
    # and namespaces that don't carry the environment label explicitly.
    dynamic "rule" {
      for_each = each.value.namespace_patterns
      content {
        type    = "ME"
        enabled = true
        attribute_rule {
          entity_type = "CLOUD_APPLICATION_NAMESPACE"
          attribute_conditions {
            condition {
              key            = "CLOUD_APPLICATION_NAMESPACE_NAME"
              operator       = "CONTAINS"
              string_value   = replace(rule.value, "*", "")
              case_sensitive = false
            }
          }
        }
      }
    }

    # ── Rule 3: SERVICE matched by k8s.namespace.name tag ─────────────────
    # k8s.namespace.name tag is created by dynatrace_autotag_v2.k8s_namespace.
    # Covers services in any namespace matching the environment's patterns.
    dynamic "rule" {
      for_each = each.value.namespace_patterns
      content {
        type    = "ME"
        enabled = true
        attribute_rule {
          entity_type               = "SERVICE"
          pg_to_service_propagation = true
          attribute_conditions {
            condition {
              key      = "SERVICE_TAGS"
              operator = "TAG_KEY_EQUALS"
              tag      = "[CONTEXTLESS]k8s.namespace.name:${replace(rule.value, "*", "")}"
            }
          }
        }
      }
    }

    # ── Rule 4: PROCESS_GROUP matched by k8s.namespace.name tag ───────────
    dynamic "rule" {
      for_each = each.value.namespace_patterns
      content {
        type    = "ME"
        enabled = true
        attribute_rule {
          entity_type               = "PROCESS_GROUP"
          pg_to_service_propagation = true
          attribute_conditions {
            condition {
              key      = "PROCESS_GROUP_TAGS"
              operator = "TAG_KEY_EQUALS"
              tag      = "[CONTEXTLESS]k8s.namespace.name:${replace(rule.value, "*", "")}"
            }
          }
        }
      }
    }

    # ── Rule 5: HTTP_MONITOR matched by environment tag ────────────────────
    rule {
      type    = "ME"
      enabled = true
      attribute_rule {
        entity_type = "HTTP_MONITOR"
        attribute_conditions {
          condition {
            key      = "HTTP_MONITOR_TAGS"
            operator = "TAG_KEY_EQUALS"
            tag      = "[CONTEXTLESS]environment:${each.value.label}"
          }
        }
      }
    }
  }
}
