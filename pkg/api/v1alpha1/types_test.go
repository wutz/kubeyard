package v1alpha1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const virtualClusterJSON = `{
  "apiVersion": "kubeyard.wutz.dev/v1alpha1",
  "kind": "VirtualCluster",
  "metadata": {"name": "acme-prod"},
  "spec": {
    "displayName": "ACME 生产集群",
    "priority": 100,
    "preemptible": true,
    "provider": "vcluster",
    "mode": "dedicated-nodes",
    "version": "0.27.0",
    "distro": "k8s",
    "exposure": {"type": "LoadBalancer", "loadBalancerIP": "10.243.145.200"},
    "valuesOverride": {},
    "poolClaims": [{
      "pool": "rtx-5090-x8",
      "min": 2,
      "max": 8,
      "desired": 4,
      "autoscale": {"enabled": true, "scaleDownIdle": "30m"},
      "pinnedNodes": ["gpu-node-07"]
    }]
  },
  "status": {
    "phase": "Ready",
    "hostNamespace": "vc-acme-prod",
    "endpoint": "https://10.243.145.200",
    "adminKubeconfigSecretRef": {"namespace": "vc-acme-prod", "name": "vc-acme-prod-admin", "key": "config"},
    "allocations": [{"pool": "rtx-5090-x8", "nodes": ["gpu-node-07", "gpu-node-08"], "pendingDemand": 1}]
  }
}`

const nodePoolJSON = `{
  "apiVersion": "kubeyard.wutz.dev/v1alpha1",
  "kind": "NodePool",
  "metadata": {"name": "rtx-5090-x8"},
  "spec": {
    "displayName": "rtx-5090.x8",
    "nodeSelector": {"matchLabels": {"nvidia.com/gpu.product": "NVIDIA-GeForce-RTX-5090", "nvidia.com/gpu.count": "8"}},
    "preemption": {"policy": "LowerPriority", "gracePeriod": "10m", "drainTimeout": "30m"},
    "release": {"wipe": "none"}
  },
  "status": {"total": 24, "free": 19, "reserved": 4, "unhealthy": 1}
}`

const nodeLeaseJSON = `{
  "apiVersion": "kubeyard.wutz.dev/v1alpha1",
  "kind": "NodeLease",
  "metadata": {"name": "gpu-node-07"},
  "spec": {"node": "gpu-node-07", "pool": "rtx-5090-x8", "virtualCluster": "acme-prod", "reason": "Pinned"},
  "status": {"phase": "Bound", "boundAt": "2026-05-01T08:00:00Z", "drainDeadline": null}
}`

func TestGroupVersion(t *testing.T) {
	if got := SchemeGroupVersion.String(); got != "kubeyard.wutz.dev/v1alpha1" {
		t.Fatalf("GroupVersion = %q", got)
	}
}

func TestSchemeRecognizesKinds(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	want := map[string]runtime.Object{
		"VirtualCluster":     &VirtualCluster{},
		"VirtualClusterList": &VirtualClusterList{},
		"NodePool":           &NodePool{},
		"NodePoolList":       &NodePoolList{},
		"NodeLease":          &NodeLease{},
		"NodeLeaseList":      &NodeLeaseList{},
	}
	for kind, typ := range want {
		obj, err := s.New(SchemeGroupVersion.WithKind(kind))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if reflect.TypeOf(obj) != reflect.TypeOf(typ) {
			t.Fatalf("%s: got %T, want %T", kind, obj, typ)
		}
	}
}

