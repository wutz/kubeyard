package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestLiveReclaimNotice reads a live preemption through App, the data the
// VirtualCluster detail page renders. It needs an operator in host mode running
// against the cluster and a victim VirtualCluster named low that is preempted
// while the test waits. It only observes, so it runs when KUBEYARD_LIVE_RECLAIM
// names a context. KUBEYARD_RECLAIM_DUMP, if set, receives the page's JSON while
// the notice is up.
func TestLiveReclaimNotice(t *testing.T) {
	name := os.Getenv("KUBEYARD_LIVE_RECLAIM")
	if name == "" {
		t.Skip("KUBEYARD_LIVE_RECLAIM is not set")
	}
	app := NewApp()
	_, cs, _, err := clients(name)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "victim low shows Preempting", 5*time.Minute, func() bool {
		_, ok := reclaimCondition(t, app, name, "low")
		return ok
	})
	notice, _ := reclaimCondition(t, app, name, "low")
	t.Logf("notice: reason=%s message=%q", notice.Reason, notice.Message)
	if notice.Reason != "NodesReclaimed" {
		t.Errorf("Preempting reason = %q, want NodesReclaimed", notice.Reason)
	}

	if path := os.Getenv("KUBEYARD_RECLAIM_DUMP"); path != "" {
		view, err := app.VirtualClusters(name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("page data written to %s", path)
	}

	events, err := cs.CoreV1().Events("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events.Items {
		if ev.Reason == "Preempting" && ev.InvolvedObject.Kind == "VirtualCluster" && ev.InvolvedObject.Name == "low" {
			found = true
			t.Logf("event: %s %q", ev.Type, ev.Message)
		}
	}
	if !found {
		t.Error("no Preempting event on VirtualCluster low in default")
	}

	waitFor(t, "notice cleared after handover", 10*time.Minute, func() bool {
		_, ok := reclaimCondition(t, app, name, "low")
		return !ok
	})
}

// reclaimCondition returns the Preempting condition of the named VirtualCluster
// when it is True.
func reclaimCondition(t *testing.T, app *App, contextName, vc string) (vcConditionView, bool) {
	t.Helper()
	view, err := app.VirtualClusters(contextName)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range view.Clusters {
		if c.Name != vc {
			continue
		}
		for _, cond := range c.Conditions {
			if cond.Type == "Preempting" && cond.Status == "True" {
				return cond, true
			}
		}
	}
	return vcConditionView{}, false
}
