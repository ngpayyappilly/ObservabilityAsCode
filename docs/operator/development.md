# Operator — Development Guide

This guide covers building, testing, and extending the `dynatrace-operator`.

---

## Repository structure

```
operator/
├── api/v1alpha1/
│   ├── types.go                 ← CRD Go type definitions + kubebuilder markers
│   ├── groupversion_info.go     ← scheme registration (AddToScheme)
│   └── zz_generated.deepcopy.go ← generated DeepCopy methods (do not edit)
├── controllers/
│   ├── dynatraceslo_controller.go        ← SLO reconciler + BuildDTClients
│   ├── dynatracealert_controller.go      ← Alert reconciler
│   ├── dynatracedashboard_controller.go  ← Dashboard reconciler + SLO watch
│   └── dynatracenotification_controller.go
├── internal/dynatrace/
│   ├── client.go                ← DT REST API client (SLO, alerts, dashboards)
│   └── dashboard_templates.go  ← service-overview, slo-report, endpoint-detail
├── config/
│   ├── crd/                     ← CRD YAML manifests (apply to cluster)
│   └── rbac/                    ← ServiceAccount, ClusterRole, ClusterRoleBinding
├── examples/                    ← example CRs for testing
├── main.go                      ← manager setup, controller registration
└── go.mod
```

---

## Local development setup

### 1. Install tools

```bash
# controller-gen — regenerates CRD YAMLs and deepcopy from Go types
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.14.0

# kubebuilder — optional, for scaffolding new controllers
curl -L -o kubebuilder \
  "https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)"
chmod +x kubebuilder && mv kubebuilder /usr/local/bin/

# Set up Go workspace
cd operator
go mod download
```

### 2. Regenerate CRDs after type changes

When you change `api/v1alpha1/types.go`, regenerate:

```bash
cd operator

# Regenerate zz_generated.deepcopy.go
controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Regenerate CRD YAML manifests from kubebuilder markers
controller-gen crd paths="./..." output:crd:artifacts:config=config/crd

# Verify the CRDs look correct
kubectl apply -k config/crd/ --dry-run=client
```

### 3. Run against a live cluster (without building an image)

```bash
# Point KUBECONFIG at your TKGs cluster
export KUBECONFIG=~/.kube/tkg-dev.yaml

# Install CRDs first
kubectl apply -k config/crd/

# Run the operator locally (talks to the real cluster and real DT)
go run . --namespace=sre-tools --leader-elect=false
# 2026-05-31T12:00:00Z INFO setup Dynatrace clients initialised {"environments": 4}
# 2026-05-31T12:00:00Z INFO Starting manager
```

The operator will reconcile all DynatraceSLO objects in the cluster using
real Dynatrace API calls. Use `--namespace=dev-test` and a dev DT tenant.

### 4. Run unit tests

```bash
cd operator
go test ./... -v
```

Tests use a fake controller-runtime client — no cluster or DT API access needed.

### 5. Run integration tests (requires a cluster)

```bash
# Uses envtest — starts a local API server without a full cluster
go test ./controllers/... -v -tags=integration
```

---

## Adding a new CRD type

Example: adding `DynatraceKeyRequest` to mark endpoints as DT key requests.

### Step 1 — Define the type in `api/v1alpha1/types.go`

```go
type DynatraceKeyRequestSpec struct {
    Environment     Environment     `json:"environment"`
    ServiceSelector ServiceSelector `json:"serviceSelector"`
    // Endpoints to mark as DT key requests
    Endpoints []KeyRequestEndpoint `json:"endpoints"`
}

type KeyRequestEndpoint struct {
    Method string `json:"method"` // GET | POST | ...
    Path   string `json:"path"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=dtkr
