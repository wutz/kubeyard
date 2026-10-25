// Package host is the host-mode operator's reconciler for NodePools. It keeps
// NodePool.status current and turns the poolClaims of VirtualClusters into
// NodeLeases, following docs/operator.md §2 (node binding) and §3 (allocation).
//
// A node that is preempted or released is marked Draining. The drain step in
// drain.go then evicts the old VirtualCluster's pods after the grace period, and
// moves the node on once it is empty: a preempted node is bound to its claimant,
// a released node is unbound. No node is re-granted while it is still draining.
package host

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wutz/kubeyard/pkg/alloc"
	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/pool"
)

// Node label and taint keys from docs/operator.md §2, and the taint that keeps
// new pods off a node while it drains (§3.4 step 1).
const (
	VCLabel     = "kubeyard.wutz.dev/vcluster"
	PoolLabel   = "kubeyard.wutz.dev/pool"
	DrainingKey = "kubeyard.wutz.dev/draining"
)

// Reconciler reconciles one NodePool per call. The controller runs calls one at
// a time, which gives the per-pool serialisation that §3 requires.
type Reconciler struct {
	client.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// observed is the state of one pool and the claims on it, read from the API.
type observed struct {
	nodes  []corev1.Node
	leases []v1alpha1.NodeLease
	vcs    []v1alpha1.VirtualCluster
	claims []alloc.Claim
}

func (r *Reconciler) observe(ctx context.Context, np *v1alpha1.NodePool) (observed, error) {
	var o observed
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return o, err
	}
	var leases v1alpha1.NodeLeaseList
	if err := r.List(ctx, &leases); err != nil {
		return o, err
	}
	var vcs v1alpha1.VirtualClusterList
	if err := r.List(ctx, &vcs); err != nil {
		return o, err
	}
	o.nodes = nodes.Items
	o.leases = poolLeases(leases.Items, np.Name)
	o.vcs = vcs.Items
	o.claims = buildClaims(np, o.vcs, o.leases)
	return o, nil
}

// Reconcile runs one allocation pass for the pool named in req.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var np v1alpha1.NodePool
	if err := r.Get(ctx, req.NamespacedName, &np); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	o, err := r.observe(ctx, &np)
	if err != nil {
		return ctrl.Result{}, err
	}
	st, err := poolStatus(&np, o)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := alloc.Validate(o.claims, st.Total-st.Unhealthy); err != nil {
		return ctrl.Result{}, fmt.Errorf("pool %s: %w", np.Name, err)
	}

	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.NodeSelector)
	if err != nil {
		return ctrl.Result{}, err
	}
	plan, err := alloc.Reconcile(freeNodes(sel, o.nodes, o.leases), o.claims)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("pool %s: %w", np.Name, err)
	}
	if err := r.apply(ctx, &np, o.claims, o.leases, plan); err != nil {
		return ctrl.Result{}, err
	}
	if o, err = r.observe(ctx, &np); err != nil {
		return ctrl.Result{}, err
	}
	pending, err := r.drain(ctx, &np, o.leases)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Observe again, so that the status and allocations describe what was written.
	if o, err = r.observe(ctx, &np); err != nil {
		return ctrl.Result{}, err
	}
	if st, err = poolStatus(&np, o); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.saveStatus(ctx, &np, st); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.saveAllocations(ctx, &np, o); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.saveNotices(ctx, r.now()); err != nil {
		return ctrl.Result{}, err
	}
	// Pods leave a draining node on their own schedule, and no watch sees that,
	// so a pool with draining nodes is polled again.
	if pending {
		return ctrl.Result{RequeueAfter: drainPoll}, nil
	}
	return ctrl.Result{}, nil
}

// poolLeases keeps the leases of one pool that are not yet released.
func poolLeases(all []v1alpha1.NodeLease, poolName string) []v1alpha1.NodeLease {
	var out []v1alpha1.NodeLease
	for _, l := range all {
		if l.Spec.Pool == poolName && l.Status.Phase != v1alpha1.LeaseReleased {
			out = append(out, l)
		}
	}
	return out
}

// buildClaims turns the VirtualClusters' poolClaims on this pool into alloc claims.
// The pool's preemption policy Never switches preemption off for every claim.
func buildClaims(np *v1alpha1.NodePool, vcs []v1alpha1.VirtualCluster, leases []v1alpha1.NodeLease) []alloc.Claim {
	var out []alloc.Claim
	for i := range vcs {
		vc := &vcs[i]
		for _, pc := range vc.Spec.PoolClaims {
			if pc.Pool != np.Name {
				continue
			}
			c := alloc.Claim{
				VC:          vc.Name,
				Priority:    int(vc.Spec.Priority),
				Created:     vc.CreationTimestamp.Time,
				Preemptible: vc.Spec.Preemptible && np.Spec.Preemption.Policy != v1alpha1.PreemptionNever,
				Min:         int(pc.Min),
				Max:         int(pc.Max),
				Desired:     int(pc.Desired),
				Pinned:      pc.PinnedNodes,
				Autoscale:   pc.Autoscale != nil && pc.Autoscale.Enabled,
			}
			for _, l := range leases {
				if l.Spec.VirtualCluster == vc.Name {
					c.Current = append(c.Current, alloc.Lease{
						Node:     l.Spec.Node,
						BoundAt:  boundAt(&l),
						Draining: l.Status.Phase == v1alpha1.LeaseDraining,
					})
				}
			}
			out = append(out, c)
		}
	}
	return out
}

