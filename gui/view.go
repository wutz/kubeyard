package main

import (
	"strings"
	"time"

	"github.com/wutz/kubeyard/pkg/catalog"
	"github.com/wutz/kubeyard/pkg/kube"
)

// The views are the JSON the page reads. They are built from the pkg types, so
// the wire format is decided here and the cluster code does not depend on it.

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