type DynatraceKeyRequest struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec   DynatraceKeyRequestSpec   `json:"spec,omitempty"`
    Status DynatraceKeyRequestStatus `json:"status,omitempty"`
}
```

### Step 2 — Register in `groupversion_info.go`

```go
SchemeBuilder.Register(
    // ... existing types ...
    &DynatraceKeyRequest{},
    &DynatraceKeyRequestList{},
)
```

### Step 3 — Write the controller

```bash
# Scaffold a new controller file
cp controllers/dynatraceslo_controller.go controllers/dynatracekeyrequest_controller.go
# Replace DynatraceSLO references, implement reconcile logic
```

### Step 4 — Register in `main.go`

```go
if err := (&controllers.DynatraceKeyRequestReconciler{
    Client:    mgr.GetClient(),
    Scheme:    mgr.GetScheme(),
    DTClients: dtClients,
}).SetupWithManager(mgr); err != nil {
    setupLog.Error(err, "Unable to create DynatraceKeyRequest controller")
    os.Exit(1)
}
```

### Step 5 — Regenerate CRDs

```bash
controller-gen object paths="./..." && controller-gen crd paths="./..." output:crd:artifacts:config=config/crd
kubectl apply -k config/crd/
```

---

## Adding a new dashboard template

See [Dashboard Templates](dashboard-templates.md#adding-a-new-template) for the
step-by-step process. Template functions live in
`internal/dynatrace/dashboard_templates.go` alongside the three existing ones.

---

## Extending the DT client

The `internal/dynatrace/client.go` exposes typed methods for each DT API
family. To add a new API (e.g. synthetic monitor key requests):

```go
// Add to client.go

type KeyRequestPayload struct {
    ID       string `json:"id,omitempty"`
    Name     string `json:"name"`
    // ... DT API fields
}

func (c *Client) ApplyKeyRequest(ctx context.Context, id string, payload KeyRequestPayload) (string, error) {
    const basePath = "/api/config/v1/service/keyRequests"
    if id != "" {
        _, err := c.doJSON(ctx, http.MethodPut, basePath+"/"+id, payload, nil)
        return id, err
    }
    var resp KeyRequestPayload
    _, err := c.doJSON(ctx, http.MethodPost, basePath, payload, &resp)
    return resp.ID, err
}
```

The `doJSON` helper handles authentication, retries (via the HTTP client timeout),
and response body parsing. Add new methods following the same
`Apply<Type>` / `Delete<Type>` convention.

---

## Build and release

```bash
# Run all checks before submitting a PR
go vet ./...
go test ./...
controller-gen object paths="./..."
controller-gen crd paths="./..." output:crd:artifacts:config=config/crd

# Build the image
docker build -t YOUR_REGISTRY/dynatrace-operator:v0.2.0 .

# Push
docker push YOUR_REGISTRY/dynatrace-operator:v0.2.0

# Deploy (rolling update — 2 replicas, zero downtime)
kubectl set image deployment/dynatrace-operator \
  manager=YOUR_REGISTRY/dynatrace-operator:v0.2.0 \
  -n sre-tools

kubectl rollout status deployment/dynatrace-operator -n sre-tools
```

---

## Design principles

1. **Idempotency**: every reconcile call must be safe to repeat. Use `ApplySLO`
   (which calls PUT if an ID exists, POST otherwise) rather than always POST.

2. **Finalizers on every CRD**: always register a finalizer before the first DT
   API call. Without it, deleting a CR leaves an orphaned resource in Dynatrace.

3. **requeueAfter for drift**: the 5-minute requeue _is_ the drift detector. No
   separate CronJob needed. Avoid long requeue intervals — drift could go
   undetected.

4. **Cross-resource ordering via status**: never store DT IDs in spec. Read them
   from `.status.dynatraceId` of referenced objects. This means resources always
   create in the right order, regardless of the order CRs are applied.

5. **setFailed pattern**: on any error, always update `status.conditions` before
   returning. This gives operators visibility via `kubectl describe` without
   having to tail logs.

6. **Secrets via k8s client**: never mount credentials as env vars. Read them at
   startup via `BuildDTClients` (which uses the k8s client to get Secrets). This
   means token rotation takes effect on the next reconcile without a pod restart.
