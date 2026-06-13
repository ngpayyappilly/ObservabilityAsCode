// Package v1alpha1 contains API Schema definitions for the oac.YOUR_ORG.io group.
// +kubebuilder:object:generate=true
// +groupName=oac.YOUR_ORG.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the API group + version for all Dynatrace CRDs.
	GroupVersion = schema.GroupVersion{Group: "oac.YOUR_ORG.io", Version: "v1alpha1"}

	// SchemeBuilder registers the Go types with the k8s runtime scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds all types in this package to a runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(
		&DynatraceSLO{},
		&DynatraceSLOList{},
		&DynatraceAlert{},
		&DynatraceAlertList{},
		&DynatraceDashboard{},
		&DynatraceDashboardList{},
		&DynatraceNotification{},
		&DynatraceNotificationList{},
	)
}
