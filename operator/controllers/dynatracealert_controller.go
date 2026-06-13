package controllers

import (
	"context"
	"fmt"
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

	oac "github.com/YOUR_ORG/dynatrace-operator/api/v1alpha1"
	dtclient "github.com/YOUR_ORG/dynatrace-operator/internal/dynatrace"
)

const alertFinalizer = "dynatrace.YOUR_ORG.io/alert-finalizer"

// DynatraceAlertReconciler reconciles DynatraceAlert objects.
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatracealerts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatracealerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=oac.YOUR_ORG.io,resources=dynatracealerts/finalizers,verbs=update
type DynatraceAlertReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Log       logr.Logger
	DTClients map[oac.Environment]*dtclient.Client
}

func (r *DynatraceAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("dynatracealert", req.NamespacedName)

	alert := &oac.DynatraceAlert{}
	if err := r.Get(ctx, req.NamespacedName, alert); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !alert.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, alert)
	}

	if !controllerutil.ContainsFinalizer(alert, alertFinalizer) {
		controllerutil.AddFinalizer(alert, alertFinalizer)
		if err := r.Update(ctx, alert); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.reconcileCreate(ctx, log, alert)
}

func (r *DynatraceAlertReconciler) reconcileCreate(ctx context.Context, log logr.Logger, alert *oac.DynatraceAlert) (ctrl.Result, error) {
	dt, ok := r.DTClients[alert.Spec.Environment]
	if !ok {
		return ctrl.Result{RequeueAfter: requeueAfterError}, fmt.Errorf("no DT client for env %s", alert.Spec.Environment)
	}

	mz := alert.Spec.ServiceSelector.ManagementZone
	if mz == "" {
		mz = fmt.Sprintf("env:%s", alert.Spec.Environment)
	}

	// For burn rate alerts, resolve the SLO ID from the referenced DynatraceSLO
	sloID := ""
	if (alert.Spec.Type == oac.AlertTypeBurnRateFast || alert.Spec.Type == oac.AlertTypeBurnRateSlow) &&
		alert.Spec.SLORef != nil {
		ref := &oac.DynatraceSLO{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: alert.Namespace, Name: *alert.Spec.SLORef}, ref); err != nil {
			return ctrl.Result{RequeueAfter: requeueAfterError}, fmt.Errorf("resolve SLORef %s: %w", *alert.Spec.SLORef, err)
		}
		sloID = ref.Status.DynatraceID
		if sloID == "" {
			// SLO not yet synced — requeue and wait
			log.Info("SLORef not yet synced, requeuing", "sloRef", *alert.Spec.SLORef)
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
	}

	payload := r.buildAlertPayload(alert, mz, sloID)

	dtID, err := dt.ApplyMetricEvent(ctx, alert.Status.DynatraceID, payload)
	if err != nil {
		log.Error(err, "Failed to apply alert to Dynatrace")
		return r.setAlertFailed(ctx, alert, "DynatraceAPI", err)
	}

	now := metav1.NewTime(time.Now())
	patch := client.MergeFrom(alert.DeepCopy())
	alert.Status.DynatraceID = dtID
	alert.Status.LastSyncTime = &now
	alert.Status.ObservedGeneration = alert.Generation
	meta.SetStatusCondition(&alert.Status.Conditions, metav1.Condition{
		Type:               string(oac.ConditionSynced),
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            fmt.Sprintf("Alert synced to Dynatrace (ID: %s)", dtID),
		ObservedGeneration: alert.Generation,
	})

	if err := r.Status().Patch(ctx, alert, patch); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Alert synced", "dynatraceId", dtID, "type", alert.Spec.Type)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *DynatraceAlertReconciler) buildAlertPayload(alert *oac.DynatraceAlert, mz, sloID string) dtclient.MetricEventPayload {
	serviceName := alert.Spec.ServiceSelector.BackstageId
	if serviceName == "" {
		serviceName = alert.Spec.ServiceSelector.ServiceName
	}

	var (
		metricKey   string
		aggregation string
		eventType   string
		titleSuffix string
	)

	switch alert.Spec.Type {
	case oac.AlertTypeErrorRate:
		metricKey = "builtin:service.errors.total.rate"
		aggregation = "AVG"
		eventType = "ERROR_EVENT"
		titleSuffix = fmt.Sprintf("error rate > %.1f%%", alert.Spec.Threshold)
	case oac.AlertTypeLatencyP99:
		metricKey = "builtin:service.response.time"
		aggregation = "PERCENTILE_99"
		eventType = "SLOWDOWN_EVENT"
		titleSuffix = fmt.Sprintf("p99 latency > %.0fms", alert.Spec.Threshold)
	case oac.AlertTypeBurnRateFast:
		metricKey = "ext:slo.errorBudgetBurnRate"
		aggregation = "AVG"
		eventType = "CUSTOM_ALERT"
		titleSuffix = fmt.Sprintf("%.0f× burn rate (1h window)", alert.Spec.Threshold)
	case oac.AlertTypeBurnRateSlow:
		metricKey = "ext:slo.errorBudgetBurnRate"
		aggregation = "AVG"
		eventType = "CUSTOM_ALERT"
		titleSuffix = fmt.Sprintf("%.0f× burn rate (6h window)", alert.Spec.Threshold)
	}

	name := fmt.Sprintf("%s — %s (%s)", serviceName, alert.Spec.Type, alert.Spec.Environment)

	// Burn rate alerts get more samples (wider window)
	samples := 5
	violating := 3
	if alert.Spec.Type == oac.AlertTypeBurnRateSlow {
		samples = 18  // 6h at 20min evaluation intervals
		violating = 1
	}

	payload := dtclient.MetricEventPayload{
		Name:    name,
		Enabled: true,
		Type:    "STATIC_THRESHOLD",
		QueryDef: dtclient.MetricEventQueryDef{
			Type:        "METRIC_KEY",
			MetricKey:   metricKey,
			Aggregation: aggregation,
		},
		ModelProps: dtclient.MetricEventModel{
			Type:              "STATIC_THRESHOLD",
			Threshold:         alert.Spec.Threshold,
			AlertCondition:    "ABOVE",
			ViolatingSamples:  violating,
			Samples:           samples,
			DealertingSamples: samples,
		},
		EventTpl: dtclient.MetricEventTemplate{
			Title:       fmt.Sprintf("%s: %s", serviceName, titleSuffix),
			Description: fmt.Sprintf("Alert for %s in %s. SLO: %s", serviceName, mz, sloID),
			EventType:   eventType,
			DavisMerge:  true,
		},
		AlertScope: []dtclient.AlertScope{
			{FilterType: "MANAGEMENT_ZONE", ManagementZone: &dtclient.MZRef{ID: mz}},
		},
	}

	return payload
}

