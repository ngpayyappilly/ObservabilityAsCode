# Building provider-dynatrace with upjet

upjet generates a Crossplane provider from any Terraform provider.
It introspects the TF schema and emits CRDs + controller code automatically.

## Prerequisites

```bash
go install github.com/crossplane/upjet/cmd/upjet@latest
```

## Scaffold the provider

```bash
# Clone the upjet provider template
git clone https://github.com/upbound/upjet-provider-template provider-dynatrace
cd provider-dynatrace

# Configure the Terraform provider to wrap
# Edit config/provider.go:
#   ProviderName = "dynatrace"
#   ProviderSource = "dynatrace-oss/dynatrace"
#   ProviderVersion = "~> 1.0"
```

## Resource groups to generate (edit config/dynatrace/config.go)

```go
// High-priority resources for OaC:
var resourceGroups = []string{
    // Application-level (per-service)
    "dynatrace_slo_v2",
    "dynatrace_alerting",
    "dynatrace_metric_events",
    "dynatrace_dashboard",
    "dynatrace_synthetic_monitor",

    // Platform-level (shared)
    "dynatrace_management_zone_v2",
    "dynatrace_autotag_v2",
    "dynatrace_pager_duty_notification",
    "dynatrace_slack_notification",
    "dynatrace_victor_ops_notification",
    "dynatrace_msteams_connection",
    "dynatrace_request_attribute",
    "dynatrace_attribute_allow_list",
    "dynatrace_attribute_masking",
    "dynatrace_span_capture_rule",
}
```

## Generate CRDs and controllers

```bash
make generate
```

This emits:
  package/crds/                  ← one CRD per DT resource type
  apis/slov2/v1alpha1/           ← Go types
  internal/controller/slov2/     ← reconciler

## Build and push

```bash
make build
make docker-build docker-push IMG=YOUR_REGISTRY/provider-dynatrace:v0.1.0
```

## Install in cluster

```bash
kubectl apply -f package/crds/
cat <<EOF | kubectl apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-dynatrace
spec:
  package: YOUR_REGISTRY/provider-dynatrace:v0.1.0
EOF
```

## What you get after generation

Each DT resource becomes a Kubernetes CRD:

  SloV2.dynatrace.crossplane.io
  Alerting.dynatrace.crossplane.io
  Dashboard.dynatrace.crossplane.io
  SyntheticMonitor.dynatrace.crossplane.io
  ManagementZoneV2.dynatrace.crossplane.io
  AutotagV2.dynatrace.crossplane.io
  ...

Teams create these objects; the provider controller continuously reconciles
them against the Dynatrace API. No Monaco, no CMP sidecar, no drift CronJob.
