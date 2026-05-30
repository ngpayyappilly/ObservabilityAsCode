# Observability as Code (OaC) — observability-template

This repository is the single source of truth for Dynatrace observability
across all services in the ADO project. It owns two distinct layers:

| Layer | What it manages | Where it lives |
|---|---|---|
| **Platform** | Management zones, auto-tags, alerting profiles, notification integrations, request attributes, span attributes | `terraform/platform-resources/` |
| **Application** | SLOs, metric event alerts, dashboards, synthetic monitors, log metrics | `scaffold/observability/` → rendered into each app repo |

The platform layer is applied once by the SRE team via Terraform. The application
layer is scaffolded into every service repo automatically by the bootstrap pipeline
and deployed continuously by Argo CD.

---

## Repository layout

```
observability-template/
├── scaffold/
│   ├── observability/                      # Jinja2 templates → rendered into each app repo
│   │   ├── manifest.yaml.j2               # Monaco v2 project manifest (dev/staging/perf/prod)
│   │   ├── environments/
│   │   │   ├── dev.yaml.j2                # SLO targets + env config for dev
│   │   │   ├── staging.yaml.j2
│   │   │   ├── perf.yaml.j2               # Relaxed thresholds for load testing
│   │   │   └── prod.yaml.j2               # Contractual SLA targets
│   │   ├── slos/
│   │   │   ├── availability.yaml.j2 + availability-slo.json.j2
│   │   │   └── latency.yaml.j2      + latency-slo.json.j2
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
│   ├── scripts/                            # Validation scripts copied into app repos
│   │   ├── ddu-estimator.py
│   │   └── slo-regression-check.py
│   └── backstage/                          # Reference templates for Backstage integration
│       ├── catalog-info.yaml.j2            # Backstage catalog descriptor
│       └── deployment-labels.yaml.j2       # Required k8s labels for DT auto-tagging
├── scripts/
│   ├── oac_utils.py                        # ADO REST client + Jinja2 render utilities
│   ├── bootstrap.py                        # Initial scaffold pipeline script
│   ├── propagate.py                        # Template update propagation script
│   └── drift_detector.py                  # Drift detection CronJob script
├── pipelines/
│   ├── bootstrap-pipeline.yaml            # Manual — scaffolds OaC into all repos
│   ├── propagation-pipeline.yaml          # Auto — pushes template updates
│   └── oac-pr-validation.yaml            # Per-app PR gate (YAML lint, Monaco dry-run, DDU, SLO regression, secret scan)
├── manifests/
│   ├── argocd/
│   │   ├── monaco-cmp/
│   │   │   ├── plugin.yaml                # CMP v2 plugin definition
│   │   │   ├── cmp-configmap.yaml         # Plugin config as ConfigMap
│   │   │   ├── repo-server-patch.yaml     # Kustomize patch — adds Monaco sidecar
│   │   │   ├── external-secrets.yaml      # ESO ExternalSecrets for DT credentials
│   │   │   ├── sync-hook.yaml             # PostSync Job — actual Monaco deploy
│   │   │   └── kustomization.yaml
│   │   └── applicationset-oac.yaml        # Matrix(ADO repos × dev/staging/perf/prod)
│   ├── kyverno/
│   │   └── enforce-oac-gitops.yaml        # Blocks direct kubectl apply on OaC resources
│   └── drift-detector/
│       ├── cronjob.yaml                   # Runs every 6h, compares manifest hashes
│       └── rbac.yaml
└── terraform/
    ├── ado-variable-group/
    │   └── main.tf                        # ADO variable group + PAT for pipelines
    ├── dynatrace-tokens/
    │   └── main.tf                        # DT API tokens per env → Vault
    └── platform-resources/
        ├── main.tf                        # Provider config
        ├── variables.tf                   # environments variable (dev/staging/perf/prod)
        ├── alerting_variables.tf          # notifications variable (Slack/MSTeams/PD/Splunk On-Call)
        ├── management_zones.tf            # One MZ per environment (env:dev … env:prod)
        ├── auto_tags.tf                   # Auto-tagging rules from Backstage k8s labels
        ├── alerting_profiles.tf           # One alerting profile per environment
        ├── alerting_notifications.tf      # Slack, MS Teams, PagerDuty, Splunk On-Call
        ├── request_attributes.tf          # Custom request attributes from HTTP headers + OTel
        ├── span_attributes.tf             # OTel span attribute allow-list, masking, capture rules
        ├── outputs.tf                     # MZ IDs, alerting profile IDs, notification IDs
        └── terraform.tfvars.example
```

