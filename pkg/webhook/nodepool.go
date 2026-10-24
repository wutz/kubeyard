package webhook

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// NodePoolValidator checks §7's NodePool rules: the selector must not take a
// node from another pool, and an update must not drop a node that is bound to
// this pool.
type NodePoolValidator struct {
	// Reader reads live objects; see VirtualClusterValidator.Reader.
	Reader client.Reader
}

var _ admission.CustomValidator = &NodePoolValidator{}

// ValidateCreate checks a new NodePool.
func (v *NodePoolValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	np, err := asNodePool(obj)
	if err != nil {
		return nil, err
	}
	return nil, v.validate(ctx, np)
}

// ValidateUpdate checks a changed NodePool.
func (v *NodePoolValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	np, err := asNodePool(newObj)
	if err != nil {
		return nil, err
	}
	return nil, v.validate(ctx, np)
}

// ValidateDelete always allows: the leases of a deleted pool are drained and
// deleted by the reconciler.
func (v *NodePoolValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *NodePoolValidator) validate(ctx context.Context, np *v1alpha1.NodePool) error {
	// An empty selector would match every node, which pool.Compute already
	// rejects; catching it here says why at admission time.
	if len(np.Spec.NodeSelector.MatchLabels) == 0 && len(np.Spec.NodeSelector.MatchExpressions) == 0 {
		return fmt.Errorf("nodeSelector 为空，会匹配集群中的全部节点")
	}
	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.NodeSelector)
	if err != nil {
		return fmt.Errorf("nodeSelector 无效: %w", err)
	}
	var nodes corev1.NodeList
	if err := v.Reader.List(ctx, &nodes); err != nil {
		return err
	}
	var pools v1alpha1.NodePoolList
	if err := v.Reader.List(ctx, &pools); err != nil {
		return err
	}
	for i := range pools.Items {
		other := &pools.Items[i]
		if other.Name == np.Name {
			continue
		}
		otherSel, err := metav1.LabelSelectorAsSelector(&other.Spec.NodeSelector)
		if err != nil {
			// An invalid sibling is that pool's problem, not this one's.
			continue
		}
		for j := range nodes.Items {
			n := &nodes.Items[j]
			if sel.Matches(labels.Set(n.Labels)) && otherSel.Matches(labels.Set(n.Labels)) {
				return fmt.Errorf("节点 %s 同时匹配资源池 %s 与 %s 的选择器", n.Name, np.Name, other.Name)
			}
		}
	}

	// A binding is a label and taint projection of the lease; a node that falls
	// out of the selector would stay bound while leaving the pool's total, which
	// breaks sum(min) ≤ total.
	var leases v1alpha1.NodeLeaseList
	if err := v.Reader.List(ctx, &leases); err != nil {
		return err
	}
	for _, l := range leases.Items {
		if l.Spec.Pool != np.Name {
			continue
		}
		n, ok := findNode(nodes.Items, l.Spec.Node)
		if !ok {
			return fmt.Errorf("租约 %s 绑定的节点已不存在，先删除该租约", l.Name)
		}
		if !sel.Matches(labels.Set(n.Labels)) {
			return fmt.Errorf("节点 %s 已租给 %s，不能移出资源池 %s 的选择器：先释放该节点", n.Name, l.Spec.VirtualCluster, np.Name)
		}
	}
	return nil
}

func findNode(nodes []corev1.Node, name string) (*corev1.Node, bool) {
	for i := range nodes {
		if nodes[i].Name == name {
			return &nodes[i], true
		}
	}
	return nil, false
}

func asNodePool(obj runtime.Object) (*v1alpha1.NodePool, error) {
	np, ok := obj.(*v1alpha1.NodePool)
	if !ok {
		return nil, fmt.Errorf("want a NodePool, got %T", obj)
	}
	return np, nil
}
