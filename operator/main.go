// main.go — Dynatrace operator entry point.
// Registers all controllers with the controller-runtime manager and starts
// the reconciliation loops.
package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	oac "github.com/YOUR_ORG/dynatrace-operator/api/v1alpha1"
	"github.com/YOUR_ORG/dynatrace-operator/controllers"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(oac.AddToScheme(scheme)) // registers all 4 CRD types
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		operatorNamespace    string
		enableLeaderElection bool
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics endpoint address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe address")
	flag.StringVar(&operatorNamespace, "namespace", "sre-tools", "Namespace containing DT credential Secrets")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Enable leader election for HA")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "dynatrace-operator.YOUR_ORG.io",
	})
	if err != nil {
		setupLog.Error(err, "Unable to start manager")
		os.Exit(1)
	}

	// Build one DT API client per environment from ExternalSecrets-managed Secrets
	dtClients, err := controllers.BuildDTClients(context.Background(), mgr.GetClient(), operatorNamespace)
	if err != nil {
		setupLog.Error(err, "Unable to build Dynatrace clients")
		os.Exit(1)
	}
	setupLog.Info("Dynatrace clients initialised", "environments", len(dtClients))

	// Register SLO controller
	if err := (&controllers.DynatraceSLOReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Log:       ctrl.Log.WithName("controllers").WithName("DynatraceSLO"),
		DTClients: dtClients,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Unable to create DynatraceSLO controller")
		os.Exit(1)
	}

	// Register Alert controller
	if err := (&controllers.DynatraceAlertReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Log:       ctrl.Log.WithName("controllers").WithName("DynatraceAlert"),
		DTClients: dtClients,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Unable to create DynatraceAlert controller")
		os.Exit(1)
	}

	// Health checks — required for k8s liveness/readiness probes
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting Dynatrace operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Manager exited with error")
		os.Exit(1)
	}
}
