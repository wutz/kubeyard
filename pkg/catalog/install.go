package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// Render splits form values, keyed by dotted parameter path, into the Helm
// values document and the postBuild substitutions. A path the schema does not
// declare is an error, so a stale form cannot write values nothing reads.
func (a Addon) Render(values map[string]any) (helm map[string]any, substitute map[string]string, err error) {
	known := make(map[string]Param, len(a.Params))
	for _, p := range a.Params {
		known[p.Path] = p
	}
	helm = map[string]any{}
	substitute = map[string]string{}
	for path, v := range values {
		p, ok := known[path]
		if !ok {
			return nil, nil, fmt.Errorf("addon %s: unknown parameter %q", a.Name, path)
		}
		if p.Target == TargetSubstitute {
			s, ok := v.(string)
			if !ok {
				return nil, nil, fmt.Errorf("addon %s: parameter %q must be a string", a.Name, path)
			}
			substitute[path] = s
			continue
		}
		setPath(helm, strings.Split(path, "."), v)
	}
	return helm, substitute, nil
}

// setPath stores v at the nested key path, creating the intermediate maps.
func setPath(m map[string]any, keys []string, v any) {
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = v
}

// Current returns the form values that Render turned into helm and substitute,
// keyed by parameter path. A parameter the install did not store is left out,
// so its form field starts at the schema default.
func (a Addon) Current(helm map[string]any, substitute map[string]string) map[string]any {
	out := map[string]any{}
	for _, p := range a.Params {
		if p.Target == TargetSubstitute {
			if s, ok := substitute[p.Path]; ok {
				out[p.Path] = s
			}
			continue
		}
		if v, ok := getPath(helm, strings.Split(p.Path, ".")); ok {
			out[p.Path] = v
		}
	}
	return out
}

// getPath returns the value at the nested key path, if every key is present.
func getPath(m map[string]any, keys []string) (any, bool) {
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	v, ok := m[keys[len(keys)-1]]
	return v, ok
}

// Statuses returns the status of every addon in the catalog, keyed by name.
func (c *Catalog) Statuses(has func(kind, namespace, name string) bool) map[string]Status {
	out := make(map[string]Status, len(c.Addons))
	for _, ad := range c.Addons {
		out[ad.Name] = ad.Status(has)
	}
	return out
}

// Addon returns the addon with the given name.
func (c *Catalog) Addon(name string) (Addon, bool) {
	for _, ad := range c.Addons {
		if ad.Name == name {
			return ad, true
		}
	}
	return Addon{}, false
}

// Dependents lists the addons that depend on name and are present in the
// cluster, installed or existing.
func (c *Catalog) Dependents(name string, statuses map[string]Status) []string {
	var out []string
	for _, ad := range c.Addons {
		if !contains(ad.DependsOn, name) {
			continue
		}
		if s := statuses[ad.Name]; s == Installed || s == Existing {
			out = append(out, ad.Name)
		}
	}
	return out
}

// Installable says why an addon cannot be installed now, or returns nil. It
// refuses anything the cluster already runs, including components installed
// outside Kubeyard, and anything whose dependencies are not present. deps maps
// each dependency name to its status in the cluster.
func (a Addon) Installable(status Status, deps map[string]Status) error {
	switch status {
	case Installed:
		return errors.New("已安装，不会重复安装")
	case Existing:
		return errors.New("已存在（非 Helm 安装），不会重复安装")
	case Unknown:
		return errors.New("无法判断是否已存在，拒绝安装")
	}
	for _, dep := range a.DependsOn {
		s, ok := deps[dep]
		if !ok || (s != Installed && s != Existing) {
			return fmt.Errorf("依赖 %s 未就绪", dep)
		}
	}
	return nil
}

// UninstallWarning says what removing the addon deletes beyond its own
// workloads: the CRDs its health check names, and every resource of those
// CRDs. It returns "" when the addon declares no CRD.
func (a Addon) UninstallWarning() string {
	var crds []string
	for _, h := range a.Health {
		if h.Kind == "CustomResourceDefinition" {
			crds = append(crds, h.Name)
		}
	}
	if len(crds) == 0 {
		return ""
	}
	return "卸载会删除这些 CRD 以及集群中的全部对应资源：" + strings.Join(crds, "、")
}

// Uninstallable says why the addon cannot be uninstalled now, or returns nil.
// Only addons Kubeyard installed are removed, so Helm releases from other
// installs are left alone. A present addon that depends on this one blocks the
// removal until it is removed first.
func (c *Catalog) Uninstallable(name string, statuses map[string]Status, managed bool) error {
	if !managed {
		return errors.New("不是由 Kubeyard 安装的，不会卸载")
	}
	if dependents := c.Dependents(name, statuses); len(dependents) > 0 {
		return fmt.Errorf("被 %s 依赖，请先卸载它们", strings.Join(dependents, ", "))
	}
	return nil
}
