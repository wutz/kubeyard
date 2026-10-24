package kube

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func testAddon() Addon {
	return Addon{
		Name:      "metallb",
		Namespace: "metallb-system",
		DependsOn: []string{"nfd"},
		Values:    map[string]any{"driver": map[string]any{"enabled": true}},
		Substitute: map[string]string{
			"metallb_addresses": "192.0.2.10-192.0.2.20",
		},
	}
}

func TestInstallWritesParamsAndKustomization(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := Install(ctx, cs, dc, testAddon()); err != nil {
		t.Fatal(err)
	}

	cm, err := cs.CoreV1().ConfigMaps(CatalogNamespace).Get(ctx, "addon-metallb-params", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cm.Data["values.yaml"], "driver:\n  enabled: true\n"; got != want {
		t.Fatalf("values.yaml = %q, want %q", got, want)
	}

	kz, err := dc.Resource(kustomizations).Namespace(CatalogNamespace).Get(ctx, "addon-metallb", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if path, _, _ := unstructured.NestedString(kz.Object, "spec", "path"); path != "./addons/metallb/manifests" {
		t.Errorf("path = %q", path)
	}
	if name, _, _ := unstructured.NestedString(kz.Object, "spec", "sourceRef", "name"); name != CatalogSource {
		t.Errorf("sourceRef.name = %q", name)
	}
	sub, _, _ := unstructured.NestedStringMap(kz.Object, "spec", "postBuild", "substitute")
	want := map[string]string{"addon_namespace": "metallb-system", "metallb_addresses": "192.0.2.10-192.0.2.20"}
	if len(sub) != len(want) || sub["addon_namespace"] != want["addon_namespace"] || sub["metallb_addresses"] != want["metallb_addresses"] {
		t.Errorf("substitute = %v, want %v", sub, want)
	}
	deps, _, _ := unstructured.NestedSlice(kz.Object, "spec", "dependsOn")
	if len(deps) != 1 || deps[0].(map[string]any)["name"] != "addon-nfd" {
		t.Errorf("dependsOn = %v", deps)
	}
}

func TestInstallUpdatesExisting(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	a := testAddon()
	if err := Install(ctx, cs, dc, a); err != nil {
		t.Fatal(err)
	}
	a.Values = map[string]any{"driver": map[string]any{"enabled": false}}
	if err := Install(ctx, cs, dc, a); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	cm, err := cs.CoreV1().ConfigMaps(CatalogNamespace).Get(ctx, "addon-metallb-params", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cm.Data["values.yaml"], "driver:\n  enabled: false\n"; got != want {
		t.Fatalf("values.yaml after update = %q, want %q", got, want)
	}
}

// Flux reconciles a HelmRelease when its values ConfigMap changes only if the
// ConfigMap carries the watch label, and every update must keep it.
func TestInstallLabelsParamsForFluxWatch(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	a := testAddon()
	for _, step := range []string{"install", "update"} {
		if err := Install(ctx, cs, dc, a); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		cm, err := cs.CoreV1().ConfigMaps(CatalogNamespace).Get(ctx, "addon-metallb-params", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := cm.Labels[watchLabel]; got != "Enabled" {
			t.Errorf("after %s: %s = %q, want Enabled", step, watchLabel, got)
		}
		a.Values = map[string]any{"driver": map[string]any{"enabled": false}}
	}
}

func TestLoadAddonParamsReadsWhatWasInstalled(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := Install(ctx, cs, dc, testAddon()); err != nil {
		t.Fatal(err)
	}
	helm, sub, err := LoadAddonParams(ctx, cs, dc, "metallb")
	if err != nil {
		t.Fatal(err)
	}
	driver, _ := helm["driver"].(map[string]any)
	if driver["enabled"] != true {
		t.Errorf("driver.enabled = %v, want true", driver["enabled"])
	}
	if sub["metallb_addresses"] != "192.0.2.10-192.0.2.20" {
		t.Errorf("metallb_addresses = %q", sub["metallb_addresses"])
	}

	helm, sub, err = LoadAddonParams(ctx, cs, dc, "absent")
	if err != nil || helm != nil || sub != nil {
		t.Errorf("absent addon: helm=%v sub=%v err=%v, want all empty", helm, sub, err)
	}
}

func TestUninstallRemovesObjectsAndToleratesMissing(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := Install(ctx, cs, dc, testAddon()); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(ctx, cs, dc, "metallb"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().ConfigMaps(CatalogNamespace).Get(ctx, "addon-metallb-params", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("ConfigMap still present: %v", err)
	}
	if _, err := dc.Resource(kustomizations).Namespace(CatalogNamespace).Get(ctx, "addon-metallb", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Kustomization still present: %v", err)
	}
	if err := Uninstall(ctx, cs, dc, "metallb"); err != nil {
		t.Errorf("second Uninstall: %v", err)
	}
}

// Flux adds its finalizer to a Kustomization after reconciling it. Kubeyard's
// own update must keep that finalizer, or deleting the Kustomization skips the
// prune and leaves the HelmRelease running.
func TestInstallKeepsFluxFinalizer(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	kzClient := dc.Resource(kustomizations).Namespace(CatalogNamespace)

	if err := Install(ctx, cs, dc, testAddon()); err != nil {
		t.Fatal(err)
	}
	kz, err := kzClient.Get(ctx, "addon-metallb", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kz.SetFinalizers([]string{"finalizers.fluxcd.io"})
	if _, err := kzClient.Update(ctx, kz, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := Install(ctx, cs, dc, testAddon()); err != nil {
		t.Fatal(err)
	}
	kz, err = kzClient.Get(ctx, "addon-metallb", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := kz.GetFinalizers(); len(got) != 1 || got[0] != "finalizers.fluxcd.io" {
		t.Errorf("finalizers after update = %v, want the Flux finalizer kept", got)
	}
}
