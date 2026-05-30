# Observability as Code (OaC) — observability-template

This repository is the single source of truth for Dynatrace observability
scaffolding across all services in the ADO project. It owns:

- **Jinja2 scaffold templates** that generate Monaco v2 configs
- **Bootstrap and propagation scripts** that push configs into application repos
- **ADO pipelines** for bootstrap, propagation, and PR validation
- **Argo CD manifests** (ApplicationSet + CMP sidecar) for GitOps deployment
- **Kyverno policy** enforcing GitOps-only mutations
- **Drift detection** CronJob
- **Terraform** for ADO variable groups and Dynatrace API tokens

---

## Repository layout

```
observability-template/
├── scaffold/
│   ├── observability/                 # Templates rendered into each app repo
│   │   ├── manifest.yaml.j2           # Monaco project manifest
│   │   ├── environments/
│   │   │   ├── dev.yaml.j2
│   │   │   ├── staging.yaml.j2
│   │   │   └── prod.yaml.j2
│   │   ├── slos/
│   │   │   ├── availability.yaml.j2 + availability-slo.json.j2
│   │   │   └── latency.yaml.j2      + latency-slo.json.j2
│   │   ├── alerts/
│   │   │   ├── error-rate.yaml.j2   + error-rate.json.j2
│   │   │   ├── latency-p99.yaml.j2  + latency-p99.json.j2
│   │   │   └── error-budget-burn.yaml.j2 + error-budget-burn.json.j2
│   │   ├── dashboards/
│   │   │   └── service-overview.yaml.j2 + service-dashboard.json.j2
│   │   ├── synthetic/
│   │   │   └── health-check.yaml.j2 + http-monitor.json.j2
│   │   └── log-metrics/
│   │       └── error-log-metric.yaml.j2
│   └── scripts/                       # Validation scripts (copied to app repos)
│       ├── ddu-estimator.py
│       └── slo-regression-check.py
├── scripts/
│   ├── oac_utils.py                   # ADO client + render utilities
│   ├── bootstrap.py                   # Initial scaffold pipeline
│   ├── propagate.py                   # Template update pipeline
│   └── drift_detector.py             # Drift detection CronJob script
├── pipelines/
│   ├── bootstrap-pipeline.yaml
│   ├── propagation-pipeline.yaml
│   └── oac-pr-validation.yaml        # Runs inside each app repo
├── manifests/
│   ├── argocd/
│   │   ├── monaco-cmp/
│   │   │   ├── plugin.yaml            # CMP v2 plugin definition
│   │   │   ├── cmp-configmap.yaml
│   │   │   ├── repo-server-patch.yaml # Kustomize patch for argocd-repo-server
│   │   │   ├── external-secrets.yaml
│   │   │   ├── sync-hook.yaml         # PostSync Job — actual DT deploy
│   │   │   └── kustomization.yaml
│   │   └── applicationset-oac.yaml
│   ├── kyverno/
│   │   └── enforce-oac-gitops.yaml
│   └── drift-detector/
│       ├── cronjob.yaml
│       └── rbac.yaml
└── terraform/
    ├── ado-variable-group/main.tf
    └── dynatrace-tokens/main.tf
```

---

## Onboarding a new service

Three steps — no manual file copying required.

**Step 1: Confirm the repo is not opted out**

Check that the application repo does not contain a `.no-oac` file at the root.
If it does, the team has explicitly opted out of OaC. Remove it (with their consent)
before proceeding.

**Step 2: Run the bootstrap pipeline filtered to the repo**

In ADO, navigate to **Pipelines → bootstrap-pipeline** and click **Run pipeline**.

Set:
- `dryRun`: `false`
- `repoFilter`: the exact repo name (or a regex matching it)

The pipeline will:
1. Render all Jinja2 templates substituting the inferred service name.
2. Push the rendered `observability/` folder to branch `feat/add-oac-scaffold`.
3. Open a PR in the application repo.

**Step 3: Review and merge the PR in the application repo**

The PR validation pipeline (`oac-pr-validation.yaml`) runs automatically
on the PR and checks:
- YAML syntax
- Monaco validation
- Monaco dry-run against staging
- DDU estimate (fails if > 5000 DDU/month)
- SLO regression (fails if any target drops > 0.1%)
- Secret scan (fails if DT tokens or tenant URLs are hardcoded)

