package host

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// drainPoll is how often a pool with draining nodes is checked again.
const drainPoll = 10 * time.Second

// hostNamespace is where a VirtualCluster's control plane and synced pods run
// (docs/operator.md §1).
func hostNamespace(vc string) string {
	return "vc-" + vc
}

// drain advances the Draining leases of one pool and reports whether any of them
// is still draining. The steps are docs/operator.md §3.4: the draining taint, a
// grace period, eviction, forced deletion at the deadline, then the handover.
func (r *Reconciler) drain(ctx context.Context, np *v1alpha1.NodePool, leases []v1alpha1.NodeLease) (bool, error) {
	now := r.now()
	drainTimeout := durationOr(np.Spec.Preemption.DrainTimeout, 0)
	pending := false
	for i := range leases {
		if leases[i].Status.Phase != v1alpha1.LeaseDraining {
			continue
		}
		// The cached list can be behind finishDrain, so act only on the lease as the
		// API server has it now.
		var l v1alpha1.NodeLease
		if err := r.live().Get(ctx, client.ObjectKey{Name: leases[i].Name}, &l); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		if l.Status.Phase != v1alpha1.LeaseDraining {
			continue
		}
		done, err := r.drainNode(ctx, &l, now, drainTimeout)
		if err != nil {
			return false, err
		}
		pending = pending || !done
	}
	return pending, nil
}

// drainNode works on one draining lease and reports whether its node is finished.
// The owner is the VirtualCluster named by the node's label. For a preempted node
// that is the old owner, because the label is only rewritten at the handover.
func (r *Reconciler) drainNode(ctx context.Context, l *v1alpha1.NodeLease, now time.Time, drainTimeout time.Duration) (bool, error) {
	var node corev1.Node
	if err := r.live().Get(ctx, client.ObjectKey{Name: l.Spec.Node}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			// A node that no longer exists has nothing left to drain.
			return true, client.IgnoreNotFound(r.Delete(ctx, l))
		}
		return false, err
	}
	if err := r.markDraining(ctx, node.Name); err != nil {
		return false, err
	}

	owner := node.Labels[VCLabel]
	deadline := now
	if l.Status.DrainDeadline != nil {
		deadline = l.Status.DrainDeadline.Time
	}
	// The grace period is the notice window for the owner, so it runs out even when
	// the node is already empty.
	evictAt := deadline.Add(-drainTimeout)
	if now.Before(evictAt) {
		return false, nil
	}
	if err := r.evictOwner(ctx, node.Name, owner, !now.Before(deadline)); err != nil {
		return false, err
	}
	pods, err := r.activePodsOn(ctx, node.Name, owner)
	if err != nil {
		return false, err
	}
	if len(pods) > 0 {
		return false, nil
	}
	return true, r.finishDrain(ctx, l, owner, now)
}

// finishDrain runs once the owner's pods are gone. If the node is still labelled
// for someone other than the lease's VirtualCluster, the lease was preempted: the
// claimant gets the node and the lease is Bound. Otherwise the lease was released,
// so the node is unbound and the lease is deleted.
//
// Deleting a released lease skips the UsageReport that §1 requires before deletion.
// Metering comes in M4.
func (r *Reconciler) finishDrain(ctx context.Context, l *v1alpha1.NodeLease, owner string, now time.Time) error {
	if owner != "" && owner != l.Spec.VirtualCluster {
		if err := r.bindNode(ctx, l.Spec.Node, l.Spec.VirtualCluster, l.Spec.Pool); err != nil {
			return err
		}
		return r.updateLease(ctx, l, l.Spec, v1alpha1.NodeLeaseStatus{
			Phase:   v1alpha1.LeaseBound,
			BoundAt: &metav1.Time{Time: now},
		})
	}
	if err := r.unbindNode(ctx, l.Spec.Node); err != nil {
		return err
	}
	return client.IgnoreNotFound(r.Delete(ctx, l))
}

// evictOwner removes the owner's pods from node. Before the deadline it uses the
// Eviction API, which honours PodDisruptionBudgets: a blocked eviction is retried
// on a later pass. From the deadline on, the pods are deleted without grace (§3.4
// step 4).
func (r *Reconciler) evictOwner(ctx context.Context, node, owner string, force bool) error {
	pods, err := r.activePodsOn(ctx, node, owner)
	if err != nil {
		return err
	}
	for i := range pods {
		p := &pods[i]
		var opErr error
		if force {
			opErr = r.Delete(ctx, p, client.GracePeriodSeconds(0))
		} else {
			ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace}}
			opErr = r.SubResource("eviction").Create(ctx, p, ev)
		}
		if opErr != nil && !apierrors.IsNotFound(opErr) && !apierrors.IsTooManyRequests(opErr) {
			return opErr
		}
	}
	return nil
}

// activePodsOn lists the owner's pods on node that have not finished. Terminating
// pods count, because the node is not empty until they are gone.
func (r *Reconciler) activePodsOn(ctx context.Context, node, owner string) ([]corev1.Pod, error) {
	if owner == "" {
		return nil, nil
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(hostNamespace(owner))); err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for _, p := range pods.Items {
		if p.Spec.NodeName != node || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// markDraining adds the draining taint, so no new pod lands on the node while it
// empties (§3.4 step 1).
func (r *Reconciler) markDraining(ctx context.Context, name string) error {
	return r.patchNode(ctx, name, func(n *corev1.Node) {
		for _, t := range n.Spec.Taints {
			if t.Key == DrainingKey {
				return
			}
		}
		n.Spec.Taints = append(n.Spec.Taints, corev1.Taint{Key: DrainingKey, Effect: corev1.TaintEffectNoSchedule})
	})
}
