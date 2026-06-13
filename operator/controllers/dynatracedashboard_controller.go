package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	oac "github.com/YOUR_ORG/dynatrace-operator/api/v1alpha1"
	dtclient "github.com/YOUR_ORG/dynatrace-operator/internal/dynatrace"
)

const dashboardFinalizer = "dynatrace.YOUR_ORG.io/dashboard-finalizer"

// DynatraceDashboardReconciler reconciles DynatraceDashboard objects.
//
// Dependency ordering:
//   DynatraceSLO must be synced (status.dynatraceId set) before the dashboard
//   can be created, because SLO tile IDs must be real DT IDs.
//   The controller watches DynatraceSLO events and re-queues any dashboard
//   that references the changed SLO, so the ordering resolves automatically.
//
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatracedashboards,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatracedashboards/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatracedashboards/finalizers,verbs=update
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatraceslos,verbs=get;list;watch
type DynatraceDashboardReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Log       logr.Logger
	DTClients map[oac.Environment]*dtclient.Client
}

func (r *DynatraceDashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("dynatracedashboard", req.NamespacedName)

	dash := &oac.DynatraceDashboard{}
	if err := r.Get(ctx, req.NamespacedName, dash); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !dash.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, dash)
	}

	if !controllerutil.ContainsFinalizer(dash, dashboardFinalizer) {
		controllerutil.AddFinalizer(dash, dashboardFinalizer)
		if err := r.Update(ctx, dash); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileCreate(ctx, log, dash)
}

// reconcileCreate resolves all dependencies, builds the dashboard from the
// correct template, and applies it to Dynatrace.
func (r *DynatraceDashboardReconciler) reconcileCreate(
	ctx context.Context,
	log logr.Logger,
	dash *oac.DynatraceDashboard,
) (ctrl.Result, error) {
	dt, ok := r.DTClients[dash.Spec.Environment]
	if !ok {
		return ctrl.Result{RequeueAfter: requeueAfterError},
			fmt.Errorf("no Dynatrace client configured for environment %q", dash.Spec.Environment)
	}

	// ── Step 1: resolve SLO IDs from sloRefs ─────────────────────────────
	// Each ref is a DynatraceSLO name in the same namespace.
	// If any SLO hasn't been synced yet, requeue and wait — we need real DT IDs.
	sloIDs := make([]string, 0, len(dash.Spec.SLORefs))
	sloNames := make([]string, 0, len(dash.Spec.SLORefs))
	pending := []string{}

	for _, ref := range dash.Spec.SLORefs {
		slo := &oac.DynatraceSLO{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: dash.Namespace, Name: ref}, slo); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setFailed(ctx, dash, "SLORefNotFound",
					fmt.Errorf("sloRef %q not found — create the DynatraceSLO first", ref))
			}
			return ctrl.Result{RequeueAfter: requeueAfterError}, err
		}
		if slo.Status.DynatraceID == "" {
			pending = append(pending, ref)
			continue
		}
		sloIDs = append(sloIDs, slo.Status.DynatraceID)
		sloNames = append(sloNames, sloDisplayName(slo))
	}

	if len(pending) > 0 {
		log.Info("Waiting for SLOs to sync before building dashboard",
			"pending", strings.Join(pending, ", "))
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// ── Step 2: resolve management zone ──────────────────────────────────
	mz := dash.Spec.ServiceSelector.ManagementZone
	if mz == "" {
		mz = fmt.Sprintf("env:%s", dash.Spec.Environment)
	}

	// ── Step 3: resolve service name ──────────────────────────────────────
	serviceName := dash.Spec.ServiceSelector.BackstageId
	if serviceName == "" {
		serviceName = dash.Spec.ServiceSelector.ServiceName
	}
	if serviceName == "" {
		serviceName = dash.Name
	}

	// ── Step 4: build dashboard payload from template ─────────────────────
	templateName := string(dash.Spec.Template)
	payload, err := dtclient.BuildDashboard(templateName, dtclient.TemplateData{
		ServiceName:    serviceName,
		Environment:    string(dash.Spec.Environment),
		ManagementZone: mz,
		SLOIDs:         sloIDs,
		SLONames:       sloNames,
	})
	if err != nil {
		return r.setFailed(ctx, dash, "TemplateBuild", err)
	}

	// Propagate the Shared flag from spec into the payload metadata
	payload.Metadata.Shared = dash.Spec.Shared

	// ── Step 5: apply to Dynatrace ────────────────────────────────────────
	dtID, err := dt.ApplyDashboard(ctx, dash.Status.DynatraceID, payload)
	if err != nil {
		log.Error(err, "Failed to apply dashboard to Dynatrace", "template", templateName)
		return r.setFailed(ctx, dash, "DynatraceAPI", err)
	}

	// ── Step 6: update status ─────────────────────────────────────────────
	dashURL := r.buildDashboardURL(dt, dash.Spec.Environment, dtID)
	now := metav1.NewTime(time.Now())
	patch := client.MergeFrom(dash.DeepCopy())
	dash.Status.DynatraceID = dtID
	dash.Status.DashboardURL = dashURL
	dash.Status.LastSyncTime = &now
	dash.Status.ObservedGeneration = dash.Generation
	meta.SetStatusCondition(&dash.Status.Conditions, metav1.Condition{
		Type:               string(oac.ConditionSynced),
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("Dashboard synced via template %q (DT ID: %s)", templateName, dtID),
		ObservedGeneration: dash.Generation,
	})
	if err := r.Status().Patch(ctx, dash, patch); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Dashboard synced",
		"template", templateName,
		"dynatraceId", dtID,
		"url", dashURL,
		"sloCount", len(sloIDs),
	)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// reconcileDelete removes the dashboard from Dynatrace and clears the finalizer.
