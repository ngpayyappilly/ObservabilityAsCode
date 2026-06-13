# Operator — Getting Started

This guide walks through installing the operator in a TKGs cluster and
creating your first Dynatrace SLO from a Kubernetes CR.

---

## Prerequisites

| Requirement | Version | Notes |
|---|---|---|
| Kubernetes | 1.27+ | TKGs tested |
| Argo CD | 2.8+ | For GitOps delivery of CRs |
| ExternalSecrets Operator | 0.9+ | Manages DT credential Secrets |
| Vault | Any | Backs the `vault-backend` ClusterSecretStore |
| Dynatrace SaaS | Any recent | Tokens need `slo.read`, `slo.write`, `settings.read`, `settings.write`, `DataExport` |
| Go | 1.22+ | Only if building the operator binary locally |
| Docker / containerd | Any | For building the container image |

---

## Step 1 — Provision DT API tokens

The operator reads credentials from two Secrets populated by ESO.
These Secrets are created by the Terraform `dynatrace-tokens` module:

```bash
cd terraform/dynatrace-tokens
terraform init
terraform apply \
  -var="dt_dev_url=https://<dev-tenant>.live.dynatrace.com" \
  -var="dt_dev_token=<bootstrap-token>" \
  -var="dt_staging_url=..." \
  -var="dt_staging_token=..." \
  -var="dt_perf_url=..." \
  -var="dt_perf_token=..." \
  -var="dt_prod_url=https://<prod-tenant>.live.dynatrace.com" \
  -var="dt_prod_token=<bootstrap-token>" \
  -var="vault_address=https://vault.internal"
```

This creates:
- Four `argocd-monaco-oac-<env>` API tokens in each DT tenant
- Writes them to Vault at `secret/dynatrace/tokens` and `secret/dynatrace/tenants`

ESO picks them up and creates `dynatrace-tenant-urls` and `dynatrace-api-tokens`
Secrets in `sre-tools` within the configured `refreshInterval` (1h).

Verify the Secrets exist before proceeding:
```bash
kubectl get secret dynatrace-tenant-urls dynatrace-api-tokens -n sre-tools
```

---

## Step 2 — Install the CRDs

```bash
# Install all four CRDs from the operator config directory
kubectl apply -k operator/config/crd/

# Verify — all four should appear
kubectl get crds | grep YOUR_ORG
# dynatracealerts.oac.YOUR_ORG.io
# dynatracedashboards.oac.YOUR_ORG.io
# dynatracenotifications.oac.YOUR_ORG.io
# dynatraceslos.oac.YOUR_ORG.io
```

---

## Step 3 — Build and push the operator image

```bash
cd operator

# Download Go dependencies
go mod download

# Run unit tests
go test ./...

# Build the binary
go build -o bin/manager .

# Build and push the container image
docker build -t YOUR_REGISTRY/dynatrace-operator:v0.1.0 .
docker push YOUR_REGISTRY/dynatrace-operator:v0.1.0
```

Update `operator/config/manager/deployment.yaml`:
```yaml
containers:
  - name: manager
    image: YOUR_REGISTRY/dynatrace-operator:v0.1.0   # ← update this
```

---

## Step 4 — Create RBAC and deploy the operator

```bash
# Create ServiceAccount, ClusterRole, ClusterRoleBinding
kubectl apply -f operator/config/rbac/serviceaccount.yaml
kubectl apply -f operator/config/rbac/role.yaml

# Deploy the operator (2 replicas, leader election enabled)
kubectl apply -f operator/config/manager/deployment.yaml

# Wait for both pods to be ready
kubectl rollout status deployment dynatrace-operator -n sre-tools
# Waiting for deployment "dynatrace-operator" rollout to finish: 0 of 2 updated replicas are available...
# deployment "dynatrace-operator" successfully rolled out

# Check which pod is the leader
kubectl logs -n sre-tools -l app.kubernetes.io/name=dynatrace-operator | grep leader
# "msg":"successfully acquired lease","leader":"dynatrace-operator-7f6b4d9c8-xk2pq"
```

---

## Step 5 — Create your first SLO

Apply the example from `operator/examples/payments-api.yaml` (edit the namespace
and alerting profile IDs first):

```bash
# Create the target namespace if it doesn't exist
kubectl create namespace payments-api

# Apply the SLO
kubectl apply -f - <<EOF
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
EOF
```

Watch the reconciliation happen:

