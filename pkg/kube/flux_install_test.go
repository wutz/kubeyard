package kube

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func emptyInventory() Inventory {
	return Inventory{objects: map[string]bool{}}
}

func TestCheckFluxAbsentRefusesExistingFlux(t *testing.T) {
	ctx := context.Background()
	empty := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	if err := checkFluxAbsent(ctx, empty, emptyInventory()); err != nil {
		t.Fatalf("empty cluster: %v", err)
	}

	cases := map[string]Inventory{
		"source controller": {objects: map[string]bool{"Deployment/flux-system/source-controller": true}},
		"operator release":  {objects: map[string]bool{"HelmRelease/flux-system/flux-operator": true}},
	}
	for name, inv := range cases {
		if err := checkFluxAbsent(ctx, empty, inv); !errors.Is(err, ErrFluxPresent) {
			t.Errorf("%s: err = %v, want ErrFluxPresent", name, err)
		}
	}

	withInstance := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fluxInstance())
	if err := checkFluxAbsent(ctx, withInstance, emptyInventory()); !errors.Is(err, ErrFluxPresent) {
		t.Errorf("FluxInstance present: err = %v, want ErrFluxPresent", err)
	}
}

func TestCreateFluxInstanceWritesSpec(t *testing.T) {
	ctx := context.Background()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := createFluxInstance(ctx, dc); err != nil {
		t.Fatal(err)
	}
	fi, err := dc.Resource(fluxInstances).Namespace(FluxNamespace).Get(ctx, fluxInstanceName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := unstructured.NestedString(fi.Object, "spec", "distribution", "version"); v != "2.x" {
		t.Errorf("distribution.version = %q, want 2.x", v)
	}
	if size, _, _ := unstructured.NestedString(fi.Object, "spec", "cluster", "size"); size != "medium" {
		t.Errorf("cluster.size = %q, want medium", size)
	}
	components, _, _ := unstructured.NestedStringSlice(fi.Object, "spec", "components")
	if len(components) != 4 || components[0] != "source-controller" {
		t.Errorf("components = %v", components)
	}
}

func TestCreateFluxInstanceNeverTakesOver(t *testing.T) {
	ctx := context.Background()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fluxInstance())

	err := createFluxInstance(ctx, dc)
	if !apierrors.IsAlreadyExists(err) {
		t.Fatalf("err = %v, want AlreadyExists", err)
	}
}
