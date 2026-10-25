package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

func testVirtualClusters() []v1alpha1.VirtualCluster {
	return []v1alpha1.VirtualCluster{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "low", Annotations: map[string]string{"note": "kept"}},
			Spec: v1alpha1.VirtualClusterSpec{
				DisplayName: "低优先级",
				Priority:    1,
				Provider:    "vcluster",
				Mode:        "dedicated-nodes",
				Exposure:    v1alpha1.Exposure{Type: v1alpha1.ExposureLoadBalancer, LoadBalancerIP: "10.243.145.9"},
				PoolClaims: []v1alpha1.PoolClaim{{
					Pool: "gpu", Min: 0, Max: 2, Desired: 1,
					Autoscale: &v1alpha1.Autoscale{Enabled: true},
				}},
			},
			Status: v1alpha1.VirtualClusterStatus{
				Phase:                    v1alpha1.VirtualClusterReady,
				Allocations:              []v1alpha1.PoolAllocation{{Pool: "gpu", Nodes: []string{"n1"}, PendingDemand: 2}},
				AdminKubeconfigSecretRef: &v1alpha1.SecretReference{Namespace: "vc-low", Name: "vc-low-admin", Key: "config"},
				Conditions:               []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Allocated", Message: "节点已就绪"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "high"},
			Spec: v1alpha1.VirtualClusterSpec{
				Priority: 9,
				Exposure: v1alpha1.Exposure{Type: v1alpha1.ExposureIngress, Ingress: &v1alpha1.IngressExposure{Host: "vc.example.com", Port: 7443}},
				PoolClaims: []v1alpha1.PoolClaim{
					{Pool: "control", Min: 1, Max: 1, Desired: 1, PinnedNodes: []string{"n0"}},
				},
			},
			Status: v1alpha1.VirtualClusterStatus{Phase: v1alpha1.VirtualClusterDraining},
		},
	}
}

func TestVirtualClustersViewOrderAndFields(t *testing.T) {
	pools := []v1alpha1.NodePool{
		{ObjectMeta: metav1.ObjectMeta{Name: "gpu"}, Spec: v1alpha1.NodePoolSpec{DisplayName: "GPU 池"}},
	}
	got := toVirtualClustersView(true, testVirtualClusters(), pools)
	if !got.Installed || len(got.Clusters) != 2 {
		t.Fatalf("view = %+v", got)
	}
	// Priority decides the order, largest first.
	if got.Clusters[0].Name != "high" || got.Clusters[1].Name != "low" {
		t.Fatalf("order = %s, %s", got.Clusters[0].Name, got.Clusters[1].Name)
	}

	low := got.Clusters[1]
	if low.Phase != "Ready" || low.Priority != 1 || low.DisplayName != "低优先级" {
		t.Errorf("low = %+v", low)
	}
	if !low.AdminKubeconfig {
		t.Error("low.AdminKubeconfig = false, want true")
	}
	if low.Exposure.Type != "LoadBalancer" || low.Exposure.LoadBalancerIP != "10.243.145.9" {
		t.Errorf("low.Exposure = %+v", low.Exposure)
	}
	if len(low.Claims) != 1 {
		t.Fatalf("low.Claims = %+v", low.Claims)
	}
	claim := low.Claims[0]
	if claim.Pool != "gpu" || !claim.Autoscale || claim.PoolDisplayName != "GPU 池" {
		t.Errorf("low claim = %+v", claim)
	}
	if len(low.Allocations) != 1 || low.Allocations[0].Nodes[0] != "n1" || low.Allocations[0].PendingDemand != 2 {
		t.Errorf("low.Allocations = %+v", low.Allocations)
	}
	if len(low.Conditions) != 1 || low.Conditions[0].Status != "True" || low.Conditions[0].Message != "节点已就绪" {
		t.Errorf("low.Conditions = %+v", low.Conditions)
	}

	high := got.Clusters[0]
	if high.AdminKubeconfig {
		t.Error("high.AdminKubeconfig = true, want false")
	}
	if high.Exposure.Type != "Ingress" || high.Exposure.Host != "vc.example.com" || high.Exposure.Port != 7443 {
		t.Errorf("high.Exposure = %+v", high.Exposure)
	}
	// A claim on a pool that is not listed keeps the pool's own name.
	if high.Claims[0].PoolDisplayName != "" || high.Claims[0].PinnedNodes[0] != "n0" {
		t.Errorf("high claim = %+v", high.Claims[0])
	}
}