```bash
kubectl get dtslo payments-api-availability -n payments-api -w
# NAME                        ENV    TYPE          TARGET   CURRENT   SYNCED   AGE
# payments-api-availability   prod   availability  99.9              False    2s
# payments-api-availability   prod   availability  99.9     99.94    True     8s
```

Verify in Dynatrace:
```bash
# Get the DT SLO ID from status
kubectl get dtslo payments-api-availability -n payments-api \
  -o jsonpath='{.status.dynatraceId}'
# SLO-XXXXXXXXXXXX

# Check the full status
kubectl describe dtslo payments-api-availability -n payments-api
```

---

## Step 6 — Set up GitOps delivery via Argo CD

Instead of `kubectl apply`, let Argo CD sync the CRs automatically when
you push to Git.

Apply the operator-specific ApplicationSet:

```bash
kubectl apply -f manifests/argocd/applicationset-oac-operator.yaml
```

This creates one Argo CD Application per (repo × environment) that syncs
`observability/<env>/` directories. CRs in those directories are applied
to the cluster; the operator reconciles them to Dynatrace.

Verify:
```bash
argocd app list | grep oac
# oac-payments-api-dev      Synced    Healthy
# oac-payments-api-staging  Synced    Healthy
# oac-payments-api-prod     OutOfSync Healthy   ← prod requires manual sync
```

---

## Step 7 — Scaffold a new service

The bootstrap pipeline renders Jinja2 templates from `scaffold/observability-operator/`
into the app repo's `observability/` folder. Each environment gets CRD manifests
instead of Monaco YAML.

Run the bootstrap pipeline in ADO:
1. Navigate to **Pipelines → bootstrap-pipeline**
2. Set `dryRun: false`, `repoFilter: payments-api`
3. Review and merge the generated PR

The scaffolded files will be:
```
observability/
├── dev/
│   ├── slos.yaml      ← DynatraceSLO (target: 99.0, latency: 500ms)
│   └── alerts.yaml    ← DynatraceAlert (errorRate, latencyP99)
├── staging/
│   ├── slos.yaml      ← tighter targets
│   └── alerts.yaml    ← + burnRateFast
├── perf/
│   └── slos.yaml      ← relaxed (load test thresholds)
└── prod/
    ├── slos.yaml      ← contractual targets (99.9%)
    ├── alerts.yaml    ← errorRate + latencyP99 + burnRateFast + burnRateSlow
    └── dashboard.yaml ← DynatraceDashboard (service-overview template)
```

---

## Verifying the full stack

```bash
# List all DynatraceSLOs across all namespaces
kubectl get dtslo -A
# NAMESPACE       NAME                          ENV    TYPE          TARGET   CURRENT   SYNCED
# payments-api    payments-api-availability     prod   availability  99.9     99.94     True
# payments-api    payments-api-latency-p99      prod   latency       99.9     99.87     True
# orders-api      orders-api-availability       prod   availability  99.5     99.72     True

# List all DynatraceAlerts
kubectl get dtalert -A

# List all dashboards with their DT URLs
kubectl get dtdash -A
# NAMESPACE       NAME                        ENV    TEMPLATE          SYNCED  URL
# payments-api    payments-api-overview       prod   service-overview  True    https://...

# Operator logs (structured JSON)
kubectl logs -n sre-tools -l app.kubernetes.io/name=dynatrace-operator --tail=50
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `SYNCED=False, reason=EntityResolution` | `backstageId` not found in DT — no SERVICE entity with that tag | Verify pod has label `backstage.io/kubernetes-id: <name>` and auto-tag rule has run |
| `SYNCED=False, reason=DynatraceAPI (401)` | DT token expired or wrong scopes | Re-run `terraform apply` in `terraform/dynatrace-tokens/`, ESO refreshes Secret within 1h |
| `SYNCED=False, reason=SLORefNotFound` | `sloRef` points to a DynatraceSLO that doesn't exist | Create the SLO first, or fix the name |
| Dashboard stays `Synced=False` | Referenced SLOs not yet synced | Wait — dashboard controller watches SLO events and retries automatically |
| Operator pod in `CrashLoopBackOff` | Can't read credential Secrets | Check ESO ExternalSecret status: `kubectl get externalsecret -n sre-tools` |
| SLO target reverted after manual DT change | Working as intended — drift detection | The 5-min requeue overwrites manual changes; update the CR in Git instead |
