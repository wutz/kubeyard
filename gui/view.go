package main

import (
	"sort"
	"strings"
	"time"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/catalog"
	"github.com/wutz/kubeyard/pkg/kube"
)

// The views are the JSON the page reads. They are built from the pkg types, so
// the wire format is decided here and the cluster code does not depend on it.

type overviewView struct {
	NodesReady      int        `json:"nodesReady"`
	NodesTotal      int        `json:"nodesTotal"`
	GPUTotal        int64      `json:"gpuTotal"`
	GPURequested    int64      `json:"gpuRequested"`
	PodsRunning     int        `json:"podsRunning"`
	PodsPending     int        `json:"podsPending"`
	PodsUnscheduled int        `json:"podsUnscheduled"`
	PodsFailed      int        `json:"podsFailed"`
	Nodes           []nodeView `json:"nodes"`
}

type nodeView struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Ready        bool     `json:"ready"`
	Roles        []string `json:"roles"`
	GPUProduct   string   `json:"gpuProduct"`
	GPUTotal     int64    `json:"gpuTotal"`
	GPURequested int64    `json:"gpuRequested"`
	CPUMilli     int64    `json:"cpuMilli"`
	CPUReqMilli  int64    `json:"cpuRequestedMilli"`
	MemBytes     int64    `json:"memBytes"`
	MemReqBytes  int64    `json:"memRequestedBytes"`
}

func toOverviewView(ov kube.Overview) overviewView {
	out := overviewView{
		NodesReady:      ov.NodesReady,
		NodesTotal:      len(ov.Nodes),
		GPUTotal:        ov.GPUTotal,
		GPURequested:    ov.GPURequested,
		PodsRunning:     ov.PodsRunning,
		PodsPending:     ov.PodsPending,
		PodsUnscheduled: ov.PodsUnscheduled,
		PodsFailed:      ov.PodsFailed,
		Nodes:           make([]nodeView, 0, len(ov.Nodes)),
	}
	for _, n := range ov.Nodes {
		out.Nodes = append(out.Nodes, nodeView{
			Name:         n.Name,
			Version:      n.Version,
			Ready:        n.Ready,
			Roles:        n.Roles,
			GPUProduct:   n.GPUProduct,
			GPUTotal:     n.GPUTotal,
			GPURequested: n.GPURequested,
			CPUMilli:     n.CPUMilli,
			CPUReqMilli:  n.CPUReqMilli,
			MemBytes:     n.MemBytes,
			MemReqBytes:  n.MemReqBytes,
		})
	}
	return out
}

// poolsView is the resource pool page. Installed is false when the cluster does
// not serve NodePools, so the page explains the missing operator instead.
type poolsView struct {
	Installed bool       `json:"installed"`
	Pools     []poolView `json:"pools"`
}

// poolView is one NodePool: the counts its status reports, and the virtual
// clusters that claim it. Pools and claims are sorted by name.
type poolView struct {
	Name        string      `json:"name"`
	DisplayName string      `json:"displayName"`
	Total       int32       `json:"total"`
	Free        int32       `json:"free"`
	Reserved    int32       `json:"reserved"`
	Unhealthy   int32       `json:"unhealthy"`
	Claims      []claimView `json:"claims"`
}

// claimView is one virtual cluster's claim on a pool.
type claimView struct {
	VirtualCluster string `json:"virtualCluster"`
	DisplayName    string `json:"displayName"`
	Min            int32  `json:"min"`
	Max            int32  `json:"max"`
	Desired        int32  `json:"desired"`
}

// toPoolsView builds the resource pool page from the NodePools and the
// VirtualClusters of a cluster. Claims on a pool that is not listed are dropped.
func toPoolsView(installed bool, pools []v1alpha1.NodePool, vcs []v1alpha1.VirtualCluster) poolsView {
	out := poolsView{Installed: installed, Pools: []poolView{}}
	if !installed {
		return out
	}
	index := make(map[string]int, len(pools))
	for _, p := range pools {
		index[p.Name] = len(out.Pools)
		out.Pools = append(out.Pools, poolView{
			Name:        p.Name,
			DisplayName: p.Spec.DisplayName,
			Total:       p.Status.Total,
			Free:        p.Status.Free,
			Reserved:    p.Status.Reserved,
			Unhealthy:   p.Status.Unhealthy,
			Claims:      []claimView{},
		})
	}
	for _, vc := range vcs {
		for _, c := range vc.Spec.PoolClaims {
			i, ok := index[c.Pool]
			if !ok {
				continue
			}
			out.Pools[i].Claims = append(out.Pools[i].Claims, claimView{
				VirtualCluster: vc.Name,
				DisplayName:    vc.Spec.DisplayName,
				Min:            c.Min,
				Max:            c.Max,
				Desired:        c.Desired,
			})
		}
	}
	sort.Slice(out.Pools, func(i, j int) bool { return out.Pools[i].Name < out.Pools[j].Name })
	for i := range out.Pools {
		claims := out.Pools[i].Claims
		sort.Slice(claims, func(a, b int) bool { return claims[a].VirtualCluster < claims[b].VirtualCluster })
	}
	return out
}