Once all checks pass, a reviewer approves and merges. Argo CD detects the
new `observability/manifest.yaml` within minutes and begins deploying
to dev → staging (automated), then prompts for manual approval for prod.

---

## Opting out of OaC

Create a file named `.no-oac` at the root of the application repo:

```bash
touch .no-oac
git add .no-oac
git commit -m "chore: opt out of OaC scaffold"
git push
```

The bootstrap and propagation scripts skip any repo with this file present.
Existing observability configs are **not** deleted — opt-out only prevents
future scaffolding and updates.

---

## Updating SLO targets

SLO targets live in `observability/environments/prod.yaml` (and `dev.yaml`,
`staging.yaml`) inside the **application repo**.

1. Open a branch in the application repo.
2. Edit `observability/environments/prod.yaml`:

   ```yaml
   my-service:
     SLOTarget: "99.95"   # raised from 99.9
   ```

3. Open a PR to `main`. The `oac-pr-validation` pipeline will:
   - Run `slo-regression-check.py` — confirms the target did not decrease.
   - Run Monaco dry-run against staging to validate the config is deployable.
4. After review and merge, Argo CD picks up the change and the PostSync Job
   applies the updated SLO to Dynatrace.

> **Never lower a prod SLO target** without going through a formal SLA change
> process. The CI gate (`slo-regression-check.py`) will block drops > 0.1%.

---

## Adding a new alert type to all services

Alert templates live in this repository under `scaffold/observability/alerts/`.

1. Add the new `.yaml.j2` and `.json.j2` template pair in `scaffold/observability/alerts/`.
2. Push to `main` in this repo.
3. The **propagation pipeline** triggers automatically, re-renders the new template
   for every service that already has an OaC scaffold, and opens PRs in each repo
   with only the new alert files added.
4. Teams review and merge their PRs. Argo CD deploys the new alert to Dynatrace.

No human needs to copy files or click through the Dynatrace UI.

---

## ADO service connection setup

The bootstrap and propagation pipelines authenticate to ADO via a PAT stored in
the `oac-bootstrap-secrets` variable group. The PAT requires:

| Scope | Reason |
|-------|--------|
| `Code (Read & Write)` | Push scaffold branches |
| `Pull Request (Read & Write)` | Open PRs |
| `Identity (Read)` | Resolve reviewer email → ADO identity |
| `Variable Groups (Read)` | (optional) read other variable groups |

To create the variable group:

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
|---------|-------------|-----|
| Bootstrap pipeline skips all repos | `observability/manifest.yaml` already exists in all repos | Normal on re-run. Use `--repo-filter` to target specific repos. |
| Monaco dry-run fails with `HTTP 401` | `DT_STAGING_TOKEN` is expired or missing scopes | Rotate token via `terraform/dynatrace-tokens/main.tf` and re-run `terraform apply` |
| Argo CD Application stuck in `OutOfSync` | CMP sidecar crashed or `init` hook failed | Check `argocd-repo-server` pod logs for the `monaco-cmp` container: `kubectl logs -n argocd deploy/argocd-repo-server -c monaco-cmp` |
| Kyverno blocks a ConfigMap with `oac/manifest-hash missing` | Direct `kubectl apply` attempted on an OaC sentinel | Only Argo CD may write `monaco-oac-state-*` ConfigMaps. Trigger a sync from Argo CD UI or CLI. |
| Drift detector reports drift every 6 hours but auto-remediation is on | Argo CD sync keeps failing (PostSync Job failing) | Check the `monaco-deploy-*` Job logs in namespace `sre-tools`: `kubectl logs -n sre-tools job/monaco-deploy-<app>-<env>` |

---

## Architecture overview

```
ADO Repos (app code + observability/)
         │ git push / PR merge
         ▼
  Argo CD ApplicationSet
  (matrix: repo × env)
         │ detects observability/manifest.yaml
         ▼
  Monaco CMP Sidecar (argocd-repo-server)
  ┌─────────────────────────────────────┐
  │ init: validate DT token scopes      │
  │ generate: --dry-run + emit ConfigMap│
  └──────────────────┬──────────────────┘
                     │ PostSync
                     ▼
  Monaco Deploy Job (sync-hook.yaml)
  → applies configs to Dynatrace SaaS

  Every 6h: Drift Detector CronJob
  → compares ConfigMap hashes
  → hard-refresh on drift
  → Slack notification
```
