// Command kubeyard-operator runs the Kubeyard operator. Host mode reconciles
// NodePools and NodeLeases on the host cluster (docs/operator.md §1 to §3) and
// serves the validating webhook of §3.2 and §7. Vcluster mode, which will own
// Workspaces inside a virtual cluster, is not implemented yet.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/operator/host"
	"github.com/wutz/kubeyard/pkg/webhook"
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
	serveWebhook := fs.Bool("webhook", true, "serve the validating webhook (docs/operator.md §3.2, §7)")
	webhookPort := fs.Int("webhook-port", 9443, "HTTPS port of the validating webhook")
	webhookCertDir := fs.String("webhook-cert-dir", filepath.Join(os.TempDir(), "kubeyard-webhook-certs"),
		"directory of the webhook serving certificate; an empty one is filled with a self-signed certificate")
	webhookHost := fs.String("webhook-host", "", "address the webhook listens on; empty binds all IPv4 and IPv6 addresses")
	webhookService := fs.String("webhook-service", "kubeyard-operator.kubeyard-system",
		"Service (name.namespace) the API server calls; the certificate is issued for its in-cluster names")
	webhookConfig := fs.String("webhook-config", "kubeyard-validating",
		"ValidatingWebhookConfiguration whose caBundle is patched with the generated CA")
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
	// The validators and the caBundle patch read the API directly instead of
	// through the manager's cache: an admission decision must see what is stored
	// now, not what this process last observed.
	direct, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	opts := ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   *leaderElect,
		LeaderElectionID: "kubeyard-operator-host",
		// Metrics come with the metering work (M4), so the endpoint stays off for now.
		Metrics: metricsserver.Options{BindAddress: "0"},
	}
	var caPEM []byte
	if *serveWebhook {
		wo := webhook.Options{
			Host:    *webhookHost,
			CertDir: *webhookCertDir,
			Port:    *webhookPort,
			Service: *webhookService,
		}
		srv, ca, err := webhook.NewServer(scheme, direct, wo)
		if err != nil {
			return err
		}
		opts.WebhookServer, caPEM = srv, ca
	}
	mgr, err := ctrl.NewManager(cfg, opts)
	if err != nil {
		return err
	}
	if *serveWebhook {
		// The manager only registers its webhook server on the first call to
		// GetWebhookServer, so ask once; without it the server never starts.
		mgr.GetWebhookServer()
	}
	r := &host.Reconciler{Client: mgr.GetClient()}
	if err := r.SetupWithManager(mgr); err != nil {
		return err
	}
	if *serveWebhook {
		// Patch before the manager starts, so the API server trusts the
		// certificate by the time the first request arrives.
		if err := webhook.PatchCABundle(context.Background(), direct, *webhookConfig, caPEM); err != nil {
			return err
		}
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}
