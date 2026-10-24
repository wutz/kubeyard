package kube

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func readyObject(apiVersion, kind, name, status, message string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": CatalogNamespace},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": status, "message": message},
		}},
	}}
}

func TestLoadAddonFlux(t *testing.T) {
	ctx := context.Background()
	kz := readyObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "addon-nfd", "True", "Applied revision: stable@sha256:abc")
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), kz)
	cs := kubefake.NewClientset(
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "kz-failed", Namespace: CatalogNamespace},
			InvolvedObject: corev1.ObjectReference{Kind: "Kustomization", Name: "addon-nfd"},
			Type:           "Warning",
			Reason:         "ReconciliationFailed",
			Message:        "timeout",
			LastTimestamp:  metav1.NewTime(time.Unix(200, 0)),
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "pod-event", Namespace: CatalogNamespace},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "addon-nfd"},
			Reason:         "Pulled",
			LastTimestamp:  metav1.NewTime(time.Unix(300, 0)),
		},
	)

	got, err := LoadAddonFlux(ctx, dc, cs, "nfd", []string{"nfd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conditions) != 2 {
		t.Fatalf("conditions = %v", got.Conditions)
	}
	if k := got.Conditions[0]; !k.Ready || k.Message != "Applied revision: stable@sha256:abc" {
		t.Errorf("Kustomization condition = %+v", k)
	}
	if h := got.Conditions[1]; h.Ready || h.Message != "未找到" {
		t.Errorf("missing HelmRelease condition = %+v, want not ready with 未找到", h)
	}
	if len(got.Events) != 1 || got.Events[0].Reason != "ReconciliationFailed" || got.Events[0].Object != "Kustomization/addon-nfd" {
		t.Errorf("events = %+v, want only the Kustomization event", got.Events)
	}
}
