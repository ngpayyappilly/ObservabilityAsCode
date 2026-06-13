# Crossplane — XRD & Composition Reference

The `ServiceObservability` XRD is the high-level API that application teams
write claims against. One claim per environment creates all observability
resources for a service: SLOs, alerts, a dashboard, and a synthetic monitor.

---

## Architecture

```
Team writes:                    Crossplane expands to:
─────────────────               ────────────────────────────────────────
ServiceObservabilityClaim       SloV2 "payments-api-availability-prod"
  spec.serviceName: payments    SloV2 "payments-api-latency-prod"
  spec.environment: prod   ──▶  MetricEvents "payments-api-error-rate-prod"
  spec.slo.target: 99.9         MetricEvents "payments-api-burn-fast-prod"
  spec.alerts.burnRateFast: 14  MetricEvents "payments-api-burn-slow-prod"
  spec.synthetic.url: ...       HttpMonitor "payments-api-health-prod"
```

The Composition uses **patch-and-transform** to map claim fields to composed
resource specs, and **environment-specific ProviderConfig routing** to target
the correct Dynatrace tenant.

---

## XRD: ServiceObservability

**Group:** `oac.YOUR_ORG.io`  
**Version:** `v1alpha1`  
**Claim kind:** `ServiceObservabilityClaim` (namespaced)  
**Composite kind:** `XServiceObservability` (cluster-scoped)

### Full spec

```yaml
spec:
  # Required — service name matching Backstage catalog metadata.name
  serviceName: payments-api          # pattern: ^[a-z0-9][a-z0-9-]*[a-z0-9]$

  # Required — target Dynatrace environment
  environment: prod                  # dev | staging | perf | prod

  # Required — owning team (must match 'team' k8s label on pods)
  team: platform

  # Optional — management zone override; defaults to env:<environment>
  managementZone: ""

  # Optional — SLO target overrides (per-environment defaults in Composition)
  slo:
    availabilityTarget: 99.9         # 0–100; Composition default: 99.9 (prod)
    availabilityWarning: 99.5
    latencyTargetMs: 300             # milliseconds
    latencyTarget: 99.9
    latencyWarning: 99.5

  # Optional — alert thresholds
  alerts:
    errorRateThreshold: 0.5          # percent
    burnRateFast: 14                 # multiplier, 1h window
    burnRateSlow: 6                  # multiplier, 6h window
    alertingProfileId: "abc-123"     # from Terraform output alerting_profile_ids

  # Optional — HTTP synthetic health check
  synthetic:
    enabled: true
    url: "https://payments-api.prod.internal/health"
    frequencyMinutes: 1              # 1–60

  # Optional — per-endpoint SLOs
  endpoints:
    - id: post-payments              # lowercase alphanumeric + hyphens
      method: POST                   # GET | POST | PUT | PATCH | DELETE
      path: /api/v1/payments
      latencyMs: 400                 # endpoint-specific latency threshold
      sloTarget: 99.9                # endpoint-specific SLO target
```

### Status

```yaml
status:
  availabilitySloId: "SLO-abc123"    # written back from composed SloV2
  latencySloId: "SLO-xyz789"
  dashboardId: "DASHBOARD-001"
  syntheticMonitorId: "SYNTHETIC-001"
  conditions:
    - type: Synced
      status: "True"
    - type: Ready
      status: "True"
```

---

## Composition: service-observability

The Composition maps claim fields to individual DT resource CRDs via
**patch-and-transform** rules.

### ProviderConfig routing by environment

The critical patch in every composed resource:

```yaml
patches:
  - type: FromCompositeFieldPath
    fromFieldPath: spec.environment
    toFieldPath: spec.providerConfigRef.name
    transforms:
      - type: map
        map:
          dev:     dynatrace-dev
          staging: dynatrace-staging
          perf:    dynatrace-perf
          prod:    dynatrace-prod
```

This routes each composed resource to the correct DT tenant automatically.
A prod claim creates resources in the prod tenant; a dev claim uses the dev tenant.

### Composed resources

| Resource name | Kind | What it creates |
|---|---|---|
| `availability-slo` | `SloV2` | Availability SLO using `builtin:service.errors.total.successCount` |
| `latency-slo` | `SloV2` | Latency p99 SLO with threshold from `spec.slo.latencyTargetMs` |
| `error-rate-alert` | `MetricEvents` | STATIC_THRESHOLD alert on `builtin:service.errors.total.rate` |
| `burn-alert-fast` | `MetricEvents` | 14× burn rate, 1h window — pages immediately |
| `burn-alert-slow` | `MetricEvents` | 6× burn rate, 6h window — ticket-level |
| `health-check-synthetic` | `HttpMonitor` | HTTP GET to `spec.synthetic.url` at `spec.synthetic.frequencyMinutes` |

