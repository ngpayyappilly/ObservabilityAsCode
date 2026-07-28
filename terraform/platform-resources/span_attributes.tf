# OpenTelemetry Span Attributes — controls which OTel span attributes Dynatrace
# indexes, and which need PII masking applied on top.
#
# Two resources work together:
#   dynatrace_attribute_allow_list  — tells DT to index and store this span key
#   dynatrace_attribute_masking     — applies PII masking to sensitive keys
#                                     (only needed for keys that are also allow-listed)
#
# Masking options (dynatrace_attribute_masking only):
#   MASK_ONLY_CONFIDENTIAL_DATA  — store value; redact anything matching DT PII patterns
#   MASK_ENTIRE_VALUE            — store key presence only; value shown as ***
#
# ── Attribute inventory ────────────────────────────────────────────────────
#
#   OTel key                 Indexed  Masked                   Purpose
#   ─────────────────────────────────────────────────────────────────────────
#   service.name             ✓        —                        Service identity
#   service.version          ✓        —                        Deployment version
#   service.namespace        ✓        —                        k8s namespace
#   deployment.environment   ✓        —                        Environment tier
#   team                     ✓        —                        Owning team
#   domain                   ✓        —                        Business domain
#   system                   ✓        —                        Backstage System
#   backstage.id             ✓        —                        Backstage entity ID
#   feature.flag             ✓        —                        Active feature flags
#   tenant.id                ✓        MASK_ONLY_CONFIDENTIAL   Multi-tenant key
#   correlation.id           ✓        —                        Trace correlation
#   http.route               ✓        —                        API route (not URL)
#   http.method              ✓        —                        GET/POST/etc.
#   db.system                ✓        —                        Database type
#   db.name                  ✓        —                        Database name
#   db.operation             ✓        —                        SELECT/INSERT/etc.
#   messaging.system         ✓        —                        Kafka/RabbitMQ/etc.
#   messaging.destination    ✓        —                        Topic/queue name
#   rpc.system               ✓        —                        gRPC/HTTP
#   rpc.service              ✓        —                        gRPC service name
#   rpc.method               ✓        —                        gRPC method name
#   error.type               ✓        —                        Exception class name
#   k8s.namespace.name       ✓        —                        Pod namespace
#   k8s.pod.name             ✓        —                        Pod name
#   k8s.deployment.name      ✓        —                        Deployment name
#
# Intentionally NOT indexed (may contain PII / sensitive payload data):
#   http.url, http.target, db.statement, http.request.body

locals {
  # All span attribute keys to index (allow-list)
  span_allow_list = toset([
    # OpenTelemetry resource semantic conventions
    "service.name",
    "service.version",
    "service.namespace",

    # Backstage / platform metadata
    "deployment.environment",
    "team",
    "domain",
    "system",
    "backstage.id",
    "feature.flag",

    # Tenant / correlation
    "tenant.id",
    "correlation.id",

    # HTTP semantic conventions (routes only — not full URLs)
    "http.route",
    "http.method",

    # Database semantic conventions (no db.statement — may contain sensitive values)
    "db.system",
    "db.name",
    "db.operation",

    # Messaging semantic conventions
    "messaging.system",
    "messaging.destination",

    # RPC / gRPC semantic conventions
    "rpc.system",
    "rpc.service",
    "rpc.method",

    # Error information
    "error.type",

    # Kubernetes resource attributes
    "k8s.namespace.name",
    "k8s.pod.name",
    "k8s.deployment.name",
  ])

  # Subset of the allow-list that require masking (key → masking rule)
  span_masking = {
    # tenant.id could contain account identifiers matching PII patterns
    "tenant.id" = "MASK_ONLY_CONFIDENTIAL_DATA"
  }
}

# ── Allow-list: index every key in the set ────────────────────────────────

resource "dynatrace_attribute_allow_list" "platform" {
  for_each = local.span_allow_list
  key      = each.key
  enabled  = true
}

# ── Masking: apply PII rules on top for sensitive keys ────────────────────
# Only keys that are also in the allow-list should have masking configured.

resource "dynatrace_attribute_masking" "platform" {
  for_each = local.span_masking
  key      = each.key
  masking  = each.value
  enabled  = true

  # Ensure the allow-list entry exists before setting masking
  depends_on = [dynatrace_attribute_allow_list.platform]
}

# ─────────────────────────────────────────────────────────────────────────────
# Span capture rules — control which spans DT records vs ignores.
# Rules are evaluated top-to-bottom; first match wins.
# ─────────────────────────────────────────────────────────────────────────────

# Rule 1: Always capture spans that carry an error attribute
resource "dynatrace_span_capture_rule" "always_capture_errors" {
  name   = "Always capture error spans"
  action = "CAPTURE"

  matches {
    match {
      source         = "ATTRIBUTE"
      key            = "error"
      comparison     = "EQUALS"
      value          = "true"
      case_sensitive = false
    }
  }
}

# Rule 2: Always capture spans from services with a known team label
# (ensures OaC-managed services are never dropped by sampling)
resource "dynatrace_span_capture_rule" "always_capture_team_spans" {
  name         = "Always capture spans for team-labelled services"
  action       = "CAPTURE"
  insert_after = dynatrace_span_capture_rule.always_capture_errors.id

  matches {
    match {
      source     = "ATTRIBUTE"
      key        = "team"
      comparison = "EXISTS"
    }
  }
}

# Rule 3: Ignore Kubernetes liveness / readiness probe spans (no signal value)
resource "dynatrace_span_capture_rule" "ignore_health_spans" {
  name         = "Ignore k8s probe and health check spans"
  action       = "IGNORE"
  insert_after = dynatrace_span_capture_rule.always_capture_team_spans.id

  matches {
    match {
      source         = "SPAN_NAME"
      comparison     = "CONTAINS"
      value          = "/health"
      case_sensitive = false
    }
    match {
      source         = "SPAN_NAME"
      comparison     = "CONTAINS"
      value          = "/ready"
      case_sensitive = false
    }
    match {
      source         = "SPAN_NAME"
      comparison     = "CONTAINS"
      value          = "/live"
      case_sensitive = false
    }
    match {
      source         = "SPAN_NAME"
      comparison     = "CONTAINS"
      value          = "/metrics"
      case_sensitive = false
    }
  }
}

# Rule 4: Ignore Istio internal telemetry spans (generated by sidecar, not your code)
resource "dynatrace_span_capture_rule" "ignore_istio_internal" {
  name         = "Ignore Istio sidecar internal spans"
  action       = "IGNORE"
  insert_after = dynatrace_span_capture_rule.ignore_health_spans.id

  matches {
    match {
      source         = "INSTRUMENTATION_LIBRARY_NAME"
      comparison     = "STARTS_WITH"
      value          = "istio"
      case_sensitive = false
    }
  }
}
