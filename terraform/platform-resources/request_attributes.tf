# Request Attributes — custom dimensions captured from HTTP requests and
# propagated onto Dynatrace service call traces.
#
# These enrich every service trace with Backstage/platform metadata so you can:
#   - Filter and scope dashboards by team, environment, or domain
#   - Build calculated metrics split by request attribute (e.g. error rate per team)
#   - Write SLO metric selectors that filter by attribute value
#
# Capture strategy:
#   HTTP header  → read from the inbound HTTP request header
#   Span attr    → read from an OpenTelemetry span attribute (via the span_attribute_key)
#
# All attributes below are set by:
#   a) Your service's instrumentation (HTTP headers or OTel SDK)
#   b) Istio / Envoy, which injects and propagates standard headers
#
# ── Attribute inventory ───────────────────────────────────────────────────
#
#   Name                  Source header / span key           Example value
#   ─────────────────────────────────────────────────────────────────────────
#   Team                  X-Backstage-Team / team            platform
#   Service               X-Backstage-Service / service.name payments-api
#   Environment           X-Backstage-Env / deployment.env   prod
#   Domain                X-Backstage-Domain / domain        checkout
#   Tenant ID             X-Tenant-ID / tenant.id            acme-corp
#   Correlation ID        X-Correlation-ID / correlation.id  uuid
#   Feature Flag          X-Feature-Flag / feature.flag      new-checkout
#   HTTP Status Class     derived from HTTP status code       2xx / 4xx / 5xx
#   Backstage System      X-Backstage-System / system        checkout-platform

locals {
  # Shared value_processing for trimming whitespace and lowercasing
  # (inline in each resource — Terraform doesn't support reusable sub-blocks)

  request_attributes = {

    # ── Identity attributes (from Backstage metadata propagated as headers) ──

    team = {
      name          = "Team"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Backstage-Team", technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "team", technology = null },
      ]
    }

    service = {
      name          = "Service Name"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Backstage-Service", technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "service.name", technology = null },
      ]
    }

    environment = {
      name          = "Environment"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Backstage-Env", technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "deployment.environment", technology = null },
      ]
    }

    domain = {
      name          = "Domain"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Backstage-Domain", technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "domain", technology = null },
      ]
    }

    system = {
      name          = "System"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Backstage-System", technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "system", technology = null },
      ]
    }

    # ── Observability / routing attributes ───────────────────────────────

    correlation_id = {
      name          = "Correlation ID"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "ORIGINAL"
      confidential  = false
      sources = [
        # Standard distributed tracing correlation header
        { source = "REQUEST_HEADER", parameter_name = "X-Correlation-ID", technology = null },
        { source = "REQUEST_HEADER", parameter_name = "X-Request-ID",     technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "correlation.id", technology = null },
      ]
    }

    tenant_id = {
      name          = "Tenant ID"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Tenant-ID",  technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "tenant.id", technology = null },
      ]
    }

    feature_flag = {
      name          = "Feature Flag"
      data_type     = "STRING"
      aggregation   = "FIRST"
      normalization = "TO_LOWER_CASE"
      confidential  = false
      sources = [
        { source = "REQUEST_HEADER", parameter_name = "X-Feature-Flag", technology = null },
        { source = "SPAN_ATTRIBUTE",  parameter_name = null, span_attribute_key = "feature.flag", technology = null },
      ]
    }
  }
}

# Create one request attribute resource per entry in the map.
# Multiple data_sources blocks are created for each source in the list
# so DT tries header capture first, falls back to span attribute.

resource "dynatrace_request_attribute" "platform" {
  for_each = local.request_attributes

  name          = each.value.name
  enabled       = true
  data_type     = each.value.data_type
  aggregation   = each.value.aggregation
  normalization = each.value.normalization
  confidential  = each.value.confidential

  # One data_sources block per source entry
  dynamic "data_sources" {
    for_each = each.value.sources
    content {
      enabled = true
      source  = data_sources.value.source

      # HTTP header capture
      parameter_name = data_sources.value.source == "REQUEST_HEADER" ? data_sources.value.parameter_name : null

      # OTel span attribute capture
      span_attribute_key = data_sources.value.source == "SPAN_ATTRIBUTE" ? data_sources.value.span_attribute_key : null

      # Trim whitespace on all captured values
      value_processing {
        trim = true
      }
    }
  }
}

# ── HTTP Status Class — derived, not from headers ──────────────────────────
# Captures the HTTP response status code and buckets it as 2xx/3xx/4xx/5xx.
# Useful for splitting error rate metrics by status class in Data Explorer.

resource "dynatrace_request_attribute" "http_status_class" {
  name          = "HTTP Status Class"
  enabled       = true
  data_type     = "STRING"
  aggregation   = "FIRST"
  normalization = "ORIGINAL"
  confidential  = false

  data_sources {
    enabled = true
    source  = "RESPONSE_CODE"

    # Extract the first digit and append "xx" — e.g. "4" + "xx" = "4xx"
    value_processing {
      trim = false
      extract_substring {
        delimiter = ""
        position  = "BEFORE"
      }
      value_extractor_regex = "^(\\d)"
    }
  }
}
