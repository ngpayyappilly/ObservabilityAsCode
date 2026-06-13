// Package v1alpha1 defines the Dynatrace operator API types.
// These CRDs are the public contract for app teams — keep them stable.
// Internal translation to DT API payloads lives in internal/dynatrace/.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ────────────────────────────────────────────────────────────────────────────
// Shared types
// ────────────────────────────────────────────────────────────────────────────

// Environment identifies which Dynatrace tenant to target.
// +kubebuilder:validation:Enum=dev;staging;perf;prod
type Environment string

const (
	EnvDev     Environment = "dev"
	EnvStaging Environment = "staging"
	EnvPerf    Environment = "perf"
	EnvProd    Environment = "prod"
)

// ServiceSelector identifies the Dynatrace service entity.
// At least one field must be set. BackstageId is preferred —
// the controller resolves it to a DT SERVICE entity automatically.
type ServiceSelector struct {
	// BackstageId is the Backstage catalog entity name (metadata.name).
	// The controller looks up the k8s label backstage.io/kubernetes-id
	// on running pods to find the matching DT SERVICE entity.
	// +optional
	BackstageId string `json:"backstageId,omitempty"`

	// ServiceName is the Dynatrace-detected service name (exact match).
	// Use this if the service name in DT differs from the Backstage ID.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// ManagementZone scopes the entity search to a specific MZ.
	// Defaults to env:<environment> if not set.
	// +optional
	ManagementZone string `json:"managementZone,omitempty"`
}

// ConditionType represents the type of a status condition.
type ConditionType string

const (
	ConditionSynced ConditionType = "Synced" // resource exists in Dynatrace
	ConditionReady  ConditionType = "Ready"  // resource is active and evaluated
)

// ────────────────────────────────────────────────────────────────────────────
// DynatraceSLO
// ────────────────────────────────────────────────────────────────────────────

// SLOType defines the SLO measurement strategy.
// +kubebuilder:validation:Enum=availability;latency
type SLOType string

const (
	SLOTypeAvailability SLOType = "availability"
	SLOTypeLatency      SLOType = "latency"
)

// DynatraceSLOSpec defines the desired state of a Dynatrace SLO.
type DynatraceSLOSpec struct {
	// Environment is the target Dynatrace tenant.
	// +kubebuilder:validation:Required
	Environment Environment `json:"environment"`

	// ServiceSelector identifies which DT service this SLO applies to.
	// +kubebuilder:validation:Required
	ServiceSelector ServiceSelector `json:"serviceSelector"`

	// Type is the SLO measurement strategy.
	// +kubebuilder:validation:Required
	Type SLOType `json:"type"`

	// Target is the SLO target percentage (e.g. 99.9).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Target float64 `json:"target"`

	// Warning is the SLO warning threshold. Must be lower than Target.
	// Defaults to Target - 0.5 if not set.
	// +optional
	Warning *float64 `json:"warning,omitempty"`

	// Window is the evaluation timeframe (e.g. "-1w", "-30d").
	// +kubebuilder:default="-1w"
	Window string `json:"window,omitempty"`

	// LatencyThresholdMs is the p99 latency threshold in milliseconds.
	// Required when Type is "latency".
	// +optional
	LatencyThresholdMs *int `json:"latencyThresholdMs,omitempty"`
}

// DynatraceSLOStatus defines the observed state of a Dynatrace SLO.
type DynatraceSLOStatus struct {
	// DynatraceID is the SLO ID assigned by Dynatrace on creation.
	// Used for updates and deletes.
	// +optional
	DynatraceID string `json:"dynatraceId,omitempty"`

	// CurrentValue is the most recently observed SLO value (%).
	// +optional
	CurrentValue *float64 `json:"currentValue,omitempty"`

	// ErrorBudgetRemaining is the remaining error budget in minutes.
	// +optional
	ErrorBudgetRemaining *float64 `json:"errorBudgetRemaining,omitempty"`

	// LastSyncTime is when the controller last successfully synced to DT.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions holds the current reconciliation status.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the .metadata.generation this status was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=dtslo
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Target",type=number,JSONPath=`.spec.target`
// +kubebuilder:printcolumn:name="Current",type=number,JSONPath=`.status.currentValue`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DynatraceSLO is the Schema for DynatraceSLOs.
type DynatraceSLO struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DynatraceSLOSpec   `json:"spec,omitempty"`
	Status DynatraceSLOStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DynatraceSLOList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DynatraceSLO `json:"items"`
}

