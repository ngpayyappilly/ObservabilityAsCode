# Crossplane — Overview & Architecture

Crossplane turns Kubernetes into a universal control plane. Instead of writing
a custom operator in Go, you define **Composite Resource Definitions (XRDs)** and
**Compositions** that wire CRD fields to provider-managed resources.

For Dynatrace, two implementation paths are available:

---

## Implementation paths

### Path A — `provider-terraform` (available today)

Wraps existing Terraform modules as Kubernetes `Workspace` resources.
The `provider-terraform` controller runs `terraform plan + apply` on a schedule,
giving you continuous reconciliation over your existing `terraform/platform-resources/`.

```
terraform/platform-resources/   ← existing TF module
        │
        │ referenced by
        ▼
Workspace CR (workspace-platform.yaml)
        │
        │ provider-terraform controller reconciles every 10min
        ▼
terraform plan + apply
        │
        ▼
Dynatrace REST API
```

**Use when:** You want continuous reconciliation today without writing Go or building
a custom provider. Full TF plan+apply runs per cycle (~30–60s).

### Path B — Native `provider-dynatrace` (2–4 months build time)

Uses [upjet](https://github.com/crossplane/upjet) to auto-generate a Crossplane
provider from the `dynatrace-oss/dynatrace` Terraform provider schema.
Each DT resource type becomes a CRD with a dedicated controller.

```
dynatrace-oss/dynatrace Terraform provider
        │
        │ upjet generates
        ▼
provider-dynatrace (CRDs + controllers)
        │
        │ per-resource reconciliation (~seconds)
        ▼
Dynatrace REST API
```

**Use when:** You want native Kubernetes-style per-resource reconciliation,
per-resource status conditions, and sub-minute drift correction.

---

## Architecture overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Kubernetes cluster (TKGs)                                               │
│                                                                          │
│  ┌────────────────────────────────┐   ┌──────────────────────────────┐   │
│  │  App namespace (payments-api)  │   │  crossplane-system namespace │   │
│  │                                │   │                              │   │
│  │  ServiceObservabilityClaim     │   │  Crossplane core             │   │
│  │  (one per environment)  ───────┼──▶│  Composition engine          │   │
│  └────────────────────────────────┘   │                              │   │
│                                       │  provider-terraform          │   │
│  ┌────────────────────────────────┐   │   OR                         │   │
│  │  Argo CD                       │   │  provider-dynatrace          │   │
│  │  ApplicationSet                │   │  (when built)                │   │
│  │  (syncs Claims to cluster) ────┼──▶│                              │   │
│  └────────────────────────────────┘   └──────────────┬───────────────┘   │
│                                                      │                   │
│  ┌────────────────────────────────┐                  │                   │
│  │  sre-tools namespace           │                  │ REST calls        │
│  │  ProviderConfig (per env)      │◀─────────────────┘                   │
│  │  dynatrace-tenant-urls Secret  │                                      │
│  │  dynatrace-api-tokens Secret   │                                      │
│  └────────────────────────────────┘                                      │
└──────────────────────────────────────────────────────────────────────────┘
        │
        ▼
Dynatrace SaaS
```

---

## Crossplane vs Operator — decision guide

| Question | Crossplane wins | Operator wins |
|---|---|---|
| Already running Crossplane in the cluster? | ✓ | |
| Want to avoid writing Go? | ✓ | |
| Need org-specific API (`spec.target: 99.9`)? | | ✓ |
| Need Backstage → DT entity auto-resolution? | | ✓ |
| Want SLO → Alert → Dashboard ordering logic? | | ✓ |
| Need drift correction in < 5 min? | | ✓ |
| Want per-resource status conditions? | provider-terraform: ✗; native: ✓ | ✓ |
| Have 2–4 months for provider build? | ✗ (provider-terraform works now) | Already built |

---

## Component inventory

| File | What it is |
|---|---|
| `crossplane/provider-terraform/provider.yaml` | Installs `provider-terraform` from Upbound registry |
| `crossplane/provider-terraform/workspace-platform.yaml` | `Workspace` CR wrapping `terraform/platform-resources/` |
| `crossplane/provider/PROVIDER_BUILD.md` | Step-by-step guide for building `provider-dynatrace` with upjet |
| `crossplane/provider/provider-configs.yaml` | One `ProviderConfig` per DT environment (dev/staging/perf/prod) |
| `crossplane/xrds/service-observability-xrd.yaml` | `ServiceObservability` XRD — the API teams write claims against |
| `crossplane/compositions/service-observability-composition.yaml` | Composition wiring the XRD to DT CRDs |
| `crossplane/claims/payments-api-prod.yaml` | Example `ServiceObservabilityClaim` |
