// Package controllers implements the reconciliation loops for all Dynatrace CRDs.
package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	oac "github.com/YOUR_ORG/dynatrace-operator/api/v1alpha1"
	dtclient "github.com/YOUR_ORG/dynatrace-operator/internal/dynatrace"
)

const (
	sloFinalizer      = "dynatrace.YOUR_ORG.io/slo-finalizer"
	requeueAfter      = 5 * time.Minute  // steady-state reconcile interval (drift detection)
	requeueAfterError = 30 * time.Second // back-off on transient DT API errors
)

// DynatraceSLOReconciler reconciles DynatraceSLO objects.
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatraceslos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatraceslos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatraceslos/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
type DynatraceSLOReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Log        logr.Logger
	DTClients  map[oac.Environment]*dtclient.Client // one client per DT tenant
}

// Reconcile is the main reconciliation loop.
// It runs on every watch event AND every requeueAfter interval (drift detection).
func (r *DynatraceSLOReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("dynatraceslo", req.NamespacedName)

	// Fetch the DynatraceSLO resource
	slo := &oac.DynatraceSLO{}
	if err := r.Get(ctx, req.NamespacedName, slo); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get DynatraceSLO: %w", err)
	}

	// Handle deletion — remove from Dynatrace before removing finalizer
	if !slo.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, slo)
	}

	// Ensure finalizer is registered so we can clean up DT resources on delete
	if !controllerutil.ContainsFinalizer(slo, sloFinalizer) {
		controllerutil.AddFinalizer(slo, sloFinalizer)
		if err := r.Update(ctx, slo); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
	}

	return r.reconcileCreate(ctx, log, slo)
}

