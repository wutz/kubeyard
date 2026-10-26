package main

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// Pinning a node is the page's drag-to-claim action (docs/gui.md §3). It writes
// the claim's pinnedNodes, which the operator then grants first (docs/operator.md
// §3.3). The rules below are the webhook's (§3.2) plus two the webhook does not
// check: a pinned node must not be bound to another VirtualCluster, because the
// allocator refuses that plan and the whole pool would stop reconciling, and the
// node must be ready, because only ready nodes can be granted.

// nodeReady reports whether a node's Ready condition is True.
func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// claimOf returns the claim of a VirtualCluster on a pool, or nil.
func claimOf(vc *v1alpha1.VirtualCluster, pool string) *v1alpha1.PoolClaim {
	for i := range vc.Spec.PoolClaims {
		if vc.Spec.PoolClaims[i].Pool == pool {
			return &vc.Spec.PoolClaims[i]
		}
	}
	return nil
}

// checkPin reports why node cannot be pinned to the claim of vc on np. node is
// nil when the cluster does not have it. vcs and leases are every VirtualCluster
// and NodeLease in the cluster.
func checkPin(np *v1alpha1.NodePool, vc *v1alpha1.VirtualCluster, node *corev1.Node, vcs []v1alpha1.VirtualCluster, leases []v1alpha1.NodeLease, name string) error {
	pc := claimOf(vc, np.Name)
	if pc == nil {
		return fmt.Errorf("虚拟集群 %s 未声明资源池 %s", vc.Name, np.Name)
	}
	if node == nil {
		return fmt.Errorf("节点 %s 不存在", name)
	}
	sel, err := metav1.LabelSelectorAsSelector(&np.Spec.NodeSelector)
	if err != nil {
		return err
	}
	if !sel.Matches(labels.Set(node.Labels)) {
		return fmt.Errorf("节点 %s 不匹配资源池 %s 的 nodeSelector", name, np.Name)
	}
	if !nodeReady(node) {
		return fmt.Errorf("节点 %s 未就绪，不能固定", name)
	}
	if slices.Contains(pc.PinnedNodes, name) {
		return fmt.Errorf("节点 %s 已固定给虚拟集群 %s", name, vc.Name)
	}
	for i := range vcs {
		other := &vcs[i]
		if other.Name == vc.Name {
			continue
		}
		for _, opc := range other.Spec.PoolClaims {
			if slices.Contains(opc.PinnedNodes, name) {
				return fmt.Errorf("节点 %s 已固定给虚拟集群 %s", name, other.Name)
			}
		}
	}
	for _, l := range leases {
		if l.Spec.Node == name && l.Status.Phase != v1alpha1.LeaseReleased && l.Spec.VirtualCluster != vc.Name {
			return fmt.Errorf("节点 %s 仍由虚拟集群 %s 占用，等它释放后再固定", name, l.Spec.VirtualCluster)
		}
	}
	if int32(len(pc.PinnedNodes)) >= pc.Max {
		return fmt.Errorf("虚拟集群 %s 在资源池 %s 的固定节点已达 max %d，先解除固定或提高 max", vc.Name, np.Name, pc.Max)
	}
	return nil
}

// checkUnpin reports why node cannot be unpinned from the claim of vc on the pool.
func checkUnpin(vc *v1alpha1.VirtualCluster, pool, name string) error {
	pc := claimOf(vc, pool)
	if pc == nil || !slices.Contains(pc.PinnedNodes, name) {
		return fmt.Errorf("节点 %s 未固定给虚拟集群 %s", name, vc.Name)
	}
	return nil
}

// setPinned pins or unpins node on the claim of vcName in pool. It reads every
// object the rules need from the cluster, checks them, and then writes the
// VirtualCluster with its resourceVersion, so a change made in between fails the
// write rather than being overwritten.
func setPinned(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, vcName, poolName, node string, pin bool) error {
	vc, err := readVirtualCluster(ctx, dc, vcName)
	if err != nil {
		return fmt.Errorf("读取虚拟集群 %s：%w", vcName, err)
	}
	var np v1alpha1.NodePool
	if err := getKubeyard(ctx, dc, nodePoolResource, poolName, &np); err != nil {
		return fmt.Errorf("读取资源池 %s：%w", poolName, err)
	}

	pc := claimOf(vc, poolName)
	if pc == nil {
		return fmt.Errorf("虚拟集群 %s 未声明资源池 %s", vcName, poolName)
	}
	if pin {
		var n *corev1.Node
		got, err := cs.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		switch {
		case err == nil:
			n = got
		case !apierrors.IsNotFound(err):
			return fmt.Errorf("读取节点 %s：%w", node, err)
		}
		var vcs v1alpha1.VirtualClusterList
		if err := listKubeyard(ctx, dc, virtualClusterResource, &vcs); err != nil {
			return fmt.Errorf("读取虚拟集群：%w", err)
		}
		var leases v1alpha1.NodeLeaseList
		if err := listKubeyard(ctx, dc, nodeLeaseResource, &leases); err != nil && !missingCRD(err) {
			return fmt.Errorf("读取节点租约：%w", err)
		}
		if err := checkPin(&np, vc, n, vcs.Items, leases.Items, node); err != nil {
			return err
		}
		pc.PinnedNodes = append(pc.PinnedNodes, node)
	} else {
		if err := checkUnpin(vc, poolName, node); err != nil {
			return err
		}
		pc.PinnedNodes = slices.DeleteFunc(pc.PinnedNodes, func(n string) bool { return n == node })
	}
	return writeVirtualCluster(ctx, dc, vc)
}

// getKubeyard reads one cluster-scoped Kubeyard object into obj, a pointer to a
// pkg/api type.
func getKubeyard(ctx context.Context, dc dynamic.Interface, gvr schema.GroupVersionResource, name string, obj any) error {
	u, err := dc.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj)
}

// readVirtualCluster reads a VirtualCluster. Its resourceVersion is kept, so
// writeVirtualCluster fails when the object changed after this read.
func readVirtualCluster(ctx context.Context, dc dynamic.Interface, name string) (*v1alpha1.VirtualCluster, error) {
	var vc v1alpha1.VirtualCluster
	if err := getKubeyard(ctx, dc, virtualClusterResource, name, &vc); err != nil {
		return nil, err
	}
	return &vc, nil
}

// writeVirtualCluster writes vc back. The write fails when the cluster changed
// the VirtualCluster since it was read.
func writeVirtualCluster(ctx context.Context, dc dynamic.Interface, vc *v1alpha1.VirtualCluster) error {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vc)
	if err != nil {
		return err
	}
	if _, err := dc.Resource(virtualClusterResource).Update(ctx, &unstructured.Unstructured{Object: m}, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("写入虚拟集群 %s：%w（可能已被他人修改，请刷新后重试）", vc.Name, err)
	}
	return nil
}
