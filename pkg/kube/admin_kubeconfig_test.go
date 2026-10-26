package kube

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// vcObject is a VirtualCluster as the dynamic client returns it. ref may be nil.
func vcObject(t *testing.T, name string, ref *v1alpha1.SecretReference) *unstructured.Unstructured {
	t.Helper()
	vc := &v1alpha1.VirtualCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     v1alpha1.VirtualClusterStatus{AdminKubeconfigSecretRef: ref},
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vc)
	if err != nil {
		t.Fatal(err)
	}
	obj := &unstructured.Unstructured{Object: m}
	obj.SetAPIVersion(v1alpha1.SchemeGroupVersion.String())
	obj.SetKind("VirtualCluster")
	return obj
}

func kubeconfigSecret(namespace, name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Data:       data,
	}
}

func fakeDynamic(t *testing.T, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		virtualClusters: "VirtualClusterList",
	}, objs...)
}

func TestAdminKubeconfigReturnsSecretKey(t *testing.T) {
	ref := &v1alpha1.SecretReference{Namespace: "vc-acme", Name: "vc-acme-admin", Key: "config"}
	cs := fake.NewSimpleClientset(kubeconfigSecret("vc-acme", "vc-acme-admin", map[string][]byte{"config": []byte("apiVersion: v1\n")}))
	dc := fakeDynamic(t, vcObject(t, "acme", ref))

	got, err := AdminKubeconfig(context.Background(), cs, dc, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "apiVersion: v1\n" {
		t.Fatalf("kubeconfig = %q", got)
	}
}

func TestAdminKubeconfigNotGeneratedYet(t *testing.T) {
	cs := fake.NewSimpleClientset()
	dc := fakeDynamic(t, vcObject(t, "acme", nil))

	_, err := AdminKubeconfig(context.Background(), cs, dc, "acme")
	if err == nil || !strings.Contains(err.Error(), "尚未生成") {
		t.Fatalf("err = %v, want not-generated-yet", err)
	}
}

func TestAdminKubeconfigMissingKeyIsAnError(t *testing.T) {
	ref := &v1alpha1.SecretReference{Namespace: "vc-acme", Name: "vc-acme-admin", Key: "config"}
	cs := fake.NewSimpleClientset(kubeconfigSecret("vc-acme", "vc-acme-admin", map[string][]byte{"other": []byte("x")}))
	dc := fakeDynamic(t, vcObject(t, "acme", ref))

	_, err := AdminKubeconfig(context.Background(), cs, dc, "acme")
	if err == nil || !strings.Contains(err.Error(), `"config"`) {
		t.Fatalf("err = %v, want a missing-key error", err)
	}
}

func TestAdminKubeconfigEmptyValueIsAnError(t *testing.T) {
	ref := &v1alpha1.SecretReference{Namespace: "vc-acme", Name: "vc-acme-admin", Key: "config"}
	cs := fake.NewSimpleClientset(kubeconfigSecret("vc-acme", "vc-acme-admin", map[string][]byte{"config": nil}))
	dc := fakeDynamic(t, vcObject(t, "acme", ref))

	if _, err := AdminKubeconfig(context.Background(), cs, dc, "acme"); err == nil {
		t.Fatal("want an error for an empty kubeconfig value")
	}
}

func TestAdminKubeconfigUnknownVirtualCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	dc := fakeDynamic(t)

	if _, err := AdminKubeconfig(context.Background(), cs, dc, "ghost"); err == nil {
		t.Fatal("want an error for a VirtualCluster that does not exist")
	}
}

func TestRecordAccessWritesClusterScopedEvent(t *testing.T) {
	ref := &v1alpha1.SecretReference{Namespace: "vc-acme", Name: "vc-acme-admin", Key: "config"}
	cs := fake.NewSimpleClientset()
	dc := fakeDynamic(t, vcObject(t, "acme", ref))

	if err := RecordAccess(context.Background(), cs, dc, "acme", ExportedKubeconfig, "管理员 kubeconfig 已导出，操作来源：alice"); err != nil {
		t.Fatal(err)
	}
	events, err := cs.CoreV1().Events(metav1.NamespaceDefault).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("events = %d, want 1", len(events.Items))
	}
	ev := events.Items[0]
	if ev.Reason != ExportedKubeconfig || ev.InvolvedObject.Name != "acme" || ev.InvolvedObject.Kind != "VirtualCluster" {
		t.Fatalf("event = %+v", ev)
	}
	if !strings.Contains(ev.Message, "alice") {
		t.Fatalf("message = %q, want the actor", ev.Message)
	}
}

func TestRecordAccessFailsWithoutSecretRef(t *testing.T) {
	cs := fake.NewSimpleClientset()
	dc := fakeDynamic(t, vcObject(t, "acme", nil))

	if err := RecordAccess(context.Background(), cs, dc, "acme", ExportedKubeconfig, "管理员 kubeconfig 已导出，操作来源：alice"); err == nil {
		t.Fatal("want an error: no namespace to record the audit Event in")
	}
}
