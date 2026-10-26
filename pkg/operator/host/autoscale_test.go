package host

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// allocNode is a ready gpu node with 4 CPUs and 8Gi of memory, unbound.
func allocNode(name string) *corev1.Node {
	n := newNode(name, true, map[string]string{"class": "gpu"})
	n.Status.Allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"),
		corev1.ResourceMemory: resource.MustParse("8Gi"),
	}
	return n
}

// autoscaleVC is a VirtualCluster whose gpu claim autoscales between min and max.
func autoscaleVC(name string, minNodes, maxNodes int32, scaleDownIdle time.Duration) *v1alpha1.VirtualCluster {
	vc := newVC(name, 1, true, minNodes, maxNodes, 0)
	vc.Spec.PoolClaims[0].Autoscale = &v1alpha1.Autoscale{
		Enabled:       true,
		ScaleDownIdle: &metav1.Duration{Duration: scaleDownIdle},
	}
	return vc
}

// unschedulablePod is a pod in ns that the scheduler could not place for lack of
// resources, asking for cpu.
func unschedulablePod(name, ns, cpu string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:      "app",
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}},
		}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodScheduled,
				Status: corev1.ConditionFalse,
				Reason: corev1.PodReasonUnschedulable,
			}},
		},
	}
}

// daemonPod is a running pod that a DaemonSet keeps on node.
func daemonPod(name, ns, node string) *corev1.Pod {
	p := newPod(name, ns, node)
	p.OwnerReferences = []metav1.OwnerReference{{Kind: "DaemonSet", Name: "agent", APIVersion: "apps/v1", UID: "ds"}}
	return p
}

func leaseOf(t *testing.T, c client.Client, node string) v1alpha1.NodeLease {
	t.Helper()
	var l v1alpha1.NodeLease
	get(t, c, node, &l)
	return l
}

func TestAutoscaleGrantsFreeNodeForPendingPod(t *testing.T) {
	c := newDrainClient(t, interceptor.Funcs{},
		drainPool(),
		allocNode("n1"),
		autoscaleVC("auto", 0, 1, time.Minute),
		unschedulablePod("job", "vc-auto", "2"),
	)
	reconcileAt(t, c, t0)

	l := leaseOf(t, c, "n1")
	if l.Spec.VirtualCluster != "auto" || l.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("lease = %+v, want n1 bound to auto", l)
	}
}

func TestManualClaimIgnoresPendingPods(t *testing.T) {
	c := newDrainClient(t, interceptor.Funcs{},
		drainPool(),
		allocNode("n1"),
		newVC("manual", 1, true, 0, 1, 0),
		unschedulablePod("job", "vc-manual", "2"),
	)
	reconcileAt(t, c, t0)

	var leases v1alpha1.NodeLeaseList
	if err := c.List(context.Background(), &leases); err != nil {
		t.Fatal(err)
	}
	if len(leases.Items) != 0 {
		t.Fatalf("leases = %+v, want none: a manual claim does not scale on pending pods", leases.Items)
	}
}

func TestAutoscaleKeepsNodeWhileWorkloadRuns(t *testing.T) {
	c := newDrainClient(t, interceptor.Funcs{},
		drainPool(),
		boundNode("n1", "auto"),
		newLease("n1", "auto"),
		autoscaleVC("auto", 0, 1, time.Minute),
		newPod("app", "vc-auto", "n1"),
	)
	reconcileAt(t, c, t0)
	reconcileAt(t, c, t0.Add(5*time.Minute))

	l := leaseOf(t, c, "n1")
	if l.Status.Phase != v1alpha1.LeaseBound || l.Status.IdleSince != nil {
		t.Fatalf("lease = %+v, want bound and not idle while a pod runs", l.Status)
	}
}

func TestAutoscaleReleasesNodeAfterScaleDownIdle(t *testing.T) {
	dsPod := daemonPod("agent-n1", "vc-auto", "n1")
	c := newDrainClient(t, interceptor.Funcs{},
		drainPool(),
		boundNode("n1", "auto"),
		newLease("n1", "auto"),
		autoscaleVC("auto", 0, 1, time.Minute),
		dsPod,
	)

	// A DaemonSet pod does not keep the node busy, so the idle clock starts.
	reconcileAt(t, c, t0)
	l := leaseOf(t, c, "n1")
	if l.Status.IdleSince == nil || !l.Status.IdleSince.Time.Equal(t0) {
		t.Fatalf("IdleSince = %v, want %v", l.Status.IdleSince, t0)
	}

	// Not yet idle for scaleDownIdle: the node stays.
	reconcileAt(t, c, t0.Add(30*time.Second))
	if l := leaseOf(t, c, "n1"); l.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("phase = %s before scaleDownIdle, want Bound", l.Status.Phase)
	}

	// Idle for scaleDownIdle: the node is released and drains.
	reconcileAt(t, c, t0.Add(2*time.Minute))
	if l := leaseOf(t, c, "n1"); l.Status.Phase != v1alpha1.LeaseDraining {
		t.Fatalf("phase = %s after scaleDownIdle, want Draining", l.Status.Phase)
	}
}

func TestDemandRoundsUpAgainstNodeSpec(t *testing.T) {
	nodes := []corev1.Node{*allocNode("n2"), *allocNode("n1")}
	pods := []corev1.Pod{
		*unschedulablePod("a", "vc-x", "2"),
		*unschedulablePod("b", "vc-x", "2"),
		*unschedulablePod("c", "vc-x", "2"),
	}
	if got := demandNodes(pods, nodes); got != 2 {
		t.Errorf("demand for 6 CPUs on 4-CPU nodes = %d, want 2", got)
	}

	memHog := unschedulablePod("m", "vc-x", "100m")
	memHog.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse("12Gi")
	if got := demandNodes([]corev1.Pod{*memHog}, nodes); got != 2 {
		t.Errorf("demand for 12Gi on 8Gi nodes = %d, want 2", got)
	}

	running := unschedulablePod("r", "vc-x", "4")
	running.Status.Phase = corev1.PodRunning
	if got := demandNodes([]corev1.Pod{*running}, nodes); got != 0 {
		t.Errorf("demand from a running pod = %d, want 0", got)
	}

	if got := demandNodes(pods, nil); got != 0 {
		t.Errorf("demand on a pool without nodes = %d, want 0", got)
	}
}

func TestAllocationShowsPendingDemandAtMax(t *testing.T) {
	// max 0 leaves no room to grow, so the demand is only reported, not granted.
	c := newDrainClient(t, interceptor.Funcs{},
		drainPool(),
		allocNode("n1"),
		autoscaleVC("auto", 0, 0, time.Minute),
		unschedulablePod("job", "vc-auto", "2"),
	)
	reconcileAt(t, c, t0)

	var vc v1alpha1.VirtualCluster
	get(t, c, "auto", &vc)
	if len(vc.Status.Allocations) != 1 || vc.Status.Allocations[0].Pool != "gpu" || vc.Status.Allocations[0].PendingDemand != 1 {
		t.Fatalf("allocations = %+v, want gpu with pendingDemand 1", vc.Status.Allocations)
	}
	if len(vc.Status.Allocations[0].Nodes) != 0 {
		t.Errorf("nodes = %v, want none", vc.Status.Allocations[0].Nodes)
	}
}
