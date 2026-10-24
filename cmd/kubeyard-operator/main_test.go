package main

import (
	"strings"
	"testing"
)

func TestRunRejectsUnimplementedMode(t *testing.T) {
	err := run([]string{"--mode=vcluster"})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("run(vcluster) = %v, want a not-implemented error", err)
	}
}

func TestRunRejectsUnknownMode(t *testing.T) {
	err := run([]string{"--mode=bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("run(bogus) = %v, want an unknown-mode error", err)
	}
}