func TestVirtualClusterMatchesDocument(t *testing.T) {
	var vc VirtualCluster
	if err := json.Unmarshal([]byte(virtualClusterJSON), &vc); err != nil {
		t.Fatal(err)
	}
	if vc.Kind != "VirtualCluster" || vc.Name != "acme-prod" {
		t.Fatalf("type/meta = %q/%q", vc.Kind, vc.Name)
	}
	if vc.Spec.Priority != 100 || !vc.Spec.Preemptible || vc.Spec.Exposure.Type != ExposureLoadBalancer {
		t.Fatalf("spec = %+v", vc.Spec)
	}
	claim := vc.Spec.PoolClaims[0]
	if claim.Min != 2 || claim.Max != 8 || claim.Desired != 4 {
		t.Fatalf("claim = %+v", claim)
	}
	if claim.Autoscale == nil || !claim.Autoscale.Enabled || claim.Autoscale.ScaleDownIdle.Duration != 30*time.Minute {
		t.Fatalf("autoscale = %+v", claim.Autoscale)
	}
	if !reflect.DeepEqual(claim.PinnedNodes, []string{"gpu-node-07"}) {
		t.Fatalf("pinnedNodes = %v", claim.PinnedNodes)
	}
	if vc.Spec.ValuesOverride == nil || string(vc.Spec.ValuesOverride.Raw) != "{}" {
		t.Fatalf("valuesOverride = %+v", vc.Spec.ValuesOverride)
	}
	if vc.Status.Phase != VirtualClusterReady || vc.Status.Allocations[0].PendingDemand != 1 {
		t.Fatalf("status = %+v", vc.Status)
	}
	if ref := vc.Status.AdminKubeconfigSecretRef; ref == nil || ref.Key != "config" {
		t.Fatalf("adminKubeconfigSecretRef = %+v", ref)
	}
}

func TestNodePoolMatchesDocument(t *testing.T) {
	var np NodePool
	if err := json.Unmarshal([]byte(nodePoolJSON), &np); err != nil {
		t.Fatal(err)
	}
	if np.Spec.NodeSelector.MatchLabels["nvidia.com/gpu.count"] != "8" {
		t.Fatalf("nodeSelector = %+v", np.Spec.NodeSelector)
	}
	if np.Spec.Preemption.Policy != PreemptionLowerPriority {
		t.Fatalf("policy = %q", np.Spec.Preemption.Policy)
	}
	if np.Spec.Preemption.GracePeriod.Duration != 10*time.Minute || np.Spec.Preemption.DrainTimeout.Duration != 30*time.Minute {
		t.Fatalf("preemption = %+v", np.Spec.Preemption)
	}
	if np.Status != (NodePoolStatus{Total: 24, Free: 19, Reserved: 4, Unhealthy: 1}) {
		t.Fatalf("status = %+v", np.Status)
	}
}

func TestNodeLeaseMatchesDocument(t *testing.T) {
	var nl NodeLease
	if err := json.Unmarshal([]byte(nodeLeaseJSON), &nl); err != nil {
		t.Fatal(err)
	}
	if nl.Spec.Reason != LeasePinned || nl.Spec.VirtualCluster != "acme-prod" {
		t.Fatalf("spec = %+v", nl.Spec)
	}
	if nl.Status.Phase != LeaseBound || nl.Status.BoundAt == nil {
		t.Fatalf("status = %+v", nl.Status)
	}
	if !nl.Status.BoundAt.Equal(&metav1.Time{Time: time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)}) {
		t.Fatalf("boundAt = %v", nl.Status.BoundAt)
	}
	if nl.Status.DrainDeadline != nil {
		t.Fatalf("drainDeadline = %v, want nil", nl.Status.DrainDeadline)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var vc VirtualCluster
	if err := json.Unmarshal([]byte(virtualClusterJSON), &vc); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(&vc)
	if err != nil {
		t.Fatal(err)
	}
	var back VirtualCluster
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vc, back) {
		t.Fatalf("round trip changed the object:\n%+v\n%+v", vc, back)
	}
}

