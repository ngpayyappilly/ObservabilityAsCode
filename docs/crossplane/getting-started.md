# Crossplane — Getting Started

This guide installs Crossplane, configures `provider-terraform`, and creates
your first managed Dynatrace resource from a Kubernetes claim.

---

## Prerequisites

| Requirement | Version | Notes |
|---|---|---|
| Kubernetes | 1.27+ | TKGs tested |
| Helm | 3.x | For installing Crossplane |
| Argo CD | 2.8+ | For GitOps delivery of claims |
| ExternalSecrets Operator | 0.9+ | Manages DT credentials |
| Vault | Any | Backs the `vault-backend` ClusterSecretStore |
| Terraform | 1.6+ | Executed by `provider-terraform` |

---

## Step 1 — Install Crossplane

```bash
# Add the Upbound Helm repository
helm repo add crossplane-stable https://charts.crossplane.io/stable
helm repo update

# Install Crossplane in its own namespace
helm install crossplane \
  crossplane-stable/crossplane \
  --namespace crossplane-system \
  --create-namespace \
  --version 1.15.0 \
  --set args='{"--enable-composition-revisions"}'

# Wait for all pods to be ready
kubectl rollout status deployment crossplane -n crossplane-system
kubectl rollout status deployment crossplane-rbac-manager -n crossplane-system
```

---

## Step 2 — Install provider-terraform

`provider-terraform` wraps Terraform as a Crossplane provider. It runs
`terraform init`, `plan`, and `apply` inside the cluster on a configurable
schedule.

```bash
# Apply the provider manifest
kubectl apply -f crossplane/provider-terraform/provider.yaml

# Wait for the provider to become healthy
kubectl wait provider.pkg provider-terraform \
  --for=condition=Healthy \
  --timeout=120s
```

Verify:
```bash
kubectl get providers
# NAME                 INSTALLED   HEALTHY   PACKAGE                                          AGE
# provider-terraform   True        True      xpkg.upbound.io/upbound/provider-terraform:v0.14.0   2m
```

---

## Step 3 — Configure credentials for provider-terraform

The Terraform provider-terraform controller needs:
1. DT tenant URLs and API tokens (from ESO-managed Secrets)
2. Vault token (to write TF outputs back to Vault)
3. ADO credentials (if TF modules pull from private repos)

Create the Vault token Secret:
```bash
# Create from Vault directly — do NOT commit this to Git
kubectl create secret generic vault-crossplane-token \
  -n sre-tools \
  --from-literal=token="<vault-token-with-write-to-secret/dynatrace>"
```

The Workspace CR (`crossplane/provider-terraform/workspace-platform.yaml`) reads
the DT URL and token Secrets from `sre-tools` — these are already created by
ESO from the Terraform `dynatrace-tokens` module.

---

## Step 4 — Apply the platform Workspace

The `Workspace` CR wraps `terraform/platform-resources/` and reconciles it
continuously (every 10 minutes by default).

```bash
kubectl apply -f crossplane/provider-terraform/workspace-platform.yaml

# Watch the reconciliation
kubectl get workspace dynatrace-platform-resources -n sre-tools -w
# NAME                            READY   SYNCED   AGE
# dynatrace-platform-resources    False   False    5s
# dynatrace-platform-resources    True    True     45s
```

Check outputs (management zone IDs, alerting profile IDs):
```bash
kubectl get secret dynatrace-platform-outputs -n sre-tools -o json \
  | jq -r '.data | to_entries[] | "\(.key): \(.value | @base64d)"'
# management_zone_ids: {"dev":"MANAGEMENT_ZONE-xxx","prod":"MANAGEMENT_ZONE-yyy",...}
# alerting_profile_ids: {"dev":"abc-123","prod":"def-456",...}
```

---

## Step 5 — Install the XRD and Composition

The `ServiceObservability` XRD is the high-level API teams write claims against.
The Composition wires it to individual DT resource CRDs.

> **Note:** The Composition references `dynatrace.crossplane.io/v1alpha1` CRDs
> from the native `provider-dynatrace`. Until that provider is built, use
> `provider-terraform` Workspaces for application-level resources.

```bash
# Install the XRD
kubectl apply -f crossplane/xrds/service-observability-xrd.yaml

# Install the Composition
kubectl apply -f crossplane/compositions/service-observability-composition.yaml

# Verify
kubectl get xrd
# NAME                                        ESTABLISHED   OFFERED   AGE
# xserviceobservabilities.oac.YOUR_ORG.io     True          True      30s

kubectl get composition
# NAME                      XR-KIND                 XR-APIVERSION              AGE
# service-observability     XServiceObservability   oac.YOUR_ORG.io/v1alpha1   30s
```

---

## Step 6 — Write your first Claim

```bash
kubectl apply -f - <<EOF
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
EOF
```

Watch the Composition expand the claim into individual resources:
```bash
kubectl get serviceobservabilityclaim payments-api-prod -n payments-api -w
# NAME                  SYNCED   READY   CONNECTION-SECRET   AGE
# payments-api-prod     True     False                       5s
# payments-api-prod     True     True                        30s

# See the composed resources
kubectl get managed -l crossplane.io/claim-name=payments-api-prod
```

---

## Step 7 — GitOps delivery via Argo CD

Add the claim to your application repo's `observability/` folder:

```bash
# In the app repo
mkdir -p observability/crossplane
cp crossplane/claims/payments-api-prod.yaml observability/crossplane/
git add observability/crossplane/
git commit -m "chore(oac): add Crossplane ServiceObservabilityClaim for prod"
git push
```

Argo CD detects the change and syncs the claim to the cluster.
Crossplane Composition engine expands it.
Provider controller reconciles to Dynatrace.

---

## Verifying the full stack

```bash
# Claim status
kubectl get serviceobservabilityclaims -A
# NAMESPACE      NAME                  SYNCED  READY  AGE
# payments-api   payments-api-prod     True    True   5m

# Composed resource detail
kubectl describe serviceobservabilityclaim payments-api-prod -n payments-api
# Events:
#   Normal  SelectComposition    Composition selected
#   Normal  ComposeResources     Created composed resource availability-slo
#   Normal  ComposeResources     Created composed resource latency-slo
#   Normal  ComposeResources     Created composed resource error-rate-alert
#   Normal  ComposeResources     Synced all composed resources

# Provider-terraform Workspace status
kubectl get workspace dynatrace-platform-resources -n sre-tools
# NAME                          READY  SYNCED  AGE
# dynatrace-platform-resources  True   True    1h
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Workspace `READY=False` | Terraform init failed — module URL unreachable | Check ADO repo URL and PAT in the Workspace spec |
| Workspace `SYNCED=False` | TF plan/apply error | `kubectl describe workspace dynatrace-platform-resources -n sre-tools` → Events section |
| Claim `READY=False` | Composed resource waiting for ProviderConfig | Ensure `provider-configs.yaml` is applied and provider is Healthy |
| `ProviderConfig not found` | Wrong ProviderConfig name in Composition patches | Names must match: `dynatrace-dev`, `dynatrace-staging`, `dynatrace-perf`, `dynatrace-prod` |
| Claim `SYNCED=False, cannot get managed resource` | native `provider-dynatrace` not installed | Use `provider-terraform` Workspaces until native provider is built |
