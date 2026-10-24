package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
)

// reservedValues are the chart paths the operator renders itself from the node
// binding (docs/operator.md §2). fromHost.nodes is what makes a virtual cluster
// see only its own nodes; toHost.pods.enforceTolerations is what makes its pods
// tolerate its taint. A valuesOverride that reached into either could point a
// virtual cluster at another cluster's nodes, so §2 says it may not.
var reservedValues = [][]string{
	{"sync", "fromHost", "nodes"},
	{"sync", "toHost", "pods"},
}

// checkValuesOverride rejects a valuesOverride that touches a reserved path.
func checkValuesOverride(raw *runtime.RawExtension) error {
	if raw == nil || len(raw.Raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw.Raw, &v); err != nil {
		return fmt.Errorf("valuesOverride 不是合法的 JSON: %w", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return errors.New("valuesOverride 必须是对象")
	}
	for _, path := range reservedValues {
		if hasPath(v, path) {
			return fmt.Errorf("valuesOverride 不能覆盖 %s：这一段由 operator 按节点绑定渲染（operator.md §2）", strings.Join(path, "."))
		}
	}
	return nil
}

// hasPath reports whether v holds the key at path, whatever its value. A null
// or empty object would still replace the rendered section, so the key being
// present is enough to reject.
func hasPath(v any, path []string) bool {
	for i, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		child, ok := m[key]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			return true
		}
		v = child
	}
	return false
}
