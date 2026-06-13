# Observability as Code (OaC) — observability-template

This repository is the single source of truth for Dynatrace observability
across all services in the ADO project. It owns two distinct layers and
supports three delivery mechanisms — choose the one that fits your team.

| Layer | What it manages | Where it lives |
|---|---|---|
| **Platform** | Management zones, auto-tags, alerting profiles, notification integrations (Slack / MS Teams / PagerDuty / Splunk On-Call), request attributes, span attributes | `terraform/platform-resources/` |
| **Application** | SLOs (service + per-endpoint), metric event alerts, dashboards, synthetic monitors, log metrics | `scaffold/observability/` or `scaffold/observability-operator/` → rendered into each app repo |

---

## Delivery mechanism — pick one

| | Monaco + Argo CD CMP | Custom Operator | Crossplane |
|---|---|---|---|
| **How** | Jinja2 templates → Monaco YAML → CMP sidecar | CRD manifests → Go controller | Claims → Composition → provider |
| **Drift detection** | CronJob every 6h | Built-in every 5 min | provider-terraform: 10 min |
| **App team writes** | Monaco YAML in `observability/` | `DynatraceSLO` / `DynatraceAlert` CRs | `ServiceObservabilityClaim` |
| **Backstage entity resolution** | Manual entity IDs | Auto via `backstageId` tag | Not supported natively |
| **Build cost** | Zero | Already built (Go operator in `operator/`) | `provider-terraform` works today; native provider = 2–4 months |
| **Best for** | Existing Monaco investment | Clean API, org-specific logic | Orgs already running Crossplane |

> Detailed docs for each approach: [`docs/`](docs/README.md)

---

## Repository layout

