// Command kubeyard-operator runs the Kubeyard operator. Host mode reconciles
// NodePools and NodeLeases on the host cluster (docs/operator.md §1 to §3).
// Vcluster mode, which will own Workspaces inside a virtual cluster, is not
// implemented yet.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/operator/host"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run parses the flags and runs the manager for the chosen mode until it stops.
func run(args []string) error {
	fs := flag.NewFlagSet("kubeyard-operator", flag.ContinueOnError)
	mode := fs.String("mode", "host", "host reconciles NodePools on this cluster; vcluster is not implemented")
	leaderElect := fs.Bool("leader-elect", true, "elect a leader before running the controllers")
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch *mode {
	case "host":
	case "vcluster":
		return errors.New("mode vcluster is not implemented yet")
	default:
		return fmt.Errorf("unknown mode %q: want host or vcluster", *mode)
	}

	ctrl.SetLogger(zap.New())
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return err
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return err
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   *leaderElect,
		LeaderElectionID: "kubeyard-operator-host",
		// Metrics come with the metering work (M4), so the endpoint stays off for now.
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		return err
	}
	r := &host.Reconciler{Client: mgr.GetClient()}
	if err := r.SetupWithManager(mgr); err != nil {
		return err
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}
