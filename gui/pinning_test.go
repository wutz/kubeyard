package main

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

var gpuLabels = map[string]string{"class": "gpu"}

// gpuPool selects the nodes labelled class=gpu.
func gpuPool() v1alpha1.NodePool {
	p := testPool("gpu", "GPU", 0, 0, 0, 0)
	p.Spec.NodeSelector = metav1.LabelSelector{MatchLabels: gpuLabels}
	return p
}

// testNode is a node with the given readiness and labels.
func testNode(name string, ready bool, labels map[string]string) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}},
	}
}

func testLease(node, vc string, phase v1alpha1.LeasePhase) v1alpha1.NodeLease {
	return v1alpha1.NodeLease{
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec:       v1alpha1.NodeLeaseSpec{Node: node, Pool: "gpu", VirtualCluster: vc},
		Status:     v1alpha1.NodeLeaseStatus{Phase: phase},
	}
}

// pinScene is the gpu pool with a and b claiming it. a has no nodes and may pin
// two; b pins n3, is bound to n5, and may pin four. n6 has a released lease to b.
func pinScene() (v1alpha1.NodePool, []v1alpha1.VirtualCluster, []v1alpha1.NodeLease) {
	np := gpuPool()
	vcs := []v1alpha1.VirtualCluster{
		testVC("a", "A", v1alpha1.PoolClaim{Pool: "gpu", Min: 0, Max: 2, Desired: 0}),
		testVC("b", "B", v1alpha1.PoolClaim{Pool: "gpu", Min: 0, Max: 4, Desired: 1, PinnedNodes: []string{"n3"}}),
	}
	leases := []v1alpha1.NodeLease{
		testLease("n5", "b", v1alpha1.LeaseBound),
		testLease("n6", "b", v1alpha1.LeaseReleased),
	}
	return np, vcs, leases
}

func TestCheckPinRules(t *testing.T) {
	cases := []struct {
		name string
		vc   string
		node string
		// ready and labels describe the node; absent nodes are passed as nil.
		absent bool
		ready  bool
		labels map[string]string
		// max overrides the claim's max of vc, for the room check.
		max  int32
		want string // substring of the refusal, or "" when allowed
	}{
		{name: "free ready node", vc: "a", node: "n1", ready: true, labels: gpuLabels},
		{name: "missing node", vc: "a", node: "n1", absent: true, want: "节点 n1 不存在"},
		{name: "node outside the selector", vc: "a", node: "n1", ready: true, labels: map[string]string{"class": "cpu"}, want: "不匹配资源池 gpu"},
		{name: "node not ready", vc: "a", node: "n1", ready: false, labels: gpuLabels, want: "未就绪"},
		{name: "pinned to another VC", vc: "a", node: "n3", ready: true, labels: gpuLabels, want: "已固定给虚拟集群 b"},
		{name: "bound to another VC", vc: "a", node: "n5", ready: true, labels: gpuLabels, want: "仍由虚拟集群 b 占用"},
		{name: "released lease does not block", vc: "a", node: "n6", ready: true, labels: gpuLabels},
		{name: "already pinned to this VC", vc: "b", node: "n3", ready: true, labels: gpuLabels, want: "已固定给虚拟集群 b"},
		{name: "claim has no room", vc: "b", node: "n1", ready: true, labels: gpuLabels, max: 1, want: "已达 max 1"},
		{name: "no claim on the pool", vc: "c", node: "n1", ready: true, labels: gpuLabels, want: "未声明资源池 gpu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			np, vcs, leases := pinScene()
			vcs = append(vcs, testVC("c", "C"))
			var vc v1alpha1.VirtualCluster
			for i := range vcs {
				if vcs[i].Name == tc.vc {
					vc = vcs[i]
				}
			}
			if tc.max != 0 {
				vc.Spec.PoolClaims[0].Max = tc.max
			}
			var node *corev1.Node
			if !tc.absent {
				node = testNode(tc.node, tc.ready, tc.labels)
			}
			err := checkPin(&np, &vc, node, vcs, leases, tc.node)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("checkPin = %v, want allowed", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("checkPin = %v, want refusal containing %q", err, tc.want)
			}
		})
	}
}

func TestCheckUnpin(t *testing.T) {
	_, vcs, _ := pinScene()
	if err := checkUnpin(&vcs[1], "gpu", "n3"); err != nil {
		t.Errorf("unpin of a pinned node = %v, want allowed", err)
	}
	if err := checkUnpin(&vcs[1], "gpu", "n5"); err == nil || !strings.Contains(err.Error(), "未固定给虚拟集群 b") {
		t.Errorf("unpin of an unpinned node = %v, want refusal", err)
	}
	if err := checkUnpin(&vcs[0], "gpu", "n3"); err == nil {
		t.Errorf("unpin from a VC that does not pin the node = nil, want refusal")
	}
}

func TestToPoolsViewNodesHolderAndPin(t *testing.T) {
	np, vcs, leases := pinScene()
	// n7 is draining from b to a: its lease already names a and is Draining.
	leases = append(leases, testLease("n7", "a", v1alpha1.LeaseDraining))
	nodes := []corev1.Node{
		*testNode("n7", true, gpuLabels),
		*testNode("n5", true, gpuLabels),
		*testNode("n3", true, gpuLabels),
		*testNode("n1", false, gpuLabels),
		*testNode("cpu1", true, map[string]string{"class": "cpu"}),
	}

	got := toPoolsView(true, []v1alpha1.NodePool{np}, vcs, nodes, leases)
	if len(got.Pools) != 1 {
		t.Fatalf("pools = %+v, want one", got.Pools)
	}
	want := []poolNodeView{
		{Name: "n1", Ready: false},
		{Name: "n3", Ready: true, PinnedTo: "b"},
		{Name: "n5", Ready: true, Holder: "b"},
		{Name: "n7", Ready: true, Holder: "a", Draining: true},
	}
	gotNodes := got.Pools[0].Nodes
	if len(gotNodes) != len(want) {
		t.Fatalf("nodes = %+v, want %+v (cpu1 is outside the selector)", gotNodes, want)
	}
	for i := range want {
		if gotNodes[i] != want[i] {
			t.Errorf("node %d = %+v, want %+v", i, gotNodes[i], want[i])
		}
	}
}
