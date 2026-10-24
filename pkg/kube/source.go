package kube

import (
	"context"
	"errors"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var ociRepositories = schema.GroupVersionResource{
	Group:    "source.toolkit.fluxcd.io",
	Version:  "v1",
	Resource: "ocirepositories",
}

// ErrNoSource means the cluster has no OCIRepository with the given name.
var ErrNoSource = errors.New("OCIRepository not found")

// Source is the state of an OCIRepository that serves a catalog.
type Source struct {
	Namespace string
	Name      string
	// URL is the repository the source pulls from, without a tag or digest.
	URL string
	// Revision is the tag and digest Flux resolved, for example
	// "stable@sha256:...".
	Revision string
	// Ready is the Ready condition. Message explains it when it is false.
	Ready   bool
	Message string
	// Verified is the SourceVerified condition: Flux checked the cosign
	// signature of the artifact at Revision.
	Verified bool
}

// LoadSource reads the status of an OCIRepository.
func LoadSource(ctx context.Context, cfg *rest.Config, namespace, name string) (Source, error) {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return Source{}, err
	}
	obj, err := dc.Resource(ociRepositories).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Source{}, ErrNoSource
	}
	if err != nil {
		return Source{}, err
	}
	s := Source{Namespace: namespace, Name: name}
	s.URL, _, _ = unstructured.NestedString(obj.Object, "spec", "url")
	s.Revision, _, _ = unstructured.NestedString(obj.Object, "status", "artifact", "revision")
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := cond["type"].(string)
		status, _ := cond["status"].(string)
		msg, _ := cond["message"].(string)
		switch typ {
		case "Ready":
			s.Ready = status == "True"
			s.Message = msg
		case "SourceVerified":
			s.Verified = status == "True"
		}
	}
	return s, nil
}

// Ref is the artifact at the revision Flux verified, pinned by digest, for
// example "oci://ghcr.io/wutz/kubeyard-catalog@sha256:...". It is empty when
// Flux has not resolved a revision yet.
func (s Source) Ref() string {
	_, digest, ok := strings.Cut(s.Revision, "@")
	if !ok || digest == "" || s.URL == "" {
		return ""
	}
	return "oci://" + strings.TrimPrefix(s.URL, "oci://") + "@" + digest
}
