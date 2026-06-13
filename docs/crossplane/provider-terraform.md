# Crossplane — provider-terraform Guide

`provider-terraform` is the pragmatic path: it wraps your existing Terraform
modules as Kubernetes `Workspace` resources and runs `terraform apply` on a
configurable schedule. No Go code, no new provider to build.

---

## How it works

```
Git repo (observability-template)
  terraform/platform-resources/   ← your existing TF module
        │
        │ referenced by URL in Workspace.spec.forProvider.module
        ▼
Workspace CR in Kubernetes
        │
        │ provider-terraform controller reconciles
        │ (polls every pollInterval, default 10min)
        ▼
Inside the cluster, the controller runs:
  terraform init   ← downloads providers, initialises state
  terraform plan   ← computes diff
  terraform apply  ← applies changes to Dynatrace
        │
        ▼
Terraform state stored in a Secret:
  workspace-platform-resources-tf-state (in sre-tools)
        │
        ▼
TF outputs written to:
  dynatrace-platform-outputs Secret (in sre-tools)
  keys: management_zone_ids, alerting_profile_ids, etc.
```

---

## Workspace CR anatomy

```yaml
apiVersion: tf.upbound.io/v1beta1
kind: Workspace
metadata:
  name: dynatrace-platform-resources
  namespace: sre-tools
spec:
  forProvider:
    # Pull module from ADO — the controller clones this on each reconcile
    source: Remote
    module: git::https://dev.azure.com/YOUR_ORG/YOUR_PROJECT/_git/observability-template//terraform/platform-resources?ref=main

    # Non-sensitive Terraform variables (sensitive ones come from varFiles)
    vars:
      - key: some_var
        value: some_value

    # Sensitive variables from a Kubernetes Secret as a JSON tfvars file
    varFiles:
      - source: SecretKey
        secretKeyRef:
          namespace: sre-tools
          name: dynatrace-platform-tfvars       # ESO creates this from Vault
          key: terraform.tfvars.json

    # Environment variables for the DT and Vault providers
    env:
      - name: DT_ENV_URL
        valueFrom:
          secretKeyRef:
            name: dynatrace-tenant-urls
            key: prod-url
      - name: DT_API_TOKEN
        valueFrom:
          secretKeyRef:
            name: dynatrace-api-tokens
            key: prod-token
      - name: VAULT_TOKEN
        valueFrom:
          secretKeyRef:
            name: vault-crossplane-token
            key: token
      - name: VAULT_ADDR
        value: "https://vault.internal"

  # TF outputs written here as base64-encoded JSON values
  writeConnectionSecretToRef:
    namespace: sre-tools
    name: dynatrace-platform-outputs
```

---

## The `terraform.tfvars.json` Secret

The Workspace needs the `notifications` variable (which contains sensitive
webhook URLs and API keys). Store it in Vault and pull via ESO:

**Vault structure:**
```
secret/dynatrace/crossplane-tfvars
  terraform.tfvars.json = '{
    "dt_url": "https://xxx.live.dynatrace.com",
    "dt_api_token": "...",
    "notifications": {
      "slack": {
        "enabled": true,
        "channels": { "prod": { "webhook_url": "https://hooks.slack.com/..." } }
      },
      "pagerduty": { ... }
    }
  }'
```

**ExternalSecret:**
```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: dynatrace-platform-tfvars
  namespace: sre-tools
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: ClusterSecretStore
  target:
    name: dynatrace-platform-tfvars
    creationPolicy: Owner
  data:
    - secretKey: terraform.tfvars.json
      remoteRef:
        key: secret/dynatrace/crossplane-tfvars
        property: terraform.tfvars.json
```

---

## Reading Workspace outputs

The `writeConnectionSecretToRef` Secret contains the TF outputs as base64 JSON:

```bash
# List all output keys
kubectl get secret dynatrace-platform-outputs -n sre-tools \
  -o json | jq -r '.data | keys[]'
# alerting_profile_ids
# management_zone_ids
# management_zone_names
# slack_notification_ids

# Read the alerting profile IDs
kubectl get secret dynatrace-platform-outputs -n sre-tools \
  -o jsonpath='{.data.alerting_profile_ids}' | base64 -d | jq
# {
#   "dev": "abc-123-dev",
#   "staging": "def-456-staging",
#   "perf": "ghi-789-perf",
#   "prod": "jkl-000-prod"
# }
```

Use these IDs to update the `alertingProfileRef` fields in your `ServiceObservabilityClaim`
or directly in Operator CRs.

---

## Triggering a manual reconcile

```bash
# Force an immediate re-apply by annotating the Workspace
kubectl annotate workspace dynatrace-platform-resources -n sre-tools \
  crossplane.io/paused="false" --overwrite

# Or patch the poll interval temporarily to 1m for faster testing
kubectl patch workspace dynatrace-platform-resources -n sre-tools \
  --type=merge \
  -p '{"spec":{"forProvider":{}}}'
```

---

## Viewing Terraform plan output

```bash
# provider-terraform stores plan output in the Workspace status
kubectl describe workspace dynatrace-platform-resources -n sre-tools
# ...
# Status:
#   At Provider:
#     Outputs:
#       alerting_profile_ids: {"dev":"abc","prod":"xyz"}
#   Conditions:
#     Last Transition Time:  2026-05-31T12:00:00Z
#     Reason:                Available
#     Status:                True
#     Type:                  Ready

# Full apply logs (if something went wrong)
kubectl logs -n crossplane-system \
  -l pkg.crossplane.io/revision=provider-terraform \
  --tail=100
```

---

## State management

`provider-terraform` stores Terraform state in a Kubernetes Secret:
`<workspace-name>-tf-state` in the Workspace's namespace.

```bash
# Inspect the state (base64-encoded terraform.tfstate JSON)
kubectl get secret dynatrace-platform-resources-tf-state -n sre-tools \
  -o jsonpath='{.data.state}' | base64 -d | jq '.resources[].type'
# "dynatrace_management_zone_v2"
# "dynatrace_autotag_v2"
# "dynatrace_alerting"
# ...
```

**Do not delete this Secret** — you will lose the Terraform state and the next
apply will attempt to re-create all resources (Dynatrace may reject duplicates
with 409 Conflict).

---

## Limitations vs native provider

| | `provider-terraform` | Native `provider-dynatrace` |
|---|---|---|
| Reconcile cycle | Full plan+apply every 10min (~30–60s) | Per-resource controller (~seconds) |
| Status per resource | Workspace-level only | Per-CRD `.status.conditions` |
| Drift granularity | Module-level | Per-resource |
| Incremental updates | Full TF plan computes diff | Controller detects spec change |
| Secret injection | Via `varFiles` | Via `ProviderConfig` secretRef |
| ADO private repos | Needs PAT in Workspace spec | N/A |
