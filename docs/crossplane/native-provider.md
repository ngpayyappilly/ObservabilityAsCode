# Crossplane — Native provider-dynatrace Guide

This guide covers building `provider-dynatrace` using upjet — the tool that
auto-generates Crossplane providers from Terraform provider schemas.

Estimated build time: **2–4 months** for a production-ready provider covering
the resource types used in this OaC system.

---

## What upjet generates

Given a Terraform provider, upjet produces:
- **CRDs** — one per Terraform resource type (e.g. `SloV2.dynatrace.crossplane.io`)
- **Controllers** — one reconciler per CRD (create/update/delete/observe)
- **Go types** — typed structs matching the TF schema
- **Conversion functions** — TF state ↔ Kubernetes spec

You write configuration (which resources to include, how to name groups) and
upjet handles the rest.

---

## Step 1 — Scaffold the provider

```bash
# Clone the upjet provider template
git clone https://github.com/upbound/upjet-provider-template provider-dynatrace
cd provider-dynatrace

# The template already has the directory structure and Makefile
ls
# Makefile  apis/  cmd/  config/  internal/  package/
```

---

## Step 2 — Configure the Terraform provider source

Edit `Makefile`:
```makefile
TERRAFORM_PROVIDER_SOURCE  = dynatrace-oss/dynatrace
TERRAFORM_PROVIDER_VERSION = 1.0.0
TERRAFORM_PROVIDER_REPO    = https://github.com/dynatrace-ace/terraform-provider-dynatrace
```

Edit `config/provider.go`:
```go
const (
    ModulePath            = "github.com/YOUR_ORG/provider-dynatrace"
    RootGroup             = "dynatrace.crossplane.io"
    ProviderName          = "dynatrace"
    ProviderIdentityType  = "token"
)
```

---

## Step 3 — Select resource groups

Create `config/dynatrace/config.go` to control which TF resources become CRDs.
Start with the minimum set for the OaC use case:

```go
package dynatrace

import "github.com/crossplane/upjet/pkg/config"

// Configure sets up the Dynatrace resource configurations.
func Configure(p *config.Provider) {

    // ── Application-level resources ───────────────────────────────────────
    // These are managed by ServiceObservabilityClaim Compositions

    p.AddResourceConfigurator("dynatrace_slo_v2", func(r *config.Resource) {
        r.ShortGroup = "slo"
        r.Kind = "SloV2"
        // Mark token field as sensitive — upjet handles Secret refs automatically
        r.TerraformResource.Schema["token"].Sensitive = true
    })

    p.AddResourceConfigurator("dynatrace_metric_events", func(r *config.Resource) {
        r.ShortGroup = "anomalydetection"
        r.Kind = "MetricEvents"
    })

    p.AddResourceConfigurator("dynatrace_dashboard", func(r *config.Resource) {
        r.ShortGroup = "dashboards"
        r.Kind = "Dashboard"
    })

    p.AddResourceConfigurator("dynatrace_browser_monitor", func(r *config.Resource) {
        r.ShortGroup = "synthetic"
        r.Kind = "BrowserMonitor"
    })

    p.AddResourceConfigurator("dynatrace_http_monitor", func(r *config.Resource) {
        r.ShortGroup = "synthetic"
        r.Kind = "HttpMonitor"
    })

    // ── Platform-level resources ──────────────────────────────────────────
    // These complement terraform/platform-resources/ for teams
    // who want full Crossplane coverage

    p.AddResourceConfigurator("dynatrace_management_zone_v2", func(r *config.Resource) {
        r.ShortGroup = "iam"
        r.Kind = "ManagementZoneV2"
    })

    p.AddResourceConfigurator("dynatrace_autotag_v2", func(r *config.Resource) {
        r.ShortGroup = "settings"
        r.Kind = "AutoTagV2"
    })

    p.AddResourceConfigurator("dynatrace_alerting", func(r *config.Resource) {
        r.ShortGroup = "alerting"
        r.Kind = "AlertingProfile"
    })

    p.AddResourceConfigurator("dynatrace_slack_notification", func(r *config.Resource) {
        r.ShortGroup = "alerting"
        r.Kind = "SlackNotification"
        // Webhook URL is sensitive — upjet will use a SecretKeyRef
        r.TerraformResource.Schema["url"].Sensitive = true
    })

    p.AddResourceConfigurator("dynatrace_pager_duty_notification", func(r *config.Resource) {
        r.ShortGroup = "alerting"
        r.Kind = "PagerDutyNotification"
        r.TerraformResource.Schema["api_key"].Sensitive = true
    })

    p.AddResourceConfigurator("dynatrace_victor_ops_notification", func(r *config.Resource) {
        r.ShortGroup = "alerting"
        r.Kind = "VictorOpsNotification"
        r.TerraformResource.Schema["api_key"].Sensitive = true
        r.TerraformResource.Schema["routing_key"].Sensitive = true
    })

    p.AddResourceConfigurator("dynatrace_request_attribute", func(r *config.Resource) {
        r.ShortGroup = "settings"
        r.Kind = "RequestAttribute"
    })

    p.AddResourceConfigurator("dynatrace_attribute_allow_list", func(r *config.Resource) {
        r.ShortGroup = "settings"
        r.Kind = "AttributeAllowList"
    })

    p.AddResourceConfigurator("dynatrace_span_capture_rule", func(r *config.Resource) {
        r.ShortGroup = "settings"
        r.Kind = "SpanCaptureRule"
    })
}
```

