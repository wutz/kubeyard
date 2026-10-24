package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestAddCatalogSourceWritesSecretAndVerifiedSource(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := AddCatalogSource(ctx, cs, dc, DefaultCatalogURL, CatalogPublicKey); err != nil {
		t.Fatal(err)
	}

	secret, err := cs.CoreV1().Secrets(CatalogNamespace).Get(ctx, "catalog-default-cosign-pub", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["cosign.pub"]) != string(CatalogPublicKey) {
		t.Errorf("secret key does not match the embedded key")
	}

	src, err := dc.Resource(ociRepositories).Namespace(CatalogNamespace).Get(ctx, CatalogSource, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if url, _, _ := unstructured.NestedString(src.Object, "spec", "url"); url != DefaultCatalogURL {
		t.Errorf("url = %q", url)
	}
	if tag, _, _ := unstructured.NestedString(src.Object, "spec", "ref", "tag"); tag != "stable" {
		t.Errorf("ref.tag = %q, want stable", tag)
	}
	if provider, _, _ := unstructured.NestedString(src.Object, "spec", "verify", "provider"); provider != "cosign" {
		t.Errorf("verify.provider = %q, want cosign", provider)
	}
	if ref, _, _ := unstructured.NestedString(src.Object, "spec", "verify", "secretRef", "name"); ref != secret.Name {
		t.Errorf("verify.secretRef.name = %q, want %q", ref, secret.Name)
	}
}

func TestAddCatalogSourceUpdatesExisting(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := AddCatalogSource(ctx, cs, dc, DefaultCatalogURL, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := AddCatalogSource(ctx, cs, dc, DefaultCatalogURL, CatalogPublicKey); err != nil {
		t.Fatalf("second AddCatalogSource: %v", err)
	}
	secret, err := cs.CoreV1().Secrets(CatalogNamespace).Get(ctx, "catalog-default-cosign-pub", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["cosign.pub"]) != string(CatalogPublicKey) {
		t.Errorf("secret key was not updated")
	}
}

func TestAddCatalogSourceKeepsExistingNamespace(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: CatalogNamespace}})
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())

	if err := AddCatalogSource(ctx, cs, dc, DefaultCatalogURL, CatalogPublicKey); err != nil {
		t.Fatalf("AddCatalogSource with existing namespace: %v", err)
	}
}
