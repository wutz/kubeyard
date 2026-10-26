package host

import (
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wutz/kubeyard/pkg/alloc"
	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// Preview is what one change to a claim's desired size would do, worked out the
// way Reconcile works the pool (docs/operator.md §3). Nothing is written. The GUI
// shows it before the change, so that a change which takes bound nodes is
// confirmed with its victims and grace periods in view.
type Preview struct {
	// Preempt lists the bound nodes taken from lower-priority claims.
	Preempt []Preemption
	// Release lists, by VirtualCluster, the nodes it gives back because the change
	// lowers its size.
	Release map[string][]string
	// Grant lists the free nodes the changed claim receives.
	Grant []string
	// Shortfall is how many nodes the changed claim still lacks of its size.
	Shortfall int
	// Approximate is set when the pool has an autoscale claim. The operator reads
	// that claim's pending pods from the cluster on each pass, and the preview does
	// not, so the plan may differ from the next pass.
	Approximate bool
	// GracePeriod is the notice window a preempted claim gets before its pods are
	// evicted, and ForceAfter is the time from the change until the forced
	// deletion (the grace period plus the drain timeout). The operator starts both
	// clocks when the change is applied, so the preview states them as durations.
	GracePeriod time.Duration
	ForceAfter  time.Duration
}

// Preemption is one bound node taken from a claim for another.
type Preemption struct {
	Node string
	// From is the claim the node is taken from; For is the claim that receives it.
	From string
	For  string
}

// PreviewDesired previews setting the desired size of the claim of vcName on np
// to desired. vcs, nodes and leases are the cluster's state, as the operator
// reads it. The error is the admission refusal the change would meet, the same
// one the webhook and the operator give: desired must lie between min and max,
// and the claim must exist.
func PreviewDesired(np *v1alpha1.NodePool, vcs []v1alpha1.VirtualCluster, nodes []corev1.Node, leases []v1alpha1.NodeLease, vcName string, desired int32, now time.Time) (Preview, error) {
	edited, err := withDesired(np, vcs, vcName, desired)
	if err != nil {
		return Preview{}, err
	}
	pl := poolLeases(leases, np.Name)
	o := observed{nodes: nodes, leases: pl, vcs: edited}
	o.claims = buildClaims(np, edited, pl, now)
	st, err := poolStatus(np, o)
	if err != nil {
		return Preview{}, err
	}
	if err := alloc.Validate(o.claims, st.Total-st.Unhealthy); err != nil {
		return Preview{}, fmt.Errorf("pool %s: %w", np.Name, err)
	}
	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.NodeSelector)
	if err != nil {
		return Preview{}, err
	}
	plan, err := alloc.Reconcile(freeNodes(sel, nodes, pl), o.claims)
	if err != nil {
		return Preview{}, fmt.Errorf("pool %s: %w", np.Name, err)
	}

	grace := durationOr(np.Spec.Preemption.GracePeriod, 0)
	forced := grace + durationOr(np.Spec.Preemption.DrainTimeout, 0)
	out := Preview{Release: plan.Release, Shortfall: plan.Shortfall[vcName], GracePeriod: grace, ForceAfter: forced}
	taken := map[string]bool{}
	for _, v := range plan.Preempt {
		taken[v.Node] = true
		out.Preempt = append(out.Preempt, Preemption{Node: v.Node, From: v.From, For: v.ForVC})
	}
	for _, n := range plan.Grant[vcName] {
		if !taken[n] {
			out.Grant = append(out.Grant, n)
		}
	}
	for _, c := range o.claims {
		if c.Autoscale {
			out.Approximate = true
		}
	}
	return out, nil
}

// withDesired copies vcs with the desired size of vcName's claim on np set to
// desired. The input is not changed.
func withDesired(np *v1alpha1.NodePool, vcs []v1alpha1.VirtualCluster, vcName string, desired int32) ([]v1alpha1.VirtualCluster, error) {
	i := slices.IndexFunc(vcs, func(vc v1alpha1.VirtualCluster) bool { return vc.Name == vcName })
	if i < 0 {
		return nil, fmt.Errorf("虚拟集群 %s 不存在", vcName)
	}
	vc := vcs[i].DeepCopy()
	j := slices.IndexFunc(vc.Spec.PoolClaims, func(pc v1alpha1.PoolClaim) bool { return pc.Pool == np.Name })
	if j < 0 {
		return nil, fmt.Errorf("虚拟集群 %s 未声明资源池 %s", vcName, np.Name)
	}
	if pc := &vc.Spec.PoolClaims[j]; pc.Autoscale != nil && pc.Autoscale.Enabled {
		return nil, fmt.Errorf("虚拟集群 %s 在资源池 %s 上开启了自动伸缩，desired 不生效", vcName, np.Name)
	}
	vc.Spec.PoolClaims[j].Desired = desired
	out := slices.Clone(vcs)
	out[i] = *vc
	return out, nil
}