func (r *DynatraceDashboardReconciler) reconcileDelete(
	ctx context.Context,
	log logr.Logger,
	dash *oac.DynatraceDashboard,
) (ctrl.Result, error) {
	if dash.Status.DynatraceID != "" {
		dt, ok := r.DTClients[dash.Spec.Environment]
		if ok {
			if err := dt.DeleteDashboard(ctx, dash.Status.DynatraceID); err != nil {
				log.Error(err, "Failed to delete dashboard from Dynatrace")
				return ctrl.Result{RequeueAfter: requeueAfterError}, err
			}
			log.Info("Dashboard deleted from Dynatrace", "dynatraceId", dash.Status.DynatraceID)
		}
	}
	controllerutil.RemoveFinalizer(dash, dashboardFinalizer)
	return ctrl.Result{}, r.Update(ctx, dash)
}

// SetupWithManager registers the controller and sets up watches.
func (r *DynatraceDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&oac.DynatraceDashboard{}).
		// Re-enqueue dashboards when any SLO they reference gets its DynatraceID
		Watches(
			&oac.DynatraceSLO{},
			handler.EnqueueRequestsFromMapFunc(r.sloToDashboards),
		).
		Complete(r)
}

// sloToDashboards maps a DynatraceSLO event to all dashboards in the same
// namespace that reference it via spec.sloRefs.
func (r *DynatraceDashboardReconciler) sloToDashboards(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	sloName := obj.GetName()
	ns := obj.GetNamespace()

	dashList := &oac.DynatraceDashboardList{}
	if err := r.List(ctx, dashList, client.InNamespace(ns)); err != nil {
		return nil
	}

	var reqs []reconcile.Request
	for _, dash := range dashList.Items {
		for _, ref := range dash.Spec.SLORefs {
			if ref == sloName {
				reqs = append(reqs, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: dash.Namespace,
						Name:      dash.Name,
					},
				})
				break
			}
		}
	}
	return reqs
}

// ── Helpers ───────────────────────────────────────────────────────────────

// sloDisplayName derives a human-readable SLO label for dashboard tile names.
func sloDisplayName(slo *oac.DynatraceSLO) string {
	switch slo.Spec.Type {
	case oac.SLOTypeAvailability:
		return fmt.Sprintf("Availability (%.1f%%)", slo.Spec.Target)
	case oac.SLOTypeLatency:
		if slo.Spec.LatencyThresholdMs != nil {
			return fmt.Sprintf("Latency p99 < %dms (%.1f%%)", *slo.Spec.LatencyThresholdMs, slo.Spec.Target)
		}
		return fmt.Sprintf("Latency p99 (%.1f%%)", slo.Spec.Target)
	}
	return slo.Name
}

// buildDashboardURL constructs the direct Dynatrace UI link for a dashboard.
// The base URL is read from the DT client's configured tenant URL.
func (r *DynatraceDashboardReconciler) buildDashboardURL(
	dt *dtclient.Client,
	env oac.Environment,
	dashboardID string,
) string {
	// Tenant URL is stored in the DT client — expose a getter
	base := dt.TenantURL()
	return fmt.Sprintf("%s/#dashboard;id=%s", base, dashboardID)
}

// setFailed marks the dashboard as failed and schedules a retry.
func (r *DynatraceDashboardReconciler) setFailed(
	ctx context.Context,
	dash *oac.DynatraceDashboard,
	reason string,
	err error,
) (ctrl.Result, error) {
	patch := client.MergeFrom(dash.DeepCopy())
	meta.SetStatusCondition(&dash.Status.Conditions, metav1.Condition{
		Type:    string(oac.ConditionSynced),
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
	_ = r.Status().Patch(ctx, dash, patch)
	return ctrl.Result{RequeueAfter: requeueAfterError}, err
}
