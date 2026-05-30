# Auto-tagging rules — read Kubernetes labels set by Backstage and propagate
# them as Dynatrace tags across all entity types.
#
# ── How Backstage metadata flows into Dynatrace ───────────────────────────
#
#   1. Backstage catalog entities define metadata in catalog-info.yaml:
#        metadata:
#          name: payments-api
#          annotations:
#            backstage.io/kubernetes-id: payments-api
#          labels:
#            team: platform
#            domain: checkout
#            tier: backend
#        spec:
#          owner: team-platform
#          lifecycle: production
#          system: checkout-platform
#
#   2. Teams mirror that metadata as Kubernetes labels on their Deployments:
#        labels:
#          backstage.io/kubernetes-id: payments-api
#          app.kubernetes.io/name: payments-api
#          app.kubernetes.io/part-of: checkout-platform
#          app.kubernetes.io/component: backend
#          team: platform
#          environment: prod
#
#   3. Dynatrace OneAgent reads pod labels automatically and surfaces them
#      as CLOUD_APPLICATION_LABELS on CLOUD_APPLICATION entities and as
#      PROCESS_GROUP_PREDEFINED_METADATA on PROCESS_GROUP entities.
#
#   4. These auto-tag rules translate those labels into DT contextless tags,
#      which the management zone rules (management_zones.tf) then match on.
#
# ── Required Kubernetes labels (Backstage convention) ─────────────────────
#
#   Label                         DT tag created
#   ─────────────────────────────────────────────────────────────────────────
#   app.kubernetes.io/name        service:<value>
#   app.kubernetes.io/part-of     system:<value>
#   app.kubernetes.io/component   component:<value>
#   team                          team:<value>
#   environment                   environment:<value>
#   backstage.io/kubernetes-id    backstage-id:<value>
#   domain                        domain:<value>
#   tier                          tier:<value>
#
# All rules apply to PROCESS_GROUP entities and propagate to SERVICE.
# ─────────────────────────────────────────────────────────────────────────

locals {
  # Label → tag-name pairs. Each becomes one dynatrace_autotag_v2 resource.
  # dynamic_key is the exact Kubernetes label key.
  backstage_label_tags = {
    service = {
      description = "Service name from app.kubernetes.io/name (Backstage kubernetes-id)"
      dynamic_key = "app.kubernetes.io/name"
      tag_prefix  = "service"
    }
    system = {
      description = "Backstage System from app.kubernetes.io/part-of"
      dynamic_key = "app.kubernetes.io/part-of"
      tag_prefix  = "system"
    }
    component = {
      description = "Component type from app.kubernetes.io/component"
      dynamic_key = "app.kubernetes.io/component"
      tag_prefix  = "component"
    }
    team = {
      description = "Owning team from the 'team' Kubernetes label (mirrors Backstage spec.owner)"
      dynamic_key = "team"
      tag_prefix  = "team"
    }
    environment = {
      description = "Environment from the 'environment' Kubernetes label"
      dynamic_key = "environment"
      tag_prefix  = "environment"
    }
    backstage_id = {
      description = "Backstage catalog entity ID from backstage.io/kubernetes-id annotation"
      dynamic_key = "backstage.io/kubernetes-id"
      tag_prefix  = "backstage-id"
    }
    domain = {
      description = "Business domain from the 'domain' Kubernetes label"
      dynamic_key = "domain"
      tag_prefix  = "domain"
    }
    tier = {
      description = "Service tier (frontend/backend/data) from the 'tier' label"
      dynamic_key = "tier"
      tag_prefix  = "tier"
    }
  }
}

resource "dynatrace_autotag_v2" "backstage" {
  for_each    = local.backstage_label_tags
  name        = each.value.tag_prefix
  description = each.value.description

  rules {
    # ── PROCESS_GROUP — read pod labels via PROCESS_GROUP_PREDEFINED_METADATA ──
    rule {
      type               = "ME"
      enabled            = true
      # Tag value = the label value from the pod, normalised to lowercase
      value_format       = "{ProcessGroup:KubernetesBasePodName}"
      value_normalization = "To lower case"

      attribute_rule {
        entity_type               = "PROCESS_GROUP"
        pg_to_service_propagation = true   # tag flows up to SERVICE automatically

        conditions {
          condition {
            key              = "PROCESS_GROUP_PREDEFINED_METADATA"
            operator         = "EXISTS"
            # dynamic_key targets the specific Kubernetes label
            dynamic_key        = each.value.dynamic_key
            dynamic_key_source = "KUBERNETES_LABEL"
          }
        }
      }

      # value_format uses the label value directly
    }

    # Override value_format for this rule to use the actual label value
    rule {
      type               = "ME"
      enabled            = true
      value_format       = "{ProcessGroup:KubernetesLabel[${each.value.dynamic_key}]}"
      value_normalization = "To lower case"

      attribute_rule {
        entity_type               = "PROCESS_GROUP"
        pg_to_service_propagation = true

        conditions {
          condition {
            key              = "PROCESS_GROUP_PREDEFINED_METADATA"
            operator         = "EXISTS"
            dynamic_key        = each.value.dynamic_key
            dynamic_key_source = "KUBERNETES_LABEL"
          }
        }
      }
    }
  }
}

# ── k8s.namespace.name — used by management zone SERVICE/PROCESS_GROUP rules ──
# This tag is what management_zones.tf matches on for the SERVICE entity type.
resource "dynatrace_autotag_v2" "k8s_namespace" {
  name        = "k8s.namespace.name"
  description = "Kubernetes namespace name — used by management zone rules to scope services to environments"

  rules {
    rule {
      type                = "ME"
      enabled             = true
      value_format        = "{ProcessGroup:KubernetesNamespace}"
      value_normalization = "Leave text as-is"

      attribute_rule {
        entity_type               = "PROCESS_GROUP"
        pg_to_service_propagation = true

        conditions {
          condition {
            key      = "PROCESS_GROUP_PREDEFINED_METADATA"
            operator = "EXISTS"
            # KUBERNETES_NAMESPACE is a built-in predefined metadata key —
            # no dynamic_key needed here.
            dynamic_key        = "KUBERNETES_NAMESPACE"
            dynamic_key_source = "KUBERNETES_LABEL"
          }
        }
      }
    }
  }
}
