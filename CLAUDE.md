# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

**Observability as Code (OaC)** — single source of truth for Dynatrace observability across all services. Three delivery mechanisms exist side-by-side; pick one per org:

| Mechanism | Entry point | Who reconciles |
|---|---|---|
| Monaco + Argo CD CMP | `scaffold/observability/` Jinja2 → Monaco YAML | PostSync Job + drift CronJob (6h) |
| Custom operator (Go) | `scaffold/observability-operator/` Jinja2 → CRD manifests | Go controller every 5 min |
| Crossplane | `crossplane/` Composition + Claim | provider-terraform every 10 min |

Shared platform layer (management zones, auto-tags, alerting profiles, notifications, span/request attributes) lives in `terraform/platform-resources/` and is applied once by SRE.

## Operator — build, test, run

```bash
cd operator

# Install tooling (once)
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.14.0

# Download dependencies
go mod download

# Run unit tests (no cluster or DT API needed — uses fake controller-runtime client)
go test ./... -v

# Run integration tests (starts local API server via envtest)
go test ./controllers/... -v -tags=integration

# Lint
go vet ./...

# Run locally against a live cluster (no image build needed)
export KUBECONFIG=~/.kube/tkg-dev.yaml
kubectl apply -k config/crd/
go run . --namespace=sre-tools --leader-elect=false

# After changing api/v1alpha1/types.go — regenerate DeepCopy + CRD YAMLs
controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
controller-gen crd paths="./..." output:crd:artifacts:config=config/crd
kubectl apply -k config/crd/ --dry-run=client   # verify

# Build and deploy image
docker build -t YOUR_REGISTRY/dynatrace-operator:vX.Y.Z .
docker push YOUR_REGISTRY/dynatrace-operator:vX.Y.Z
kubectl set image deployment/dynatrace-operator manager=YOUR_REGISTRY/dynatrace-operator:vX.Y.Z -n sre-tools
kubectl rollout status deployment/dynatrace-operator -n sre-tools
```

**Pre-PR checklist for operator changes:**
```bash
go vet ./... && go test ./...
controller-gen object paths="./..."
controller-gen crd paths="./..." output:crd:artifacts:config=config/crd
```

## Terraform — platform resources

```bash
cd terraform/platform-resources
cp terraform.tfvars.example terraform.tfvars   # fill in dt_url, dt_api_token, notifications
terraform init && terraform plan && terraform apply

# Capture IDs needed by app-layer configs
terraform output -json alerting_profile_ids
```

## Python scripts

```bash
# Bootstrap: scaffolds OaC into all ADO repos (renders Jinja2 templates, opens PRs)
python scripts/bootstrap.py

# Propagate: pushes template updates to app repos on push to main
python scripts/propagate.py

# Drift detection (Monaco approach): compares manifest hashes
python scripts/drift_detector.py

# Generate per-endpoint SLO configs from endpoints YAML (run and commit output)
python scaffold/scripts/generate-endpoint-slos.py \
  --endpoints observability/slos/endpoints/critical-endpoints.yaml \
  --env-file observability/environments/prod.yaml

# DDU estimate (Monaco approach)
python scaffold/scripts/ddu-estimator.py

# SLO regression check (used as PR gate)
python scaffold/scripts/slo-regression-check.py
```

`scripts/oac_utils.py` is the shared library: ADO REST client (api-version=7.1, PAT auth as `Basic base64(:{pat})`), Jinja2 rendering utilities. Both `bootstrap.py` and `propagate.py` import from it.

## Architecture — how the layers connect

The Backstage → Dynatrace data flow ties all three delivery paths together:

1. Teams add required k8s labels to Deployments (`app.kubernetes.io/name`, `backstage.io/kubernetes-id`, `team`, `environment`, `domain`).
2. DT OneAgent reads pod labels → `dynatrace_autotag_v2` rules translate them to DT contextless tags (`service:<name>`, `team:<name>`, etc.).
3. Management zones match on the `environment:<env>` tag to scope SLOs, alerts, and dashboards per environment.
4. The operator's `backstageId` resolution calls `GET /api/v2/entities?tag(backstage-id:<id>)` to find the exact DT SERVICE entity ID — this is how `spec.serviceSelector.backstageId` works without hard-coding DT entity IDs.

The bootstrap pipeline scaffolds template files into app repos. The propagation pipeline pushes template updates. The PR validation pipeline (`pipelines/oac-pr-validation.yaml`) runs on each app PR touching `observability/**`: YAML lint → Monaco dry-run → DDU estimate (cap 5,000 DDU/month) → endpoint SLO sync check → SLO regression gate (blocks target drop > 0.1%) → secret scan.

## Operator design principles

- **Finalizer before first DT API call**: every CRD must register a finalizer before any API write. Skipping this leaves orphaned DT resources on CR deletion.
- **Status as cross-resource ordering**: never store DT IDs in spec. Read them from `.status.dynatraceId` of referenced objects. The dashboard controller waits for SLO `.status.dynatraceId` before building dashboard JSON — apply order is irrelevant.
- **`Apply<Type>` pattern**: PUT if `status.dynatraceId` is set, POST otherwise. Never always-POST — idempotency is required.
- **5-minute requeueAfter is the drift detector**: no separate CronJob. Don't lengthen this interval.
- **Token rotation without pod restart**: credentials are read at startup via `BuildDTClients` (k8s client → ExternalSecrets-managed Secrets). A `terraform apply` in `dynatrace-tokens/` rotates tokens; the next reconcile picks them up automatically.
- **`setFailed` on every error path**: always update `status.conditions` before returning an error, so `kubectl describe` shows the reason without log tailing.

## Adding a new CRD

1. Define type in [operator/api/v1alpha1/types.go](operator/api/v1alpha1/types.go) with `+kubebuilder` markers.
2. Register `&NewType{}` and `&NewTypeList{}` in [operator/api/v1alpha1/groupversion_info.go](operator/api/v1alpha1/groupversion_info.go).
3. Copy a controller as a starting point (e.g. `dynatraceslo_controller.go`) and implement `Reconcile`.
4. Register the reconciler in [operator/main.go](operator/main.go).
5. Regenerate: `controller-gen object paths="./..." && controller-gen crd paths="./..." output:crd:artifacts:config=config/crd`.

## Opting a repo out of scaffolding

```bash
touch .no-oac && git add .no-oac && git commit -m "chore: opt out of OaC scaffold" && git push
```

Bootstrap and propagation scripts skip repos with this file.