// fullVirtualCluster sets every reference field so that DeepCopy has to copy
// each of them.
func fullVirtualCluster() *VirtualCluster {
	boundAt := metav1.NewTime(time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC))
	idle := metav1.Duration{Duration: 30 * time.Minute}
	return &VirtualCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "acme-prod", Labels: map[string]string{"team": "a"}},
		Spec: VirtualClusterSpec{
			Priority:       100,
			Exposure:       Exposure{Type: ExposureIngress, Ingress: &IngressExposure{Host: "acme.example", Port: 7443}},
			ValuesOverride: &runtime.RawExtension{Raw: []byte(`{"x":1}`)},
			PoolClaims: []PoolClaim{{
				Pool:        "rtx-5090-x8",
				Autoscale:   &Autoscale{Enabled: true, ScaleDownIdle: &idle},
				PinnedNodes: []string{"gpu-node-07"},
			}},
		},
		Status: VirtualClusterStatus{
			AdminKubeconfigSecretRef: &SecretReference{Namespace: "vc-acme-prod", Name: "admin", Key: "config"},
			Allocations:              []PoolAllocation{{Pool: "rtx-5090-x8", Nodes: []string{"gpu-node-07"}}},
			Conditions:               []metav1.Condition{{Type: "Preempting", Status: metav1.ConditionTrue, LastTransitionTime: boundAt}},
		},
	}
}

func TestVirtualClusterDeepCopyIsIndependent(t *testing.T) {
	orig := fullVirtualCluster()
	cp := orig.DeepCopy()
	if !reflect.DeepEqual(orig, cp) {
		t.Fatalf("copy differs from original")
	}
	if cp == orig {
		t.Fatal("DeepCopy returned the same pointer")
	}

	cp.Labels["team"] = "b"
	cp.Spec.Exposure.Ingress.Port = 1
	cp.Spec.ValuesOverride.Raw[0] = 'X'
	cp.Spec.PoolClaims[0].PinnedNodes[0] = "other"
	cp.Spec.PoolClaims[0].Autoscale.ScaleDownIdle.Duration = time.Hour
	cp.Status.AdminKubeconfigSecretRef.Name = "changed"
	cp.Status.Allocations[0].Nodes[0] = "other"
	cp.Status.Conditions[0].Status = metav1.ConditionFalse

	fresh := fullVirtualCluster()
	if !reflect.DeepEqual(orig, fresh) {
		t.Fatalf("mutating the copy changed the original:\n%+v", orig)
	}
}

func TestDeepCopyNilAndObject(t *testing.T) {
	var nilVC *VirtualCluster
	if nilVC.DeepCopy() != nil {
		t.Fatal("nil DeepCopy should return nil")
	}
	var obj runtime.Object = fullVirtualCluster()
	if _, ok := obj.DeepCopyObject().(*VirtualCluster); !ok {
		t.Fatal("DeepCopyObject returned the wrong type")
	}
}

func TestNodeLeaseDeepCopyIsIndependent(t *testing.T) {
	at := metav1.NewTime(time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC))
	orig := &NodeLease{
		Spec:   NodeLeaseSpec{Node: "gpu-node-07", Pool: "rtx-5090-x8", VirtualCluster: "acme-prod", Reason: LeaseScale},
		Status: NodeLeaseStatus{Phase: LeaseBound, BoundAt: &at},
	}
	cp := orig.DeepCopy()
	if !reflect.DeepEqual(orig, cp) {
		t.Fatal("copy differs from original")
	}
	cp.Status.BoundAt.Time = cp.Status.BoundAt.Add(time.Hour)
	cp.Status.DrainDeadline = &at
	if orig.Status.BoundAt.Time.Equal(cp.Status.BoundAt.Time) || orig.Status.DrainDeadline != nil {
		t.Fatal("mutating the copy changed the original")
	}
}

func TestNodePoolDeepCopyIsIndependent(t *testing.T) {
	grace := metav1.Duration{Duration: 10 * time.Minute}
	orig := &NodePool{Spec: NodePoolSpec{
		Preemption: PreemptionSpec{Policy: PreemptionLowerPriority, GracePeriod: &grace},
	}}
	cp := orig.DeepCopy()
	cp.Spec.Preemption.GracePeriod.Duration = time.Hour
	if orig.Spec.Preemption.GracePeriod.Duration != 10*time.Minute {
		t.Fatal("mutating the copy changed the original")
	}
}