```
observability-template/
│
├── scaffold/
│   ├── observability/                       # Monaco approach — Jinja2 → Monaco YAML
│   │   ├── manifest.yaml.j2                # Monaco v2 project manifest
│   │   ├── environments/
│   │   │   ├── dev.yaml.j2                 # SLO targets + env config
│   │   │   ├── staging.yaml.j2
│   │   │   ├── perf.yaml.j2                # Relaxed thresholds for load testing
│   │   │   └── prod.yaml.j2                # Contractual SLA targets
│   │   ├── slos/
│   │   │   ├── availability.yaml.j2 + availability-slo.json.j2
│   │   │   ├── latency.yaml.j2      + latency-slo.json.j2
│   │   │   └── endpoints/                  # Per-endpoint SLOs
│   │   │       ├── critical-endpoints.yaml.j2   # Teams list their endpoints here
│   │   │       ├── endpoint-availability-slo.json.j2
│   │   │       ├── endpoint-latency-slo.json.j2
│   │   │       └── generated/              # Output of generate-endpoint-slos.py
│   │   ├── alerts/
│   │   │   ├── error-rate.yaml.j2   + error-rate.json.j2
│   │   │   ├── latency-p99.yaml.j2  + latency-p99.json.j2
│   │   │   └── error-budget-burn.yaml.j2  + error-budget-burn.json.j2
│   │   ├── dashboards/
│   │   │   └── service-overview.yaml.j2   + service-dashboard.json.j2
│   │   ├── synthetic/
│   │   │   └── health-check.yaml.j2       + http-monitor.json.j2
│   │   └── log-metrics/
│   │       └── error-log-metric.yaml.j2
│   │
│   ├── observability-operator/              # Operator approach — Jinja2 → CRD manifests
│   │   ├── dev/
│   │   │   ├── slos.yaml.j2               # DynatraceSLO CRDs (target: 99.0)
│   │   │   └── alerts.yaml.j2             # DynatraceAlert CRDs
│   │   ├── staging/
│   │   │   ├── slos.yaml.j2
│   │   │   └── alerts.yaml.j2
│   │   ├── perf/
│   │   │   └── slos.yaml.j2               # Relaxed thresholds for load tests
│   │   └── prod/
│   │       ├── slos.yaml.j2               # DynatraceSLO (target: 99.9%)
│   │       ├── alerts.yaml.j2             # errorRate + latencyP99 + burnRateFast + burnRateSlow
│   │       └── dashboard.yaml.j2          # DynatraceDashboard (service-overview template)
│   │
│   ├── scripts/                            # Validation + generation scripts → app repos
│   │   ├── ddu-estimator.py               # DDU consumption estimate (Monaco approach)
│   │   ├── slo-regression-check.py        # Block PRs that lower SLO targets
│   │   └── generate-endpoint-slos.py      # Generate Monaco configs from endpoints YAML
│   │
│   └── backstage/                          # Backstage integration reference templates
│       ├── catalog-info.yaml.j2            # Backstage catalog descriptor
│       └── deployment-labels.yaml.j2       # Required k8s labels for DT auto-tagging
│
├── scripts/
│   ├── oac_utils.py                        # ADO REST client + Jinja2 render utilities
│   ├── bootstrap.py                        # Initial scaffold pipeline script
│   ├── propagate.py                        # Template update propagation script
│   └── drift_detector.py                  # Drift detection CronJob script (Monaco approach)
│
├── pipelines/
│   ├── bootstrap-pipeline.yaml            # Manual — scaffolds OaC into all repos
│   ├── propagation-pipeline.yaml          # Auto — pushes template updates on main push
│   └── oac-pr-validation.yaml            # Per-app PR gate (YAML lint, Monaco dry-run,
│                                          #   DDU estimate, endpoint SLO sync check,
│                                          #   SLO regression, secret scan)
│
├── manifests/
│   ├── argocd/
│   │   ├── monaco-cmp/                    # Monaco CMP sidecar (Monaco approach only)
│   │   │   ├── plugin.yaml                # CMP v2 plugin — discover/init/generate hooks
│   │   │   ├── cmp-configmap.yaml
│   │   │   ├── repo-server-patch.yaml     # Adds Monaco sidecar to argocd-repo-server
│   │   │   ├── external-secrets.yaml      # ESO ExternalSecrets for DT credentials
│   │   │   ├── sync-hook.yaml             # PostSync Job — actual Monaco deploy
│   │   │   └── kustomization.yaml
│   │   ├── applicationset-oac.yaml        # Monaco: matrix(ADO repos × dev/staging/perf/prod)
│   │   └── applicationset-oac-operator.yaml  # Operator: syncs CRD manifests per env dir
│   ├── kyverno/
│   │   └── enforce-oac-gitops.yaml        # Blocks direct kubectl apply on OaC resources
│   └── drift-detector/                    # Monaco approach drift detection
│       ├── cronjob.yaml                   # Runs every 6h, compares manifest hashes
│       └── rbac.yaml
│
├── operator/                               # Custom Kubernetes operator (Go)
│   ├── api/v1alpha1/
│   │   ├── types.go                       # DynatraceSLO, DynatraceAlert, DynatraceDashboard,
│   │   │                                  #   DynatraceNotification type definitions
│   │   ├── groupversion_info.go           # Scheme registration
│   │   └── zz_generated.deepcopy.go       # Generated DeepCopy methods
│   ├── controllers/
│   │   ├── dynatraceslo_controller.go     # SLO reconciler (entity resolution, finalizer, drift)
│   │   ├── dynatracealert_controller.go   # Alert reconciler (sloRef cross-reference)
│   │   ├── dynatracedashboard_controller.go  # Dashboard reconciler (template engine, SLO watch)
│   │   └── dynatracenotification_controller.go
│   ├── internal/dynatrace/
│   │   ├── client.go                      # Typed DT REST API client
│   │   └── dashboard_templates.go         # service-overview, slo-report, endpoint-detail
│   ├── config/
│   │   ├── crd/                           # CRD YAML manifests — install with kubectl apply -k
│   │   │   ├── dynatraceslos.yaml
│   │   │   ├── dynatracealerts.yaml
│   │   │   ├── dynatracedashboards.yaml
│   │   │   ├── dynatracenotifications.yaml
│   │   │   └── kustomization.yaml
│   │   ├── rbac/                          # ServiceAccount, ClusterRole, ClusterRoleBinding
│   │   └── manager/deployment.yaml        # 2-replica HA deployment
│   ├── examples/
│   │   ├── payments-api.yaml              # Full example: SLOs + alerts + dashboard + notification
│   │   └── dashboard-templates.yaml       # All three dashboard templates side-by-side
│   ├── main.go                            # Manager setup, controller registration
│   └── go.mod
│
├── crossplane/                             # Crossplane integration
│   ├── provider-terraform/
│   │   ├── provider.yaml                  # Installs provider-terraform from Upbound registry
│   │   └── workspace-platform.yaml        # Workspace CR wrapping terraform/platform-resources/
│   ├── provider/
│   │   ├── PROVIDER_BUILD.md             # Guide: build provider-dynatrace with upjet
│   │   └── provider-configs.yaml         # ProviderConfig per environment (dev/staging/perf/prod)
│   ├── xrds/
│   │   └── service-observability-xrd.yaml # ServiceObservability XRD (team-facing API)
│   ├── compositions/
│   │   └── service-observability-composition.yaml  # Expands claim → SLO + alerts + synthetic
│   └── claims/
│       └── payments-api-prod.yaml         # Example ServiceObservabilityClaim
│
├── terraform/
│   ├── ado-variable-group/main.tf          # ADO variable group + pipeline PAT
│   ├── dynatrace-tokens/main.tf            # DT API tokens per env → Vault
│   └── platform-resources/
│       ├── main.tf                         # Provider config
│       ├── variables.tf                    # environments variable (dev/staging/perf/prod)
│       ├── alerting_variables.tf           # notifications variable
│       ├── management_zones.tf             # One MZ per environment (env:dev … env:prod)
│       ├── auto_tags.tf                    # Auto-tagging from Backstage k8s labels
│       ├── alerting_profiles.tf            # One alerting profile per environment
│       ├── alerting_notifications.tf       # Slack, MS Teams, PagerDuty, Splunk On-Call
│       ├── request_attributes.tf           # Custom request attributes (HTTP headers + OTel)
│       ├── span_attributes.tf              # OTel allow-list, masking, capture rules
│       ├── outputs.tf                      # MZ IDs, alerting profile IDs, notification IDs
│       └── terraform.tfvars.example
│
└── docs/                                   # Detailed implementation guides
    ├── README.md                           # Index + which approach to choose
    ├── operator/
    │   ├── README.md                       # Architecture, reconciliation loop, HA
    │   ├── getting-started.md              # Install → first SLO → GitOps
    │   ├── crds-reference.md              # Full API reference for all 4 CRDs
    │   ├── dashboard-templates.md          # Template layouts, tile reference, adding templates
    │   └── development.md                  # Build, test, extend the operator
    └── crossplane/
        ├── README.md                       # Architecture, Path A vs B decision guide
        ├── getting-started.md              # Install Crossplane → provider → first Claim
        ├── provider-terraform.md           # Workspace anatomy, state, outputs, limitations
        ├── native-provider.md              # upjet build guide, timeline, maintenance
        └── compositions.md                 # XRD spec, Composition patches, extending
```

