package host

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// drainPool is the gpu pool with a five minute drain timeout, so a drain runs
// for one grace period (a minute) and then the timeout.
func drainPool() *v1alpha1.NodePool {
	np := newPool(v1alpha1.PreemptionLowerPriority)
	np.Spec.Preemption.DrainTimeout = &metav1.Duration{Duration: 5 * time.Minute}
	return np
}

// boundNode is a ready gpu node that the VC vc has labelled and tainted.
func boundNode(name, vc string) *corev1.Node {
	n := newNode(name, true, map[string]string{"class": "gpu", VCLabel: vc, PoolLabel: "gpu"})
	n.Spec.Taints = []corev1.Taint{{Key: VCLabel, Value: vc, Effect: corev1.TaintEffectNoSchedule}}
	return n
}

func newPod(name, namespace, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func newDrainClient(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) client.Client {
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
		WithInterceptorFuncs(funcs).
		Build()
}

// reconcileAt runs one pass for the gpu pool at the given time.
func reconcileAt(t *testing.T, c client.Client, now time.Time) ctrl.Result {
	t.Helper()
	r := &Reconciler{Client: c, Now: func() time.Time { return now }}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "gpu"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func hasDrainingTaint(n corev1.Node) bool {
	for _, taint := range n.Spec.Taints {
		if taint.Key == DrainingKey {
			return true
		}
	}
	return false
}

func podExists(t *testing.T, c client.Client, name, namespace string) bool {
	t.Helper()
	var p corev1.Pod
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &p)
	if apierrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

// preemptionScene has low on n1 with one pod, and high, which outranks it, waiting.
func preemptionScene(t *testing.T, funcs interceptor.Funcs) client.Client {
	return newDrainClient(t, funcs,
		drainPool(),
		newVC("low", 1, true, 0, 1, 1),
		newVC("high", 5, false, 0, 1, 1),
		boundNode("n1", "low"),
		newLease("n1", "low"),
		newPod("p1", "vc-low", "n1"),
	)
}

func TestDrainWaitsForGracePeriod(t *testing.T) {
	c := preemptionScene(t, interceptor.Funcs{})

	res := reconcileAt(t, c, t0)
	if res.RequeueAfter != drainPoll {
		t.Fatalf("requeue = %v, want %v while the node drains", res.RequeueAfter, drainPoll)
	}
	var n corev1.Node
	get(t, c, "n1", &n)
	if !hasDrainingTaint(n) {
		t.Fatalf("node taints = %v, want the draining taint at once", n.Spec.Taints)
	}

	reconcileAt(t, c, t0.Add(30*time.Second))
	if !podExists(t, c, "p1", "vc-low") {
		t.Fatal("pod p1 evicted inside the grace period")
	}
	get(t, c, "n1", &n)
	if !hasTaint(n, "low") {
		t.Fatalf("node taints = %v, want the low VC taint until the handover", n.Spec.Taints)
	}
}

func TestDrainEvictsThenHandsNodeToClaimant(t *testing.T) {
	c := preemptionScene(t, interceptor.Funcs{})
	reconcileAt(t, c, t0)

	reconcileAt(t, c, t0.Add(time.Minute))
	if podExists(t, c, "p1", "vc-low") {
		t.Fatal("pod p1 still there after the grace period")
	}

	var l v1alpha1.NodeLease
	get(t, c, "n1", &l)
	if l.Spec.VirtualCluster != "high" || l.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("lease = %+v, want high / Bound after the drain", l)
	}
	if l.Status.BoundAt == nil || !l.Status.BoundAt.Time.Equal(t0.Add(time.Minute)) {
		t.Fatalf("boundAt = %v, want the handover time", l.Status.BoundAt)
	}
	if l.Status.DrainDeadline != nil {
		t.Fatalf("drainDeadline = %v, want it cleared", l.Status.DrainDeadline)
	}

	var n corev1.Node
	get(t, c, "n1", &n)
	if n.Labels[VCLabel] != "high" || !hasTaint(n, "high") || hasTaint(n, "low") || hasDrainingTaint(n) {
		t.Fatalf("node labels = %v taints = %v, want high bound with no draining taint", n.Labels, n.Spec.Taints)
	}
}

func TestBlockedEvictionIsForcedAtDeadline(t *testing.T) {
	// A PodDisruptionBudget refuses every eviction, so only the forced deletion at
	// the deadline can empty the node.
	c := preemptionScene(t, interceptor.Funcs{
		SubResourceCreate: func(ctx context.Context, cl client.Client, name string, obj client.Object, sub client.Object, opts ...client.SubResourceCreateOption) error {
			if name == "eviction" {
				return apierrors.NewTooManyRequests("blocked by a PodDisruptionBudget", 10)
			}
			return cl.SubResource(name).Create(ctx, obj, sub, opts...)
		},
	})
	reconcileAt(t, c, t0)

	reconcileAt(t, c, t0.Add(time.Minute))
	if !podExists(t, c, "p1", "vc-low") {
		t.Fatal("pod p1 gone while its eviction was blocked")
	}
	var l v1alpha1.NodeLease
	get(t, c, "n1", &l)
	if l.Spec.VirtualCluster != "high" || l.Status.Phase != v1alpha1.LeaseDraining {
		t.Fatalf("lease = %+v, want high / Draining while the eviction is blocked", l)
	}

	// The deadline is grace plus drain timeout, six minutes after t0.
	reconcileAt(t, c, t0.Add(6*time.Minute))
	if podExists(t, c, "p1", "vc-low") {
		t.Fatal("pod p1 still there at the drain deadline")
	}
	get(t, c, "n1", &l)
	if l.Spec.VirtualCluster != "high" || l.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("lease = %+v, want high / Bound after the forced drain", l)
	}
}

