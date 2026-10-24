package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
// release (matched by release name), or "Deployment" / "DaemonSet".
func (inv Inventory) Has(kind, namespace, name string) bool {
	return inv.objects[objectKey(kind, namespace, name)]
}

func objectKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

// LoadInventory reads the Helm release records and the Deployments and
// DaemonSets of every namespace. Only metadata is requested, so Helm release
// payloads stay on the server.
func LoadInventory(ctx context.Context, cfg *rest.Config) (Inventory, error) {
	mc, err := metadata.NewForConfig(cfg)
	if err != nil {
		return Inventory{}, err
	}
	lists := []struct {
		kind     string
		gvr      schema.GroupVersionResource
		selector string
	}{
		// Helm 3 stores each release revision in a secret labeled owner=helm
		// and name=<release>.
		{"HelmRelease", corev1.SchemeGroupVersion.WithResource("secrets"), "owner=helm"},
		{"Deployment", appsv1.SchemeGroupVersion.WithResource("deployments"), ""},
		{"DaemonSet", appsv1.SchemeGroupVersion.WithResource("daemonsets"), ""},
	}
	inv := Inventory{objects: map[string]bool{}}
	for _, l := range lists {
		items, err := mc.Resource(l.gvr).List(ctx, metav1.ListOptions{LabelSelector: l.selector})
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