---

## Platform resources (Terraform)

Applied once by the SRE team. Creates the shared Dynatrace infrastructure
that all application-level configs (Monaco or operator) depend on.

### Management zones

One zone per environment: `env:dev`, `env:staging`, `env:perf`, `env:prod`.
Primary rule matches the `environment:<label>` auto-tag; namespace CONTAINS rules
provide coverage before auto-tags propagate.

### Auto-tagging — Backstage metadata → Dynatrace tags

| k8s label | Dynatrace tag | Backstage source |
|---|---|---|
| `app.kubernetes.io/name` | `service:<name>` | `metadata.name` |
| `app.kubernetes.io/part-of` | `system:<name>` | `spec.system` |
| `app.kubernetes.io/component` | `component:<type>` | `spec.type` |
| `team` | `team:<name>` | `spec.owner` |
| `environment` | `environment:<env>` | deployment convention |
| `backstage.io/kubernetes-id` | `backstage-id:<id>` | `metadata.name` |
| `domain` | `domain:<name>` | `metadata.labels.domain` |
| `tier` | `tier:<name>` | `metadata.labels.tier` |
| pod namespace (built-in) | `k8s.namespace.name:<ns>` | namespace convention |

### Alerting profiles

| Environment | Severities routed | Delay |
|---|---|---|
| dev | AVAILABILITY, ERROR, PERFORMANCE, CUSTOM | 0 min |
| staging | All above + MONITORING_UNAVAILABLE | 0 min |
| perf | All above + RESOURCE_CONTENTION | 0 min |
| prod | All severities | 0 min (AVAILABILITY/ERROR); 5 min (PERFORMANCE) |

