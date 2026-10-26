package host

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// previewScene is the gpu pool with two ready nodes bound to low (priority 1,
// preemptible, desired 2) and an empty high claim (priority 10, max 1).
func previewScene() (*v1alpha1.NodePool, []v1alpha1.VirtualCluster, []corev1.Node, []v1alpha1.NodeLease) {
	np := newPool(v1alpha1.PreemptionLowerPriority)
	vcs := []v1alpha1.VirtualCluster{
		*newVC("low", 1, true, 0, 2, 2),
		*newVC("high", 10, false, 0, 1, 0),
	}
	nodes := []corev1.Node{*newNode("n1", true, gpu), *newNode("n2", true, gpu)}
	leases := []v1alpha1.NodeLease{*newLease("n1", "low"), *newLease("n2", "low")}
	return np, vcs, nodes, leases
}

func TestPreviewPreemptsLowerPriorityNodeWithGrace(t *testing.T) {
	np, vcs, nodes, leases := previewScene()
	got, err := PreviewDesired(np, vcs, nodes, leases, "high", 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Preempt) != 1 {
		t.Fatalf("preempt = %+v, want one victim", got.Preempt)
	}
	p := got.Preempt[0]
	if p.From != "low" || p.For != "high" || (p.Node != "n1" && p.Node != "n2") {
		t.Errorf("victim = %+v, want a node taken from low for high", p)
	}
	// The gpu pool has a one-minute grace period and no drain timeout.
	if got.GracePeriod != time.Minute || got.ForceAfter != time.Minute {
		t.Errorf("grace = %v, force after = %v, want one minute each", got.GracePeriod, got.ForceAfter)
	}
	if len(got.Grant) != 0 || got.Shortfall != 0 {
		t.Errorf("grant = %v, shortfall = %d, want none", got.Grant, got.Shortfall)
	}
}

func TestPreviewGrantsFreeNodeWithoutVictims(t *testing.T) {
	np, vcs, nodes, leases := previewScene()
	nodes = append(nodes, *newNode("n3", true, gpu))
	got, err := PreviewDesired(np, vcs, nodes, leases, "high", 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Preempt) != 0 || len(got.Grant) != 1 || got.Grant[0] != "n3" {
		t.Errorf("preview = %+v, want n3 granted and no victims", got)
	}
}

func TestPreviewLoweringDesiredReleasesNodes(t *testing.T) {
	np, vcs, nodes, leases := previewScene()
	got, err := PreviewDesired(np, vcs, nodes, leases, "low", 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Preempt) != 0 || len(got.Release["low"]) != 2 {
		t.Errorf("preview = %+v, want both nodes of low released and no victims", got)
	}
}

func TestPreviewShortfallWhenNothingCanBeTaken(t *testing.T) {
	// high cannot preempt a claim of its own priority or higher, so with no free
	// node its change to max leaves a shortfall and no victims.
	np, vcs, nodes, leases := previewScene()
	vcs[0].Spec.Priority = 10
	got, err := PreviewDesired(np, vcs, nodes, leases, "high", 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Preempt) != 0 || got.Shortfall != 1 {
		t.Errorf("preview = %+v, want no victims and shortfall 1", got)
	}
}

func TestPreviewRefusesAdmissionViolations(t *testing.T) {
	np, vcs, nodes, leases := previewScene()
	cases := []struct {
		name    string
		vc      string
		desired int32
	}{
		{"desired above max", "high", 2},
		{"desired below min", "low", -1},
		{"unknown claim", "missing", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PreviewDesired(np, vcs, nodes, leases, tc.vc, tc.desired, t0); err == nil {
				t.Errorf("PreviewDesired(%s, %d) = nil error, want refusal", tc.vc, tc.desired)
			}
		})
	}
}

func TestPreviewDoesNotChangeInput(t *testing.T) {
	np, vcs, nodes, leases := previewScene()
	if _, err := PreviewDesired(np, vcs, nodes, leases, "high", 1, t0); err != nil {
		t.Fatal(err)
	}
	if d := vcs[1].Spec.PoolClaims[0].Desired; d != 0 {
		t.Errorf("input desired of high = %d after preview, want 0", d)
	}
}
