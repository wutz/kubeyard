package main

import (
	"testing"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/catalog"
)

func testCatalog() *catalog.Catalog {
	return &catalog.Catalog{Addons: []catalog.Addon{
		{
			Name:      "nfd",
			Namespace: "nfd",
			Health:    []catalog.Health{{Kind: "HelmRelease", Name: "nfd"}},
		},
		{
			Name:      "gpu-operator",
			Namespace: "gpu-operator",
			DependsOn: []string{"nfd"},
			Health:    []catalog.Health{{Kind: "CustomResourceDefinition", Name: "clusterpolicies.nvidia.com"}},
		},
	}}
}

func TestAddonBaseInstallGuard(t *testing.T) {
	cat := testCatalog()
	statuses := map[string]catalog.Status{"nfd": catalog.Absent, "gpu-operator": catalog.Absent}

	nfd, _ := cat.Addon("nfd")
	gpu, _ := cat.Addon("gpu-operator")
	if v := addonBase(cat, nfd, statuses, false); v.InstallBlocked != "" || v.Status != "absent" {
		t.Errorf("nfd = %+v, want installable and absent", v)
	}
	if v := addonBase(cat, gpu, statuses, false); v.InstallBlocked != "依赖 nfd 未就绪" {
		t.Errorf("gpu-operator InstallBlocked = %q", v.InstallBlocked)
	}

	statuses["nfd"] = catalog.Installed
	if v := addonBase(cat, nfd, statuses, true); v.InstallBlocked != "已安装，不会重复安装" || v.Status != "installed" {
		t.Errorf("installed nfd = %+v", v)
	}
	if v := addonBase(cat, gpu, statuses, false); v.InstallBlocked != "" {
		t.Errorf("gpu-operator with nfd installed should be installable, got %q", v.InstallBlocked)
	}
}

func TestAddonBaseUninstallGuard(t *testing.T) {
	cat := testCatalog()
	statuses := map[string]catalog.Status{"nfd": catalog.Installed, "gpu-operator": catalog.Installed}
	nfd, _ := cat.Addon("nfd")

	if v := addonBase(cat, nfd, statuses, false); v.UninstallBlocked != "不是由 Kubeyard 安装的，不会卸载" {
		t.Errorf("unmanaged UninstallBlocked = %q", v.UninstallBlocked)
	}
	if v := addonBase(cat, nfd, statuses, true); v.UninstallBlocked != "被 gpu-operator 依赖，请先卸载它们" {
		t.Errorf("dependent UninstallBlocked = %q", v.UninstallBlocked)
	}
}

func TestAddonBaseWarnsAboutCRDs(t *testing.T) {
	cat := testCatalog()
	gpu, _ := cat.Addon("gpu-operator")
	v := addonBase(cat, gpu, map[string]catalog.Status{}, true)
	if v.UninstallWarning == "" {
		t.Errorf("gpu-operator declares a CRD, want an uninstall warning")
	}
	if len(v.Health) != 1 || v.Health[0].Kind != "CustomResourceDefinition" {
		t.Errorf("health = %+v", v.Health)
	}
}

func TestStatusName(t *testing.T) {
	cases := map[catalog.Status]string{
		catalog.Installed: "installed",
		catalog.Existing:  "existing",
		catalog.Absent:    "absent",
		catalog.Unknown:   "unknown",
	}
	for s, want := range cases {
		if got := statusName(s); got != want {
			t.Errorf("statusName(%v) = %q, want %q", s, got, want)
		}
	}
}

func TestShortRevision(t *testing.T) {
	cases := map[string]string{
		"stable@sha256:abcdef0123456789abcdef": "stable@sha256:abcdef012345…",
		"v2026.10.5":                           "v2026.10.5",
		"stable@sha256:abc":                    "stable@sha256:abc",
	}
	for in, want := range cases {
		if got := shortRevision(in); got != want {
			t.Errorf("shortRevision(%q) = %q, want %q", in, got, want)
		}
	}
}

func testPool(name, display string, total, free, reserved, unhealthy int32) v1alpha1.NodePool {
	p := v1alpha1.NodePool{}
	p.Name = name
	p.Spec.DisplayName = display
	p.Status = v1alpha1.NodePoolStatus{Total: total, Free: free, Reserved: reserved, Unhealthy: unhealthy}
	return p
}

func testVC(name, display string, claims ...v1alpha1.PoolClaim) v1alpha1.VirtualCluster {
	vc := v1alpha1.VirtualCluster{}
	vc.Name = name
	vc.Spec.DisplayName = display
	vc.Spec.PoolClaims = claims
	return vc
}

func TestToPoolsViewCountsAndClaims(t *testing.T) {
	pools := []v1alpha1.NodePool{
		testPool("rtx-5090-x8", "rtx-5090.x8", 24, 19, 4, 1),
		testPool("a100-x8", "", 8, 0, 0, 0),
	}
	vcs := []v1alpha1.VirtualCluster{
		testVC("beta", "Beta", v1alpha1.PoolClaim{Pool: "rtx-5090-x8", Min: 2, Max: 6, Desired: 3}),
		testVC("acme", "ACME 生产", v1alpha1.PoolClaim{Pool: "rtx-5090-x8", Min: 2, Max: 8, Desired: 4}, v1alpha1.PoolClaim{Pool: "missing", Min: 1, Max: 1}),
	}

	got := toPoolsView(true, pools, vcs)
	if !got.Installed {
		t.Fatalf("Installed = false, want true")
	}
	if len(got.Pools) != 2 || got.Pools[0].Name != "a100-x8" || got.Pools[1].Name != "rtx-5090-x8" {
		t.Fatalf("pools = %+v, want a100-x8 then rtx-5090-x8", got.Pools)
	}

	a100 := got.Pools[0]
	if a100.Claims == nil || len(a100.Claims) != 0 {
		t.Errorf("a100 claims = %+v, want an empty non-nil list", a100.Claims)
	}

	rtx := got.Pools[1]
	if rtx.DisplayName != "rtx-5090.x8" || rtx.Total != 24 || rtx.Free != 19 || rtx.Reserved != 4 || rtx.Unhealthy != 1 {
		t.Errorf("rtx counts = %+v", rtx)
	}
	want := []claimView{
		{VirtualCluster: "acme", DisplayName: "ACME 生产", Min: 2, Max: 8, Desired: 4},
		{VirtualCluster: "beta", DisplayName: "Beta", Min: 2, Max: 6, Desired: 3},
	}
	if len(rtx.Claims) != len(want) {
		t.Fatalf("rtx claims = %+v, want %+v", rtx.Claims, want)
	}
	for i := range want {
		if rtx.Claims[i] != want[i] {
			t.Errorf("claim %d = %+v, want %+v", i, rtx.Claims[i], want[i])
		}
	}
}

func TestToPoolsViewEmpty(t *testing.T) {
	got := toPoolsView(true, nil, nil)
	if !got.Installed {
		t.Errorf("Installed = false, want true when the CRD is served")
	}
	if got.Pools == nil || len(got.Pools) != 0 {
		t.Errorf("pools = %#v, want an empty non-nil list", got.Pools)
	}
}

func TestToPoolsViewNotInstalled(t *testing.T) {
	// Objects that might be left over are ignored when the CRD is not served.
	pools := []v1alpha1.NodePool{testPool("rtx-5090-x8", "", 1, 1, 0, 0)}
	got := toPoolsView(false, pools, nil)
	if got.Installed {
		t.Errorf("Installed = true, want false")
	}
	if got.Pools == nil || len(got.Pools) != 0 {
		t.Errorf("pools = %#v, want an empty non-nil list", got.Pools)
	}
}