---

## Platform resources (Terraform)

The platform layer is managed in `terraform/platform-resources/` and applied
**once** by the SRE team. It creates the shared Dynatrace infrastructure that
all service-level Monaco configs depend on.

### Management zones

One management zone per environment: `env:dev`, `env:staging`, `env:perf`, `env:prod`.

Each zone captures entities via three complementary rules:
1. **SELECTOR** rule matching the `environment:<label>` auto-tag — the primary rule,
   catches every entity type automatically once labels are set.
2. **Namespace name CONTAINS** rule — belt-and-suspenders for new deployments
   before auto-tags have propagated.
3. **HTTP_MONITOR tag** rule — scopes synthetic monitors to the correct zone.

### Auto-tagging — Backstage metadata → Dynatrace tags

Auto-tag rules read **Kubernetes pod labels** set by your teams (which mirror
Backstage catalog metadata) and translate them into Dynatrace contextless tags:

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

See `scaffold/backstage/deployment-labels.yaml.j2` for the exact label set to
add to your Deployments.

### Alerting profiles

One alerting profile per environment, scoped to its management zone.
Escalation policy tightens from dev → prod:

| Environment | Severities routed | Delay |
|---|---|---|
| dev | AVAILABILITY, ERROR, PERFORMANCE, CUSTOM | 0 min |
| staging | All above + MONITORING_UNAVAILABLE | 0 min |
| perf | All above + RESOURCE_CONTENTION | 0 min |
| prod | All severities | 0 min for AVAILABILITY/ERROR; 5 min for PERFORMANCE |

> The alerting profile IDs output by Terraform (`alerting_profile_ids`) are
> what the Monaco scaffold env files reference as `AlertingProfileId`.

### Notification integrations

Four channels — enable only what your org uses via `var.notifications`:

| Channel | Dev | Staging | Perf | Prod |
|---|---|---|---|---|
| Slack | `#alerts-dev` | `#alerts-staging` | `#alerts-perf` | `#alerts-prod` |
| MS Teams | Dev Alerts channel | Staging Alerts | Perf Alerts | Prod Alerts |
| PagerDuty | — | — | — | ✓ prod-p1 policy |
| Splunk On-Call | — | — | — | ✓ prod routing key |

All webhook URLs and API keys are passed via `var.notifications` (marked `sensitive`)
— never stored in plaintext. Populate via Vault or `TF_VAR_notifications`.

### Request attributes

Ten custom request attributes captured from inbound HTTP headers, with OTel
span attributes as fallback sources:

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

26 OTel span attribute keys are indexed via `dynatrace_attribute_allow_list`
so they are queryable in DQL, Notebooks, and Davis AI. Sensitive keys
(`tenant.id`) have `dynatrace_attribute_masking` applied.

Four **span capture rules** control sampling:
1. **CAPTURE** — spans with `error=true` (always kept)
2. **CAPTURE** — spans from services with a `team` attribute (your managed services)
3. **IGNORE** — `/health`, `/ready`, `/live`, `/metrics` probe spans
4. **IGNORE** — spans from `instrumentation_library_name` starting with `istio`

---

## Applying platform resources

```bash
cd terraform/platform-resources
cp terraform.tfvars.example terraform.tfvars
# fill in dt_url, dt_api_token, and the notifications block
terraform init
terraform plan
terraform apply

# Capture alerting profile IDs for the Monaco scaffold env files
terraform output -json alerting_profile_ids
# → {"dev": "abc-123", "staging": "def-456", "perf": "ghi-789", "prod": "jkl-000"}

# Update AlertingProfileId in scaffold/observability/environments/*.yaml.j2
# with the values above, then commit and push.
```

---

## Onboarding a new service

Three steps — no manual file copying required.

**Step 1: Add the required labels to your Deployment**

Copy the label block from `scaffold/backstage/deployment-labels.yaml.j2`
into your Deployment manifest and fill in your team, domain, and system values.
These labels are what wire your service into management zones, auto-tags,
alerting profiles, and request attribute capture automatically.

```yaml
labels:
  app.kubernetes.io/name: payments-api
  app.kubernetes.io/part-of: checkout-platform
  app.kubernetes.io/component: backend
  backstage.io/kubernetes-id: payments-api
  environment: prod          # dev | staging | perf | prod
  team: platform
  domain: checkout
  tier: backend
```

**Step 2: Confirm the repo is not opted out**

Check that the application repo does not contain a `.no-oac` file at the root.
If it does, the team has explicitly opted out. Remove it (with their consent)
before proceeding.

