// Package catalog parses a Kubeyard catalog: the index, the addons it lists,
// and each addon's parameter schema. It only reads files. Installing an addon
// is the cluster's job.
package catalog

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"testing/fstest"

	"sigs.k8s.io/yaml"
)

// maxFileSize bounds each file read from an artifact.
const maxFileSize = 64 << 20

// Catalog is a parsed catalog index with its addons.
type Catalog struct {
	Name        string
	DisplayName string
	Addons      []Addon
}

// Addon is one installable component.
type Addon struct {
	Name        string
	DisplayName string
	Description string
	Category    string
	Version     string
	Namespace   string
	Scope       []string
	DependsOn   []string
	Health      []Health
	// Params are the leaves of the addon's parameter schema, sorted by path.
	Params []Param
}

// Health names an object whose presence shows the addon is installed.
type Health struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Param is one leaf of a parameter schema: a value the install form sets.
type Param struct {
	Path     string
	Type     string
	Title    string
	Default  any
	Required bool
	// Target says where the value goes: TargetValues for the Helm values
	// document, TargetSubstitute for a Flux postBuild variable.
	Target string
}

// Where a parameter's value is written, from x-kubeyard-target in the schema.
const (
	TargetValues     = "values"
	TargetSubstitute = "substitute"
)

// Status says whether an addon is already in a cluster.
type Status int

const (
	// Unknown means no health entry can be checked with Helm release, workload
	// or CRD lookups, such as a Kustomization.
	Unknown Status = iota
	// Absent means the addon's health objects are missing.
	Absent
	// Existing means a workload or CRD of the addon is present, but no Helm
	// release for it exists. The component was installed by other means, so
	// it must not be installed again.
	Existing
	// Installed means the addon's Helm release exists.
	Installed
)

// Status checks the addon against a cluster. has reports whether an object of
// the given kind exists, using the kinds "HelmRelease", "Deployment",
// "DaemonSet" and "CustomResourceDefinition" (cluster-scoped, so its namespace
// is empty).
func (a Addon) Status(has func(kind, namespace, name string) bool) Status {
	checkable := false
	existing := false
	for _, h := range a.Health {
		switch h.Kind {
		case "HelmRelease":
			checkable = true
			if has("HelmRelease", a.Namespace, h.Name) {
				return Installed
			}
			if has("Deployment", a.Namespace, h.Name) || has("DaemonSet", a.Namespace, h.Name) {
				existing = true
			}
		case "CustomResourceDefinition":
			checkable = true
			if has("CustomResourceDefinition", "", h.Name) {
				existing = true
			}
		}
	}
	switch {
	case existing:
		return Existing
	case checkable:
		return Absent
	default:
		return Unknown
	}
}

// Parse reads catalog.yaml at the root of fsys, and the addon.yaml and
// params schema of every addon the index lists.
func Parse(fsys fs.FS) (*Catalog, error) {
	var idx index
	if err := readYAML(fsys, "catalog.yaml", &idx); err != nil {
		return nil, err
	}
	c := &Catalog{Name: idx.Metadata.Name, DisplayName: idx.Spec.DisplayName}
	for _, ref := range idx.Spec.Addons {
		var file addonFile
		if err := readYAML(fsys, path.Join(ref.Path, "addon.yaml"), &file); err != nil {
			return nil, err
		}
		if file.Metadata.Name != ref.Name {
			return nil, fmt.Errorf("catalog lists %q at %s, but its addon.yaml is named %q", ref.Name, ref.Path, file.Metadata.Name)
		}
		if file.Spec.Version != ref.Version {
			return nil, fmt.Errorf("addon %q: index version %s, addon.yaml version %s", ref.Name, ref.Version, file.Spec.Version)
		}
		a := Addon{
			Name:        ref.Name,
			DisplayName: file.Spec.DisplayName,
			Description: file.Spec.Description,
			Category:    file.Spec.Category,
			Version:     file.Spec.Version,
			Namespace:   file.Spec.Namespace,
			Scope:       file.Spec.Scope,
			DependsOn:   file.Spec.DependsOn,
			Health:      file.Spec.Health,
		}
		if file.Spec.Params != "" {
			var schema schemaNode
			if err := readJSON(fsys, path.Join(ref.Path, file.Spec.Params), &schema); err != nil {
				return nil, err
			}
			a.Params = flattenParams("", schema, false, "", nil)
		}
		c.Addons = append(c.Addons, a)
	}
	return c, nil
}

// ReadArtifact reads a tar.gz artifact, as Flux stores a catalog, into an
// in-memory file system.
func ReadArtifact(r io.Reader) (fs.FS, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("artifact is not gzip: %w", err)
	}
	defer zr.Close()
	files := fstest.MapFS{}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read artifact: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxFileSize))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		files[path.Clean(hdr.Name)] = &fstest.MapFile{Data: data}
	}
	return files, nil
}

type index struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		DisplayName string `json:"displayName"`
		Addons      []struct {
			Name      string   `json:"name"`
			Category  string   `json:"category"`
			Scope     []string `json:"scope"`
			Version   string   `json:"version"`
			Path      string   `json:"path"`
			DependsOn []string `json:"dependsOn"`
		} `json:"addons"`
	} `json:"spec"`
}

type addonFile struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		DisplayName string   `json:"displayName"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Scope       []string `json:"scope"`
		Version     string   `json:"version"`
		Namespace   string   `json:"namespace"`
		DependsOn   []string `json:"dependsOn"`
		Params      string   `json:"params"`
		Health      []Health `json:"health"`
	} `json:"spec"`
}

// schemaNode is the part of a JSON Schema the install form uses.
type schemaNode struct {
	Type       string                `json:"type"`
	Title      string                `json:"title"`
	Default    any                   `json:"default"`
	Required   []string              `json:"required"`
	Target     string                `json:"x-kubeyard-target"`
	Properties map[string]schemaNode `json:"properties"`
}

// flattenParams turns nested object properties into dotted paths. Only
// leaves are kept, sorted by path. A leaf without its own target inherits the
// target of the property above it.
func flattenParams(prefix string, n schemaNode, required bool, target string, out []Param) []Param {
	if n.Target != "" {
		target = n.Target
	}
	if len(n.Properties) == 0 {
		if prefix != "" {
			out = append(out, Param{Path: prefix, Type: n.Type, Title: n.Title, Default: n.Default, Required: required, Target: target})
		}
		return out
	}
	keys := make([]string, 0, len(n.Properties))
	for k := range n.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := n.Properties[k]
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		out = flattenParams(p, child, contains(n.Required, k), target, out)
	}
	return out
}

func contains(items []string, s string) bool {
	for _, item := range items {
		if item == s {
			return true
		}
	}
	return false
}

func readYAML(fsys fs.FS, name string, v any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return nil
}

func readJSON(fsys fs.FS, name string, v any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return nil
}
