package host

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wutz/kubeyard/pkg/alloc"
	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// autoscalePoll is how often a pool with an autoscale claim is checked again.
// Pending pods and idle time change without a watch that the reconciler sees.
const autoscalePoll = 15 * time.Second

// defaultScaleDownIdle is how long a node must be idle before it is released when
// the autoscale claim does not set scaleDownIdle.
const defaultScaleDownIdle = 10 * time.Minute

// autoscaleVCs names the VirtualClusters whose claim on the pool autoscales.
func autoscaleVCs(np *v1alpha1.NodePool, vcs []v1alpha1.VirtualCluster) map[string]bool {
	out := map[string]bool{}
	for i := range vcs {
		for _, pc := range vcs[i].Spec.PoolClaims {
			if pc.Pool == np.Name && pc.Autoscale != nil && pc.Autoscale.Enabled {
				out[vcs[i].Name] = true
			}
		}
	}
	return out
}

// workloadPodsOn lists the active pods of vc on node that count as workload. A
// DaemonSet pod runs on every node, so it does not keep a node busy
// (docs/operator.md §3.1).
func (r *Reconciler) workloadPodsOn(ctx context.Context, node, vc string) ([]corev1.Pod, error) {
	pods, err := r.activePodsOn(ctx, node, vc)
	if err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for i := range pods {
		if !ownedByDaemonSet(&pods[i]) {
			out = append(out, pods[i])
		}
	}
	return out, nil
}

func ownedByDaemonSet(p *corev1.Pod) bool {
	for _, ref := range p.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// trackIdle sets or clears IdleSince on the bound leases of the pool. An autoscale
// VirtualCluster's node is idle while it has no workload there; any other node has
// no IdleSince. It reports whether it wrote anything.
func (r *Reconciler) trackIdle(ctx context.Context, np *v1alpha1.NodePool, o observed) (bool, error) {
	autoscale := autoscaleVCs(np, o.vcs)
	now := r.now()
	changed := false
	for i := range o.leases {
		l := &o.leases[i]
		if l.Status.Phase != v1alpha1.LeaseBound {
			continue
		}
		idle := false
		if autoscale[l.Spec.VirtualCluster] {
			pods, err := r.workloadPodsOn(ctx, l.Spec.Node, l.Spec.VirtualCluster)
			if err != nil {
				return false, err
			}
			idle = len(pods) == 0
		}
		if idle == (l.Status.IdleSince != nil) {
			continue
		}
		orig := l.DeepCopy()
		if idle {
			l.Status.IdleSince = &metav1.Time{Time: now}
		} else {
			l.Status.IdleSince = nil
		}
		if err := r.Status().Patch(ctx, l, client.MergeFrom(orig)); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// idleNodes counts the bound leases that have been idle for at least scaleDownIdle.
func idleNodes(leases []v1alpha1.NodeLease, now time.Time, scaleDownIdle time.Duration) int {
	n := 0
	for i := range leases {
		l := &leases[i]
		if l.Status.Phase == v1alpha1.LeaseBound && l.Status.IdleSince != nil && now.Sub(l.Status.IdleSince.Time) >= scaleDownIdle {
			n++
		}
	}
	return n
}

// pendingDemand is how many nodes of the pool the unschedulable pods of vc need.
// Their requests are summed and rounded up against the allocatable of one pool
// node (docs/operator.md §3.1). nodes are the pool's nodes.
func (r *Reconciler) pendingDemand(ctx context.Context, vc string, nodes []corev1.Node) (int, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(hostNamespace(vc))); err != nil {
		return 0, err
	}
	return demandNodes(pods.Items, nodes), nil
}

// demandNodes is the node count behind pendingDemand. A pool without nodes has no
// node size to round against, so it reports no demand.
func demandNodes(pods []corev1.Pod, nodes []corev1.Node) int {
	spec, ok := nodeSpec(nodes)
	if !ok {
		return 0
	}
	cpu := spec.Cpu().MilliValue()
	mem := spec.Memory().Value()
	var needCPU, needMem int64
	count := 0
	for i := range pods {
		if !unschedulable(&pods[i]) {
			continue
		}
		count++
		for _, c := range pods[i].Spec.Containers {
			needCPU += c.Resources.Requests.Cpu().MilliValue()
			needMem += c.Resources.Requests.Memory().Value()
		}
	}
	if count == 0 {
		return 0
	}
	n := 1
	if cpu > 0 {
		n = max(n, int((needCPU+cpu-1)/cpu))
	}
	if mem > 0 {
		n = max(n, int((needMem+mem-1)/mem))
	}
	return n
}

// nodeSpec is the allocatable of the pool's first node by name, which stands for
// the single-node spec of the pool.
func nodeSpec(nodes []corev1.Node) (corev1.ResourceList, bool) {
	if len(nodes) == 0 {
		return nil, false
	}
	sorted := make([]corev1.Node, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted[0].Status.Allocatable, true
}

// unschedulable reports whether the scheduler could not place the pod for lack of
// resources, which is the demand autoscale reads.
func unschedulable(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodPending {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
			return true
		}
	}
	return false
}

// poolNodes lists the nodes that match the pool's selector.
func poolNodes(sel labels.Selector, nodes []corev1.Node) []corev1.Node {
	var out []corev1.Node
	for i := range nodes {
		if sel.Matches(labels.Set(nodes[i].Labels)) {
			out = append(out, nodes[i])
		}
	}
	return out
}

func scaleDownIdle(pc v1alpha1.PoolClaim) time.Duration {
	if pc.Autoscale == nil {
		return defaultScaleDownIdle
	}
	return durationOr(pc.Autoscale.ScaleDownIdle, defaultScaleDownIdle)
}

func hasAutoscale(claims []alloc.Claim) bool {
	for _, c := range claims {
		if c.Autoscale {
			return true
		}
	}
	return false
}
