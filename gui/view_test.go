package main

import (
	"testing"

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
