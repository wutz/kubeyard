package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/kube"
)

func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func TestPathsReturnValidJSON(t *testing.T) {
	ts := fakeServer(t)
	cases := []struct {
		path   string
		status int
		kind   string
	}{
		{"/api", http.StatusOK, "APIVersions"},
		{"/api/v1", http.StatusOK, "APIResourceList"},
		{"/apis", http.StatusOK, "APIGroupList"},
		{"/api/v1/nodes", http.StatusOK, "NodeList"},
		{"/api/v1/pods", http.StatusOK, "PodList"},
		{"/api/v1/secrets", http.StatusNotFound, "Status"},
		{"/apis/apiextensions.k8s.io/v1/customresourcedefinitions", http.StatusNotFound, "Status"},
	}
	for _, tc := range cases {
		status, ctype, body := get(t, ts.URL+tc.path)
		if status != tc.status {
			t.Errorf("GET %s: status = %d, want %d", tc.path, status, tc.status)
		}
		if ctype != "application/json" {
			t.Errorf("GET %s: Content-Type = %q, want application/json", tc.path, ctype)
		}
		var obj struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatalf("GET %s: body is not JSON: %v\n%s", tc.path, err, body)
		}
		if obj.Kind != tc.kind {
			t.Errorf("GET %s: kind = %q, want %q", tc.path, obj.Kind, tc.kind)
		}
	}
}

func TestNotFoundIsStatus(t *testing.T) {
	ts := fakeServer(t)
	_, _, body := get(t, ts.URL+"/api/v1/secrets")
	var st metav1.Status
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Code != http.StatusNotFound || st.Reason != metav1.StatusReasonNotFound || st.Status != metav1.StatusFailure {
		t.Errorf("status = %+v, want a NotFound failure with code 404", st)
	}
}

func TestResponsesAreDeterministic(t *testing.T) {
	ts := fakeServer(t)
	for _, path := range []string{"/api/v1/nodes", "/api/v1/pods"} {
		_, _, first := get(t, ts.URL+path)
		_, _, second := get(t, ts.URL+path)
		if string(first) != string(second) {
			t.Errorf("GET %s changed between requests", path)
		}
	}
}

// The overview is the check the GUI smoke test makes. It must see one Ready
// node and the two fake pods scheduled on it, through the same client code the
// GUI uses.
func TestOverviewReadsFakeCluster(t *testing.T) {
	ts := fakeServer(t)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	ov, err := kube.Load(context.Background(), cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Nodes) != 1 || ov.NodesReady != 1 {
		t.Fatalf("nodes = %d, ready = %d; want 1 and 1", len(ov.Nodes), ov.NodesReady)
	}
	if !ov.Nodes[0].Ready || ov.Nodes[0].Name != nodeName {
		t.Errorf("node = %+v, want %s ready", ov.Nodes[0], nodeName)
	}
	if ov.PodsRunning != 2 {
		t.Errorf("PodsRunning = %d, want 2", ov.PodsRunning)
	}
	if got, want := ov.Nodes[0].CPUReqMilli, int64(300); got != want {
		t.Errorf("CPUReqMilli = %d, want %d (pods scheduled on the node)", got, want)
	}
}

func TestDiscoveryListsServedResources(t *testing.T) {
	ts := fakeServer(t)
	_, _, body := get(t, ts.URL+"/api/v1")
	var list metav1.APIResourceList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, r := range list.APIResources {
		names[r.Name] = true
	}
	if !names["nodes"] || !names["pods"] {
		t.Errorf("discovery resources = %v, want nodes and pods", names)
	}
}
