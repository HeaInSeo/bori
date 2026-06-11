// bori-operator is the Kubernetes controller-runtime based operator for bori.
//
// It watches BoriDataPlane custom resources and reconciles the desired state
// (spec.release + spec.environment) by running the same plan→deploy→verify→promote
// cycle as the bori CLI, then patching .status.conditions on the CR.
//
// Usage:
//
//	bori-operator \
//	  --bori-root /bori \
//	  --bori-dir /bori/.bori \
//	  --apps-dir /apps \
//	  --leader-elect \
//	  --requeue-interval 30s
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	devspaceadapter "github.com/HeaInSeo/bori/adapters/devspace"
	imageswapAdp "github.com/HeaInSeo/bori/adapters/imageswap"
	koadapter "github.com/HeaInSeo/bori/adapters/ko"
	kubeapplyadapter "github.com/HeaInSeo/bori/adapters/kubeapply"
	shelladapter "github.com/HeaInSeo/bori/adapters/shell"
	v1alpha1 "github.com/HeaInSeo/bori/apis/bori/v1alpha1"
	"github.com/HeaInSeo/bori/controllers"
	"github.com/HeaInSeo/bori/pkg/adapter"
	reconcilepkg "github.com/HeaInSeo/bori/pkg/reconcile"
)

func main() {
	var (
		boriRoot        string
		boriDir         string
		appsDir         string
		metricsAddr     string
		healthAddr      string
		leaderElect     bool
		requeueInterval time.Duration
		deployDryRun    bool
	)

	flag.StringVar(&boriRoot, "bori-root", "/bori",
		"path to bori repo root (releases/, components/, environments/)")
	flag.StringVar(&boriDir, "bori-dir", "/bori/.bori",
		"bori state directory for run archives and shadow state")
	flag.StringVar(&appsDir, "apps-dir", "",
		"directory containing app repos (default: parent of bori-root)")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"bind address for the metrics server")
	flag.StringVar(&healthAddr, "health-probe-bind-address", ":8081",
		"bind address for health probes")
	flag.BoolVar(&leaderElect, "leader-elect", false,
		"enable leader election (required for multi-replica deployments)")
	flag.DurationVar(&requeueInterval, "requeue-interval", 30*time.Second,
		"how often to re-evaluate each BoriDataPlane")
	flag.BoolVar(&deployDryRun, "deploy-dry-run", false,
		"skip adapter.Deploy() calls but promote revisions (for kind-based digest smoke tests)")

	zapOpts := zap.Options{Development: true}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	setupLog := ctrl.Log.WithName("setup")

	if appsDir == "" {
		abs, err := filepath.Abs(boriRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "abs bori-root: %v\n", err)
			os.Exit(1)
		}
		appsDir = filepath.Join(abs, "..")
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "add client-go scheme")
		os.Exit(1)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "add v1alpha1 scheme")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress:  healthAddr,
		LeaderElection:          leaderElect,
		LeaderElectionID:        "bori-operator-leader.bori.dev",
		LeaderElectionNamespace: "bori-system",
	})
	if err != nil {
		setupLog.Error(err, "create manager")
		os.Exit(1)
	}

	// Build the bori reconciler with the full adapter registry.
	logf := func(f string, a ...any) {
		ctrl.Log.WithName("reconciler").Info(fmt.Sprintf(f, a...))
	}
	runner := reconcilepkg.NewReconciler(appsDir, logf)
	// kustomize and manifest are backed by in-process SSA in the operator
	// (distroless image has no kubectl). The CLI uses the kubectl-backed adapters.
	kubeApply := kubeapplyadapter.New(mgr.GetClient())
	runner.AdapterRegistry = map[string]adapter.DeployAdapter{
		"devspace":  devspaceadapter.New(appsDir),
		"imageswap": imageswapAdp.New(),
		"ko":        koadapter.New(appsDir),
		"kustomize": kubeApply,
		"manifest":  kubeApply,
		"shell":     shelladapter.New(appsDir),
	}

	if err := (&controllers.DataPlaneReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorderFor("bori-operator"), //nolint:staticcheck
		BoriRoot:        boriRoot,
		BoriDir:         boriDir,
		AppsDir:         appsDir,
		Runner:          runner,
		DeployDryRun:    deployDryRun,
		RequeueInterval: requeueInterval,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "setup DataPlaneReconciler")
		os.Exit(1)
	}

	if err := (&controllers.BoriReleaseReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "setup BoriReleaseReconciler")
		os.Exit(1)
	}

	if err := (&controllers.BoriVerificationRunReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "setup BoriVerificationRunReconciler")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "add healthz check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "add readyz check")
		os.Exit(1)
	}

	setupLog.Info("starting bori-operator",
		"bori-root", boriRoot,
		"requeue-interval", requeueInterval,
		"leader-elect", leaderElect,
	)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "start manager")
		os.Exit(1)
	}
}
