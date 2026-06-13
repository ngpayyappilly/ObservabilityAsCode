# Operator — CRD API Reference

All CRDs live in group `oac.YOUR_ORG.io/v1alpha1` and are namespace-scoped.
Convention: each service team owns one namespace and creates their CRs there.

---

## DynatraceSLO

Creates a Dynatrace SLO v2 resource. Supports availability and latency p99 types.

**Short name:** `dtslo`

### Spec

```yaml
spec:
  # Required — target Dynatrace tenant
  environment: prod          # dev | staging | perf | prod

  # Required — how to identify the DT service entity
  serviceSelector:
    backstageId: payments-api    # Backstage metadata.name — resolved via auto-tag
    serviceName: ""              # Exact DT service name (alternative to backstageId)
    managementZone: ""           # Scopes entity search; defaults to env:<environment>

  # Required — SLO measurement strategy
  type: availability             # availability | latency

  # Required — SLO target percentage (0–100)
  target: 99.9

  # Optional — warning threshold; defaults to target - 0.5
  warning: 99.5

  # Optional — evaluation window; default -1w
  window: "-1w"                  # -1w | -30d | etc.

  # Required when type: latency
  latencyThresholdMs: 300        # p99 must be below this value
```

### Status

```yaml
status:
  dynatraceId: "SLO-XXXXXXXXXXXX"    # Assigned by DT on creation; used for updates
  currentValue: 99.94                # Live SLO percentage from DT
  errorBudgetRemaining: 4320.0       # Minutes of error budget left
  lastSyncTime: "2026-05-31T12:00:00Z"
  observedGeneration: 3
  conditions:
    - type: Synced
      status: "True"
      reason: Synced
      message: "SLO SLO-XXX synced to Dynatrace"
```

### Printer columns

```
NAME                        ENV    TYPE          TARGET   CURRENT   SYNCED   AGE
payments-api-availability   prod   availability  99.9     99.94     True     2d
payments-api-latency-p99    prod   latency       99.9     99.87     True     2d
```

### Examples

```yaml
# Availability SLO — 99.9% successful requests, scoped by Backstage ID
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: DynatraceSLO
metadata:
  name: payments-api-availability
  namespace: payments-api
spec:
  environment: prod
  serviceSelector:
    backstageId: payments-api
  type: availability
  target: 99.9
  warning: 99.5
  window: "-1w"
```

```yaml
# Latency SLO — p99 < 300ms, 99.9% of the time
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: DynatraceSLO
metadata:
  name: payments-api-latency-p99
  namespace: payments-api
spec:
  environment: prod
  serviceSelector:
    backstageId: payments-api
  type: latency
  target: 99.9
  warning: 99.5
  latencyThresholdMs: 300
```

---

## DynatraceAlert

Creates a Dynatrace metric event anomaly detection rule.
Burn rate alerts (`burnRateFast` / `burnRateSlow`) require a `sloRef` pointing
to a `DynatraceSLO` in the same namespace — the controller reads the DT SLO ID
from `.status.dynatraceId` at reconcile time.

**Short name:** `dtalert`

### Spec

```yaml
spec:
  # Required — target Dynatrace tenant
  environment: prod

  # Required — how to identify the service to alert on
  serviceSelector:
    backstageId: payments-api
    serviceName: ""
    managementZone: ""

  # Required — alert strategy
  type: errorRate          # errorRate | latencyP99 | burnRateFast | burnRateSlow

  # Required — alert threshold (units depend on type — see table below)
  threshold: 0.5

  # Required for burnRateFast / burnRateSlow — DynatraceSLO name in same namespace
  sloRef: payments-api-availability

  # Optional — DT alerting profile ID from Terraform output alerting_profile_ids
  alertingProfileRef: "abc-123-prod-profile-id"
```

### Threshold units by type

| `type` | `threshold` unit | Example |
|---|---|---|
| `errorRate` | Percentage | `0.5` → alert when error rate > 0.5% |
| `latencyP99` | Milliseconds | `300` → alert when p99 > 300ms |
| `burnRateFast` | Multiplier (1h window) | `14` → 14× burn rate in 1h |
| `burnRateSlow` | Multiplier (6h window) | `6` → 6× burn rate in 6h |

> `burnRateFast` and `burnRateSlow` implement the Google SRE Workbook
> dual-window multi-burn-rate alerting pattern.

### Status

```yaml
status:
  dynatraceId: "CUSTOM_ALERT-XXXXXXXXXXXX"
  lastSyncTime: "2026-05-31T12:00:00Z"
  observedGeneration: 1
  conditions:
    - type: Synced
      status: "True"
      reason: Synced
      message: "Alert synced to Dynatrace (ID: CUSTOM_ALERT-XXX)"
```

### Examples

```yaml
# Error rate alert
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: DynatraceAlert
metadata:
  name: payments-api-error-rate
  namespace: payments-api
spec:
  environment: prod
  serviceSelector:
    backstageId: payments-api
  type: errorRate
  threshold: 0.5
  alertingProfileRef: "REPLACE_WITH_TF_OUTPUT"
```