func boundAt(l *v1alpha1.NodeLease) time.Time {
	if l.Status.BoundAt == nil {
		return time.Time{}
	}
	return l.Status.BoundAt.Time
}

// poolStatus computes NodePool.status. reserved is the sum of min over the claims.
func poolStatus(np *v1alpha1.NodePool, o observed) (pool.Status, error) {
	reserved := 0
	for _, c := range o.claims {
		reserved += c.Min
	}
	return pool.Compute(np.Spec.NodeSelector, o.nodes, leasedNames(o.leases), reserved)
}

func leasedNames(leases []v1alpha1.NodeLease) []string {
	out := make([]string, 0, len(leases))
	for _, l := range leases {
		out = append(out, l.Spec.Node)
	}
	return out
}

// freeNodes lists the ready nodes that match the selector and have no lease.
func freeNodes(sel labels.Selector, nodes []corev1.Node, leases []v1alpha1.NodeLease) []string {
	leased := map[string]bool{}
	for _, n := range leasedNames(leases) {
		leased[n] = true
	}
	var out []string
	for i := range nodes {
		n := &nodes[i]
		if sel.Matches(labels.Set(n.Labels)) && !leased[n.Name] && isReady(n) {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}

func isReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (r *Reconciler) saveStatus(ctx context.Context, np *v1alpha1.NodePool, st pool.Status) error {
	want := v1alpha1.NodePoolStatus{
		Total:     int32(st.Total),
		Free:      int32(st.Free),
		Reserved:  int32(st.Reserved),
		Unhealthy: int32(st.Unhealthy),
	}
	if np.Status == want {
		return nil
	}
	np.Status = want
	return r.Status().Update(ctx, np)
}

// apply writes the plan. Granted free nodes get a new Bound lease and the node
// binding of §2. A preempted node keeps its lease, which moves to the claimant
// as Draining. A released node and a lease whose claim is gone are set to
// Draining with a deadline.
func (r *Reconciler) apply(ctx context.Context, np *v1alpha1.NodePool, claims []alloc.Claim, leases []v1alpha1.NodeLease, plan alloc.Plan) error {
	byNode := map[string]*v1alpha1.NodeLease{}
	for i := range leases {
		byNode[leases[i].Spec.Node] = &leases[i]
	}
	pinned := map[string]map[string]bool{}
	for _, c := range claims {
		pinned[c.VC] = map[string]bool{}
		for _, n := range c.Pinned {
			pinned[c.VC][n] = true
		}
	}
	preempted := map[string]bool{}
	for _, v := range plan.Preempt {
		preempted[v.Node] = true
	}

	now := r.now()
	for vc, nodes := range plan.Grant {
		for _, node := range nodes {
			if preempted[node] {
				continue
			}
			reason := v1alpha1.LeaseScale
			if pinned[vc][node] {
				reason = v1alpha1.LeasePinned
			}
			lease := &v1alpha1.NodeLease{
				ObjectMeta: metav1.ObjectMeta{Name: node},
				Spec:       v1alpha1.NodeLeaseSpec{Node: node, Pool: np.Name, VirtualCluster: vc, Reason: reason},
			}
			if err := r.Create(ctx, lease); err != nil {
				return fmt.Errorf("bind %s to %s: %w", node, vc, err)
			}
			lease.Status = v1alpha1.NodeLeaseStatus{Phase: v1alpha1.LeaseBound, BoundAt: &metav1.Time{Time: now}}
			if err := r.Status().Update(ctx, lease); err != nil {
				return err
			}
			if err := r.bindNode(ctx, node, vc, np.Name); err != nil {
				return err
			}
		}
	}

	// The deadline is the forced-deletion time: grace (the notice window), then
	// drain timeout. drainStart in drain.go works back from it.
	deadline := now.Add(durationOr(np.Spec.Preemption.GracePeriod, 0) + durationOr(np.Spec.Preemption.DrainTimeout, 0))
	for _, v := range plan.Preempt {
		l := byNode[v.Node]
		if l == nil {
			return fmt.Errorf("preempt %s: no lease", v.Node)
		}
		victim := l.Spec.VirtualCluster
		spec := l.Spec
		spec.VirtualCluster = v.ForVC
		spec.Reason = v1alpha1.LeasePreempt
		if err := r.updateLease(ctx, l, spec, draining(l, deadline)); err != nil {
			return err
		}
		if err := r.notifyPreempted(ctx, victim, v.ForVC, v.Node, deadline, now); err != nil {
			return err
		}
	}

	for vc, nodes := range plan.Release {
		for _, node := range nodes {
			l := byNode[node]
			if l == nil || l.Spec.VirtualCluster != vc {
				return fmt.Errorf("release %s from %s: no matching lease", node, vc)
			}
			if err := r.updateLease(ctx, l, l.Spec, draining(l, deadline)); err != nil {
				return err
			}
		}
	}

	// A lease whose claim on this pool is gone can no longer be served; drain it.
	claimed := map[string]bool{}
	for _, c := range claims {
		claimed[c.VC] = true
	}
	for i := range leases {
		l := &leases[i]
		if claimed[l.Spec.VirtualCluster] || l.Status.Phase == v1alpha1.LeaseDraining || l.Status.Phase == v1alpha1.LeaseReleased {
			continue
		}
		if err := r.updateLease(ctx, l, l.Spec, draining(l, deadline)); err != nil {
			return err
		}
	}
	return nil
}

// draining is the status of a lease that is being emptied, keeping its BoundAt.
func draining(l *v1alpha1.NodeLease, deadline time.Time) v1alpha1.NodeLeaseStatus {
	d := metav1.Time{Time: deadline}
	return v1alpha1.NodeLeaseStatus{
		Phase:         v1alpha1.LeaseDraining,
		BoundAt:       l.Status.BoundAt,
		DrainDeadline: &d,
	}
}

// updateLease writes spec and status. Update returns the stored status, so the
// desired status is set again before the status write.
func (r *Reconciler) updateLease(ctx context.Context, l *v1alpha1.NodeLease, spec v1alpha1.NodeLeaseSpec, status v1alpha1.NodeLeaseStatus) error {
	l.Spec = spec
	if err := r.Update(ctx, l); err != nil {
		return err
	}
	l.Status = status
	return r.Status().Update(ctx, l)
}

// bindNode puts the VC label, pool label, and NoSchedule taint on a node
// (docs/operator.md §2, steps 1 and 2). Binding ends a drain, so the draining
// taint is removed too.
func (r *Reconciler) bindNode(ctx context.Context, name, vc, poolName string) error {
	return r.patchNode(ctx, name, func(n *corev1.Node) {
		if n.Labels == nil {
			n.Labels = map[string]string{}
		}
		n.Labels[VCLabel] = vc
		n.Labels[PoolLabel] = poolName
		taints := withoutTaint(withoutTaint(n.Spec.Taints, VCLabel), DrainingKey)
		n.Spec.Taints = append(taints, corev1.Taint{Key: VCLabel, Value: vc, Effect: corev1.TaintEffectNoSchedule})
	})
}

// unbindNode takes the labels and taints of a binding off a node (§2, reverse
// order), so the node is free again.
func (r *Reconciler) unbindNode(ctx context.Context, name string) error {
	return r.patchNode(ctx, name, func(n *corev1.Node) {
		delete(n.Labels, VCLabel)
		delete(n.Labels, PoolLabel)
		n.Spec.Taints = withoutTaint(withoutTaint(n.Spec.Taints, VCLabel), DrainingKey)
	})
}

// patchNode applies change to a node and patches the fields that changed.
func (r *Reconciler) patchNode(ctx context.Context, name string, change func(*corev1.Node)) error {
	var node corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
		return err
	}
	orig := node.DeepCopy()
	change(&node)
	if reflect.DeepEqual(orig.Labels, node.Labels) && reflect.DeepEqual(orig.Spec.Taints, node.Spec.Taints) {
		return nil
	}
	return r.Patch(ctx, &node, client.MergeFrom(orig))
}

func withoutTaint(taints []corev1.Taint, key string) []corev1.Taint {
	var out []corev1.Taint
	for _, t := range taints {
		if t.Key != key {
			out = append(out, t)
		}
	}
	return out
}

// saveAllocations rewrites each VirtualCluster's allocation entry for this pool
// from the leases as they are now. Draining nodes are still listed, because the
// claim still holds them until they are drained.
func (r *Reconciler) saveAllocations(ctx context.Context, np *v1alpha1.NodePool, o observed) error {
	nodesByVC := map[string][]string{}
	for _, l := range o.leases {
		nodesByVC[l.Spec.VirtualCluster] = append(nodesByVC[l.Spec.VirtualCluster], l.Spec.Node)
	}
	for i := range o.vcs {
		vc := &o.vcs[i]
		var entries []v1alpha1.PoolAllocation
		for _, a := range vc.Status.Allocations {
			if a.Pool != np.Name {
				entries = append(entries, a)
			}
		}
		if nodes := nodesByVC[vc.Name]; len(nodes) > 0 {
			sort.Strings(nodes)
			entries = append(entries, v1alpha1.PoolAllocation{Pool: np.Name, Nodes: nodes})
		}
		if reflect.DeepEqual(entries, vc.Status.Allocations) {
			continue
		}
		orig := vc.DeepCopy()
		vc.Status.Allocations = entries
		if err := r.Status().Patch(ctx, vc, client.MergeFrom(orig)); err != nil {
			return err
		}
	}
	return nil
}

func durationOr(d *metav1.Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return d.Duration
}