### Notification integrations

| Channel | Dev | Staging | Perf | Prod |
|---|---|---|---|---|
| Slack | `#alerts-dev` | `#alerts-staging` | `#alerts-perf` | `#alerts-prod` |
| MS Teams | Dev Alerts | Staging Alerts | Perf Alerts | Prod Alerts |
| PagerDuty | — | — | — | ✓ prod-p1 policy |
| Splunk On-Call | — | — | — | ✓ prod routing key |

### Request attributes (from HTTP headers + OTel span fallback)

| Attribute | Header → Span key | Purpose |
|---|---|---|
| Team | `X-Backstage-Team` → `team` | Route alerts, filter dashboards |
| Service Name | `X-Backstage-Service` → `service.name` | Service-level filtering |
| Environment | `X-Backstage-Env` → `deployment.environment` | Cross-MZ querying |
| Domain | `X-Backstage-Domain` → `domain` | Business domain grouping |
| System | `X-Backstage-System` → `system` | Backstage System grouping |
| Correlation ID | `X-Correlation-ID` → `correlation.id` | Distributed trace stitching |
| Tenant ID | `X-Tenant-ID` → `tenant.id` | Multi-tenant SLO splitting |
| Feature Flag | `X-Feature-Flag` → `feature.flag` | Incident ↔ flag correlation |
| HTTP Status Class | Derived from response code | Split error rate by 2xx/4xx/5xx |

### Span attributes (OTel)

26 OTel keys indexed via `dynatrace_attribute_allow_list` — queryable in DQL, Notebooks, and Davis AI.
`tenant.id` masked via `dynatrace_attribute_masking`.
Four span capture rules: always keep error spans + team-labelled spans; ignore health probes + Istio internal spans.

### Apply platform resources

```bash
cd terraform/platform-resources
cp terraform.tfvars.example terraform.tfvars   # fill in dt_url, dt_api_token, notifications block
terraform init && terraform plan && terraform apply

# Capture IDs needed by the app-layer configs
terraform output -json alerting_profile_ids
# → {"dev":"abc-123","staging":"def-456","perf":"ghi-789","prod":"jkl-000"}
```

---

## Application layer — Monaco approach

### Onboarding a new service

1. **Add required k8s labels** to your Deployment (from `scaffold/backstage/deployment-labels.yaml.j2`):
   ```yaml
   labels:
     app.kubernetes.io/name: payments-api
     app.kubernetes.io/part-of: checkout-platform
     backstage.io/kubernetes-id: payments-api
     environment: prod
     team: platform
     domain: checkout
   ```

2. **Run the bootstrap pipeline** in ADO (`dryRun: false`, `repoFilter: <repo-name>`).
   This renders Monaco Jinja2 templates into an `observability/` PR in the app repo.

3. **Review and merge the PR.** The `oac-pr-validation` pipeline gates:
   - YAML syntax lint
   - Monaco static validation + staging dry-run
   - DDU estimate (blocks > 5,000 DDU/month)
   - Endpoint SLO generated files in sync
   - SLO regression check (blocks target drop > 0.1%)
   - Secret scan (blocks hardcoded DT tokens or tenant URLs)

4. **Argo CD** detects `observability/manifest.yaml` and deploys to dev → staging → perf (automated) → prod (manual approval).

### Per-endpoint SLOs

Teams list critical endpoints in `observability/slos/endpoints/critical-endpoints.yaml`:

