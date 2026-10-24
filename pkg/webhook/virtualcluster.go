// Package webhook enforces the admission invariants of docs/operator.md §3.2 and
// the validating rules of §7. The API server calls these validators before a
// NodePool or VirtualCluster is stored; the allocator and the reconciler assume
// exactly what they check, so a stored object never wedges a pool reconcile.
package webhook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/pool"
)

// VirtualClusterValidator checks the invariants of §3.2 and the delete rule of
// §7 on a VirtualCluster.
type VirtualClusterValidator struct {
	// Reader reads live objects rather than the controller's cache: a decision
	// made on a cache that trails the request could let two claims overcommit a
	// pool. main.go passes the manager's API reader.
	Reader client.Reader
}

var _ admission.CustomValidator = &VirtualClusterValidator{}

// ValidateCreate checks a new VirtualCluster.
func (v *VirtualClusterValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	vc, err := asVirtualCluster(obj)
	if err != nil {
		return nil, err
	}
	return nil, v.validate(ctx, vc)
}

// ValidateUpdate checks a changed VirtualCluster. The stored object is the one
// the pool sums read, so the new claim replaces it rather than adding to it.
func (v *VirtualClusterValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	vc, err := asVirtualCluster(newObj)
	if err != nil {
		return nil, err
	}
	return nil, v.validate(ctx, vc)
}

// ValidateDelete refuses to remove a VirtualCluster that still holds nodes. The
// claims have to reach zero and the leases drain first (§3.4); deleting the VC
// underneath them would leave the nodes bound with no owner.
func (v *VirtualClusterValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	vc, err := asVirtualCluster(obj)
	if err != nil {
		return nil, err
	}
	var leases v1alpha1.NodeLeaseList
	if err := v.Reader.List(ctx, &leases); err != nil {
		return nil, err
	}
	var held []string
	for _, l := range leases.Items {
		if l.Spec.VirtualCluster == vc.Name && l.Status.Phase != v1alpha1.LeaseReleased {
			held = append(held, l.Spec.Node)
		}
	}
	if len(held) == 0 {
		return nil, nil
	}
	sort.Strings(held)
	return nil, fmt.Errorf("虚拟集群还占用 %d 个节点（%s）：先把各 poolClaim 的 min 与 desired 调到 0、清空 pinnedNodes，等节点释放后再删除",
		len(held), strings.Join(held, ", "))
}

func (v *VirtualClusterValidator) validate(ctx context.Context, vc *v1alpha1.VirtualCluster) error {
	if err := checkClaims(vc); err != nil {
		return err
	}
	if err := checkValuesOverride(vc.Spec.ValuesOverride); err != nil {
		return err
	}
	return v.checkPools(ctx, vc)
}

// checkClaims enforces the bounds of §3.2 that need only the object: min ≤
// desired ≤ max, pinnedNodes within max, and no node pinned twice.
func checkClaims(vc *v1alpha1.VirtualCluster) error {
	pools := map[string]bool{}
	pinned := map[string]bool{}
	for _, pc := range vc.Spec.PoolClaims {
		if pc.Pool == "" {
			return fmt.Errorf("poolClaims 中有一项没有 pool 名")
		}
		if pools[pc.Pool] {
			return fmt.Errorf("pool %s 被声明了两次", pc.Pool)
		}
		pools[pc.Pool] = true
		if pc.Min < 0 || pc.Min > pc.Max {
			return fmt.Errorf("pool %s: min %d 必须在 0 与 max %d 之间", pc.Pool, pc.Min, pc.Max)
		}
		autoscale := pc.Autoscale != nil && pc.Autoscale.Enabled
		if !autoscale && (pc.Desired < pc.Min || pc.Desired > pc.Max) {
			return fmt.Errorf("pool %s: desired %d 必须在 min %d 与 max %d 之间", pc.Pool, pc.Desired, pc.Min, pc.Max)
		}
		if int32(len(pc.PinnedNodes)) > pc.Max {
			return fmt.Errorf("pool %s: pinnedNodes 有 %d 个，超过 max %d", pc.Pool, len(pc.PinnedNodes), pc.Max)
		}
		for _, n := range pc.PinnedNodes {
			if n == "" {
				return fmt.Errorf("pool %s: pinnedNodes 中有空节点名", pc.Pool)
			}
			if pinned[n] {
				return fmt.Errorf("节点 %s 在 poolClaims 中被固定了两次", n)
			}
			pinned[n] = true
		}
	}
	return nil
}