```yaml
# Fast burn rate alert — pages immediately on 14× burn in 1h
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: DynatraceAlert
metadata:
  name: payments-api-burn-fast
  namespace: payments-api
spec:
  environment: prod
  serviceSelector:
    backstageId: payments-api
  type: burnRateFast
  threshold: 14
  sloRef: payments-api-availability
  alertingProfileRef: "REPLACE_WITH_TF_OUTPUT"
```

---

## DynatraceDashboard

Creates a Dynatrace dashboard from a named template. The template determines
the tile layout; service name, environment, management zone, and SLO IDs are
substituted automatically.

See [Dashboard Templates](dashboard-templates.md) for full tile layout diagrams.

**Short name:** `dtdash`

### Spec

```yaml
spec:
  # Required — target Dynatrace tenant
  environment: prod

  # Required — service identity for metric filter substitution
  serviceSelector:
    backstageId: payments-api
    serviceName: ""
    managementZone: ""       # defaults to env:<environment>

  # Optional — dashboard layout template; default: service-overview
  template: service-overview  # service-overview | slo-report | endpoint-detail

  # Optional — DynatraceSLO names in the same namespace whose DT IDs
  # are embedded in SLO tiles. Controller waits for all SLOs to sync first.
  sloRefs:
    - payments-api-availability
    - payments-api-latency-p99

  # Optional — whether the dashboard is publicly visible in the DT UI
  shared: false
```

### Status

```yaml
status:
  dynatraceId: "DASHBOARD-XXXXXXXXXXXX"
  dashboardUrl: "https://<tenant>.live.dynatrace.com/#dashboard;id=DASHBOARD-XXX"
  lastSyncTime: "2026-05-31T12:00:00Z"
  observedGeneration: 2
  conditions:
    - type: Synced
      status: "True"
      reason: Synced
      message: "Dashboard synced via template 'service-overview' (DT ID: DASHBOARD-XXX)"
```

### Printer columns

```
NAMESPACE      NAME                      ENV    TEMPLATE          SYNCED  URL
payments-api   payments-api-overview     prod   service-overview  True    https://...
payments-api   payments-api-slo-report   prod   slo-report        True    https://...
```

---

## DynatraceNotification

Creates a Dynatrace notification integration. Credentials are read from a
Kubernetes Secret in the same namespace (created by ExternalSecrets from Vault).

**Short name:** `dtnotif`

### Spec

```yaml
spec:
  # Required — target Dynatrace tenant
  environment: prod

  # Required — Dynatrace alerting profile ID (from Terraform output)
  alertingProfileRef: "REPLACE_WITH_TF_OUTPUT_alerting_profile_ids_prod"

  # Required — notification channel type
  channel: pagerduty       # slack | msteams | pagerduty | splunkOncall

  # Required — Secret in the same namespace containing channel credentials
  secretRef:
    name: pagerduty-payments-api   # ESO creates this from Vault

  # Optional — display name for Slack/MSTeams
  channelName: "#alerts-prod"
```

### Required Secret keys per channel

| Channel | Secret keys required |
|---|---|
| `slack` | `webhook-url` |
| `msteams` | `webhook-url` |
| `pagerduty` | `service-key` |
| `splunkOncall` | `routing-key`, `api-key` |

### Creating the Secret via ExternalSecrets

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: pagerduty-payments-api
  namespace: payments-api
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: ClusterSecretStore
  target:
    name: pagerduty-payments-api
    creationPolicy: Owner
  data:
    - secretKey: service-key
      remoteRef:
        key: secret/teams/payments/pagerduty
        property: service-key
```

### Example

```yaml
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: DynatraceNotification
metadata:
  name: payments-api-pagerduty
  namespace: payments-api
spec:
  environment: prod
  alertingProfileRef: "REPLACE_WITH_TF_OUTPUT_alerting_profile_ids_prod"
  channel: pagerduty
  secretRef:
    name: pagerduty-payments-api
```

---

## Common kubectl operations

```bash
# List all OaC resources across all namespaces
kubectl get dtslo,dtalert,dtdash,dtnotif -A

# Watch a specific SLO for live status updates
kubectl get dtslo payments-api-availability -n payments-api -w

# Describe a CRD to see full status and events
kubectl describe dtslo payments-api-availability -n payments-api

# Force an immediate reconcile (by touching the object)
kubectl annotate dtslo payments-api-availability -n payments-api \
  oac/force-reconcile="$(date -u +%FT%TZ)" --overwrite

# Delete an SLO (controller removes it from DT via finalizer)
kubectl delete dtslo payments-api-availability -n payments-api

# View controller logs for a specific namespace/resource
kubectl logs -n sre-tools -l app.kubernetes.io/name=dynatrace-operator \
  | grep payments-api | tail -20
```