// reconcileCreate creates or updates the SLO in Dynatrace.
func (r *DynatraceSLOReconciler) reconcileCreate(ctx context.Context, log logr.Logger, slo *oac.DynatraceSLO) (ctrl.Result, error) {
	dt, err := r.dtClient(slo.Spec.Environment)
	if err != nil {
		return r.setFailed(ctx, slo, "ClientResolution", err)
	}

	// Resolve management zone — defaults to env:<environment>
	mz := slo.Spec.ServiceSelector.ManagementZone
	if mz == "" {
		mz = fmt.Sprintf("env:%s", slo.Spec.Environment)
	}

	// Resolve service entity from Backstage ID if provided
	// This is the key value-add vs Monaco — the controller handles entity resolution
	entityFilter, err := r.resolveEntityFilter(ctx, dt, slo.Spec.ServiceSelector, mz)
	if err != nil {
		return r.setFailed(ctx, slo, "EntityResolution", err)
	}

	// Compute warning threshold (default: target - 0.5)
	warning := slo.Spec.Target - 0.5
	if slo.Spec.Warning != nil {
		warning = *slo.Spec.Warning
	}

	// Build the DT SLO payload based on SLO type
	payload, err := r.buildSLOPayload(slo, entityFilter, warning)
	if err != nil {
		return r.setFailed(ctx, slo, "PayloadBuild", err)
	}

	// Apply to Dynatrace (create or update)
	dtID, err := dt.ApplySLO(ctx, slo.Status.DynatraceID, payload)
	if err != nil {
		log.Error(err, "Failed to apply SLO to Dynatrace")
		return r.setFailed(ctx, slo, "DynatraceAPI", err)
	}

	// Fetch current SLO value for status reporting
	current, _ := dt.GetSLO(ctx, dtID) // non-fatal if this fails

	// Update status
	now := metav1.NewTime(time.Now())
	patch := client.MergeFrom(slo.DeepCopy())
	slo.Status.DynatraceID = dtID
	slo.Status.LastSyncTime = &now
	slo.Status.ObservedGeneration = slo.Generation
	if current != nil {
		slo.Status.CurrentValue = &current.EvaluatedPercentage
		slo.Status.ErrorBudgetRemaining = &current.ErrorBudgetRemaining
	}
	meta.SetStatusCondition(&slo.Status.Conditions, metav1.Condition{
		Type:               string(oac.ConditionSynced),
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("SLO %s synced to Dynatrace (ID: %s)", slo.Name, dtID),
		ObservedGeneration: slo.Generation,
	})

	if err := r.Status().Patch(ctx, slo, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}

	log.Info("SLO synced successfully", "dynatraceId", dtID, "target", slo.Spec.Target)

	// Requeue after interval for drift detection
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// reconcileDelete removes the SLO from Dynatrace and clears the finalizer.
func (r *DynatraceSLOReconciler) reconcileDelete(ctx context.Context, log logr.Logger, slo *oac.DynatraceSLO) (ctrl.Result, error) {
	if slo.Status.DynatraceID == "" {
		// Never made it to DT — just remove the finalizer
		controllerutil.RemoveFinalizer(slo, sloFinalizer)
		return ctrl.Result{}, r.Update(ctx, slo)
	}

	dt, err := r.dtClient(slo.Spec.Environment)
	if err != nil {
		return ctrl.Result{RequeueAfter: requeueAfterError}, err
	}

	if err := dt.DeleteSLO(ctx, slo.Status.DynatraceID); err != nil {
		log.Error(err, "Failed to delete SLO from Dynatrace", "dynatraceId", slo.Status.DynatraceID)
		return ctrl.Result{RequeueAfter: requeueAfterError}, err
	}

	log.Info("SLO deleted from Dynatrace", "dynatraceId", slo.Status.DynatraceID)
	controllerutil.RemoveFinalizer(slo, sloFinalizer)
	return ctrl.Result{}, r.Update(ctx, slo)
}

// buildSLOPayload constructs the Dynatrace SLO API body from the CR spec.
func (r *DynatraceSLOReconciler) buildSLOPayload(
	slo *oac.DynatraceSLO,
	entityFilter string,
	warning float64,
) (dtclient.SLOPayload, error) {
	payload := dtclient.SLOPayload{
		Name:           fmt.Sprintf("%s %s SLO (%s)", slo.Spec.ServiceSelector.BackstageId, slo.Spec.Type, slo.Spec.Environment),
		Enabled:        true,
		EvaluationType: "AGGREGATE",
		Filter:         entityFilter,
		Target:         slo.Spec.Target,
		Warning:        warning,
		Timeframe:      slo.Spec.Window,
		UseRateMetric:  true,
		ErrorBudgetBurnRate: &dtclient.SLOBurnRate{
			FastBurnThreshold:            14,
			BurnRateVisualizationEnabled: true,
		},
	}

	switch slo.Spec.Type {
	case oac.SLOTypeAvailability:
		payload.MetricNumerator = "builtin:service.errors.total.successCount"
		payload.MetricDenominator = "builtin:service.requestCount.total"

	case oac.SLOTypeLatency:
		if slo.Spec.LatencyThresholdMs == nil {
			return dtclient.SLOPayload{}, fmt.Errorf("latencyThresholdMs is required for latency SLOs")
		}
		thresholdMicros := *slo.Spec.LatencyThresholdMs * 1000
		payload.MetricNumerator = fmt.Sprintf(
			"builtin:service.response.time:percentile(99):filter(lt(value,%d)):splitBy()",
			thresholdMicros,
		)
		payload.MetricDenominator = "builtin:service.response.time:percentile(99):splitBy()"

	default:
		return dtclient.SLOPayload{}, fmt.Errorf("unknown SLO type: %s", slo.Spec.Type)
	}

	return payload, nil
}

// resolveEntityFilter builds a DT entity selector string.
// If BackstageId is set, resolves the actual SERVICE entity ID via the DT API.
// Falls back to management zone scoping if entity resolution isn't needed.
func (r *DynatraceSLOReconciler) resolveEntityFilter(
	ctx context.Context,
	dt *dtclient.Client,
	sel oac.ServiceSelector,
	mz string,
) (string, error) {
	if sel.BackstageId != "" {
		// Resolve the DT SERVICE entity using the backstage-id auto-tag
		entityID, err := dt.ResolveServiceEntity(ctx, sel.BackstageId)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("type(\"SERVICE\"),entityId(\"%s\")", entityID), nil
	}

	if sel.ServiceName != "" {
		return fmt.Sprintf("type(\"SERVICE\"),entityName(\"%s\"),mzName(\"%s\")", sel.ServiceName, mz), nil
	}

	// Fall back to management zone scope (all services in the zone)
	return fmt.Sprintf("type(\"SERVICE\"),mzName(\"%s\")", mz), nil
}

// dtClient returns the Dynatrace API client for the given environment.
func (r *DynatraceSLOReconciler) dtClient(env oac.Environment) (*dtclient.Client, error) {
	c, ok := r.DTClients[env]
	if !ok {
		return nil, fmt.Errorf("no Dynatrace client configured for environment %q", env)
	}
	return c, nil
}

// setFailed marks the SLO as failed with a condition and requeues.
func (r *DynatraceSLOReconciler) setFailed(ctx context.Context, slo *oac.DynatraceSLO, reason string, err error) (ctrl.Result, error) {
	patch := client.MergeFrom(slo.DeepCopy())
	meta.SetStatusCondition(&slo.Status.Conditions, metav1.Condition{
		Type:               string(oac.ConditionSynced),
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            err.Error(),
		ObservedGeneration: slo.Generation,
	})
	_ = r.Status().Patch(ctx, slo, patch)
	return ctrl.Result{RequeueAfter: requeueAfterError}, err
}

// SetupWithManager registers the controller with the manager.
func (r *DynatraceSLOReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&oac.DynatraceSLO{}).
		// Also watch Secrets so credential rotation triggers re-reconcile
		Owns(&corev1.Secret{}).
		Complete(r)
}

// ── Helper: build DTClients from namespace Secrets ────────────────────────

// BuildDTClients constructs one DT client per environment by reading
// the credentials Secrets managed by ExternalSecrets Operator.
func BuildDTClients(ctx context.Context, k8s client.Client, namespace string) (map[oac.Environment]*dtclient.Client, error) {
	urlSecret := &corev1.Secret{}
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "dynatrace-tenant-urls"}, urlSecret); err != nil {
		return nil, fmt.Errorf("get dynatrace-tenant-urls secret: %w", err)
	}

	tokenSecret := &corev1.Secret{}
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "dynatrace-api-tokens"}, tokenSecret); err != nil {
		return nil, fmt.Errorf("get dynatrace-api-tokens secret: %w", err)
	}

	clients := map[oac.Environment]*dtclient.Client{}
	for _, env := range []oac.Environment{oac.EnvDev, oac.EnvStaging, oac.EnvPerf, oac.EnvProd} {
		urlKey := string(env) + "-url"
		tokenKey := string(env) + "-token"

		url, ok := urlSecret.Data[urlKey]
		if !ok {
			continue // this env not configured
		}
		token, ok := tokenSecret.Data[tokenKey]
		if !ok {
			continue
		}

		clients[env] = dtclient.NewClient(string(url), string(token))
	}
	return clients, nil
}
