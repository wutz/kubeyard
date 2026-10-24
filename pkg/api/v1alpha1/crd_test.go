package v1alpha1

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// schemaNode is the part of an openAPIV3Schema that these tests read.
type schemaNode struct {
	Type       string                `json:"type"`
	Required   []string              `json:"required"`
	Enum       []string              `json:"enum"`
	Properties map[string]schemaNode `json:"properties"`
	Items      *schemaNode           `json:"items"`
}

// crdManifest is the part of a CustomResourceDefinition that these tests read.
type crdManifest struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Group string `json:"group"`
		Scope string `json:"scope"`
		Names struct {
			Kind   string `json:"kind"`
			Plural string `json:"plural"`
		} `json:"names"`
		Versions []struct {
			Name    string `json:"name"`
			Served  bool   `json:"served"`
			Storage bool   `json:"storage"`
			Schema  struct {
				OpenAPIV3Schema schemaNode `json:"openAPIV3Schema"`
			} `json:"schema"`
		} `json:"versions"`
	} `json:"spec"`
}

func loadCRD(t *testing.T, file string) crdManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "crds", file))
	if err != nil {
		t.Fatal(err)
	}
	var crd crdManifest
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return crd
}

// schema returns the root openAPIV3Schema of the single served version.
func (c crdManifest) schema() schemaNode {
	return c.Spec.Versions[0].Schema.OpenAPIV3Schema
}

// jsonFields lists the JSON names of a struct's fields, sorted.
func jsonFields(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestCRDIdentity(t *testing.T) {
	tests := []struct {
		file, plural, kind string
	}{
		{"virtualcluster.yaml", "virtualclusters", "VirtualCluster"},
		{"nodepool.yaml", "nodepools", "NodePool"},
		{"nodelease.yaml", "nodeleases", "NodeLease"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			crd := loadCRD(t, tt.file)
			if crd.Spec.Group != GroupName {
				t.Fatalf("group = %q, want %q", crd.Spec.Group, GroupName)
			}
			if want := tt.plural + "." + GroupName; crd.Metadata.Name != want {
				t.Fatalf("metadata.name = %q, want %q", crd.Metadata.Name, want)
			}
			if crd.Spec.Names.Plural != tt.plural || crd.Spec.Names.Kind != tt.kind {
				t.Fatalf("names = %+v, want plural %s kind %s", crd.Spec.Names, tt.plural, tt.kind)
			}
			if crd.Spec.Scope != "Cluster" {
				t.Fatalf("scope = %q, want Cluster", crd.Spec.Scope)
			}
			if len(crd.Spec.Versions) != 1 {
				t.Fatalf("versions = %d, want 1", len(crd.Spec.Versions))
			}
			v := crd.Spec.Versions[0]
			if v.Name != "v1alpha1" || !v.Served || !v.Storage {
				t.Fatalf("version = %+v, want served and stored v1alpha1", v)
			}
		})
	}
}

// TestCRDSchemasMatchTypes fails when a field is added to or removed from a Go
// type without the matching change to its CRD schema.
func TestCRDSchemasMatchTypes(t *testing.T) {
	tests := []struct {
		file         string
		spec, status reflect.Type
	}{
		{"virtualcluster.yaml", reflect.TypeOf(VirtualClusterSpec{}), reflect.TypeOf(VirtualClusterStatus{})},
		{"nodepool.yaml", reflect.TypeOf(NodePoolSpec{}), reflect.TypeOf(NodePoolStatus{})},
		{"nodelease.yaml", reflect.TypeOf(NodeLeaseSpec{}), reflect.TypeOf(NodeLeaseStatus{})},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			root := loadCRD(t, tt.file).schema()
			checks := []struct {
				name string
				typ  reflect.Type
				node schemaNode
			}{
				{"spec", tt.spec, root.Properties["spec"]},
				{"status", tt.status, root.Properties["status"]},
			}
			for _, c := range checks {
				if got, want := sortedKeys(c.node.Properties), jsonFields(c.typ); !reflect.DeepEqual(got, want) {
					t.Fatalf("%s properties = %v, want %v from %s", c.name, got, want, c.typ.Name())
				}
			}
		})
	}

	t.Run("poolClaims items", func(t *testing.T) {
		spec := loadCRD(t, "virtualcluster.yaml").schema().Properties["spec"]
		items := spec.Properties["poolClaims"].Items
		if items == nil {
			t.Fatal("poolClaims has no items schema")
		}
		if got, want := sortedKeys(items.Properties), jsonFields(reflect.TypeOf(PoolClaim{})); !reflect.DeepEqual(got, want) {
			t.Fatalf("poolClaims properties = %v, want %v from PoolClaim", got, want)
		}
	})
}

// TestCRDEnumsMatchConstants keeps the enum lists in the schemas equal to the Go constants.
func TestCRDEnumsMatchConstants(t *testing.T) {
	tests := []struct {
		file string
		path []string
		want []string
	}{
		{"virtualcluster.yaml", []string{"status", "phase"}, []string{
			string(VirtualClusterPending), string(VirtualClusterProvisioning), string(VirtualClusterReady),
			string(VirtualClusterDegraded), string(VirtualClusterDraining), string(VirtualClusterDeleting),
		}},
		{"virtualcluster.yaml", []string{"spec", "exposure", "type"}, []string{
			string(ExposureLoadBalancer), string(ExposureIngress),
		}},
		{"nodepool.yaml", []string{"spec", "preemption", "policy"}, []string{
			string(PreemptionNever), string(PreemptionLowerPriority),
		}},
		{"nodelease.yaml", []string{"spec", "reason"}, []string{
			string(LeasePinned), string(LeaseMin), string(LeaseScale), string(LeasePreempt),
		}},
		{"nodelease.yaml", []string{"status", "phase"}, []string{
			string(LeaseBinding), string(LeaseBound), string(LeaseDraining), string(LeaseReleased),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.file+" "+strings.Join(tt.path, "."), func(t *testing.T) {
			node := loadCRD(t, tt.file).schema()
			for _, p := range tt.path {
				next, ok := node.Properties[p]
				if !ok {
					t.Fatalf("no property %q on the path %v", p, tt.path)
				}
				node = next
			}
			got := append([]string(nil), node.Enum...)
			want := append([]string(nil), tt.want...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("enum = %v, want %v", got, want)
			}
		})
	}
}
