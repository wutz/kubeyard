package host

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// SetupWithManager registers the reconciler with the manager. A change to a
// NodePool, Node, NodeLease, or VirtualCluster re-runs every pool. A pass is
// cheap, and this avoids tracking which pool a node or claim belongs to.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	allPools := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
		var pools v1alpha1.NodePoolList
		if err := mgr.GetClient().List(ctx, &pools); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(pools.Items))
		for i := range pools.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&pools.Items[i])})
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		Named("nodepool-host").
		For(&v1alpha1.NodePool{}).
		Watches(&corev1.Node{}, allPools).
		Watches(&v1alpha1.NodeLease{}, allPools).
		Watches(&v1alpha1.VirtualCluster{}, allPools).
		Complete(r)
}
