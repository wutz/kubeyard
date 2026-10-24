package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/wutz/kubeyard/pkg/catalog"
)

var formParams = []catalog.Param{
	{Path: "gpu.count", Type: "integer"},
	{Path: "name", Type: "string", Required: true},
	{Path: "enabled", Type: "boolean"},
	{Path: "note", Type: "string"},
}

func TestBuildValues(t *testing.T) {
	got, err := buildValues(formParams, map[string]any{
		"gpu.count": "3",
		"name":      "  demo  ",
		"enabled":   true,
		"note":      "   ",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Trimmed text, typed integers, and an empty optional field left out so the
	// schema default applies.
	want := map[string]any{"gpu.count": 3, "name": "demo", "enabled": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values = %#v, want %#v", got, want)
	}
}

func TestBuildValuesErrors(t *testing.T) {
	cases := map[string]struct {
		fields map[string]any
		want   string
	}{
		"required missing":  {map[string]any{"name": " "}, "name 必填"},
		"required absent":   {map[string]any{}, "name 必填"},
		"integer not int":   {map[string]any{"name": "a", "gpu.count": "x"}, "gpu.count 必须是整数"},
		"text not a string": {map[string]any{"name": 5.0}, "name 必须是文本"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := buildValues(formParams, c.fields)
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestBuildValuesBooleanDefaultsToFalse(t *testing.T) {
	got, err := buildValues(formParams, map[string]any{"name": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got["enabled"] != false {
		t.Errorf("enabled = %v, want false", got["enabled"])
	}
}

func TestRenderAddonUnknownName(t *testing.T) {
	cat := &catalog.Catalog{}
	_, _, _, err := renderAddon(cat, "missing", nil)
	if err == nil || !strings.Contains(err.Error(), "目录中没有组件 missing") {
		t.Fatalf("err = %v", err)
	}
}
