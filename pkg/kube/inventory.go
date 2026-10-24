package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
)

// Inventory is the set of Helm releases and workloads in a cluster. The GUI
// compares it with the catalog to tell which components are already there.
type Inventory struct {
	objects map[string]bool
}

// Has reports whether an object exists. Kind is "HelmRelease" for a Helm
// release (matched by release name), "Deployment" / "DaemonSet", or
// "Kustomization" for an addon Kubeyard installed.
func (inv Inventory) Has(kind, namespace, name string) bool {
	return inv.objects[objectKey(kind, namespace, name)]
}

// Managed reports whether Kubeyard installed the addon, which is when its
// Kustomization exists.
func (inv Inventory) Managed(addon string) bool {
	return inv.Has("Kustomization", CatalogNamespace, kustomizationName(addon))
}

func objectKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

// LoadInventory reads the Helm release records, the Deployments and DaemonSets
// of every namespace, and the addon Kustomizations Kubeyard created. Only
// metadata is requested, so Helm release payloads stay on the server.
func LoadInventory(ctx context.Context, cfg *rest.Config) (Inventory, error) {
	mc, err := metadata.NewForConfig(cfg)
	if err != nil {
		return Inventory{}, err
	}
	lists := []struct {
		kind     string
		gvr      schema.GroupVersionResource
		selector string
		// optional lists are skipped when their CRD is not installed.
		optional bool
	}{
		// Helm 3 stores each release revision in a secret labeled owner=helm
		// and name=<release>.
		{"HelmRelease", corev1.SchemeGroupVersion.WithResource("secrets"), "owner=helm", false},
		{"Deployment", appsv1.SchemeGroupVersion.WithResource("deployments"), "", false},
		{"DaemonSet", appsv1.SchemeGroupVersion.WithResource("daemonsets"), "", false},
		{"Kustomization", kustomizations, addonLabel, true},
	}
	inv := Inventory{objects: map[string]bool{}}
	for _, l := range lists {
		items, err := mc.Resource(l.gvr).List(ctx, metav1.ListOptions{LabelSelector: l.selector})
		if apierrors.IsNotFound(err) && l.optional {
			continue
		}
		if err != nil {
			return Inventory{}, fmt.Errorf("list %s: %w", l.kind, err)
		}
		for _, item := range items.Items {
			name := item.Name
			if l.kind == "HelmRelease" {
				name = item.Labels["name"]
			}
			inv.objects[objectKey(l.kind, item.Namespace, name)] = true
		}
	}
	return inv, nil
}
