# Dynatrace Operator — Overview & Architecture

The `dynatrace-operator` is a Kubernetes controller that continuously reconciles
four custom resource types to the Dynatrace REST API. It is the application-layer
complement to the Terraform platform resources — Terraform owns shared infrastructure
(management zones, alerting profiles), the operator owns per-service configs
(SLOs, alerts, dashboards).

---

## Why a custom operator instead of Monaco or Crossplane?

| Concern | Monaco CMP | Crossplane | Custom Operator |
|---|---|---|---|
| Reconciliation | Push on git sync only | ~10 min Workspace loop | ~5 min per-object loop |
| Drift correction | Separate CronJob | Workspace re-apply | Built into requeueAfter |
| API design | Monaco YAML + Jinja2 | `spec.forProvider.*` (verbose) | `spec.target: 99.9` (clean) |
| Business logic | None — templates only | Composition patches | Go controllers |
| Backstage resolution | Manual entity IDs | Not supported | Auto via `backstageId` tag |
| SLO → Alert ordering | Separate config entries | Composition ordering | `sloRef` cross-reference |
| Status visibility | ConfigMap hash | Composition status | `.status.currentValue` |

The operator wins on API clarity and business logic. When a team writes
`spec.type: availability` and `spec.target: 99.9`, the controller handles entity
resolution, metric selector construction, error budget configuration, and drift
detection — invisibly.

---

## Components

```
┌──────────────────────────────────────────────────────────────────────┐
│  Kubernetes cluster (TKGs)                                           │
│                                                                      │
│  ┌─────────────────────────────────┐   ┌─────────────────────────┐   │
│  │  App namespace (payments-api)   │   │  sre-tools namespace    │   │
│  │                                 │   │                         │   │
│  │  DynatraceSLO                   │   │  dynatrace-operator     │   │
│  │  DynatraceAlert  ───────────────┼──▶│  (2 replicas, HA)       │   │
│  │  DynatraceDashboard             │   │                         │   │
│  │  DynatraceNotification          │   │  Secrets (from ESO):    │   │
│  └─────────────────────────────────┘   │  dynatrace-tenant-urls  │   │
│                                        │  dynatrace-api-tokens   │   │
│  ┌─────────────────────────────────┐   └──────────┬──────────────┘   │
│  │  Argo CD                        │              │                  │
│  │  ApplicationSet                 │              │ REST API calls   │
│  │  oac-<repo>-<env>  ─────────────┼──────────────┘                  │
│  │  (syncs CRD manifests)          │                                 │
│  └─────────────────────────────────┘                                 │
└──────────────────────────────────────────────────────────────────────┘
        │ HTTP
        ▼
┌────────────────────────────────┐
│  Dynatrace SaaS                │
│  POST /api/v2/slos             │
│  POST /api/config/v1/...       │
│  POST /api/config/v1/dashboards│
└────────────────────────────────┘
```

---

## The four CRDs

| CRD | Short name | What it creates in DT |
|---|---|---|
| `DynatraceSLO` | `dtslo` | SLO v2 (availability or latency p99) |
| `DynatraceAlert` | `dtalert` | Metric event anomaly detection rule |
| `DynatraceDashboard` | `dtdash` | Dashboard from a named template |
| `DynatraceNotification` | `dtnotif` | Notification integration (Slack/MSTeams/PD/SplunkOC) |

All are namespaced. Convention: each service team owns one namespace and creates
their CRs there. The operator has cluster-wide read permissions on CRDs but
writes only to Dynatrace — it never mutates other namespaces.

---

## Reconciliation loop

Every controller follows the same pattern:

```
Watch event fires (create / update / delete / requeueAfter)
        │
        ├── DeletionTimestamp set?
        │     └── Call DT DELETE API → remove finalizer → done
        │
        ├── Finalizer missing?
        │     └── Add finalizer → update object → re-enter loop
        │
        ├── Resolve dependencies
        │   (sloRef → DT SLO ID, backstageId → DT SERVICE entityId)
        │   If dependency not ready → requeue after 15s
        │
        ├── Build DT API payload from spec
        │
        ├── status.dynatraceId set?
        │   ├── Yes → PUT /api/... (update)
        │   └── No  → POST /api/... (create) → store returned ID
        │
        ├── Update status conditions + currentValue
        │
        └── Requeue after 5min  ← drift detection
```

**Drift detection** is not a separate CronJob — it is the `RequeueAfter: 5 * time.Minute`
at the bottom of every reconcile. Every five minutes the controller fetches
the live DT resource and re-applies the spec. If someone changed the SLO target
directly in the DT UI, the next reconcile overwrites it back to what Git says.

---

## Backstage → Dynatrace entity resolution

When `spec.serviceSelector.backstageId` is set, the controller calls:

```
GET /api/v2/entities
  ?entitySelector=type("SERVICE"),tag("backstage-id:payments-api")
  &fields=entityId,displayName
```

This works because the Terraform auto-tagging rules (`auto_tags.tf`) create
a `backstage-id:<value>` tag on every DT SERVICE entity whose pods carry
the `backstage.io/kubernetes-id` Kubernetes label.

If no entity is found (service not yet deployed, or label missing), the controller
sets `status.conditions[Synced]=False` with reason `EntityResolution` and
requeues after 30s.

---

## Cross-resource dependencies

```
DynatraceSLO "payments-api-availability"
  status.dynatraceId = "SLO-abc123"   ← set on first successful reconcile
        │
        │ DynatraceAlert reads spec.sloRef
        ▼
DynatraceAlert "payments-api-burn-fast"
  spec.sloRef: payments-api-availability
  controller reads SLO.status.dynatraceId → uses "SLO-abc123" in burn rate metric selector
        │
        │ DynatraceDashboard watches DynatraceSLO events
        ▼
DynatraceDashboard "payments-api-overview"
  spec.sloRefs: [payments-api-availability, payments-api-latency-p99]
  controller waits for both SLO IDs before building dashboard JSON
  SLO tile references "SLO-abc123" directly (not a name — a real DT ID)
```

The `DynatraceDashboard` controller registers a `Watch` on `DynatraceSLO` objects.
When any SLO in the same namespace transitions to `Synced=True` (i.e., gets its
`status.dynatraceId`), the controller automatically re-queues all dashboards
in that namespace that reference it.

---

## Deletion and finalizers

Every CRD uses a finalizer:
- `dynatrace.YOUR_ORG.io/slo-finalizer`
- `dynatrace.YOUR_ORG.io/alert-finalizer`
- `dynatrace.YOUR_ORG.io/dashboard-finalizer`
- `dynatrace.YOUR_ORG.io/notification-finalizer`

When `kubectl delete dynatraceslo payments-api-availability` is run:
1. Kubernetes sets `DeletionTimestamp` — object is not deleted yet.
2. Controller sees the timestamp, calls `DELETE /api/v2/slos/SLO-abc123`.
3. On success, removes the finalizer.
4. Kubernetes deletes the object.

If the DT API call fails (e.g. token expired), the controller logs the error
and requeues. The object stays in the cluster until deletion succeeds.
This prevents orphaned resources in Dynatrace.

---

## High availability

The operator runs with `replicas: 2` and `--leader-elect=true`.
Only the leader reconciles — the standby is hot and takes over within seconds
if the leader pod is evicted (TKGs node drain, rollout, etc.).

Leader election uses a `Lease` object in the `sre-tools` namespace:
`dynatrace-operator.YOUR_ORG.io`.
