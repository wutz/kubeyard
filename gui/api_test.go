package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The API is tested through its methods, the same calls the page makes through
// the Wails bindings.

func TestContextsListsKubeconfig(t *testing.T) {
	const kubeconfig = `apiVersion: v1
kind: Config
current-context: prod
clusters:
- name: c1
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: dev
  context: {cluster: c1, user: u1}
- name: prod
  context: {cluster: c1, user: u1}
users:
- name: u1
  user: {token: x}
`
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)

	got := NewApp().Contexts()
	if len(got.Contexts) != 2 || got.Contexts[0].Name != "dev" || got.Contexts[1].Name != "prod" || !got.Contexts[1].Current {
		t.Errorf("contexts = %+v", got.Contexts)
	}
}

// Every write refuses an empty context before it touches the lock or a cluster.
func TestWriteNeedsContext(t *testing.T) {
	a := NewApp()
	calls := map[string]func() (string, error){
		"InstallFlux":      func() (string, error) { return a.InstallFlux("") },
		"AddCatalogSource": func() (string, error) { return a.AddCatalogSource("") },
		"InstallAddon":     func() (string, error) { return a.InstallAddon("", "nfd", nil) },
		"UpdateAddon":      func() (string, error) { return a.UpdateAddon("", "nfd", nil) },
		"UninstallAddon":   func() (string, error) { return a.UninstallAddon("", "nfd") },
	}
	for name, call := range calls {
		if _, err := call(); err == nil || !strings.Contains(err.Error(), "缺少 context") {
			t.Errorf("%s: err = %v, want the missing context error", name, err)
		}
	}
}
