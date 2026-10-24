package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHost = "127.0.0.1:7777"

func testServer(t *testing.T) *server {
	t.Helper()
	s, err := newServer(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7777})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// do sends a request to the server's handler with the given host and headers.
func do(t *testing.T, h http.Handler, method, path, host string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGuardRefusesForeignHost(t *testing.T) {
	s := testServer(t)
	rec := do(t, s.handler(), "GET", "/", "evil.example:7777", nil, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAPIRequiresToken(t *testing.T) {
	s := testServer(t)
	h := s.handler()
	for name, headers := range map[string]map[string]string{
		"no token":    nil,
		"wrong token": {tokenHeader: "nope"},
	} {
		rec := do(t, h, "GET", "/api/contexts", testHost, headers, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, http.StatusUnauthorized)
		}
	}
}

func TestWriteRequiresTokenBeforeBodyIsRead(t *testing.T) {
	s := testServer(t)
	rec := do(t, s.handler(), "POST", "/api/flux", testHost, nil, `{"context":"dev"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestIndexCarriesTokenAndLockedHeaders(t *testing.T) {
	s := testServer(t)
	rec := do(t, s.handler(), "GET", "/", testHost, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `content="`+s.token+`"`) {
		t.Errorf("page does not carry the token")
	}
	if strings.Contains(body, tokenPlaceholder) {
		t.Errorf("token placeholder was not replaced")
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("Content-Security-Policy = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/static/app.js", "/static/app.css"} {
		rec := do(t, s.handler(), "GET", path, testHost, nil, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
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

	s := testServer(t)
	rec := do(t, s.handler(), "GET", "/api/contexts", testHost, map[string]string{tokenHeader: s.token}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got contextsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Contexts) != 2 || got.Contexts[0].Name != "dev" || got.Contexts[1].Name != "prod" || !got.Contexts[1].Current {
		t.Errorf("contexts = %+v", got.Contexts)
	}
}

func TestWriteNeedsContext(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{tokenHeader: s.token, "Content-Type": "application/json"}
	for name, body := range map[string]string{
		"missing context": `{"fields":{}}`,
		"not json":        `{"context":`,
	} {
		rec := do(t, s.handler(), "POST", "/api/addons/nfd/install", testHost, headers, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want %d (body %s)", name, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

const (
	nodePoolsPath       = "/apis/kubeyard.wutz.dev/v1alpha1/nodepools"
	virtualClustersPath = "/apis/kubeyard.wutz.dev/v1alpha1/virtualclusters"

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

func TestPoolsRequiresToken(t *testing.T) {
	s := testServer(t)
	h := s.handler()
	for name, headers := range map[string]map[string]string{
		"no token":    nil,
		"wrong token": {tokenHeader: "nope"},
	} {
		rec := do(t, h, "GET", "/api/pools?context=fake", testHost, headers, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, http.StatusUnauthorized)
		}
	}
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
	}))
	s := testServer(t)
	rec := do(t, s.handler(), "GET", "/api/pools?context=fake", testHost, map[string]string{tokenHeader: s.token}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var top struct {
		Installed json.RawMessage   `json:"installed"`
		Pools     []json.RawMessage `json:"pools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil {
		t.Fatal(err)
	}
	if string(top.Installed) != "true" || len(top.Pools) != 1 {
		t.Fatalf("installed = %s, pools = %d; body %s", top.Installed, len(top.Pools), rec.Body.String())
	}

	var pool map[string]json.RawMessage
	if err := json.Unmarshal(top.Pools[0], &pool); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "pool", pool, "name", "displayName", "total", "free", "reserved", "unhealthy", "claims")

	var claims []map[string]json.RawMessage
	if err := json.Unmarshal(pool["claims"], &claims); err != nil || len(claims) != 1 {
		t.Fatalf("claims = %s, err %v", pool["claims"], err)
	}
	assertKeys(t, "claim", claims[0], "virtualCluster", "displayName", "min", "max", "desired")

	var got poolsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
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
	s := testServer(t)
	rec := do(t, s.handler(), "GET", "/api/pools?context=fake", testHost, map[string]string{tokenHeader: s.token}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
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
	s := testServer(t)
	rec := do(t, s.handler(), "GET", "/api/pools?context=fake", testHost, map[string]string{tokenHeader: s.token}, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"installed"`) {
		t.Errorf("body = %s, want an error, not a page", rec.Body.String())
	}
}

func TestExportAdminKubeconfigRequiresToken(t *testing.T) {
	s := testServer(t)
	rec := do(t, s.handler(), "POST", "/api/virtualclusters/acme/admin-kubeconfig", testHost, nil, `{"context":"dev","confirm":"acme"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestExportAdminKubeconfigRequiresConfirm(t *testing.T) {
	s := testServer(t)
	rec := do(t, s.handler(), "POST", "/api/virtualclusters/acme/admin-kubeconfig", testHost,
		map[string]string{tokenHeader: s.token}, `{"context":"dev","confirm":"acm"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "确认导出") {
		t.Fatalf("body = %s, want the confirmation prompt", rec.Body)
	}
}