func TestVirtualClustersViewNotInstalled(t *testing.T) {
	got := toVirtualClustersView(false, nil, nil)
	if got.Installed || got.Clusters == nil || len(got.Clusters) != 0 {
		t.Fatalf("view = %+v, want installed false and an empty list", got)
	}
}

func TestVirtualClustersViewEmptyListsAreNotNull(t *testing.T) {
	// The page calls list() on these, so null would be fine, but the JSON shape
	// is easier to read with []: check that a bare cluster still emits arrays.
	got := toVirtualClustersView(true, []v1alpha1.VirtualCluster{{ObjectMeta: metav1.ObjectMeta{Name: "bare"}}}, nil)
	c := got.Clusters[0]
	if c.Claims == nil || c.Allocations == nil || c.Conditions == nil {
		t.Errorf("cluster = %+v, want empty arrays", c)
	}
}

const clustersList = `{"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"VirtualClusterList","metadata":{},"items":[
  {"apiVersion":"kubeyard.wutz.dev/v1alpha1","kind":"VirtualCluster","metadata":{"name":"acme-prod"},
   "spec":{"displayName":"ACME 生产集群","priority":100,"preemptible":true,"provider":"vcluster","mode":"dedicated-nodes",
   "exposure":{"type":"Ingress","ingress":{"host":"vc.acme.example","port":7443}},
   "poolClaims":[{"pool":"rtx-5090-x8","min":2,"max":8,"desired":4}]},
   "status":{"phase":"Ready","allocations":[{"pool":"rtx-5090-x8","nodes":["gpu-1","gpu-2"]}],
   "adminKubeconfigSecretRef":{"namespace":"vc-acme-prod","name":"vc-acme-prod-admin","key":"config"},
   "conditions":[{"type":"Ready","status":"True","reason":"Allocated","message":"ok"}]}}]}`

func TestVirtualClustersJSONShape(t *testing.T) {
	fakeCluster(t, serveLists(map[string]string{
		virtualClustersPath: clustersList,
		nodePoolsPath:       nodePoolsList,
	}))
	got, err := NewApp().VirtualClusters("fake")
	if err != nil {
		t.Fatal(err)
	}

	var top struct {
		Installed json.RawMessage   `json:"installed"`
		Clusters  []json.RawMessage `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(wire(t, got)), &top); err != nil {
		t.Fatal(err)
	}
	if string(top.Installed) != "true" || len(top.Clusters) != 1 {
		t.Fatalf("installed = %s, clusters = %d", top.Installed, len(top.Clusters))
	}
	var cluster map[string]json.RawMessage
	if err := json.Unmarshal(top.Clusters[0], &cluster); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "cluster", cluster, "name", "displayName", "description", "phase", "priority", "preemptible",
		"provider", "mode", "version", "endpoint", "exposure", "claims", "allocations", "adminKubeconfig", "conditions")

	c := got.Clusters[0]
	if c.Name != "acme-prod" || c.Phase != "Ready" || c.Priority != 100 || !c.Preemptible || !c.AdminKubeconfig {
		t.Errorf("cluster = %+v", c)
	}
	if c.Exposure.Host != "vc.acme.example" || c.Exposure.Port != 7443 {
		t.Errorf("exposure = %+v", c.Exposure)
	}
	// The pool display name is joined in from the NodePools.
	if len(c.Claims) != 1 || c.Claims[0].PoolDisplayName != "rtx-5090.x8" {
		t.Errorf("claims = %+v", c.Claims)
	}
	if len(c.Allocations) != 1 || len(c.Allocations[0].Nodes) != 2 {
		t.Errorf("allocations = %+v", c.Allocations)
	}
}

func TestVirtualClustersNotInstalled(t *testing.T) {
	fakeCluster(t, serveLists(nil))
	got, err := NewApp().VirtualClusters("fake")
	if err != nil {
		t.Fatal(err)
	}
	body := wire(t, got)
	if !strings.Contains(body, `"installed":false`) || !strings.Contains(body, `"clusters":[]`) {
		t.Errorf("body = %s, want installed false and an empty list", body)
	}
}

func TestVirtualClustersForbiddenIsAnError(t *testing.T) {
	fakeCluster(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(forbiddenStatus))
	}))
	if _, err := NewApp().VirtualClusters("fake"); err == nil {
		t.Fatal("VirtualClusters returned no error for a forbidden list")
	}
}
