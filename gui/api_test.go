package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The API is tested through its methods, the same calls the page makes through
// the Wails bindings. The JSON the page reads is checked by encoding the results.

// wire returns the JSON the page receives for v. Wails encodes results with
// encoding/json, as this does.
func wire(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

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
		"InstallFlux":           func() (string, error) { return a.InstallFlux("") },
		"AddCatalogSource":      func() (string, error) { return a.AddCatalogSource("") },
		"InstallAddon":          func() (string, error) { return a.InstallAddon("", "nfd", nil) },
		"UpdateAddon":           func() (string, error) { return a.UpdateAddon("", "nfd", nil) },
		"UninstallAddon":        func() (string, error) { return a.UninstallAddon("", "nfd") },
		"ExportAdminKubeconfig": func() (string, error) { return a.ExportAdminKubeconfig("", "acme", "acme") },
	}
	for name, call := range calls {
		if _, err := call(); err == nil || !strings.Contains(err.Error(), "缺少 context") {
			t.Errorf("%s: err = %v, want the missing context error", name, err)
		}
	}
}

const (
	nodePoolsPath       = "/apis/kubeyard.wutz.dev/v1alpha1/nodepools"
	virtualClustersPath = "/apis/kubeyard.wutz.dev/v1alpha1/virtualclusters"
	nodeLeasesPath      = "/apis/kubeyard.wutz.dev/v1alpha1/nodeleases"
	nodesPath           = "/api/v1/nodes"

	emptyNodeLeasesList = `{"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"NodeLeaseList","metadata":{},"items":[]}`
	emptyNodesList      = `{"apiVersion":"v1","kind":"NodeList","metadata":{},"items":[]}`

	notFoundStatus  = `{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure","message":"the server could not find the requested resource","reason":"NotFound","code":404}`
	forbiddenStatus = `{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure","message":"nodepools is forbidden","reason":"Forbidden","code":403}`

	nodePoolsList = `{"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"NodePoolList","metadata":{},"items":[
  {"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"NodePool","metadata":{"name":"rtx-5090-x8"},
   "spec":{"displayName":"rtx-5090.x8","nodeSelector":{"matchLabels":{"nvidia.com/gpu.product":"NVIDIA-GeForce-RTX-5090"}}},
   "status":{"total":24,"free":19,"reserved":4,"unhealthy":1}}]}`

	virtualClustersList = `{"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"VirtualClusterList","metadata":{},"items":[
  {"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"VirtualCluster","metadata":{"name":"acme-prod"},
   "spec":{"displayName":"ACME 生产集群","priority":100,"preemptible":true,"provider":"vcluster","mode":"dedicated-nodes",
   "exposure":{"type":"LoadBalancer","loadBalancerIP":"10.243.145.200"},
   "poolClaims":[{"pool":"rtx-5090-x8","min":2,"max":8,"desired":4}]}}]}`
)

// serveLists answers the list paths in bodies. Any other path answers 404, as
// an API server does for a resource whose CRD is not installed.
func serveLists(bodies map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			body = notFoundStatus
		}
		fmt.Fprint(w, body)
	})
}

// fakeCluster starts an API server stand-in that answers with api, and points
// KUBECONFIG at it as the context "fake".
func fakeCluster(t *testing.T, api http.Handler) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: fake
clusters:
- name: fake
  cluster: {server: %s}
contexts:
- name: fake
  context: {cluster: fake, user: fake}
users:
- name: fake
  user: {token: x}
`, srv.URL)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
}

// assertKeys fails the test for every key that obj does not carry. The JSON
// tags are the wire format the page reads, so this checks them by name.
func assertKeys(t *testing.T, what string, obj map[string]json.RawMessage, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Errorf("%s has no key %q", what, k)
		}
	}
}

func TestPoolsJSONShape(t *testing.T) {
	fakeCluster(t, serveLists(map[string]string{
		nodePoolsPath:       nodePoolsList,
		virtualClustersPath: virtualClustersList,
		nodeLeasesPath:      emptyNodeLeasesList,
		nodesPath:           emptyNodesList,
	}))
	got, err := NewApp().Pools("fake")
	if err != nil {
		t.Fatal(err)
	}

	var top struct {
		Installed json.RawMessage   `json:"installed"`
		Pools     []json.RawMessage `json:"pools"`
	}
	if err := json.Unmarshal([]byte(wire(t, got)), &top); err != nil {
		t.Fatal(err)
	}
	if string(top.Installed) != "true" || len(top.Pools) != 1 {
		t.Fatalf("installed = %s, pools = %d", top.Installed, len(top.Pools))
	}

	var pool map[string]json.RawMessage
	if err := json.Unmarshal(top.Pools[0], &pool); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "pool", pool, "name", "displayName", "total", "free", "reserved", "unhealthy", "claims", "nodes")

	var claims []map[string]json.RawMessage
	if err := json.Unmarshal(pool["claims"], &claims); err != nil || len(claims) != 1 {
		t.Fatalf("claims = %s, err %v", pool["claims"], err)
	}
	assertKeys(t, "claim", claims[0], "virtualCluster", "displayName", "min", "max", "desired")

	p := got.Pools[0]
	if p.Name != "rtx-5090-x8" || p.DisplayName != "rtx-5090.x8" || p.Total != 24 || p.Free != 19 || p.Reserved != 4 || p.Unhealthy != 1 {
		t.Errorf("pool = %+v", p)
	}
	want := claimView{VirtualCluster: "acme-prod", DisplayName: "ACME 生产集群", Min: 2, Max: 8, Desired: 4}
	if len(p.Claims) != 1 || p.Claims[0] != want {
		t.Errorf("claims = %+v, want [%+v]", p.Claims, want)
	}
}

func TestPoolsNotInstalled(t *testing.T) {
	// No list path is served, so both lists answer 404 as they do without the CRDs.
	fakeCluster(t, serveLists(nil))
	got, err := NewApp().Pools("fake")
	if err != nil {
		t.Fatal(err)
	}
	body := wire(t, got)
	if !strings.Contains(body, `"installed":false`) || !strings.Contains(body, `"pools":[]`) {
		t.Errorf("body = %s, want installed false and an empty pools list", body)
	}
}

func TestPoolsForbiddenIsAnError(t *testing.T) {
	// A forbidden list is not the same as a missing CRD: the page must show it.
	fakeCluster(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, forbiddenStatus)
	}))
	if _, err := NewApp().Pools("fake"); err == nil {
		t.Fatal("Pools returned no error for a forbidden list")
	}
}

// The export refuses a confirmation that does not repeat the name, before it
// opens the save dialog or reads the cluster.
func TestExportAdminKubeconfigRequiresConfirm(t *testing.T) {
	_, err := NewApp().ExportAdminKubeconfig("dev", "acme", "acm")
	if err == nil || !strings.Contains(err.Error(), "确认导出") {
		t.Fatalf("err = %v, want the confirmation prompt", err)
	}
}

func TestExportAdminKubeconfigCancelWritesNothing(t *testing.T) {
	saved := saveFileDialog
	t.Cleanup(func() { saveFileDialog = saved })
	saveFileDialog = func(context.Context, runtime.SaveDialogOptions) (string, error) { return "", nil }
	path, err := NewApp().ExportAdminKubeconfig("dev", "acme", "acme")
	if err != nil || path != "" {
		t.Fatalf("cancel = (%q, %v), want an empty path and no error", path, err)
	}
}