**Step 3: Run the bootstrap pipeline**

In ADO, navigate to **Pipelines → bootstrap-pipeline** and click **Run pipeline**.

Set:
- `dryRun`: `false`
- `repoFilter`: the repo name or a matching regex

The pipeline will:
1. Render all Jinja2 templates substituting the inferred service name.
2. Push the `observability/` folder to branch `feat/add-oac-scaffold`.
3. Open a PR in the application repo.

**Step 4: Review and merge the PR**

The `oac-pr-validation` pipeline runs automatically and gates:
- YAML syntax lint
- Monaco static validation
- Monaco dry-run against staging
- DDU estimate (blocks if > 5,000 DDU/month)
- SLO regression check (blocks if any target drops > 0.1%)
- Secret scan (blocks if DT tokens or tenant URLs are hardcoded)

Once all checks pass, approve and merge. Argo CD detects `observability/manifest.yaml`
within minutes and deploys to dev → staging → perf (automated), then waits for
manual approval for prod.

---

## Opting out

Create `.no-oac` at the repo root:

```bash
touch .no-oac
git add .no-oac
git commit -m "chore: opt out of OaC scaffold"
git push
```

Bootstrap and propagation scripts skip repos with this file. Existing configs
in Dynatrace are **not** deleted — opt-out only stops future scaffolding.

---

## Day-2 operations

### Updating SLO targets

SLO targets live in `observability/environments/prod.yaml` inside the **application repo**.

1. Branch → edit `observability/environments/prod.yaml`:
   ```yaml
   my-service:
     SLOTarget: "99.95"   # raised from 99.9
   ```
2. Open a PR. The `slo-regression-check.py` gate confirms the target did not
   decrease. Monaco dry-run validates it is deployable.
3. Merge → Argo CD applies the updated SLO to Dynatrace via the PostSync Job.

> **Never lower a prod SLO target** without a formal SLA change process.
> The CI gate blocks drops > 0.1 percentage points.

### Adding a new alert type to all services

1. Add the new `.yaml.j2` + `.json.j2` template pair under `scaffold/observability/alerts/`.
2. Push to `main` in this repo.
3. The propagation pipeline re-renders the new template for every already-scaffolded
   repo and opens PRs only where the rendered output changed.
4. Teams merge. Argo CD deploys.

### Updating notification channels

Notification webhooks and API keys live in `terraform/platform-resources/terraform.tfvars`
(not committed — managed via Vault).

1. Update the relevant value in Vault at `secret/dynatrace/notifications`.
2. Re-run `terraform apply` in `terraform/platform-resources/`.
3. No Monaco changes needed — notification resources are Terraform-only.

### Adding a new environment

1. Add the new environment to `var.environments` in `terraform.tfvars`.
2. Add a notification entry in `var.notifications` if the new env needs alerting.
3. Run `terraform apply` — creates the management zone, auto-tags, alerting profile,
   and notification integrations.
4. Add a corresponding `<env>.yaml.j2` under `scaffold/observability/environments/`.
5. Update `manifest.yaml.j2` to include the new environment block.
6. Push to `main` — propagation pipeline opens update PRs in all app repos.

### Rotating Dynatrace API tokens

```bash
cd terraform/dynatrace-tokens
terraform apply   # creates new tokens in DT and writes them to Vault
# ExternalSecrets Operator picks up the new values within refreshInterval (1h)
# No pod restarts required
```

---

## ADO service connection setup

The bootstrap and propagation pipelines authenticate via a PAT in the
`oac-bootstrap-secrets` variable group. Required PAT scopes:

| Scope | Reason |
|---|---|
| `Code (Read & Write)` | Push scaffold branches |
| `Pull Request (Read & Write)` | Open PRs |
| `Identity (Read)` | Resolve reviewer email → ADO identity |

Create the variable group via Terraform:

