package main

import (
	"os"
	"testing"
)

// TestLiveCatalog reads the catalog page of a live cluster through App, the call
// the page makes on every refresh. It runs only when KUBEYARD_LIVE_CONTEXT names
// a context in the kubeconfig, so CI can point it at a kind cluster. A cluster
// without a catalog source is not an error: the view explains why.
func TestLiveCatalog(t *testing.T) {
	name := os.Getenv("KUBEYARD_LIVE_CONTEXT")
	if name == "" {
		t.Skip("KUBEYARD_LIVE_CONTEXT is not set")
	}
	cat, err := NewApp().Catalog(name)
	if err != nil {
		t.Fatal(err)
	}
	if cat.Error != "" {
		t.Errorf("catalog error = %q, want none", cat.Error)
	}
	if !cat.FluxInstalled && cat.Note == "" {
		t.Errorf("catalog = %+v, want a note when Flux is not installed", cat)
	}
}
