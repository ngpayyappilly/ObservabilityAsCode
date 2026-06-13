# Operator — Dashboard Templates

The `DynatraceDashboard` controller builds dashboard JSON from three named templates.
Each template defines a fixed tile layout; service-specific values (service name,
management zone, SLO IDs) are substituted at reconcile time.

---

## How templates work

```
DynatraceDashboard CR
  spec.template: "service-overview"
  spec.sloRefs: [payments-api-availability, payments-api-latency-p99]
  spec.serviceSelector.backstageId: payments-api
  spec.environment: prod
        │
        │ controller resolves sloRefs → DT SLO IDs
        │ controller resolves backstageId → management zone
        ▼
internal/dynatrace/dashboard_templates.go
  BuildDashboard("service-overview", TemplateData{
    ServiceName:    "payments-api",
    Environment:    "prod",
    ManagementZone: "env:prod",
    SLOIDs:         ["SLO-abc", "SLO-xyz"],
    SLONames:       ["Availability (99.9%)", "Latency p99 < 300ms (99.9%)"],
  })
        │
        │ returns DashboardPayload with fully-populated tiles
        ▼
POST /api/config/v1/dashboards → DT creates dashboard
status.dashboardUrl = "https://<tenant>/#dashboard;id=DASHBOARD-xxx"
```

All metrics are automatically scoped to the management zone (`env:prod` by default).
This ensures prod dashboards show only prod traffic, even on a shared DT tenant.

---

## Template: `service-overview` (default)

**Use when:** Every service should have this as their standard dashboard.

### Layout (grid: 1216px wide, 304px row height)

```
┌────────────────────────────────────────────────────────────────────────┐ 152px
│  ## payments-api — Service Overview (prod)                             │
│  Management zone: `env:prod`                                           │
├────────────┬────────────┬────────────────────────────────────────────┤ 304px
│  SLO Tile  │  SLO Tile  │  Request Rate (req/min)                    │
│  Avail     │  Latency   │  builtin:service.requestCount.total        │
│  (99.9%)   │  (99.9%)   │                                            │
├────────────────────────┬───────────────────────────────────────────┤ 304px
│  Error Rate (%)        │  p99 Latency (ms)                         │
│  with color thresholds │  builtin:service.response.time:p99        │
│  green<0.5, red>1.0   │                                            │
├────────────────────────────────────────────────────────────────────┤ 304px
│  Error Budget Burn Rate (full width)                               │
│  ext:slo.errorBudgetBurnRate per SLO                               │
│  color bands: green<6×, yellow<14×, red≥14×                        │
├────────────┬────────────┬────────────┬──────────────────────────┤ 304px
│  p50 (ms)  │  p75 (ms)  │  p95 (ms)  │  p99 (ms)                │
│  single    │  single    │  single    │  single value tile       │
│  value     │  value     │  value     │                          │
└────────────┴────────────┴────────────┴──────────────────────────┘
```

### Tiles detail

| Tile | Type | Metric | Notes |
|---|---|---|---|
| Header | MARKDOWN | — | Service name + env + MZ |
| SLO-1 | SLO | `FUNC:slo.target` | First entry in `sloRefs` |
| SLO-2 | SLO | `FUNC:slo.target` | Second entry in `sloRefs` |
| Request Rate | DATA_EXPLORER | `builtin:service.requestCount.total` | Filtered by MZ |
| Error Rate | DATA_EXPLORER | `builtin:service.errors.total.rate` | Color bands at 0.5% / 1.0% |
| p99 Latency | DATA_EXPLORER | `builtin:service.response.time:percentile(99)` | Filtered by MZ |
| Burn Rate | DATA_EXPLORER | `ext:slo.errorBudgetBurnRate` | One query per SLO ID |
| p50 | DATA_EXPLORER | `builtin:service.response.time:percentile(50)` | Single value |
| p75 | DATA_EXPLORER | `builtin:service.response.time:percentile(75)` | Single value |
| p95 | DATA_EXPLORER | `builtin:service.response.time:percentile(95)` | Single value |
| p99 | DATA_EXPLORER | `builtin:service.response.time:percentile(99)` | Single value |

---

## Template: `slo-report`

**Use when:** Weekly SLA reviews, error budget reporting, incident post-mortems.

### Layout

```
┌────────────────────────────────────────────────────────────────────────┐ 152px
│  ## payments-api — SLO Report (prod)                                   │
│  Error budget and burn rate analysis. Zone: `env:prod`                 │
├────────────┬────────────┬──────────────────────────────────────────┤ 304px
│  SLO Tile  │  SLO Tile  │  SLO Compliance History                  │
│  Avail     │  Latency   │  ext:slo.status per SLO over time        │
│  (99.9%)   │  (99.9%)   │                                          │
├────────────────────────┬───────────────────────────────────────────┤ 304px
│  Error Budget Remaining│  Fast Burn Rate (1h window)               │
│  ext:slo.errorBudget   │  ext:slo.errorBudgetBurnRate:res(1h)      │
│  per SLO               │  red threshold at 14×                     │
├────────────────────────┬───────────────────────────────────────────┤ 304px
│  Slow Burn Rate (6h)   │  Availability vs SLO Target               │
│  ext:slo.burn:res(6h)  │  builtin:service.errors.total.successCount│
│  yellow at 6×          │                                           │
└────────────────────────┴───────────────────────────────────────────┘
```

