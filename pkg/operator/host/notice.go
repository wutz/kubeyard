package host

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// PreemptingCondition is the VirtualCluster condition that tells its admin nodes
// are being reclaimed (docs/operator.md §3.4 step 2).
const (
	PreemptingCondition = "Preempting"
	preemptingReason    = "NodesReclaimed"
	preemptingEvent     = "Preempting"
)

// notifyPreempted records a Warning Event on the victim VirtualCluster when one
// of its nodes is taken for another. The Event lives in "default", because a
// VirtualCluster is cluster-scoped.
func (r *Reconciler) notifyPreempted(ctx context.Context, victim, claimant, node string, deadline, now time.Time) error {
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kubeyard-preempt-",
			Namespace:    metav1.NamespaceDefault,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "VirtualCluster",
			Name:       victim,
		},
		Type:           corev1.EventTypeWarning,
		Reason:         preemptingEvent,
		Message:        fmt.Sprintf("节点 %s 将于 %s 前回收，转给 %s", node, deadline.Format(time.RFC3339), claimant),
		FirstTimestamp: metav1.NewTime(now),
		LastTimestamp:  metav1.NewTime(now),
		Count:          1,
		Source:         corev1.EventSource{Component: "kubeyard-operator"},
	}
	return r.Create(ctx, ev)
}

// saveNotices sets the Preempting condition on each VirtualCluster that is losing
// a node to a preemption which has not finished. The condition is derived from
// every pool's leases and nodes, so pools cannot overwrite each other's notices,
// and it clears itself once the handover ends the drain.
//
// A draining lease that was preempted names the claimant in its spec. The node
// label still names the victim until the handover, so the victim is read there.
func (r *Reconciler) saveNotices(ctx context.Context, now time.Time) error {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return err
	}
	var leases v1alpha1.NodeLeaseList
	if err := r.List(ctx, &leases); err != nil {
		return err
	}
	var vcs v1alpha1.VirtualClusterList
	if err := r.List(ctx, &vcs); err != nil {
		return err
	}
	owner := map[string]string{}
	for _, n := range nodes.Items {
		owner[n.Name] = n.Labels[VCLabel]
	}

	type notice struct {
		nodes    []string
		deadline time.Time
	}
	byVictim := map[string]*notice{}
	for _, l := range leases.Items {
		if l.Status.Phase != v1alpha1.LeaseDraining || l.Spec.Reason != v1alpha1.LeasePreempt {
			continue
		}
		victim := owner[l.Spec.Node]
		if victim == "" || victim == l.Spec.VirtualCluster {
			continue
		}
		n := byVictim[victim]
		if n == nil {
			n = &notice{}
			byVictim[victim] = n
		}
		n.nodes = append(n.nodes, l.Spec.Node)
		if l.Status.DrainDeadline != nil && (n.deadline.IsZero() || l.Status.DrainDeadline.Time.Before(n.deadline)) {
			n.deadline = l.Status.DrainDeadline.Time
		}
	}

	for i := range vcs.Items {
		vc := &vcs.Items[i]
		orig := vc.DeepCopy()
		if n := byVictim[vc.Name]; n != nil {
			sort.Strings(n.nodes)
			meta.SetStatusCondition(&vc.Status.Conditions, metav1.Condition{
				Type:    PreemptingCondition,
				Status:  metav1.ConditionTrue,
				Reason:  preemptingReason,
				Message: fmt.Sprintf("%d 个节点将于 %s 前回收：%s", len(n.nodes), n.deadline.Format(time.RFC3339), strings.Join(n.nodes, ", ")),
			})
		} else {
			meta.RemoveStatusCondition(&vc.Status.Conditions, PreemptingCondition)
		}
		if equalConditions(orig.Status.Conditions, vc.Status.Conditions) {
			continue
		}
		if err := r.Status().Patch(ctx, vc, client.MergeFrom(orig)); err != nil {
			return err
		}
	}
	return nil
}

// equalConditions compares the fields a notice sets. LastTransitionTime is left
// out, since SetStatusCondition keeps it when the status does not change.
func equalConditions(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Status != b[i].Status || a[i].Reason != b[i].Reason || a[i].Message != b[i].Message {
			return false
		}
	}
	return true
}
