package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// adminScene serves one VirtualCluster "acme" whose admin kubeconfig Secret is
// recorded, one node and one pod. The admin kubeconfig points back at the same
// stand-in server, so reading through it exercises the admin client for real.
// order collects the requests in the order they arrive.
func adminScene(t *testing.T, eventStatus int, order *[]string) {
	t.Helper()
	const vc = `{"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"VirtualCluster","metadata":{"name":"acme"},` +
		`"status":{"adminKubeconfigSecretRef":{"namespace":"vc-acme","name":"acme-admin","key":"config"}}}`
	const node = `{"apiVersion":"v1","kind":"Node","metadata":{"name":"n1"}}`
	const pod = `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"p1","namespace":"default"}}`
	fakeCluster(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*order = append(*order, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == virtualClustersPath+"/acme":
			fmt.Fprint(w, vc)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/default/events":
			if eventStatus != http.StatusCreated {
				w.WriteHeader(eventStatus)
				fmt.Fprint(w, notFoundStatus)
				return
			}
			// The client sends the Event as protobuf, so the answer is JSON.
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Event","metadata":{"name":"acme.1","namespace":"default"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/vc-acme/secrets/acme-admin":
			kc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: acme
  cluster: {server: http://%s}
users:
- name: admin
  user: {token: admin-token}
contexts:
- name: acme
  context: {cluster: acme, user: admin}
current-context: acme
`, r.Host)
			fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"acme-admin","namespace":"vc-acme"},"data":{"config":%q}}`,
				base64.StdEncoding.EncodeToString([]byte(kc)))
		case r.Method == http.MethodGet && r.URL.Path == nodesPath:
			fmt.Fprintf(w, `{"apiVersion":"v1","kind":"NodeList","metadata":{},"items":[%s]}`, node)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/pods":
			fmt.Fprintf(w, `{"apiVersion":"v1","kind":"PodList","metadata":{},"items":[%s]}`, pod)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, notFoundStatus)
		}
	}))
}

func TestOpenVirtualClusterAdminRefusesWithoutConfirm(t *testing.T) {
	var order []string
	adminScene(t, http.StatusCreated, &order)
	var a App
	if _, err := a.OpenVirtualClusterAdmin("fake", "acme", "acm"); err == nil {
		t.Fatal("open without the name typed = nil error, want refusal")
	}
	if len(order) != 0 {
		t.Fatalf("requests = %v, want none before the confirmation", order)
	}
}

func TestOpenVirtualClusterAdminRecordsBeforeReadingCredential(t *testing.T) {
	var order []string
	adminScene(t, http.StatusCreated, &order)
	var a App
	ov, err := a.OpenVirtualClusterAdmin("fake", "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if ov.NodesTotal != 1 || len(ov.Nodes) != 1 || ov.Nodes[0].Name != "n1" {
		t.Errorf("overview = %+v, want the one node read through the admin client", ov)
	}
	event, secret := -1, -1
	for i, req := range order {
		if req == "POST /api/v1/namespaces/default/events" && event < 0 {
			event = i
		}
		if req == "GET /api/v1/namespaces/vc-acme/secrets/acme-admin" && secret < 0 {
			secret = i
		}
	}
	if event < 0 || secret < 0 || event > secret {
		t.Fatalf("order = %v, want the audit Event written before the Secret is read", order)
	}
}

func TestOpenVirtualClusterAdminDoesNotReadCredentialWhenRecordFails(t *testing.T) {
	var order []string
	adminScene(t, http.StatusForbidden, &order)
	var a App
	if _, err := a.OpenVirtualClusterAdmin("fake", "acme", "acme"); err == nil {
		t.Fatal("open with the audit Event failing = nil error, want refusal")
	}
	for _, req := range order {
		if strings.Contains(req, "secrets") {
			t.Fatalf("order = %v, the credential was read after the audit Event failed", order)
		}
	}
}
