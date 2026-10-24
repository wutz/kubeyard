package kube

import (
	"context"
	_ "embed"
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// CatalogPublicKey is the cosign public key the default catalog is signed
// with. It is embedded so a source can be verified without fetching the key.
//
//go:embed cosign.pub
var CatalogPublicKey []byte

// DefaultCatalogURL is the OCI repository of the default catalog.
const DefaultCatalogURL = "oci://ghcr.io/wutz/kubeyard-catalog"

// catalogLabel marks the objects that belong to a catalog source.
const catalogLabel = "kubeyard.wutz.dev/catalog"

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

// AddCatalogSource creates the catalog source in the cluster: the cosign public
// key in a Secret, and an OCIRepository that follows the stable tag of url and
// rejects artifacts without a valid signature. Existing objects are updated in
// place. Flux must already be installed, since it reconciles the source.
func AddCatalogSource(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, url string, pubKey []byte) error {
	if err := ensureNamespace(ctx, cs, CatalogNamespace); err != nil {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cosignSecretName(),
			Namespace: CatalogNamespace,
			Labels:    map[string]string{catalogLabel: "default"},
		},
		Data: map[string][]byte{"cosign.pub": pubKey},
	}
	if err := upsertSecret(ctx, cs, secret); err != nil {
		return err
	}
	return upsertObject(ctx, dc.Resource(ociRepositories).Namespace(CatalogNamespace), catalogSource(url))
}

func cosignSecretName() string { return CatalogSource + "-cosign-pub" }

// catalogSource builds the OCIRepository for a catalog URL. Its tag is stable,
// which the catalog publishes after each release.
func catalogSource(url string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "source.toolkit.fluxcd.io/v1",
		"kind":       "OCIRepository",
		"metadata": map[string]any{
			"name":      CatalogSource,
			"namespace": CatalogNamespace,
			"labels":    map[string]any{catalogLabel: "default"},
		},
		"spec": map[string]any{
			"url":      url,
			"ref":      map[string]any{"tag": "stable"},
			"interval": "1h",
			"verify": map[string]any{
				"provider":  "cosign",
				"secretRef": map[string]any{"name": cosignSecretName()},
			},
		},
	}}
}

func ensureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func upsertSecret(ctx context.Context, cs kubernetes.Interface, secret *corev1.Secret) error {
	client := cs.CoreV1().Secrets(secret.Namespace)
	cur, err := client.Get(ctx, secret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, secret, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	cur.Labels = secret.Labels
	cur.Data = secret.Data
	_, err = client.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}