### SLO name construction

The Composition uses `CombineFromComposite` to build the SLO name:

```yaml
- type: CombineFromComposite
  combine:
    variables:
      - fromFieldPath: spec.serviceName
      - fromFieldPath: spec.environment
    strategy: string
    string:
      fmt: "%s Availability SLO (%s)"
  toFieldPath: spec.forProvider.name
# Result: "payments-api Availability SLO (prod)"
```

### Entity filter construction

The availability SLO entity filter:

```yaml
- type: CombineFromComposite
  combine:
    variables:
      - fromFieldPath: spec.environment
    strategy: string
    string:
      fmt: "type(\"SERVICE\"),mzName(\"env:%s\")"
  toFieldPath: spec.forProvider.filter
# Result: type("SERVICE"),mzName("env:prod")
```

### Latency metric selector

The latency SLO numerator is built dynamically from `spec.slo.latencyTargetMs`:

```yaml
- type: CombineFromComposite
  combine:
    variables:
      - fromFieldPath: spec.slo.latencyTargetMs
    strategy: string
    string:
      fmt: "builtin:service.response.time:percentile(99):filter(lt(value,%d000)):splitBy()"
  toFieldPath: spec.forProvider.metricNumerator
# Result: builtin:service.response.time:percentile(99):filter(lt(value,300000)):splitBy()
```

---

## Writing a claim

### Minimal claim (dev)

```yaml
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: ServiceObservabilityClaim
metadata:
  name: payments-api-dev
  namespace: payments-api
spec:
  serviceName: payments-api
  environment: dev
  team: platform
  # SLO and alert values use Composition defaults for dev:
  # availabilityTarget: 99.0, errorRateThreshold: 2.0, burnRateFast: 10
```

### Full prod claim

```yaml
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: ServiceObservabilityClaim
metadata:
  name: payments-api-prod
  namespace: payments-api
spec:
  serviceName: payments-api
  environment: prod
  team: platform

  slo:
    availabilityTarget: 99.9
    availabilityWarning: 99.5
    latencyTargetMs: 300
    latencyTarget: 99.9
    latencyWarning: 99.5

  alerts:
    errorRateThreshold: 0.5
    burnRateFast: 14
    burnRateSlow: 6
    alertingProfileId: "REPLACE_WITH_TF_OUTPUT_alerting_profile_ids_prod"

  synthetic:
    enabled: true
    url: "https://payments-api.prod.internal/health"
    frequencyMinutes: 1

  endpoints:
    - id: post-payments
      method: POST
      path: /api/v1/payments
      latencyMs: 400
      sloTarget: 99.9
    - id: get-status
      method: GET
      path: /api/v1/payments/{id}/status
      latencyMs: 200
      sloTarget: 99.95
```

---

## Inspecting composed resources

```bash
# See all resources created by a claim
kubectl get managed -l crossplane.io/claim-name=payments-api-prod

# See the composite resource (cluster-scoped)
kubectl get xserviceobservability
# NAME                           SYNCED   READY   AGE
# payments-api-prod-xxxxx        True     True    5m

# Describe for events and patch trace
kubectl describe xserviceobservability payments-api-prod-xxxxx

# Get the availability SLO ID written back to the claim
kubectl get serviceobservabilityclaim payments-api-prod -n payments-api \
  -o jsonpath='{.status.availabilitySloId}'
# SLO-abc123
```

---

## Extending the Composition

### Adding a new composed resource (e.g. log metric)

1. Add to `crossplane/compositions/service-observability-composition.yaml`:

```yaml
resources:
  - name: error-log-metric
    base:
      apiVersion: settings.dynatrace.crossplane.io/v1alpha1
      kind: AttributeAllowList          # or the log metric CRD when available
      spec:
        forProvider:
          key: "log.error.count"
          enabled: true
    patches:
      - type: FromCompositeFieldPath
        fromFieldPath: spec.environment
        toFieldPath: spec.providerConfigRef.name
        transforms:
          - type: map
            map:
              dev: dynatrace-dev
              staging: dynatrace-staging
              perf: dynatrace-perf
              prod: dynatrace-prod
```

2. Add the corresponding field to the XRD schema if teams need to configure it.

3. Apply the updated Composition — existing claims pick up the new resource
   on their next reconcile (Crossplane re-evaluates all Compositions against
   all composite resources automatically).

### Composition revisions

Crossplane supports `CompositionRevisions` — old claims keep working on the
old revision while new claims use the new one. Enable via:

```bash
helm upgrade crossplane crossplane-stable/crossplane \
  --set args='{"--enable-composition-revisions"}'
```

Then roll out new Composition versions safely by setting `compositionUpdatePolicy: Manual`
on sensitive claims.
