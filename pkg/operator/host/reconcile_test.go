package host

import (
	"context"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

var t0 = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

var gpu = map[string]string{"class": "gpu"}

func newNode(name string, ready bool, nodeLabels map[string]string) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: nodeLabels},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: status},
		}},
	}
}

// newPool is the gpu pool. Its grace period is one minute.
func newPool(policy v1alpha1.PreemptionPolicy) *v1alpha1.NodePool {
	return &v1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu"},
		Spec: v1alpha1.NodePoolSpec{
			NodeSelector: metav1.LabelSelector{MatchLabels: gpu},
			Preemption: v1alpha1.PreemptionSpec{
				Policy:      policy,
				GracePeriod: &metav1.Duration{Duration: time.Minute},
			},
		},
	}
}

func newVC(name string, priority int32, preemptible bool, minNodes, maxNodes, desired int32) *v1alpha1.VirtualCluster {
	return &v1alpha1.VirtualCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.VirtualClusterSpec{
			Priority:    priority,
			Preemptible: preemptible,
			Provider:    "vcluster",
			Mode:        "dedicated-nodes",
			PoolClaims: []v1alpha1.PoolClaim{
				{Pool: "gpu", Min: minNodes, Max: maxNodes, Desired: desired},
			},
		},
	}
}

func newLeaseAt(node, vc string, at time.Time) *v1alpha1.NodeLease {
	return &v1alpha1.NodeLease{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec:       v1alpha1.NodeLeaseSpec{Node: node, Pool: "gpu", VirtualCluster: vc},
		Status:     v1alpha1.NodeLeaseStatus{Phase: v1alpha1.LeaseBound, BoundAt: &metav1.Time{Time: at}},
	}
}

func newLease(node, vc string) *v1alpha1.NodeLease {
	return newLeaseAt(node, vc, t0)
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.NodePool{}, &v1alpha1.NodeLease{}, &v1alpha1.VirtualCluster{}).
		Build()
}

// runPass runs one pass for the gpu pool with a fixed clock.
func runPass(c client.Client) error {
	r := &Reconciler{Client: c, Now: func() time.Time { return t0 }}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "gpu"}})
	return err
}

func get(t *testing.T, c client.Client, name string, obj client.Object) {
	t.Helper()
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, obj); err != nil {
		t.Fatal(err)
	}
}

func hasTaint(n corev1.Node, vc string) bool {
	for _, taint := range n.Spec.Taints {
		if taint.Key == VCLabel && taint.Value == vc && taint.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func TestBindsFreeReadyNodes(t *testing.T) {
	c := newClient(t,
		newPool(v1alpha1.PreemptionLowerPriority),
		newVC("a", 5, false, 0, 2, 2),
		newNode("n1", true, gpu),
		newNode("n2", true, gpu),
		newNode("n3", true, nil),
		newNode("n4", false, gpu),
	)
	if err := runPass(c); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"n1", "n2"} {
		var l v1alpha1.NodeLease
		get(t, c, name, &l)
		if l.Spec.VirtualCluster != "a" || l.Spec.Pool != "gpu" || l.Status.Phase != v1alpha1.LeaseBound {
			t.Fatalf("lease %s = %+v", name, l)
		}
		var n corev1.Node
		get(t, c, name, &n)
		if n.Labels[VCLabel] != "a" || n.Labels[PoolLabel] != "gpu" {
			t.Fatalf("node %s labels = %v", name, n.Labels)
		}
		if !hasTaint(n, "a") {
			t.Fatalf("node %s taints = %v, want the vc taint", name, n.Spec.Taints)
		}
	}

	var leases v1alpha1.NodeLeaseList
	if err := c.List(context.Background(), &leases); err != nil {
		t.Fatal(err)
	}
	if len(leases.Items) != 2 {
		t.Fatalf("leases = %d, want 2: n3 is outside the selector and n4 is not ready", len(leases.Items))
	}

	var np v1alpha1.NodePool
	get(t, c, "gpu", &np)
	want := v1alpha1.NodePoolStatus{Total: 3, Free: 0, Reserved: 0, Unhealthy: 1}
	if np.Status != want {
		t.Fatalf("pool status = %+v, want %+v", np.Status, want)
	}

	var vc v1alpha1.VirtualCluster
	get(t, c, "a", &vc)
	wantAlloc := []v1alpha1.PoolAllocation{{Pool: "gpu", Nodes: []string{"n1", "n2"}}}
	if !reflect.DeepEqual(vc.Status.Allocations, wantAlloc) {
		t.Fatalf("allocations = %+v, want %+v", vc.Status.Allocations, wantAlloc)
	}
}

func TestPinnedNodeIsGrantedFirst(t *testing.T) {
	claim := newVC("a", 5, false, 0, 1, 1)
	claim.Spec.PoolClaims[0].PinnedNodes = []string{"n2"}
	c := newClient(t,
		newPool(v1alpha1.PreemptionLowerPriority),
		claim,
		newNode("n1", true, gpu),
		newNode("n2", true, gpu),
	)
	if err := runPass(c); err != nil {
		t.Fatal(err)
	}

	var l v1alpha1.NodeLease
	get(t, c, "n2", &l)
	if l.Spec.Reason != v1alpha1.LeasePinned {
		t.Fatalf("n2 reason = %q, want Pinned", l.Spec.Reason)
	}
	var none v1alpha1.NodeLease
	if err := c.Get(context.Background(), client.ObjectKey{Name: "n1"}, &none); err == nil {
		t.Fatalf("n1 leased, but desired 1 is already met by the pinned n2")
	}
}

func TestPreemptionTransfersLeaseAsDraining(t *testing.T) {
	c := newClient(t,
		newPool(v1alpha1.PreemptionLowerPriority),
		newVC("low", 1, true, 0, 1, 1),
		newVC("high", 5, false, 0, 1, 1),
		newNode("n1", true, gpu),
		newLease("n1", "low"),
	)
	if err := runPass(c); err != nil {
		t.Fatal(err)
	}

	var l v1alpha1.NodeLease
	get(t, c, "n1", &l)
	if l.Spec.VirtualCluster != "high" || l.Spec.Reason != v1alpha1.LeasePreempt || l.Status.Phase != v1alpha1.LeaseDraining {
		t.Fatalf("lease = %+v, want high / Preempt / Draining", l)
	}
	if got := l.Status.DrainDeadline; got == nil || !got.Time.Equal(t0.Add(time.Minute)) {
		t.Fatalf("drain deadline = %v, want %v", got, t0.Add(time.Minute))
	}
}

func TestPoolPolicyNeverBlocksPreemption(t *testing.T) {
	c := newClient(t,
		newPool(v1alpha1.PreemptionNever),
		newVC("low", 1, true, 0, 1, 1),
		newVC("high", 5, false, 0, 1, 1),
		newNode("n1", true, gpu),
		newLease("n1", "low"),
	)
	if err := runPass(c); err != nil {
		t.Fatal(err)
	}

	var l v1alpha1.NodeLease
	get(t, c, "n1", &l)
	if l.Spec.VirtualCluster != "low" || l.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("lease = %+v, want low / Bound: policy Never takes no nodes", l)
	}
}

