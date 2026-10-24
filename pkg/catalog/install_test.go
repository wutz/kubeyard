package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func testAddon() Addon {
	return Addon{
		Name:      "metallb",
		Namespace: "metallb-system",
		DependsOn: []string{"nfd"},
		Params: []Param{
			{Path: "metallb_addresses", Type: "string", Target: TargetSubstitute},
			{Path: "driver.enabled", Type: "boolean", Target: TargetValues},
			{Path: "driver.version", Type: "string", Target: TargetValues},
		},
	}
}

func TestUninstallWarningNamesCRDs(t *testing.T) {
	gateway := Addon{Name: "gateway-api-crds", Health: []Health{{Kind: "CustomResourceDefinition", Name: "gateways.gateway.networking.k8s.io"}}}
	if got := gateway.UninstallWarning(); !strings.Contains(got, "gateways.gateway.networking.k8s.io") {
		t.Errorf("warning = %q, want it to name the CRD", got)
	}
	nfd := Addon{Name: "nfd", Health: []Health{{Kind: "HelmRelease", Name: "nfd"}}}
	if got := nfd.UninstallWarning(); got != "" {
		t.Errorf("addon without CRDs warning = %q, want empty", got)
	}
}

func TestCurrentRoundTripsRender(t *testing.T) {
	ad := Addon{Name: "x", Params: []Param{
		{Path: "driver.enabled", Type: "boolean", Target: TargetValues},
		{Path: "driver.version", Type: "string", Target: TargetValues},
		{Path: "vip", Type: "string", Target: TargetSubstitute},
	}}
	values := map[string]any{"driver.enabled": true, "driver.version": "580.95.05", "vip": "192.0.2.10"}
	helm, substitute, err := ad.Render(values)
	if err != nil {
		t.Fatal(err)
	}
	got := ad.Current(helm, substitute)
	if len(got) != len(values) {
		t.Fatalf("Current = %v, want %v", got, values)
	}
	for path, want := range values {
		if got[path] != want {
			t.Errorf("%s = %v, want %v", path, got[path], want)
		}
	}
	if empty := ad.Current(map[string]any{}, nil); len(empty) != 0 {
		t.Errorf("Current of nothing stored = %v, want empty", empty)
	}
}

func TestRenderSplitsTargets(t *testing.T) {
	helm, sub, err := testAddon().Render(map[string]any{
		"metallb_addresses": "192.0.2.10-192.0.2.20",
		"driver.enabled":    true,
		"driver.version":    "580.95.05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sub, map[string]string{"metallb_addresses": "192.0.2.10-192.0.2.20"}) {
		t.Fatalf("substitute = %v", sub)
	}
	want := map[string]any{"driver": map[string]any{"enabled": true, "version": "580.95.05"}}
	if !reflect.DeepEqual(helm, want) {
		t.Fatalf("helm = %v, want %v", helm, want)
	}
}

func TestRenderRejectsUnknownPath(t *testing.T) {
	if _, _, err := testAddon().Render(map[string]any{"nope": 1}); err == nil {
		t.Fatal("Render accepted a parameter the schema does not declare")
	}
}

func TestRenderRejectsWrongSubstituteType(t *testing.T) {
	if _, _, err := testAddon().Render(map[string]any{"metallb_addresses": true}); err == nil {
		t.Fatal("Render accepted a non-string postBuild value")
	}
}

func testCatalog() *Catalog {
	return &Catalog{Addons: []Addon{
		{Name: "nfd"},
		{Name: "gpu-operator", DependsOn: []string{"nfd"}},
		{Name: "dcgm", DependsOn: []string{"gpu-operator"}},
	}}
}

func TestDependents(t *testing.T) {
	c := testCatalog()
	statuses := map[string]Status{"nfd": Installed, "gpu-operator": Existing, "dcgm": Absent}
	if got := c.Dependents("nfd", statuses); !reflect.DeepEqual(got, []string{"gpu-operator"}) {
		t.Errorf("Dependents(nfd) = %v", got)
	}
	if got := c.Dependents("dcgm", statuses); got != nil {
		t.Errorf("Dependents(dcgm) = %v, want none: dcgm is absent", got)
	}
}

func TestUninstallable(t *testing.T) {
	c := testCatalog()
	statuses := map[string]Status{"nfd": Installed, "gpu-operator": Existing, "dcgm": Absent}
	if err := c.Uninstallable("dcgm", statuses, true); err != nil {
		t.Errorf("managed addon without dependents: %v", err)
	}
	if err := c.Uninstallable("dcgm", statuses, false); err == nil {
		t.Error("addon not installed by Kubeyard was allowed")
	}
	if err := c.Uninstallable("nfd", statuses, true); err == nil {
		t.Error("addon with a present dependent was allowed")
	}
}

func TestInstallable(t *testing.T) {
	a := testAddon()
	ready := map[string]Status{"nfd": Installed}
	cases := []struct {
		name   string
		status Status
		deps   map[string]Status
		ok     bool
	}{
		{"absent with ready dependency", Absent, ready, true},
		{"dependency existing counts as ready", Absent, map[string]Status{"nfd": Existing}, true},
		{"already installed", Installed, ready, false},
		{"existing outside Helm", Existing, ready, false},
		{"status unknown", Unknown, ready, false},
		{"dependency missing", Absent, map[string]Status{"nfd": Absent}, false},
		{"dependency not in catalog", Absent, nil, false},
	}
	for _, tc := range cases {
		err := a.Installable(tc.status, tc.deps)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
