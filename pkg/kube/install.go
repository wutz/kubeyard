package kube

import (
	"context"

	"github.com/wutz/kubeyard/pkg/catalog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

const (
	// CatalogNamespace holds the catalog source, and the Kustomizations and
	// parameter ConfigMaps of installed addons.
	CatalogNamespace = catalog.ManagedNamespace
	// CatalogSource is the OCIRepository the addon Kustomizations pull from.
	CatalogSource = "catalog-default"
	// addonLabel names the addon an object belongs to.
	addonLabel = "kubeyard.wutz.dev/addon"
)

var kustomizations = schema.GroupVersionResource{
	Group:    "kustomize.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "kustomizations",
}

// Addon is a request to install one catalog addon.
type Addon struct {
	// Name is the addon's directory in the catalog.
	Name string
	// Namespace is where the addon installs. Manifests read it as addon_namespace.
	Namespace string
	// DependsOn lists addons whose Kustomizations must be ready first.
	DependsOn []string
	// Values is the Helm values document, stored in the parameter ConfigMap.
	Values map[string]any
	// Substitute holds the postBuild variables the manifests read.
	Substitute map[string]string
}

// Install creates or updates the addon's parameter ConfigMap and Kustomization.
// Flux then applies the catalog manifests. Install does not check whether the
// addon is already present; the caller must, with catalog.Addon.Installable.
func Install(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, a Addon) error {
	values, err := yaml.Marshal(a.Values)
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      paramsName(a.Name),
			Namespace: CatalogNamespace,
			Labels:    map[string]string{addonLabel: a.Name},
		},
		Data: map[string]string{"values.yaml": string(values)},
	}
	if err := upsertConfigMap(ctx, cs, cm); err != nil {
		return err
	}
	return upsertObject(ctx, dc.Resource(kustomizations).Namespace(CatalogNamespace), kustomization(a))
}

// Uninstall deletes the addon's Kustomization. Flux prunes what it applied,
// which removes the HelmRelease and with it the Helm release. Then the
// parameter ConfigMap is deleted. Missing objects are not an error.
func Uninstall(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, name string) error {
	err := dc.Resource(kustomizations).Namespace(CatalogNamespace).Delete(ctx, kustomizationName(name), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	err = cs.CoreV1().ConfigMaps(CatalogNamespace).Delete(ctx, paramsName(name), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// LoadAddonParams reads the values an addon was installed with: the Helm values
// from its parameter ConfigMap, and the postBuild substitutions from its
// Kustomization. Nothing stored yields nil values and no error.
func LoadAddonParams(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, addon string) (map[string]any, map[string]string, error) {
	var helm map[string]any
	cm, err := cs.CoreV1().ConfigMaps(CatalogNamespace).Get(ctx, paramsName(addon), metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return nil, nil, err
	default:
		if err := yaml.Unmarshal([]byte(cm.Data["values.yaml"]), &helm); err != nil {
			return nil, nil, err
		}
	}
	kz, err := dc.Resource(kustomizations).Namespace(CatalogNamespace).Get(ctx, kustomizationName(addon), metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return helm, nil, nil
	case err != nil:
		return nil, nil, err
	}
	substitute, _, err := unstructured.NestedStringMap(kz.Object, "spec", "postBuild", "substitute")
	return helm, substitute, err
}

func kustomizationName(addon string) string { return "addon-" + addon }

func paramsName(addon string) string { return "addon-" + addon + "-params" }

// kustomization builds the Flux Kustomization for an addon. Values are plain
// maps and strings, which unstructured can deep-copy.
func kustomization(a Addon) *unstructured.Unstructured {
	dependsOn := make([]any, 0, len(a.DependsOn))
	for _, dep := range a.DependsOn {
		dependsOn = append(dependsOn, map[string]any{"name": kustomizationName(dep)})
	}
	substitute := map[string]any{"addon_namespace": a.Namespace}
	for k, v := range a.Substitute {
		substitute[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
		"kind":       "Kustomization",
		"metadata": map[string]any{
			"name":      kustomizationName(a.Name),
			"namespace": CatalogNamespace,
			"labels":    map[string]any{addonLabel: a.Name},
		},
		"spec": map[string]any{
			"interval":  "10m",
			"timeout":   "15m",
			"path":      "./addons/" + a.Name + "/manifests",
			"prune":     true,
			"wait":      true,
			"sourceRef": map[string]any{"kind": "OCIRepository", "name": CatalogSource},
			"dependsOn": dependsOn,
			"postBuild": map[string]any{"substitute": substitute},
		},
	}}
}

func upsertConfigMap(ctx context.Context, cs kubernetes.Interface, cm *corev1.ConfigMap) error {
	client := cs.CoreV1().ConfigMaps(cm.Namespace)
	cur, err := client.Get(ctx, cm.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, cm, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	cur.Labels = cm.Labels
	cur.Data = cm.Data
	_, err = client.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// upsertObject creates obj, or updates the existing object of the same name.
func upsertObject(ctx context.Context, client dynamic.ResourceInterface, obj *unstructured.Unstructured) error {
	cur, err := client.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	obj.SetResourceVersion(cur.GetResourceVersion())
	_, err = client.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}