```yaml
service: payments-api
endpoints:
  - id: post-payments
    method: POST
    path: /api/v1/payments
    latency_ms: 400
    slo_target: 99.9
  - id: get-payment-status
    method: GET
    path: /api/v1/payments/{id}/status
    latency_ms: 200
    slo_target: 99.95
```

Run the generator and commit the output:
```bash
python scripts/generate-endpoint-slos.py \
  --endpoints observability/slos/endpoints/critical-endpoints.yaml \
  --env-file observability/environments/prod.yaml
git add observability/slos/endpoints/generated/
git commit -m "chore(oac): add endpoint SLOs for payments-api"
```

The PR validation pipeline fails if `critical-endpoints.yaml` is edited but
`generated/` is not regenerated.

### Updating SLO targets

Edit `observability/environments/prod.yaml` in the **app repo**:
```yaml
my-service:
  SLOTarget: "99.95"
```
Open a PR. The SLO regression gate blocks any decrease > 0.1%. Merge → Argo CD PostSync Job applies the change to Dynatrace.

### Updating notification channels

1. Update the webhook / API key in Vault at `secret/dynatrace/notifications`.
2. `terraform apply` in `terraform/platform-resources/`.
3. No Monaco changes needed.

---

## Application layer — Operator approach

The operator delivers a clean, domain-specific Kubernetes API. No Monaco YAML,
no CMP sidecar, no PostSync Job, no drift CronJob — the controller handles all of it.

### Install the operator

```bash
# Install all four CRDs
kubectl apply -k operator/config/crd/

# Deploy the operator (2 replicas, leader election)
kubectl apply -f operator/config/rbac/serviceaccount.yaml
kubectl apply -f operator/config/rbac/role.yaml
kubectl apply -f operator/config/manager/deployment.yaml

# Verify
kubectl rollout status deployment dynatrace-operator -n sre-tools
```

### The four CRDs

| CRD | Short name | What it creates in Dynatrace |
|---|---|---|
| `DynatraceSLO` | `dtslo` | SLO v2 — availability or latency p99 |
| `DynatraceAlert` | `dtalert` | Metric event anomaly detection rule |
| `DynatraceDashboard` | `dtdash` | Dashboard from a named template |
| `DynatraceNotification` | `dtnotif` | Notification integration |

### Onboarding a service with the operator

Run the bootstrap pipeline with the `observability-operator/` scaffold. The PR
in the app repo contains per-env CRD manifests instead of Monaco YAML:

```
observability/
├── dev/   slos.yaml + alerts.yaml
├── staging/ slos.yaml + alerts.yaml
├── perf/  slos.yaml
└── prod/  slos.yaml + alerts.yaml + dashboard.yaml
```

Argo CD syncs these as standard Kubernetes manifests (no Monaco plugin needed).
The operator controller reconciles each CRD to Dynatrace every 5 minutes.

### What teams write

```yaml
apiVersion: oac.YOUR_ORG.io/v1alpha1
kind: DynatraceSLO
metadata:
  name: payments-api-availability
  namespace: payments-api
spec:
  environment: prod
  serviceSelector:
    backstageId: payments-api    # controller resolves to DT SERVICE entity automatically
  type: availability
  target: 99.9
  window: "-1w"
```

```bash
kubectl get dtslo -A
# NAMESPACE      NAME                        ENV    TYPE          TARGET   CURRENT   SYNCED
# payments-api   payments-api-availability   prod   availability  99.9     99.94     True
```

### Dashboard templates

Three built-in templates — select via `spec.template`:

| Template | What it shows |
|---|---|
| `service-overview` | SLO tiles + request/error rates + latency percentiles + burn rate |
| `slo-report` | SLO compliance history + error budget remaining + 1h/6h burn rates |
| `endpoint-detail` | Per-endpoint throughput, error rate, p50/p99 latency, slowest table |

The controller resolves `spec.sloRefs` to real Dynatrace SLO IDs from
`status.dynatraceId` of referenced `DynatraceSLO` objects before building
the dashboard JSON.

> See [`docs/operator/`](docs/operator/README.md) for full architecture, CRD reference, and development guide.

---

## Application layer — Crossplane approach

### Path A — provider-terraform (works today)