func (r *DynatraceAlertReconciler) reconcileDelete(ctx context.Context, log logr.Logger, alert *oac.DynatraceAlert) (ctrl.Result, error) {
	if alert.Status.DynatraceID != "" {
		dt, ok := r.DTClients[alert.Spec.Environment]
		if ok {
			if err := dt.DeleteMetricEvent(ctx, alert.Status.DynatraceID); err != nil {
				log.Error(err, "Failed to delete alert from Dynatrace")
				return ctrl.Result{RequeueAfter: requeueAfterError}, err
			}
		}
	}
	controllerutil.RemoveFinalizer(alert, alertFinalizer)
	return ctrl.Result{}, r.Update(ctx, alert)
}

func (r *DynatraceAlertReconciler) setAlertFailed(ctx context.Context, alert *oac.DynatraceAlert, reason string, err error) (ctrl.Result, error) {
	patch := client.MergeFrom(alert.DeepCopy())
	meta.SetStatusCondition(&alert.Status.Conditions, metav1.Condition{
		Type:    string(oac.ConditionSynced),
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
	_ = r.Status().Patch(ctx, alert, patch)
	return ctrl.Result{RequeueAfter: requeueAfterError}, err
}

func (r *DynatraceAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&oac.DynatraceAlert{}).
		Complete(r)
}
