package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the API group of the kubeyard CRDs.
const GroupName = "kubeyard.wutz.dev"

// SchemeGroupVersion is the group and version that the types are registered under.
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

var (
	// SchemeBuilder collects the function that registers the types.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme registers the types in a runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&VirtualCluster{}, &VirtualClusterList{},
		&NodePool{}, &NodePoolList{},
		&NodeLease{}, &NodeLeaseList{},
	)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
}
