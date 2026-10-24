package kube

import (
	"context"
	"errors"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// FluxOperatorRelease is the Helm release of the Flux Operator chart.
	FluxOperatorRelease = "flux-operator"
	// FluxInstallTimeout bounds the operator's Helm install, which waits for the
	// operator to be ready, and the FluxInstance creation that follows.
	FluxInstallTimeout = 10 * time.Minute

	fluxOperatorChart   = "oci://ghcr.io/controlplaneio-fluxcd/charts/flux-operator"
	fluxOperatorVersion = "0.61.0"
	fluxInstanceName    = "flux"
)

// ErrFluxPresent means Flux or its operator already exists. Kubeyard does not
// take over an installation made another way.
var ErrFluxPresent = errors.New("Flux 已存在，Kubeyard 不接管已有安装")

var fluxInstances = schema.GroupVersionResource{
	Group:    "fluxcd.controlplane.io",
	Version:  "v1",
	Resource: "fluxinstances",
}

// InstallFlux installs Flux the way the Flux Operator documents it: the
// operator's Helm chart from its OCI registry, then the FluxInstance
// flux-system/flux that deploys the Flux controllers. Helm waits for the
// operator to be ready before the FluxInstance is created. It refuses when
// Flux is present; see checkFluxAbsent.
func InstallFlux(ctx context.Context, contextName string, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
	inv, err := LoadInventory(ctx, cfg)
	if err != nil {
		return err
	}
	if err := checkFluxAbsent(ctx, dc, inv); err != nil {
		return err
	}
	if err := ensureNamespace(ctx, cs, FluxNamespace); err != nil {
		return err
	}
	if err := installOperator(ctx, contextName); err != nil {
		return err
	}
	return createFluxInstance(ctx, dc)
}

// checkFluxAbsent refuses when Flux controllers, the operator release or a
// FluxInstance already exist. Creating over any of them would take them over.
func checkFluxAbsent(ctx context.Context, dc dynamic.Interface, inv Inventory) error {
	if !inv.FluxInstallable() {
		return ErrFluxPresent
	}
	_, err := dc.Resource(fluxInstances).Namespace(FluxNamespace).Get(ctx, fluxInstanceName, metav1.GetOptions{})
	if err == nil {
		return ErrFluxPresent
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// installOperator installs the Flux Operator chart into FluxNamespace with
// Helm, pulling the chart from its OCI registry. The release is created in
// the same way helm install does, and Helm waits for the operator to be ready.
func installOperator(ctx context.Context, contextName string) error {
	flags := genericclioptions.NewConfigFlags(false)
	flags.Context = &contextName
	ns := FluxNamespace
	flags.Namespace = &ns

	cfg := new(action.Configuration)
	if err := cfg.Init(flags, FluxNamespace, "secret", func(string, ...any) {}); err != nil {
		return err
	}
	reg, err := registry.NewClient()
	if err != nil {
		return err
	}
	cfg.RegistryClient = reg

	install := action.NewInstall(cfg)
	install.ReleaseName = FluxOperatorRelease
	install.Namespace = FluxNamespace
	install.Version = fluxOperatorVersion
	install.Wait = true
	install.Timeout = FluxInstallTimeout

	chartPath, err := install.LocateChart(fluxOperatorChart, cli.New())
	if err != nil {
		return err
	}
	chart, err := loader.Load(chartPath)
	if err != nil {
		return err
	}
	_, err = install.RunWithContext(ctx, chart, nil)
	return err
}

// createFluxInstance creates the FluxInstance that deploys Flux. It uses Create
// rather than an upsert, so an existing FluxInstance is never modified.
func createFluxInstance(ctx context.Context, dc dynamic.Interface) error {
	_, err := dc.Resource(fluxInstances).Namespace(FluxNamespace).Create(ctx, fluxInstance(), metav1.CreateOptions{})
	return err
}

// fluxInstance is the FluxInstance Kubeyard creates. It is the configuration
// of the bz1-dev1 cluster: medium size, the four core controllers, Flux 2.x
// from the upstream distribution.
func fluxInstance() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "fluxcd.controlplane.io/v1",
		"kind":       "FluxInstance",
		"metadata": map[string]any{
			"name":      fluxInstanceName,
			"namespace": FluxNamespace,
		},
		"spec": map[string]any{
			"distribution": map[string]any{
				"registry": "ghcr.io/fluxcd",
				"variant":  "upstream-alpine",
				"version":  "2.x",
			},
			"cluster": map[string]any{
				"type":                        "kubernetes",
				"size":                        "medium",
				"domain":                      "cluster.local",
				"multitenant":                 false,
				"multitenantWorkloadIdentity": false,
				"networkPolicy":               true,
			},
			"components": []any{
				"source-controller",
				"kustomize-controller",
				"helm-controller",
				"notification-controller",
			},
			"migrateResources": true,
			"wait":             true,
		},
	}}
}