// ────────────────────────────────────────────────────────────────────────────
// DynatraceAlert
// ────────────────────────────────────────────────────────────────────────────

// AlertType defines what the alert monitors.
// +kubebuilder:validation:Enum=errorRate;latencyP99;burnRateFast;burnRateSlow
type AlertType string

const (
	AlertTypeErrorRate    AlertType = "errorRate"
	AlertTypeLatencyP99   AlertType = "latencyP99"
	AlertTypeBurnRateFast AlertType = "burnRateFast" // 1h window
	AlertTypeBurnRateSlow AlertType = "burnRateSlow" // 6h window
)

// DynatraceAlertSpec defines the desired state of a Dynatrace metric event alert.
type DynatraceAlertSpec struct {
	// Environment is the target Dynatrace tenant.
	// +kubebuilder:validation:Required
	Environment Environment `json:"environment"`

	// ServiceSelector identifies the service to alert on.
	// +kubebuilder:validation:Required
	ServiceSelector ServiceSelector `json:"serviceSelector"`

	// Type is the alert strategy.
	// +kubebuilder:validation:Required
	Type AlertType `json:"type"`

	// Threshold is the value at which the alert fires.
	//   errorRate:    percent  (e.g. 0.5 = alert when error rate > 0.5%)
	//   latencyP99:   ms       (e.g. 500 = alert when p99 > 500ms)
	//   burnRateFast: multiplier (e.g. 14 = 14× faster than sustainable)
	//   burnRateSlow: multiplier (e.g. 6)
	// +kubebuilder:validation:Required
	Threshold float64 `json:"threshold"`

	// SLORef links a burn rate alert to a DynatraceSLO in the same namespace.
	// Required when Type is burnRateFast or burnRateSlow.
	// +optional
	SLORef *string `json:"sloRef,omitempty"`

	// AlertingProfileRef names the alerting profile to route this alert to.
	// If empty, the controller uses the default profile for the environment.
	// +optional
	AlertingProfileRef *string `json:"alertingProfileRef,omitempty"`
}

// DynatraceAlertStatus defines the observed state of a Dynatrace alert.
type DynatraceAlertStatus struct {
	// DynatraceID is the metric event ID assigned by Dynatrace.
	// +optional
	DynatraceID string `json:"dynatraceId,omitempty"`

	// LastSyncTime is when the controller last successfully synced to DT.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions holds the current reconciliation status.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration tracks spec version.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=dtalert
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Threshold",type=number,JSONPath=`.spec.threshold`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`

// DynatraceAlert manages a Dynatrace metric event alert.
type DynatraceAlert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DynatraceAlertSpec   `json:"spec,omitempty"`
	Status DynatraceAlertStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DynatraceAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DynatraceAlert `json:"items"`
}

// ────────────────────────────────────────────────────────────────────────────
// DynatraceDashboard
// ────────────────────────────────────────────────────────────────────────────

// DashboardTemplate defines which pre-built dashboard layout to use.
// +kubebuilder:validation:Enum=service-overview;slo-report;endpoint-detail
type DashboardTemplate string

const (
	TemplateServiceOverview DashboardTemplate = "service-overview"
	TemplateSLOReport       DashboardTemplate = "slo-report"
	TemplateEndpointDetail  DashboardTemplate = "endpoint-detail"
)