// catalogView is the component catalog of a context and what the cluster runs
// of it. Addons is nil when no catalog can be shown; Error or Note says why.
type catalogView struct {
	Source          *sourceView `json:"source"`
	Note            string      `json:"note,omitempty"`
	Error           string      `json:"error,omitempty"`
	FluxInstalled   bool        `json:"fluxInstalled"`
	FluxInstallable bool        `json:"fluxInstallable"`
	Addons          []addonView `json:"addons"`
}

// sourceView is the OCIRepository that serves the catalog.
type sourceView struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Revision  string `json:"revision"`
	Ready     bool   `json:"ready"`
	Verified  bool   `json:"verified"`
	Message   string `json:"message"`
}

func toSourceView(src kube.Source) *sourceView {
	return &sourceView{
		Namespace: src.Namespace,
		Name:      src.Name,
		Revision:  shortRevision(src.Revision),
		Ready:     src.Ready,
		Verified:  src.Verified,
		Message:   src.Message,
	}
}

// addonView is one component of the catalog. Status is one of "installed",
// "existing", "absent" and "unknown". InstallBlocked and UninstallBlocked hold
// the reason an action is refused, and are empty when it is allowed.
type addonView struct {
	Name             string         `json:"name"`
	DisplayName      string         `json:"displayName"`
	Description      string         `json:"description"`
	Category         string         `json:"category"`
	Version          string         `json:"version"`
	Namespace        string         `json:"namespace"`
	Scope            []string       `json:"scope"`
	DependsOn        []string       `json:"dependsOn"`
	Health           []healthView   `json:"health"`
	Params           []paramView    `json:"params"`
	Status           string         `json:"status"`
	Managed          bool           `json:"managed"`
	Current          map[string]any `json:"current"`
	InstallBlocked   string         `json:"installBlocked"`
	UninstallBlocked string         `json:"uninstallBlocked"`
	UninstallWarning string         `json:"uninstallWarning"`
	Flux             *fluxView      `json:"flux"`
	FluxError        string         `json:"fluxError,omitempty"`
}

type healthView struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type paramView struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Required bool   `json:"required"`
	Default  any    `json:"default"`
}

// fluxView is the Flux state of an addon Kubeyard manages.
type fluxView struct {
	Conditions []conditionView `json:"conditions"`
	Events     []eventView     `json:"events"`
}

type conditionView struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Message string `json:"message"`
}

type eventView struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Reason  string    `json:"reason"`
	Object  string    `json:"object"`
	Message string    `json:"message"`
}

// addonBase fills the parts of an addon's view that come from the catalog and
// the cluster's inventory. The Flux state and current parameters are added by
// the caller, which reads them from the cluster.
func addonBase(cat *catalog.Catalog, ad catalog.Addon, statuses map[string]catalog.Status, managed bool) addonView {
	out := addonView{
		Name:        ad.Name,
		DisplayName: ad.DisplayName,
		Description: ad.Description,
		Category:    ad.Category,
		Version:     ad.Version,
		Namespace:   ad.Namespace,
		Scope:       ad.Scope,
		DependsOn:   ad.DependsOn,
		Status:      statusName(statuses[ad.Name]),
		Managed:     managed,
	}
	for _, h := range ad.Health {
		out.Health = append(out.Health, healthView{Kind: h.Kind, Name: h.Name})
	}
	for _, p := range ad.Params {
		out.Params = append(out.Params, paramView{
			Path:     p.Path,
			Type:     p.Type,
			Title:    p.Title,
			Required: p.Required,
			Default:  p.Default,
		})
	}
	if err := ad.Installable(statuses[ad.Name], statuses); err != nil {
		out.InstallBlocked = err.Error()
	}
	if err := cat.Uninstallable(ad.Name, statuses, managed); err != nil {
		out.UninstallBlocked = err.Error()
	}
	out.UninstallWarning = ad.UninstallWarning()
	return out
}

func toFluxView(fx kube.AddonFlux) *fluxView {
	out := &fluxView{
		Conditions: make([]conditionView, 0, len(fx.Conditions)),
		Events:     make([]eventView, 0, len(fx.Events)),
	}
	for _, c := range fx.Conditions {
		out.Conditions = append(out.Conditions, conditionView{Kind: c.Kind, Name: c.Name, Ready: c.Ready, Message: c.Message})
	}
	for _, ev := range fx.Events {
		out.Events = append(out.Events, eventView{Time: ev.Time, Type: ev.Type, Reason: ev.Reason, Object: ev.Object, Message: ev.Message})
	}
	return out
}

func statusName(s catalog.Status) string {
	switch s {
	case catalog.Installed:
		return "installed"
	case catalog.Existing:
		return "existing"
	case catalog.Absent:
		return "absent"
	default:
		return "unknown"
	}
}

// shortRevision shortens the digest in "tag@sha256:…" for display.
func shortRevision(rev string) string {
	const digestLen = 12
	const prefix = "sha256:"
	i := strings.Index(rev, prefix)
	if i < 0 || len(rev) <= i+len(prefix)+digestLen {
		return rev
	}
	return rev[:i+len(prefix)+digestLen] + "…"
}
