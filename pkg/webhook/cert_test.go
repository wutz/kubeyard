package webhook

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var names = []string{"kubeyard-operator.kubeyard-system.svc", "localhost"}

func TestEnsureCertIssuesAndReuses(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	ca, err := EnsureCert(dir, names, now)
	if err != nil {
		t.Fatal(err)
	}
	serving, err := os.ReadFile(filepath.Join(dir, CertFile))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(serving)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname(names[0]); err != nil {
		t.Fatalf("certificate does not cover %s: %v", names[0], err)
	}
	// The serving certificate must chain to the returned CA.
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("CA bundle does not parse")
	}
	if _, err := cert.Verify(x509.VerifyOptions{DNSName: names[0], Roots: roots, CurrentTime: now}); err != nil {
		t.Fatalf("serving certificate does not verify against the CA: %v", err)
	}

	// A second start well before expiry keeps the same files.
	again, err := EnsureCert(dir, names, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(ca) {
		t.Fatal("a stored certificate was replaced before it needed renewal")
	}

	// Near expiry, both the CA and the serving certificate are replaced.
	late := now.Add(certValidity - 24*time.Hour)
	renewed, err := EnsureCert(dir, names, late)
	if err != nil {
		t.Fatal(err)
	}
	if string(renewed) == string(ca) {
		t.Fatal("an expiring certificate was reused")
	}
}

func TestEnsureCertReplacesCertificateForNewNames(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	first, err := EnsureCert(dir, names, now)
	if err != nil {
		t.Fatal(err)
	}
	// The service was renamed: the stored certificate does not cover the new name.
	second, err := EnsureCert(dir, []string{"other.kube.svc"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("certificate was reused although it does not cover the new name")
	}
	serving, err := os.ReadFile(filepath.Join(dir, CertFile))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(serving)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("other.kube.svc"); err != nil {
		t.Fatalf("new certificate does not cover the new name: %v", err)
	}
}

func TestEnsureCertRejectsNoNames(t *testing.T) {
	if _, err := EnsureCert(t.TempDir(), nil, time.Now()); err == nil {
		t.Fatal("want an error without DNS names")
	}
}

func TestPatchCABundle(t *testing.T) {
	cfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "kubeyard-validating"},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "vc.kubeyard.wutz.dev"},
			{Name: "pool.kubeyard.wutz.dev"},
		},
	}
	scheme := runtime.NewScheme()
	if err := admissionregistrationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cfg).Build()
	if err := PatchCABundle(context.Background(), c, "kubeyard-validating", []byte("ca")); err != nil {
		t.Fatal(err)
	}
	var got admissionregistrationv1.ValidatingWebhookConfiguration
	if err := c.Get(context.Background(), client.ObjectKey{Name: "kubeyard-validating"}, &got); err != nil {
		t.Fatal(err)
	}
	for _, w := range got.Webhooks {
		if string(w.ClientConfig.CABundle) != "ca" {
			t.Fatalf("%s: caBundle is %q, want ca", w.Name, w.ClientConfig.CABundle)
		}
	}
	// A missing configuration is not fatal.
	if err := PatchCABundle(context.Background(), c, "missing", []byte("ca")); err != nil {
		t.Fatalf("want no error for a missing configuration, got %v", err)
	}
}