Wraps `terraform/platform-resources/` as a `Workspace` CR for continuous reconciliation:

```bash
kubectl apply -f crossplane/provider-terraform/provider.yaml
kubectl apply -f crossplane/provider-terraform/workspace-platform.yaml
```

Outputs (MZ IDs, alerting profile IDs) written to `dynatrace-platform-outputs` Secret.

### Path B — Native provider-dynatrace (build with upjet)

Auto-generates a Crossplane provider from the `dynatrace-oss/dynatrace` Terraform schema.
Once built, installs CRDs for every DT resource type and a dedicated controller per CRD.

### Teams write a single Claim per environment

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
    latencyTargetMs: 300
  alerts:
    errorRateThreshold: 0.5
    burnRateFast: 14
    burnRateSlow: 6
  synthetic:
    url: "https://payments-api.prod.internal/health"
    frequencyMinutes: 1
```

The Composition expands this into: availability SLO + latency SLO + error rate alert + fast burn alert + slow burn alert + synthetic monitor.

> See [`docs/crossplane/`](docs/crossplane/README.md) for full architecture, provider build guide, and XRD/Composition reference.

---

## Opting out (Monaco approach)

```bash
touch .no-oac && git add .no-oac && git commit -m "chore: opt out of OaC scaffold" && git push
```

Bootstrap and propagation scripts skip repos with this file. Existing DT configs are not deleted.

---

## ADO service connection setup

Variable group `oac-bootstrap-secrets` — required PAT scopes:

| Scope | Reason |
|---|---|
| `Code (Read & Write)` | Push scaffold branches |
| `Pull Request (Read & Write)` | Open PRs |
| `Identity (Read)` | Resolve reviewer email → ADO identity |

```bash
cd terraform/ado-variable-group
terraform init && terraform apply \
  -var="ado_org_service_url=https://dev.azure.com/YOUR_ORG" \
  -var="ado_project=YOUR_PROJECT" \
  -var="ado_pat=<admin-pat>" \
  -var="pipeline_pat=<pipeline-pat>" \
  -var="pr_reviewer_emails=alice@example.com,bob@example.com"
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Bootstrap skips all repos | `observability/manifest.yaml` already exists | Normal on re-run. Use `--repo-filter`. |
| Monaco dry-run fails `HTTP 401` | DT token expired or wrong scopes | `terraform apply` in `dynatrace-tokens/`. ESO refreshes within 1h. |
| Argo CD `OutOfSync` — Monaco | CMP sidecar `init` hook failed | `kubectl logs -n argocd deploy/argocd-repo-server -c monaco-cmp` |
| Kyverno blocks ConfigMap | Direct `kubectl apply` on `monaco-oac-state-*` | Only Argo CD sync creates these. Run `argocd app sync <name>`. |
| Drift detector pages every 6h | PostSync Job failing | `kubectl logs -n sre-tools job/monaco-deploy-<app>-<env>` |
| `dtslo` `SYNCED=False, reason=EntityResolution` | `backstageId` tag not found in DT | Verify pod has label `backstage.io/kubernetes-id` and auto-tag has run |
| `dtslo` `SYNCED=False, reason=DynatraceAPI (401)` | DT token expired | `terraform apply` in `dynatrace-tokens/` |
| Dashboard stays `SYNCED=False` | Referenced SLOs not yet synced | Controller retries automatically when SLO status updates |
| Operator pod `CrashLoopBackOff` | Can't read credential Secrets | `kubectl get externalsecret -n sre-tools` |
| Crossplane Workspace `READY=False` | Terraform init failed | `kubectl describe workspace dynatrace-platform-resources -n sre-tools` → Events |
| Endpoint SLO CI gate fails | `generated/` not regenerated after editing `critical-endpoints.yaml` | Run `python scripts/generate-endpoint-slos.py` and commit `generated/` |
| Management zone empty | `environment` k8s label missing on pods | `kubectl get pods -n <ns> --show-labels` |
| Request attributes empty in traces | Istio stripping `X-Backstage-*` headers | Check `EnvoyFilter`; OTel span attributes are the fallback |
| Span attributes not visible in DQL | OTel key not in allow-list | Add to `local.span_allow_list` in `span_attributes.tf` and re-apply |

