package webhook

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newClient(t *testing.T, objs ...client.Object) client.Reader {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(scheme(t)).WithObjects(objs...).Build()
}

// readyNode is a Ready node with the given labels.
func readyNode(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}},
	}
}

func gpuPool(name string, nodes ...*corev1.Node) (*v1alpha1.NodePool, []client.Object) {
	np := &v1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.NodePoolSpec{
			NodeSelector: metav1.LabelSelector{MatchLabels: map[string]string{"class": name}},
		},
	}
	objs := []client.Object{np}
	for _, n := range nodes {
		objs = append(objs, n)
	}
	return np, objs
}

func claim(pool string, min, max, desired int32, pinned ...string) v1alpha1.PoolClaim {
	return v1alpha1.PoolClaim{Pool: pool, Min: min, Max: max, Desired: desired, PinnedNodes: pinned}
}

func vc(name string, claims ...v1alpha1.PoolClaim) *v1alpha1.VirtualCluster {
	return &v1alpha1.VirtualCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.VirtualClusterSpec{PoolClaims: claims},
	}
}

func createVC(t *testing.T, c client.Reader, obj *v1alpha1.VirtualCluster) error {
	t.Helper()
	_, err := (&VirtualClusterValidator{Reader: c}).ValidateCreate(context.Background(), obj)
	return err
}

// wantErr checks that err is present and mentions every fragment.
func wantErr(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error mentioning %q, got nil", fragments)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Fatalf("error %q does not mention %q", err, f)
		}
	}
}

func TestVirtualClusterAcceptsValidClaims(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}), readyNode("n2", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	if err := createVC(t, c, vc("acme", claim("gpu", 1, 2, 2, "n1"))); err != nil {
		t.Fatalf("want accepted, got %v", err)
	}
}

func TestVirtualClusterRejectsBadClaimBounds(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	cases := map[string]v1alpha1.PoolClaim{
		"min above max":     claim("gpu", 2, 1, 1),
		"negative min":      claim("gpu", -1, 1, 1),
		"desired below min": claim("gpu", 1, 2, 0),
		"desired above max": claim("gpu", 0, 1, 2),
		"pinned above max":  claim("gpu", 0, 1, 1, "n1", "n1"),
		"node pinned twice": claim("gpu", 0, 2, 2, "n1", "n1"),
	}
	for name, pc := range cases {
		t.Run(name, func(t *testing.T) {
			wantErr(t, createVC(t, c, vc("acme", pc)))
		})
	}
}

func TestVirtualClusterAllowsDesiredOutOfRangeWhenAutoscaled(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	pc := claim("gpu", 0, 1, 9)
	pc.Autoscale = &v1alpha1.Autoscale{Enabled: true}
	if err := createVC(t, c, vc("acme", pc)); err != nil {
		t.Fatalf("want accepted, got %v", err)
	}
}

func TestVirtualClusterRejectsDuplicatePoolClaim(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	wantErr(t, createVC(t, c, vc("acme", claim("gpu", 0, 1, 1), claim("gpu", 0, 1, 0))), "被声明了两次")
}

func TestVirtualClusterRejectsUnknownPool(t *testing.T) {
	c := newClient(t)
	wantErr(t, createVC(t, c, vc("acme", claim("gpu", 0, 1, 1))), "资源池 gpu 不存在")
}

func TestVirtualClusterRejectsSumMinAboveHealthy(t *testing.T) {
	// Two nodes, one of them not Ready: only one healthy node, so two min=1
	// claims cannot both hold their guarantee.
	unready := readyNode("n2", map[string]string{"class": "gpu"})
	unready.Status.Conditions = nil
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}), unready)
	other := vc("other", claim("gpu", 1, 1, 1))
	c := newClient(t, append(objs, other)...)
	wantErr(t, createVC(t, c, vc("acme", claim("gpu", 1, 1, 1))), "min 之和 2", "健康节点数 1")
}

func TestVirtualClusterUpdateReplacesItsOwnMin(t *testing.T) {
	// The stored copy of acme already counts min=1 toward the sum; an update that
	// keeps it at 1 must not be read as 1 + 1.
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	stored := vc("acme", claim("gpu", 1, 1, 1))
	c := newClient(t, append(objs, stored)...)
	updated := vc("acme", claim("gpu", 1, 1, 1))
	if _, err := (&VirtualClusterValidator{Reader: c}).ValidateUpdate(context.Background(), stored, updated); err != nil {
		t.Fatalf("want accepted, got %v", err)
	}
}

func TestVirtualClusterRejectsPinnedNodeProblems(t *testing.T) {
	n1 := readyNode("n1", map[string]string{"class": "gpu"})
	other := readyNode("other", map[string]string{"class": "cpu"})
	_, objs := gpuPool("gpu", n1)
	c := newClient(t, append(objs, other, vc("rival", claim("gpu", 0, 1, 1, "n1")))...)

	wantErr(t, createVC(t, c, vc("acme", claim("gpu", 0, 1, 1, "missing"))), "不存在")
	wantErr(t, createVC(t, c, vc("acme", claim("gpu", 0, 1, 1, "other"))), "不匹配")
	wantErr(t, createVC(t, c, vc("acme", claim("gpu", 0, 1, 1, "n1"))), "已被虚拟集群 rival 固定")
}

