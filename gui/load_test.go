package main

import (
	"context"
	"strings"
	"testing"

	"github.com/wutz/kubeyard/pkg/kube"
)

// A source Flux has not reconciled yet has no message. The page shows the
// error as is, so it must still say something after the colon.
func TestVerifiedCatalogNotReadyWithoutMessage(t *testing.T) {
	_, err := verifiedCatalog(context.Background(), nil, kube.Source{Name: "catalog-default"})
	if err == nil {
		t.Fatal("verifiedCatalog returned no error for a source that is not ready")
	}
	if strings.HasSuffix(err.Error(), "：") || !strings.Contains(err.Error(), "就绪") {
		t.Errorf("error = %q, want a sentence that says the source is still becoming ready", err)
	}
}

// A source that is not ready with a message keeps Flux's message.
func TestVerifiedCatalogNotReadyKeepsMessage(t *testing.T) {
	_, err := verifiedCatalog(context.Background(), nil, kube.Source{Name: "catalog-default", Message: "pull failed"})
	if err == nil || err.Error() != "catalog 源未就绪：pull failed" {
		t.Errorf("error = %v, want the not-ready message with Flux's message", err)
	}
}