func TestSurplusLeaseIsDrainedNotDeleted(t *testing.T) {
	c := newClient(t,
		newPool(v1alpha1.PreemptionLowerPriority),
		newVC("a", 1, false, 0, 3, 1),
		newNode("n1", true, gpu),
		newNode("n2", true, gpu),
		newLease("n1", "a"),
		newLeaseAt("n2", "a", t0.Add(time.Hour)),
	)
	if err := runPass(c); err != nil {
		t.Fatal(err)
	}

	var newest v1alpha1.NodeLease
	get(t, c, "n2", &newest)
	if newest.Status.Phase != v1alpha1.LeaseDraining || newest.Spec.VirtualCluster != "a" {
		t.Fatalf("n2 lease = %+v, want Draining under a", newest)
	}
	var kept v1alpha1.NodeLease
	get(t, c, "n1", &kept)
	if kept.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("n1 lease = %+v, want Bound", kept)
	}
}

// A lease that was preempted and is later released for a smaller desired size
// drains for that reason, so its reason is Scale, not the old Preempt.
func TestReleasedLeaseTakesScaleReason(t *testing.T) {
	stale := newLeaseAt("n2", "a", t0.Add(time.Hour))
	stale.Spec.Reason = v1alpha1.LeasePreempt
	c := newClient(t,
		newPool(v1alpha1.PreemptionLowerPriority),
		newVC("a", 1, false, 0, 3, 1),
		newNode("n1", true, gpu),
		newNode("n2", true, gpu),
		newLease("n1", "a"),
		stale,
	)
	if err := runPass(c); err != nil {
		t.Fatal(err)
	}
	var released v1alpha1.NodeLease
	get(t, c, "n2", &released)
	if released.Status.Phase != v1alpha1.LeaseDraining || released.Spec.Reason != v1alpha1.LeaseScale {
		t.Fatalf("n2 lease = %+v, want Draining with reason Scale", released)
	}
}

func TestMinAboveHealthyNodesIsRejected(t *testing.T) {
	c := newClient(t,
		newPool(v1alpha1.PreemptionLowerPriority),
		newVC("a", 5, false, 2, 2, 2),
		newNode("n1", true, gpu),
	)
	if err := runPass(c); err == nil {
		t.Fatal("want an error: min 2 exceeds the 1 healthy node")
	}
	var leases v1alpha1.NodeLeaseList
	if err := c.List(context.Background(), &leases); err != nil {
		t.Fatal(err)
	}
	if len(leases.Items) != 0 {
		t.Fatalf("leases = %+v, want none after a rejected pass", leases.Items)
	}
}