func TestVirtualClusterRejectsReservedValuesOverride(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	cases := map[string]string{
		"fromHost.nodes": `{"sync":{"fromHost":{"nodes":{"enabled":false}}}}`,
		"toHost.pods":    `{"sync":{"toHost":{"pods":{}}}}`,
		"null override":  `{"sync":{"fromHost":{"nodes":null}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			obj := vc("acme", claim("gpu", 0, 1, 1))
			obj.Spec.ValuesOverride = &runtime.RawExtension{Raw: []byte(raw)}
			wantErr(t, createVC(t, c, obj), "valuesOverride")
		})
	}
}

func TestVirtualClusterAcceptsFreeValuesOverride(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	obj := vc("acme", claim("gpu", 0, 1, 1))
	obj.Spec.ValuesOverride = &runtime.RawExtension{Raw: []byte(`{"sync":{"ingress":{"enabled":true}},"isolation":{"enabled":true}}`)}
	if err := createVC(t, c, obj); err != nil {
		t.Fatalf("want accepted, got %v", err)
	}
}

func TestVirtualClusterRejectsMalformedValuesOverride(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, objs...)
	for name, raw := range map[string]string{"not json": `{`, "not an object": `[1,2]`} {
		t.Run(name, func(t *testing.T) {
			obj := vc("acme", claim("gpu", 0, 1, 1))
			obj.Spec.ValuesOverride = &runtime.RawExtension{Raw: []byte(raw)}
			wantErr(t, createVC(t, c, obj), "valuesOverride")
		})
	}
}

func lease(node, poolName, vcName string, phase v1alpha1.LeasePhase) *v1alpha1.NodeLease {
	return &v1alpha1.NodeLease{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec:       v1alpha1.NodeLeaseSpec{Node: node, Pool: poolName, VirtualCluster: vcName},
		Status:     v1alpha1.NodeLeaseStatus{Phase: phase},
	}
}

func TestVirtualClusterDeleteWaitsForReleasedNodes(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}), readyNode("n2", map[string]string{"class": "gpu"}))
	acme := vc("acme", claim("gpu", 0, 2, 2))
	other := vc("other", claim("gpu", 0, 1, 1))
	c := newClient(t, append(objs, acme, other,
		lease("n2", "gpu", "acme", v1alpha1.LeaseDraining),
		lease("n1", "gpu", "other", v1alpha1.LeaseBound))...)

	_, err := (&VirtualClusterValidator{Reader: c}).ValidateDelete(context.Background(), acme)
	wantErr(t, err, "还占用 1 个节点", "n2")

	// Once the lease is Released, the delete goes through.
	released := newClient(t, append(objs[:1:1], acme)...)
	if _, err := (&VirtualClusterValidator{Reader: released}).ValidateDelete(context.Background(), acme); err != nil {
		t.Fatalf("want accepted without leases, got %v", err)
	}
}

func TestNodePoolRejectsEmptySelector(t *testing.T) {
	c := newClient(t)
	np := &v1alpha1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "all"}}
	_, err := (&NodePoolValidator{Reader: c}).ValidateCreate(context.Background(), np)
	wantErr(t, err, "nodeSelector 为空")
}

func TestNodePoolRejectsOverlappingSelectors(t *testing.T) {
	n1 := readyNode("n1", map[string]string{"class": "gpu", "vendor": "acme"})
	gpu, objs := gpuPool("gpu", n1)
	c := newClient(t, objs...)
	np := &v1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "vendor"},
		Spec:       v1alpha1.NodePoolSpec{NodeSelector: metav1.LabelSelector{MatchLabels: map[string]string{"vendor": "acme"}}},
	}
	_, err := (&NodePoolValidator{Reader: c}).ValidateCreate(context.Background(), np)
	wantErr(t, err, "同时匹配资源池", gpu.Name, "vendor")
}

func TestNodePoolUpdateKeepsLeasedNodesInSelector(t *testing.T) {
	_, objs := gpuPool("gpu", readyNode("n1", map[string]string{"class": "gpu"}))
	c := newClient(t, append(objs, lease("n1", "gpu", "acme", v1alpha1.LeaseBound))...)
	v := &NodePoolValidator{Reader: c}

	// Same selector: accepted.
	same := &v1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu"},
		Spec:       v1alpha1.NodePoolSpec{NodeSelector: metav1.LabelSelector{MatchLabels: map[string]string{"class": "gpu"}}},
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, same); err != nil {
		t.Fatalf("want accepted, got %v", err)
	}

	// Narrowed away from the bound node: rejected.
	narrowed := &v1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu"},
		Spec:       v1alpha1.NodePoolSpec{NodeSelector: metav1.LabelSelector{MatchLabels: map[string]string{"class": "h100"}}},
	}
	_, err := v.ValidateUpdate(context.Background(), nil, narrowed)
	wantErr(t, err, "n1", "不能移出资源池")
}
