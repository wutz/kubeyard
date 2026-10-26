package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/operator/host"
)

// typed encodes obj with its apiVersion and kind, as the API server returns it.
func typed(t *testing.T, apiVersion, kind string, obj any) string {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["apiVersion"], m["kind"] = apiVersion, kind
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// desiredScene serves a gpu pool with two ready nodes bound to low (priority 1,
// preemptible, desired 2) and an empty high claim (priority 10, max 1). Only GET
// is served, so a write to a VirtualCluster answers 404.
func desiredScene(t *testing.T) {
	t.Helper()
	gpu := map[string]string{"class": "gpu"}
	const kubeyard = "kubeyard.wutz.dev/v1alpha1"
	node := func(name string) string {
		return typed(t, "v1", "Node", corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: gpu},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		})
	}
	lease := func(name string) string {
		return typed(t, kubeyard, "NodeLease", v1alpha1.NodeLease{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.NodeLeaseSpec{Node: name, Pool: "gpu", VirtualCluster: "low"},
			Status:     v1alpha1.NodeLeaseStatus{Phase: v1alpha1.LeaseBound},
		})
	}
	pool := v1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu"},
		Spec: v1alpha1.NodePoolSpec{
			NodeSelector: metav1.LabelSelector{MatchLabels: gpu},
			Preemption: v1alpha1.PreemptionSpec{
				Policy:      v1alpha1.PreemptionLowerPriority,
				GracePeriod: &metav1.Duration{Duration: time.Minute},
			},
		},
	}
	vc := func(name string, priority int32, preemptible bool, max, desired int32) v1alpha1.VirtualCluster {
		return v1alpha1.VirtualCluster{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.VirtualClusterSpec{
				Priority:    priority,
				Preemptible: preemptible,
				PoolClaims:  []v1alpha1.PoolClaim{{Pool: "gpu", Min: 0, Max: max, Desired: desired}},
			},
		}
	}
	low := vc("low", 1, true, 2, 2)
	high := vc("high", 10, false, 1, 0)
	list := func(kind, apiVersion string, items ...string) string {
		return fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"metadata":{},"items":[%s]}`, apiVersion, kind, strings.Join(items, ","))
	}
	base := serveLists(map[string]string{
		nodesPath:              list("NodeList", "v1", node("n1"), node("n2")),
		nodeLeasesPath:         list("NodeLeaseList", kubeyard, lease("n1"), lease("n2")),
		nodePoolsPath:          list("NodePoolList", kubeyard, typed(t, kubeyard, "NodePool", pool)),
		nodePoolsPath + "/gpu": typed(t, kubeyard, "NodePool", pool),
		virtualClustersPath: list("VirtualClusterList", kubeyard,
			typed(t, kubeyard, "VirtualCluster", low), typed(t, kubeyard, "VirtualCluster", high)),
		virtualClustersPath + "/high": typed(t, kubeyard, "VirtualCluster", high),
	})
	fakeCluster(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, notFoundStatus)
			return
		}
		base.ServeHTTP(w, r)
	}))
}

func TestPreviewDesiredViewIsSortedAndNeverNull(t *testing.T) {
	body := wire(t, toDesiredPreviewView(host.Preview{}))
	for _, key := range []string{`"preempt":[]`, `"release":[]`, `"granted":[]`} {
		if !strings.Contains(body, key) {
			t.Errorf("body = %s, want %s", body, key)
		}
	}
	p := host.Preview{Release: map[string][]string{"zeta": {"n9"}, "alpha": {"n1", "n2"}}}
	rel := toDesiredPreviewView(p).Release
	if len(rel) != 3 || rel[0].VirtualCluster != "alpha" || rel[2] != (releaseView{VirtualCluster: "zeta", Node: "n9"}) {
		t.Errorf("release = %+v, want alpha's nodes before zeta's", rel)
	}
}

func TestSetDesiredRefusesWhenPreemptionChanged(t *testing.T) {
	desiredScene(t)
	_, cs, dc, err := clients("fake")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	p, err := planDesired(ctx, cs, dc, "high", "gpu", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(affectedNodes(p)) != 1 {
		t.Fatalf("affected = %v, want one preempted node", affectedNodes(p))
	}
	// Nothing was confirmed, so the preemption the cluster would now make is refused.
	err = setDesired(ctx, cs, dc, "high", "gpu", 1, nil)
	if err == nil || !strings.Contains(err.Error(), "预览与当前状态不一致") {
		t.Fatalf("setDesired without the confirmed nodes = %v, want refusal", err)
	}
}

func TestSetDesiredReachesWriteWhenConfirmedNodesMatch(t *testing.T) {
	desiredScene(t)
	_, cs, dc, err := clients("fake")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	p, err := planDesired(ctx, cs, dc, "high", "gpu", 1)
	if err != nil {
		t.Fatal(err)
	}
	// The guard passes, so the error is the write's: the fake server refuses PUT.
	err = setDesired(ctx, cs, dc, "high", "gpu", 1, affectedNodes(p))
	if err == nil || !strings.Contains(err.Error(), "写入虚拟集群 high") {
		t.Fatalf("setDesired with the confirmed nodes = %v, want the write to be reached", err)
	}
}

func TestDurationTextStatesMinutesWhenWhole(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, "5 分钟"},
		{15 * time.Minute, "15 分钟"},
		{90 * time.Second, "90 秒"},
		{0, "0 分钟"},
	} {
		if got := durationText(tc.d); got != tc.want {
			t.Errorf("durationText(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