// checkPools enforces what §3.2 needs from the rest of the cluster: the pool
// exists, sum(min) over its claims fits the healthy nodes, and a pinned node
// belongs to the pool's selector and to no other VirtualCluster.
func (v *VirtualClusterValidator) checkPools(ctx context.Context, vc *v1alpha1.VirtualCluster) error {
	if len(vc.Spec.PoolClaims) == 0 {
		return nil
	}
	var pools v1alpha1.NodePoolList
	if err := v.Reader.List(ctx, &pools); err != nil {
		return err
	}
	byName := map[string]*v1alpha1.NodePool{}
	for i := range pools.Items {
		byName[pools.Items[i].Name] = &pools.Items[i]
	}
	var nodes corev1.NodeList
	if err := v.Reader.List(ctx, &nodes); err != nil {
		return err
	}
	nodeByName := map[string]*corev1.Node{}
	for i := range nodes.Items {
		nodeByName[nodes.Items[i].Name] = &nodes.Items[i]
	}
	var vcs v1alpha1.VirtualClusterList
	if err := v.Reader.List(ctx, &vcs); err != nil {
		return err
	}

	for _, pc := range vc.Spec.PoolClaims {
		np, ok := byName[pc.Pool]
		if !ok {
			return fmt.Errorf("资源池 %s 不存在", pc.Pool)
		}
		st, err := pool.Compute(np.Spec.NodeSelector, nodes.Items, nil, 0)
		if err != nil {
			return fmt.Errorf("资源池 %s: %w", np.Name, err)
		}
		sel, err := metav1.LabelSelectorAsSelector(&np.Spec.NodeSelector)
		if err != nil {
			return fmt.Errorf("资源池 %s: %w", np.Name, err)
		}
		for _, name := range pc.PinnedNodes {
			n, ok := nodeByName[name]
			if !ok {
				return fmt.Errorf("资源池 %s: 固定的节点 %s 不存在", np.Name, name)
			}
			if !sel.Matches(labels.Set(n.Labels)) {
				return fmt.Errorf("资源池 %s: 固定的节点 %s 不匹配该池的 nodeSelector", np.Name, name)
			}
			for i := range vcs.Items {
				other := &vcs.Items[i]
				if other.Name == vc.Name {
					continue
				}
				if owner := pinOwner(other, name); owner != "" {
					return fmt.Errorf("节点 %s 已被虚拟集群 %s 固定在资源池 %s", name, other.Name, owner)
				}
			}
		}
		sum := int(pc.Min)
		for i := range vcs.Items {
			other := &vcs.Items[i]
			if other.Name == vc.Name {
				continue
			}
			for _, opc := range other.Spec.PoolClaims {
				if opc.Pool == pc.Pool {
					sum += int(opc.Min)
				}
			}
		}
		if healthy := st.Total - st.Unhealthy; sum > healthy {
			return fmt.Errorf("资源池 %s: 各虚拟集群 min 之和 %d 超过健康节点数 %d", np.Name, sum, healthy)
		}
	}
	return nil
}

// pinOwner returns the pool in vc whose claim pins node, or "".
func pinOwner(vc *v1alpha1.VirtualCluster, node string) string {
	for _, pc := range vc.Spec.PoolClaims {
		for _, n := range pc.PinnedNodes {
			if n == node {
				return pc.Pool
			}
		}
	}
	return ""
}

func asVirtualCluster(obj runtime.Object) (*v1alpha1.VirtualCluster, error) {
	vc, ok := obj.(*v1alpha1.VirtualCluster)
	if !ok {
		return nil, fmt.Errorf("want a VirtualCluster, got %T", obj)
	}
	return vc, nil
}
