package webhook

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// The paths the API server calls; they match the clientConfig.service paths in
// the ValidatingWebhookConfiguration.
const (
	VirtualClusterPath = "/validate-kubeyard-wutz-dev-v1alpha1-virtualcluster"
	NodePoolPath       = "/validate-kubeyard-wutz-dev-v1alpha1-nodepool"
)

// Options configures the validating webhook server.
type Options struct {
	// CertDir holds the serving certificate; it is created if it is empty.
	CertDir string
	// Port is the HTTPS port the API server calls.
	Port int
	// Service is the operator Service in the form svc.namespace. The certificate
	// is issued for its in-cluster names.
	Service string
	// Host is the address the server listens on. Empty listens on every
	// interface, which is what a Service-backed deployment needs; a local run
	// against a k3d cluster sets it to the wildcard too, because the API server
	// reaches the host from inside the k3d network.
	Host string
}

// CertificateNames is the DNS names the serving certificate must cover.
func (o Options) CertificateNames() []string {
	return []string{o.Service + ".svc", o.Service + ".svc.cluster.local", "localhost", LocalName}
}

// NewServer issues the serving certificate and returns the webhook server for
// ctrl.Options.WebhookServer and the CA bundle its certificate chains to. It runs
// before the manager starts, so the certificate exists before the API server's
// first call.
//
// reader must read the API directly rather than from a cache: a validation made
// on a stale cache could let two claims overcommit a pool.
func NewServer(scheme *runtime.Scheme, reader client.Reader, o Options) (webhook.Server, []byte, error) {
	if o.Port == 0 {
		o.Port = 9443
	}
	ca, err := EnsureCert(o.CertDir, o.CertificateNames(), time.Now())
	if err != nil {
		return nil, nil, fmt.Errorf("issue the webhook certificate: %w", err)
	}
	srv := webhook.NewServer(webhook.Options{Host: o.Host, Port: o.Port, CertDir: o.CertDir})
	srv.Register(VirtualClusterPath, &webhook.Admission{Handler: admission.WithCustomValidator(
		scheme, &v1alpha1.VirtualCluster{}, &VirtualClusterValidator{Reader: reader})})
	srv.Register(NodePoolPath, &webhook.Admission{Handler: admission.WithCustomValidator(
		scheme, &v1alpha1.NodePool{}, &NodePoolValidator{Reader: reader})})
	return srv, ca, nil
}
