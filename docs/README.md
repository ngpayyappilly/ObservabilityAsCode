# Observability as Code — Documentation Index

This directory contains implementation guides for the two GitOps-native approaches
to deploying Dynatrace observability configs at scale.

---

## Choose your approach

| | Operator | Crossplane |
|---|---|---|
| **What it is** | Custom Kubernetes controller built for this org | Generic control plane framework |
| **API design** | Domain-specific (`spec.target: 99.9`) | Verbose (`spec.forProvider.*`) |
| **Provider exists?** | Yes — built here | No native DT provider; `provider-terraform` bridges today |
| **Drift correction** | Every 5 min (requeueAfter) | Every 10 min (Workspace reconcile) |
| **Business logic** | In Go controllers — auto-resolve Backstage → DT entity, enforce SLO minimums | In Compositions — patch-based, no custom code |
| **Build cost** | 2–3 months to production | `provider-terraform` works today; native provider = 2–4 months |
| **Best for** | Orgs wanting a clean, org-specific API and long-term investment | Orgs already running Crossplane, or wanting to avoid Go |

---

## Documentation

### Operator

| Document | What it covers |
|---|---|
| [Overview & Architecture](operator/README.md) | How the operator works, component diagram, reconciliation loop |
| [Getting Started](operator/getting-started.md) | Install CRDs, deploy operator, write your first CR |
| [CRD Reference](operator/crds-reference.md) | Full API reference for all four CRDs |
| [Dashboard Templates](operator/dashboard-templates.md) | service-overview, slo-report, endpoint-detail layouts |
| [Development Guide](operator/development.md) | Build, test, extend the operator |

### Crossplane

| Document | What it covers |
|---|---|
| [Overview & Architecture](crossplane/README.md) | How Crossplane integrates with Dynatrace, component diagram |
| [Getting Started](crossplane/getting-started.md) | Install Crossplane, install providers, apply first Composition |
| [provider-terraform Guide](crossplane/provider-terraform.md) | Wrapping Terraform modules as Workspaces today |
| [Native Provider Guide](crossplane/native-provider.md) | Building `provider-dynatrace` with upjet |
| [XRD & Composition Reference](crossplane/compositions.md) | ServiceObservability XRD and Composition API |

---

## Which approach owns which resources?

```
Platform resources (shared, SRE-owned)
  Management zones, auto-tags, alerting profiles, notifications,
  request attributes, span attributes
  → terraform/platform-resources/  (Terraform, applied by SRE team)

Application resources (per-service, team-owned)
  SLOs, alerts, dashboards, synthetic monitors
  → EITHER:
      operator/   — DynatraceSLO / DynatraceAlert / DynatraceDashboard CRDs
      crossplane/ — ServiceObservabilityClaim CR
```

Both approaches read Dynatrace credentials from the same ExternalSecrets-managed
Secrets (`dynatrace-tenant-urls`, `dynatrace-api-tokens`) in namespace `sre-tools`.
