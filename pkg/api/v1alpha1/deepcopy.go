package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *VirtualCluster) DeepCopyInto(out *VirtualCluster) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy of the VirtualCluster.
func (in *VirtualCluster) DeepCopy() *VirtualCluster {
	if in == nil {
		return nil
	}
	out := new(VirtualCluster)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *VirtualCluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *VirtualClusterList) DeepCopyInto(out *VirtualClusterList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]VirtualCluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the VirtualClusterList.
func (in *VirtualClusterList) DeepCopy() *VirtualClusterList {
	if in == nil {
		return nil
	}
	out := new(VirtualClusterList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *VirtualClusterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *VirtualClusterSpec) DeepCopyInto(out *VirtualClusterSpec) {
	*out = *in
	in.Exposure.DeepCopyInto(&out.Exposure)
	if in.ValuesOverride != nil {
		out.ValuesOverride = new(runtime.RawExtension)
		in.ValuesOverride.DeepCopyInto(out.ValuesOverride)
	}
	if in.PoolClaims != nil {
		out.PoolClaims = make([]PoolClaim, len(in.PoolClaims))
		for i := range in.PoolClaims {
			in.PoolClaims[i].DeepCopyInto(&out.PoolClaims[i])
		}
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *Exposure) DeepCopyInto(out *Exposure) {
	*out = *in
	if in.Ingress != nil {
		out.Ingress = new(IngressExposure)
		*out.Ingress = *in.Ingress
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *PoolClaim) DeepCopyInto(out *PoolClaim) {
	*out = *in
	if in.Autoscale != nil {
		out.Autoscale = new(Autoscale)
		in.Autoscale.DeepCopyInto(out.Autoscale)
	}
	if in.PinnedNodes != nil {
		out.PinnedNodes = make([]string, len(in.PinnedNodes))
		copy(out.PinnedNodes, in.PinnedNodes)
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *Autoscale) DeepCopyInto(out *Autoscale) {
	*out = *in
	if in.ScaleDownIdle != nil {
		out.ScaleDownIdle = new(metav1.Duration)
		*out.ScaleDownIdle = *in.ScaleDownIdle
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *VirtualClusterStatus) DeepCopyInto(out *VirtualClusterStatus) {
	*out = *in
	if in.AdminKubeconfigSecretRef != nil {
		out.AdminKubeconfigSecretRef = new(SecretReference)
		*out.AdminKubeconfigSecretRef = *in.AdminKubeconfigSecretRef
	}
	if in.Allocations != nil {
		out.Allocations = make([]PoolAllocation, len(in.Allocations))
		for i := range in.Allocations {
			in.Allocations[i].DeepCopyInto(&out.Allocations[i])
		}
	}
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *PoolAllocation) DeepCopyInto(out *PoolAllocation) {
	*out = *in
	if in.Nodes != nil {
		out.Nodes = make([]string, len(in.Nodes))
		copy(out.Nodes, in.Nodes)
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *NodePool) DeepCopyInto(out *NodePool) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy returns a deep copy of the NodePool.
func (in *NodePool) DeepCopy() *NodePool {
	if in == nil {
		return nil
	}
	out := new(NodePool)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *NodePool) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *NodePoolList) DeepCopyInto(out *NodePoolList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]NodePool, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the NodePoolList.
func (in *NodePoolList) DeepCopy() *NodePoolList {
	if in == nil {
		return nil
	}
	out := new(NodePoolList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *NodePoolList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *NodePoolSpec) DeepCopyInto(out *NodePoolSpec) {
	*out = *in
	in.NodeSelector.DeepCopyInto(&out.NodeSelector)
	in.Preemption.DeepCopyInto(&out.Preemption)
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *PreemptionSpec) DeepCopyInto(out *PreemptionSpec) {
	*out = *in
	if in.GracePeriod != nil {
		out.GracePeriod = new(metav1.Duration)
		*out.GracePeriod = *in.GracePeriod
	}
	if in.DrainTimeout != nil {
		out.DrainTimeout = new(metav1.Duration)
		*out.DrainTimeout = *in.DrainTimeout
	}
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *NodeLease) DeepCopyInto(out *NodeLease) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy of the NodeLease.
func (in *NodeLease) DeepCopy() *NodeLease {
	if in == nil {
		return nil
	}
	out := new(NodeLease)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *NodeLease) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *NodeLeaseList) DeepCopyInto(out *NodeLeaseList) {
	*out = *in
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]NodeLease, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the NodeLeaseList.
func (in *NodeLeaseList) DeepCopy() *NodeLeaseList {
	if in == nil {
		return nil
	}
	out := new(NodeLeaseList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject implements runtime.Object.
func (in *NodeLeaseList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver into out. out must be non-nil.
func (in *NodeLeaseStatus) DeepCopyInto(out *NodeLeaseStatus) {
	*out = *in
	if in.BoundAt != nil {
		out.BoundAt = new(metav1.Time)
		*out.BoundAt = *in.BoundAt
	}
	if in.DrainDeadline != nil {
		out.DrainDeadline = new(metav1.Time)
		*out.DrainDeadline = *in.DrainDeadline
	}
	if in.IdleSince != nil {
		out.IdleSince = new(metav1.Time)
		*out.IdleSince = *in.IdleSince
	}
}
