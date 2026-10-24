package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// ExportedKubeconfig is the Event reason written when an admin kubeconfig is
// read through the GUI. docs/operator.md §4.1 requires an audit record for it.
const ExportedKubeconfig = "AdminKubeconfigExported"

var virtualClusters = v1alpha1.SchemeGroupVersion.WithResource("virtualclusters")

// AdminKubeconfig returns the cluster-admin kubeconfig of a VirtualCluster. The
// bytes come from the Secret that VirtualCluster.status.adminKubeconfigSecretRef
// names, so the caller needs read access to that Secret. The returned value is
// the full credential; callers must not cache it.
func AdminKubeconfig(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, vcName string) ([]byte, error) {
	ref, err := adminSecretRef(ctx, dc, vcName)
	if err != nil {
		return nil, err
	}
	secret, err := cs.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("读取管理员 kubeconfig Secret %s/%s：%w", ref.Namespace, ref.Name, err)
	}
	data, ok := secret.Data[ref.Key]
	if !ok || len(data) == 0 {
		return nil, fmt.Errorf("Secret %s/%s 中没有键 %q", ref.Namespace, ref.Name, ref.Key)
	}
	return data, nil
}

// adminSecretRef reads the reference the operator records once vcluster has
// generated the kubeconfig. Before that, there is nothing to export.
func adminSecretRef(ctx context.Context, dc dynamic.Interface, vcName string) (v1alpha1.SecretReference, error) {
	obj, err := dc.Resource(virtualClusters).Get(ctx, vcName, metav1.GetOptions{})
	if err != nil {
		return v1alpha1.SecretReference{}, fmt.Errorf("读取虚拟集群 %s：%w", vcName, err)
	}
	var vc v1alpha1.VirtualCluster
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &vc); err != nil {
		return v1alpha1.SecretReference{}, fmt.Errorf("解析虚拟集群 %s：%w", vcName, err)
	}
	ref := vc.Status.AdminKubeconfigSecretRef
	if ref == nil {
		return v1alpha1.SecretReference{}, fmt.Errorf("虚拟集群 %s 尚未生成管理员 kubeconfig", vcName)
	}
	return *ref, nil
}

// RecordExport writes the audit Event for an export. VirtualCluster is cluster-scoped,
// so the Event lives in "default" with an empty involvedObject namespace, which is how
// Kubernetes records events for cluster-scoped objects. The caller must not return
// the kubeconfig if this fails.
func RecordExport(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, vcName, actor string) error {
	if _, err := adminSecretRef(ctx, dc, vcName); err != nil {
		return err
	}
	now := metav1.Now()
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "kubeyard-kubeconfig-",
			Namespace:    metav1.NamespaceDefault,
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "VirtualCluster",
			Name:       vcName,
		},
		Reason:         ExportedKubeconfig,
		Message:        fmt.Sprintf("管理员 kubeconfig 已导出，操作来源：%s", actor),
		Type:           corev1.EventTypeNormal,
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
		Source:         corev1.EventSource{Component: "kubeyard-gui"},
	}
	if _, err := cs.CoreV1().Events(metav1.NamespaceDefault).Create(ctx, ev, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("写入导出审计 Event：%w", err)
	}
	return nil
}
