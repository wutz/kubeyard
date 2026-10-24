package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
	"testing/fstest"
)

const indexYAML = `
apiVersion: catalog.kubeyard.wutz.dev/v1alpha1
kind: Catalog
metadata:
  name: kubeyard-default
spec:
  displayName: Kubeyard 默认目录
  addons:
    - name: nfd
      category: base
      scope: [host]
      version: 0.18.3
      path: addons/nfd
      dependsOn: []
    - name: gpu-operator
      category: ai
      scope: [host]
      version: v25.10.1
      path: addons/gpu-operator
      dependsOn: [nfd]
`

const nfdYAML = `
apiVersion: catalog.kubeyard.wutz.dev/v1alpha1
kind: Addon
metadata:
  name: nfd
spec:
  displayName: Node Feature Discovery
  description: 节点特征发现
  category: base
  scope: [host]
  version: 0.18.3
  namespace: nfd
  dependsOn: []
  health:
    - kind: HelmRelease
      name: nfd
`

const gpuYAML = `
apiVersion: catalog.kubeyard.wutz.dev/v1alpha1
kind: Addon
metadata:
  name: gpu-operator
spec:
  displayName: NVIDIA GPU Operator
  category: ai
  scope: [host]
  version: v25.10.1
  namespace: gpu-operator
  dependsOn: [nfd]
  params: schema.json
  health:
    - kind: HelmRelease
      name: gpu-operator
`

const gpuSchema = `{
  "type": "object",
  "required": ["driver"],
  "properties": {
    "driver": {
      "type": "object",
      "required": ["version"],
      "properties": {
        "enabled": { "type": "boolean", "default": false, "title": "由 Operator 安装 GPU 驱动" },
        "version": { "type": "string", "default": "580.95.05", "title": "驱动版本" }
      }
    },
    "dcgmExporter": {
      "type": "object",
      "properties": {
        "enabled": { "type": "boolean", "default": false, "title": "启用 DCGM Exporter" }
      }
    }
  }
}`

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"catalog.yaml":                    {Data: []byte(indexYAML)},
		"addons/nfd/addon.yaml":           {Data: []byte(nfdYAML)},
		"addons/gpu-operator/addon.yaml":  {Data: []byte(gpuYAML)},
		"addons/gpu-operator/schema.json": {Data: []byte(gpuSchema)},
	}
}

func TestParse(t *testing.T) {
	c, err := Parse(fixture())
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "kubeyard-default" || c.DisplayName != "Kubeyard 默认目录" {
		t.Fatalf("catalog metadata = %q %q", c.Name, c.DisplayName)
	}
	if len(c.Addons) != 2 {
		t.Fatalf("addons = %d, want 2", len(c.Addons))
	}
	nfd := c.Addons[0]
	if nfd.Namespace != "nfd" || nfd.Description != "节点特征发现" || len(nfd.Health) != 1 {
		t.Fatalf("nfd = %+v", nfd)
	}
	gpu := c.Addons[1]
	if len(gpu.DependsOn) != 1 || gpu.DependsOn[0] != "nfd" {
		t.Fatalf("gpu dependsOn = %v", gpu.DependsOn)
	}
	want := []Param{
		{Path: "dcgmExporter.enabled", Type: "boolean", Title: "启用 DCGM Exporter", Default: false},
		{Path: "driver.enabled", Type: "boolean", Title: "由 Operator 安装 GPU 驱动", Default: false},
		{Path: "driver.version", Type: "string", Title: "驱动版本", Default: "580.95.05", Required: true},
	}
	if len(gpu.Params) != len(want) {
		t.Fatalf("params = %+v", gpu.Params)
	}
	for i, p := range want {
		got := gpu.Params[i]
		if got.Path != p.Path || got.Type != p.Type || got.Title != p.Title || got.Default != p.Default || got.Required != p.Required {
			t.Errorf("param %d = %+v, want %+v", i, got, p)
		}
	}
}

func TestParseRejectsMismatch(t *testing.T) {
	fsys := fixture()
	// The addon's version no longer matches the index entry.
	fsys["addons/gpu-operator/addon.yaml"] = &fstest.MapFile{
		Data: bytes.Replace([]byte(gpuYAML), []byte("version: v25.10.1"), []byte("version: v25.9.0"), 1),
	}
	if _, err := Parse(fsys); err == nil {
		t.Fatal("Parse accepted an addon whose version differs from the index")
	}
}

func TestStatus(t *testing.T) {
	c, err := Parse(fixture())
	if err != nil {
		t.Fatal(err)
	}
	nfd, gpu := c.Addons[0], c.Addons[1]

	releases := map[string]bool{"HelmRelease/nfd/nfd": true}
	has := func(kind, ns, name string) bool { return releases[kind+"/"+ns+"/"+name] }
	if got := nfd.Status(has); got != Installed {
		t.Errorf("nfd with release = %v, want Installed", got)
	}
	if got := gpu.Status(has); got != Absent {
		t.Errorf("gpu with nothing = %v, want Absent", got)
	}

	releases["Deployment/gpu-operator/gpu-operator"] = true
	if got := gpu.Status(has); got != Existing {
		t.Errorf("gpu with deployment only = %v, want Existing", got)
	}

	crdOnly := Addon{Name: "gateway", Namespace: "kube-system", Health: []Health{{Kind: "Kustomization", Name: "addon-gateway"}}}
	if got := crdOnly.Status(has); got != Unknown {
		t.Errorf("non-Helm health = %v, want Unknown", got)
	}
}

func TestReadArtifact(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, f := range fixture() {
		if err := tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0o644, Size: int64(len(f.Data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	fsys, err := ReadArtifact(&buf)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Addons) != 2 {
		t.Fatalf("addons from artifact = %d, want 2", len(c.Addons))
	}
}

func TestReadArtifactRejectsNonGzip(t *testing.T) {
	if _, err := ReadArtifact(io.NopCloser(bytes.NewReader([]byte("plain")))); err == nil {
		t.Fatal("ReadArtifact accepted non-gzip input")
	}
}