```bash
cd terraform/ado-variable-group
terraform init
terraform apply \
  -var="ado_org_service_url=https://dev.azure.com/YOUR_ORG" \
  -var="ado_project=YOUR_PROJECT" \
  -var="ado_pat=<admin-pat>" \
  -var="pipeline_pat=<pipeline-pat>" \
  -var="pr_reviewer_emails=alice@example.com,bob@example.com"
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Bootstrap skips all repos | `observability/manifest.yaml` already exists | Normal on re-run. Use `--repo-filter` to target a specific repo. |
| Monaco dry-run fails `HTTP 401` | `DT_STAGING_TOKEN` expired or missing scopes | Rotate via `terraform/dynatrace-tokens` and re-apply. ESO refreshes the k8s Secret within 1h. |
| Argo CD Application stuck `OutOfSync` | CMP sidecar `init` hook failed | `kubectl logs -n argocd deploy/argocd-repo-server -c monaco-cmp` — check for missing env vars or token scope errors. |
| Kyverno blocks ConfigMap: `oac/manifest-hash missing` | Direct `kubectl apply` attempted on an OaC sentinel | Only Argo CD sync may write `monaco-oac-state-*` ConfigMaps. Trigger sync from Argo CD UI or `argocd app sync <name>`. |
| Drift detector pages every 6h despite `AUTO_REMEDIATE=true` | PostSync Job failing — Argo CD sync succeeds but Monaco deploy fails | `kubectl logs -n sre-tools job/monaco-deploy-<app>-<env>` — look for DT API errors (quota, token scopes). |
| Management zone shows no entities | `environment` k8s label missing on pods | Check `deployment-labels.yaml.j2` and verify labels on running pods: `kubectl get pods -n <ns> --show-labels` |
| Request attributes empty in traces | HTTP headers not being set or forwarded by Istio | Verify Istio `EnvoyFilter` is not stripping `X-Backstage-*` headers. Check span attributes via OTel SDK as fallback. |
| Slack/PagerDuty not firing for prod alerts | AlertingProfileId in Monaco env file still has placeholder value | Run `terraform output alerting_profile_ids` and update `observability/environments/prod.yaml` in the app repo, then re-sync. |
| Span attributes not visible in traces | OTel key not in allow-list | Add the key to `local.span_allow_list` in `span_attributes.tf` and re-apply Terraform. |

---

## Architecture overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│  PLATFORM LAYER  (terraform/platform-resources — applied once by SRE)   │
│                                                                         │
│  Management zones  ──── Auto-tags (from Backstage k8s labels)           │
│       env:dev               service, team, domain, system, ...          │
│       env:staging                                                       │
│       env:perf       ──── Alerting profiles (one per env)               │
│       env:prod              dev→Slack, prod→Slack+MSTeams+PD+SplunkOC   │
│                                                                         │
│  Request attributes ──── Span attribute allow-list + masking            │
│  (from HTTP headers)        (OTel keys indexed for DQL/Davis AI)        │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │ IDs referenced as Monaco parameters
┌────────────────────────────────▼────────────────────────────────────────┐
│  APPLICATION LAYER  (Monaco configs in each app repo's observability/)  │
│                                                                         │
│  SLOs (availability + latency p99)                                      │
│  Alerts (error rate, latency p99, error budget burn — fast + slow)      │
│  Dashboards (SLO tiles + request rate + error rate)                     │
│  Synthetic monitors (private ActiveGate — Istio mTLS compatible)        │
│  Log metrics (DQL — ERROR level, split by error.type)                   │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │
┌────────────────────────────────▼────────────────────────────────────────┐
│  GITOPS DELIVERY  (Argo CD + Monaco CMP sidecar)                        │
│                                                                         │
│  ADO repo push / PR merge                                               │
│       ↓                                                                 │
│  ApplicationSet  (matrix: ADO repos × dev/staging/perf/prod)            │
│       ↓  detect observability/manifest.yaml                             │
│  Monaco CMP sidecar  init → validate token scopes                       │
│                       generate → dry-run + emit ConfigMap sentinel      │
│       ↓  PostSync                                                       │
│  Monaco Deploy Job → applies configs to Dynatrace tenant                │
│                                                                         │
│  Every 6h: Drift Detector CronJob                                       │
│       → compare oac/manifest-hash on live ConfigMap vs Argo CD state    │
│       → hard-refresh on drift  → Slack notification                     │
└─────────────────────────────────────────────────────────────────────────┘
```

### Backstage → Dynatrace data flow

```
catalog-info.yaml  (Backstage)
  ↓ teams mirror as Kubernetes labels on Deployments
Pod labels  (k8s)
  ↓ OneAgent reads pod labels automatically
PROCESS_GROUP_PREDEFINED_METADATA  (Dynatrace)
  ↓ dynatrace_autotag_v2 rules translate labels
Contextless tags  (team:platform, environment:prod, domain:checkout …)
  ↓ management zone SELECTOR rule matches `environment:prod`
Management zone  env:prod  scopes SLOs, alerts, dashboards
  ↓ alerting profile routes to PagerDuty + Slack #alerts-prod
  ↓ request attributes enrich every service trace
  ↓ span attribute allow-list makes OTel keys queryable in DQL
```