### Tiles detail

| Tile | Type | Metric | Notes |
|---|---|---|---|
| Header | MARKDOWN | — | Report context |
| SLO-1 | SLO | `FUNC:slo.target` | Status indicator |
| SLO-2 | SLO | `FUNC:slo.target` | Status indicator |
| Compliance History | DATA_EXPLORER | `ext:slo.status` | One query per SLO |
| Error Budget Remaining | DATA_EXPLORER | `ext:slo.errorBudget` | One query per SLO |
| Fast Burn (1h) | DATA_EXPLORER | `ext:slo.errorBudgetBurnRate:resolution(1h)` | Red at 14× |
| Slow Burn (6h) | DATA_EXPLORER | `ext:slo.errorBudgetBurnRate:resolution(6h)` | Yellow at 6× |
| Availability vs Target | DATA_EXPLORER | `builtin:service.errors.total.successCount` | Trend line |

---

## Template: `endpoint-detail`

**Use when:** Deep-dive on high-traffic APIs, finding slow or error-prone endpoints.
Does not use `sloRefs` — metrics are split by `dt.entity.service_method`.

> **Prerequisite:** Endpoints must appear as `SERVICE_METHOD` entities in Dynatrace
> (detected from real traffic or marked as key requests).

### Layout

```
┌────────────────────────────────────────────────────────────────────────┐ 152px
│  ## payments-api — Endpoint Detail (prod)                              │
│  Per-endpoint throughput, error rate, and latency breakdown.           │
├────────────────────────┬───────────────────────────────────────────┤ 304px
│  Throughput by Endpoint│  Error Rate by Endpoint                   │
│  keyRequest.count      │  keyRequest.errorCount                    │
│  split by SERVICE_METHOD│  split by SERVICE_METHOD, red >1%        │
├────────────────────────┬───────────────────────────────────────────┤ 304px
│  p50 Latency by Endpoint│  p99 Latency by Endpoint                 │
│  keyRequest.response   │  keyRequest.response.time:p99             │
│  .time:p50 split by SM │  split by SERVICE_METHOD                  │
├────────────────────────────────────────────────────────────────────┤ 304px
│  Slowest Endpoints — Table (full width, top 20 by p99)             │
│  keyRequest.response.time:p99:sort(desc):limit(20)                 │
└────────────────────────────────────────────────────────────────────┘
```

### Tiles detail

| Tile | Type | Metric | Notes |
|---|---|---|---|
| Header | MARKDOWN | — | Context |
| Throughput by Endpoint | DATA_EXPLORER | `builtin:service.keyRequest.count.total` | Top 10, split by SERVICE_METHOD |
| Error Rate by Endpoint | DATA_EXPLORER | `builtin:service.keyRequest.errorCount` | Top 10, red >1% |
| p50 by Endpoint | DATA_EXPLORER | `builtin:service.keyRequest.response.time:percentile(50)` | Top 10 |
| p99 by Endpoint | DATA_EXPLORER | `builtin:service.keyRequest.response.time:percentile(99)` | Top 10 |
| Slowest Endpoints | DATA_EXPLORER | `builtin:service.keyRequest.response.time:percentile(99):sort(desc)` | Table, top 20 |

---

## Adding a new template

1. Add a new case to `operator/internal/dynatrace/dashboard_templates.go`:

   ```go
   func myCustomTemplate(d TemplateData) DashboardPayload {
       tiles := []DashboardTile{}
       // add tiles using helpers: sloTile(), dataExplorerTile(), markdownTile()
       return DashboardPayload{
           Metadata: dashboardMeta("My Custom Dashboard — "+d.ServiceName, ...),
           Tiles: tiles,
       }
   }
   ```

2. Register it in `BuildDashboard()`:
   ```go
   case "my-custom":
       return myCustomTemplate(d), nil
   ```

3. Add the enum value to `DynatraceDashboardSpec.Template` in `api/v1alpha1/types.go`:
   ```go
   const TemplateMyCustom DashboardTemplate = "my-custom"
   ```

4. Update the CRD validation in `config/crd/dynatracedashboards.yaml`:
   ```yaml
   template:
     type: string
     enum: [service-overview, slo-report, endpoint-detail, my-custom]
   ```

5. Rebuild and redeploy the operator. Teams can immediately use:
   ```yaml
   spec:
     template: my-custom
   ```