---

## Architecture overview

### Three delivery paths

```
PLATFORM LAYER — terraform/platform-resources/ (applied once by SRE)
┌───────────────────────────────────────────────────────────────────────┐
│  Management zones (env:dev/staging/perf/prod)                         │
│  Auto-tags (Backstage k8s labels → DT contextless tags)               │
│  Alerting profiles (one per env, scoped to MZ)                        │
│  Notifications (Slack / MS Teams / PagerDuty / Splunk On-Call)        │
│  Request attributes (HTTP headers + OTel span fallback)               │
│  Span attribute allow-list + masking + capture rules                  │
└───────────────────────────┬───────────────────────────────────────────┘
                            │ IDs referenced by app layer
              ┌──────────── ┼─────────────┐
              │             │             │
    ┌─────────▼───────┐ ┌───▼────────┐ ┌──▼───────────────────┐
    │ MONACO APPROACH │ │ OPERATOR   │ │ CROSSPLANE APPROACH  │
    │                 │ │            │ │                      │
    │ observability/  │ │ obs-oper/  │ │ ServiceObservability │
    │ (Jinja2→Monaco) │ │ (CRD YML)  │ │ Claim                │
    │       │         │ │    │       │ │       │              │
    │ Argo CD CMP     │ │ Argo CD    │ │ Argo CD (std sync)   │
    │ sidecar         │ │ std sync   │ │       │              │
    │       │         │ │    │       │ │ Composition engine   │
    │ Monaco deploy   │ │ operator   │ │       │              │
    │ PostSync Job    │ │ controller │ │ provider controller  │
    │ Drift CronJob   │ │ every 5m   │ │ every 10m            │
    └────────┬────────┘ └────┬───────┘ └────────┬─────────────┘
             └───────────────┴──────────────────┘
                             │
                     Dynatrace REST API
                   SLOs / Alerts / Dashboards
                   Synthetic / Log metrics
```

### Backstage → Dynatrace data flow

```
catalog-info.yaml (Backstage)
  ↓ teams mirror as Kubernetes pod labels
app.kubernetes.io/name, team, environment, domain, backstage.io/kubernetes-id
  ↓ Dynatrace OneAgent reads pod labels automatically
  ↓ dynatrace_autotag_v2 translates labels to DT contextless tags
team:platform, environment:prod, service:payments-api, domain:checkout
  ↓ management zone SELECTOR rule matches environment:prod
env:prod MZ scopes SLOs, alerts, dashboards to prod traffic only
  ↓ alerting profile routes prod alerts → PagerDuty + #alerts-prod Slack
  ↓ request attributes enrich every trace with team/domain/tenant context
  ↓ span attribute allow-list makes OTel keys queryable in DQL / Davis AI
  ↓ operator backstageId resolution: GET /api/v2/entities?tag(backstage-id:payments-api)
       → resolves to specific SERVICE entity ID for per-service SLO scoping
```

---

## Documentation

Full implementation guides are in [`docs/`](docs/README.md):

- **[Operator overview](docs/operator/README.md)** — architecture, reconciliation loop, finalizers, HA
- **[Operator getting started](docs/operator/getting-started.md)** — install CRDs, deploy, first SLO
- **[Operator CRD reference](docs/operator/crds-reference.md)** — full spec/status for all 4 CRDs
- **[Dashboard templates](docs/operator/dashboard-templates.md)** — tile layouts, adding new templates
- **[Operator development](docs/operator/development.md)** — build, test, extend
- **[Crossplane overview](docs/crossplane/README.md)** — Path A vs B, component map
- **[Crossplane getting started](docs/crossplane/getting-started.md)** — install, first Claim
- **[provider-terraform guide](docs/crossplane/provider-terraform.md)** — Workspace, state, outputs
- **[Native provider guide](docs/crossplane/native-provider.md)** — upjet build, timeline
- **[XRD & Composition reference](docs/crossplane/compositions.md)** — full spec, patch mechanics
