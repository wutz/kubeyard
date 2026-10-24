package main

import (
	"encoding/json"
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