// DynatraceDashboardSpec defines the desired state of a Dynatrace dashboard.
type DynatraceDashboardSpec struct {
	// Environment is the target Dynatrace tenant.
	// +kubebuilder:validation:Required
	Environment Environment `json:"environment"`

	// ServiceSelector identifies the service this dashboard is for.
	// +kubebuilder:validation:Required
	ServiceSelector ServiceSelector `json:"serviceSelector"`

	// Template selects the pre-built dashboard layout.
	// +kubebuilder:default=service-overview
	Template DashboardTemplate `json:"template,omitempty"`

	// SLORefs lists DynatraceSLO names in the same namespace whose
	// current values are tiled on the dashboard.
	// +optional
	SLORefs []string `json:"sloRefs,omitempty"`

	// Shared controls whether the dashboard is publicly visible in DT.
	// +kubebuilder:default=false
	Shared bool `json:"shared,omitempty"`
}

// DynatraceDashboardStatus defines the observed state of a Dynatrace dashboard.
type DynatraceDashboardStatus struct {
	// DynatraceID is the dashboard ID assigned by Dynatrace.
	// +optional
	DynatraceID string `json:"dynatraceId,omitempty"`

	// DashboardURL is the direct link to the dashboard in the DT UI.
	// +optional
	DashboardURL string `json:"dashboardUrl,omitempty"`

	// LastSyncTime is when the controller last successfully synced.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions holds the current reconciliation status.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration tracks spec version.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=dtdash
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.template`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.dashboardUrl`

// DynatraceDashboard manages a Dynatrace dashboard.
type DynatraceDashboard struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DynatraceDashboardSpec   `json:"spec,omitempty"`
	Status DynatraceDashboardStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DynatraceDashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DynatraceDashboard `json:"items"`
}

// ────────────────────────────────────────────────────────────────────────────
// DynatraceNotification
// ────────────────────────────────────────────────────────────────────────────

// NotificationChannel defines the notification integration type.
// +kubebuilder:validation:Enum=slack;msteams;pagerduty;splunkOncall
type NotificationChannel string

const (
	ChannelSlack       NotificationChannel = "slack"
	ChannelMSTeams     NotificationChannel = "msteams"
	ChannelPagerDuty   NotificationChannel = "pagerduty"
	ChannelSplunkOncall NotificationChannel = "splunkOncall"
)

// DynatraceNotificationSpec defines the desired state of a notification integration.
type DynatraceNotificationSpec struct {
	// Environment is the target Dynatrace tenant.
	// +kubebuilder:validation:Required
	Environment Environment `json:"environment"`

	// AlertingProfileRef is the name of a DynatraceAlertingProfile CR,
	// or a raw DT alerting profile ID from Terraform output.
	// +kubebuilder:validation:Required
	AlertingProfileRef string `json:"alertingProfileRef"`

	// Channel is the notification destination type.
	// +kubebuilder:validation:Required
	Channel NotificationChannel `json:"channel"`

	// SecretRef references a Kubernetes Secret containing channel credentials.
	// The controller reads the required keys per channel type:
	//   slack:        webhook-url
	//   msteams:      webhook-url
	//   pagerduty:    service-key
	//   splunkOncall: routing-key, api-key
	// +kubebuilder:validation:Required
	SecretRef SecretRef `json:"secretRef"`

	// ChannelName is the display name or channel identifier.
	//   slack:   "#alerts-prod"
	//   msteams: "Prod Alerts"
	// +optional
	ChannelName string `json:"channelName,omitempty"`
}

// SecretRef points to a Kubernetes Secret in the same namespace.
type SecretRef struct {
	// Name is the Secret name.
	Name string `json:"name"`
}

// DynatraceNotificationStatus defines the observed state of a notification integration.
type DynatraceNotificationStatus struct {
	// DynatraceID is the notification integration ID in Dynatrace.
	// +optional
	DynatraceID string `json:"dynatraceId,omitempty"`

	// LastSyncTime is when the controller last successfully synced.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions holds the current reconciliation status.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration tracks spec version.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=dtnotif
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Channel",type=string,JSONPath=`.spec.channel`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`

// DynatraceNotification manages a Dynatrace notification integration.
type DynatraceNotification struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DynatraceNotificationSpec   `json:"spec,omitempty"`
	Status DynatraceNotificationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DynatraceNotificationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DynatraceNotification `json:"items"`
}