func TestReleasedNodeIsUnboundThenRegranted(t *testing.T) {
	// a no longer wants its node, and b does. The node is only free after a's drain.
	c := newDrainClient(t, interceptor.Funcs{},
		drainPool(),
		newVC("a", 1, false, 0, 0, 0),
		newVC("b", 1, false, 0, 1, 1),
		boundNode("n1", "a"),
		newLease("n1", "a"),
	)
	reconcileAt(t, c, t0)

	var n corev1.Node
	get(t, c, "n1", &n)
	if !hasDrainingTaint(n) {
		t.Fatalf("node taints = %v, want the draining taint on a released node", n.Spec.Taints)
	}
	var none v1alpha1.NodeLease
	get(t, c, "n1", &none)
	if none.Spec.VirtualCluster != "a" || none.Status.Phase != v1alpha1.LeaseDraining {
		t.Fatalf("lease = %+v, want a / Draining until the grace period ends", none)
	}

	reconcileAt(t, c, t0.Add(time.Minute))
	var gone v1alpha1.NodeLease
	if err := c.Get(context.Background(), client.ObjectKey{Name: "n1"}, &gone); !apierrors.IsNotFound(err) {
		t.Fatalf("lease n1 err = %v, want it deleted once the node is free", err)
	}
	get(t, c, "n1", &n)
	if _, ok := n.Labels[VCLabel]; ok {
		t.Fatalf("node labels = %v, want the VC label removed", n.Labels)
	}
	if len(n.Spec.Taints) != 0 {
		t.Fatalf("node taints = %v, want none left", n.Spec.Taints)
	}

	// The node was free at the start of this pass, so b gets it now.
	reconcileAt(t, c, t0.Add(time.Minute))
	var l v1alpha1.NodeLease
	get(t, c, "n1", &l)
	if l.Spec.VirtualCluster != "b" || l.Status.Phase != v1alpha1.LeaseBound {
		t.Fatalf("lease = %+v, want b / Bound on the next pass", l)
	}
}