---

## Step 4 — Generate the provider

```bash
# Pull the Terraform provider binary for schema extraction
make pull-docs

# Generate CRDs, controllers, and Go types from the TF schema
make generate

# What gets created:
# apis/slo/v1alpha1/zz_slov2_types.go
# apis/slo/v1alpha1/zz_slov2_terraformed.go
# internal/controller/slo/zz_slov2_controller.go
# package/crds/slo.dynatrace.crossplane.io_slov2s.yaml
# ... one set per configured resource type
```

---

## Step 5 — Build and push

```bash
# Build the provider binary
make build

# Build and push the OCI package
make docker-build docker-push \
  IMG=YOUR_REGISTRY/provider-dynatrace:v0.1.0

# Build the Crossplane package (xpkg)
make xpkg-build xpkg-push \
  XPKG=YOUR_REGISTRY/provider-dynatrace-package:v0.1.0
```

---

## Step 6 — Install in the cluster

```bash
# Install the provider
cat <<EOF | kubectl apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-dynatrace
spec:
  package: YOUR_REGISTRY/provider-dynatrace-package:v0.1.0
EOF

# Wait for it to become healthy
kubectl wait provider.pkg provider-dynatrace \
  --for=condition=Healthy \
  --timeout=120s

# Verify CRDs are installed
kubectl get crds | grep dynatrace.crossplane.io
# slov2s.slo.dynatrace.crossplane.io
# metricevents.anomalydetection.dynatrace.crossplane.io
# dashboards.dashboards.dynatrace.crossplane.io
# managementzonev2s.iam.dynatrace.crossplane.io
# ...
```

---

## Step 7 — Configure ProviderConfigs

Apply the ProviderConfig manifests (one per DT environment):

```bash
kubectl apply -f crossplane/provider/provider-configs.yaml

# Verify
kubectl get providerconfig
# NAME               READY   AGE
# dynatrace-dev      True    30s
# dynatrace-staging  True    30s
# dynatrace-perf     True    30s
# dynatrace-prod     True    30s
```

Each ProviderConfig reads credentials from the ESO-managed Secrets
(`dynatrace-tenant-urls`, `dynatrace-api-tokens`) in `sre-tools`.

---

## Step 8 — Enable the Composition

With the native provider installed, the `ServiceObservabilityClaim` Composition
in `crossplane/compositions/service-observability-composition.yaml` becomes
fully functional. Apply it:

```bash
kubectl apply -f crossplane/xrds/service-observability-xrd.yaml
kubectl apply -f crossplane/compositions/service-observability-composition.yaml
```

Teams can now apply claims and every composed resource will be reconciled
individually by the native provider controllers.

---

## CRDs generated by upjet

After generation, these CRDs are available:

| CRD | API Group | Kind |
|---|---|---|
| SLO v2 | `slo.dynatrace.crossplane.io` | `SloV2` |
| Metric Events | `anomalydetection.dynatrace.crossplane.io` | `MetricEvents` |
| Dashboard | `dashboards.dynatrace.crossplane.io` | `Dashboard` |
| HTTP Monitor | `synthetic.dynatrace.crossplane.io` | `HttpMonitor` |
| Management Zone | `iam.dynatrace.crossplane.io` | `ManagementZoneV2` |
| Auto Tag | `settings.dynatrace.crossplane.io` | `AutoTagV2` |
| Alerting Profile | `alerting.dynatrace.crossplane.io` | `AlertingProfile` |
| Slack Notification | `alerting.dynatrace.crossplane.io` | `SlackNotification` |
| PagerDuty | `alerting.dynatrace.crossplane.io` | `PagerDutyNotification` |
| Splunk On-Call | `alerting.dynatrace.crossplane.io` | `VictorOpsNotification` |
| Request Attribute | `settings.dynatrace.crossplane.io` | `RequestAttribute` |
| Span Allow List | `settings.dynatrace.crossplane.io` | `AttributeAllowList` |
| Span Capture Rule | `settings.dynatrace.crossplane.io` | `SpanCaptureRule` |

---

## Build timeline estimate

| Phase | Duration | Deliverable |
|---|---|---|
| Provider scaffold + upjet config | 1–2 weeks | Provider compiles, basic CRDs generated |
| Resource group coverage | 3–4 weeks | All 13 resource types configured |
| Sensitive field handling | 1 week | Webhook URLs, API keys use SecretKeyRef |
| Integration testing | 2–3 weeks | Each CRD tested against a dev DT tenant |
| Composition updates | 1 week | Update ServiceObservability Composition to use native CRDs |
| Documentation + runbooks | 1 week | Team onboarding docs |
| Production hardening | 2–4 weeks | Error handling, rate limiting, leader election |
| **Total** | **~3 months** | Production-ready provider |

---

## Maintaining the provider

When the `dynatrace-oss/dynatrace` Terraform provider releases a new version:

```bash
# Update the provider version in Makefile
TERRAFORM_PROVIDER_VERSION = 1.1.0

# Re-pull docs and regenerate
make pull-docs generate

# Review generated diffs for breaking schema changes
git diff apis/ internal/

# Rebuild and release
make build docker-build docker-push xpkg-build xpkg-push
```

Most updates are additive (new resource types, new optional fields).
Breaking changes (field renames, type changes) require manual migration
of existing resources.
