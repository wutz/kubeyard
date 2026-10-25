package main

import (
	"os"
	"testing"
)

// TestLiveOverview reads the overview of a live cluster through App, the call the
// page makes on every poll. It runs only when KUBEYARD_LIVE_CONTEXT names a
// context in the kubeconfig, so CI can point it at a kind cluster.
func TestLiveOverview(t *testing.T) {
	name := os.Getenv("KUBEYARD_LIVE_CONTEXT")
	if name == "" {
		t.Skip("KUBEYARD_LIVE_CONTEXT is not set")
	}
	ov, err := NewApp().Overview(name)
	if err != nil {
		t.Fatal(err)
	}
	if ov.NodesTotal < 1 || ov.NodesReady < 1 {
		t.Errorf("overview = %+v, want at least one node and one of them ready", ov)
	}
}
