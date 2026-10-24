package pool

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func node(name string, ready *bool, nodeLabels map[string]string) corev1.Node {
	n := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: nodeLabels}}
	if ready != nil {
		status := corev1.ConditionFalse
		if *ready {
			status = corev1.ConditionTrue
		}
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	}
	return n
}

func boolPtr(b bool) *bool { return &b }

var (
	gpuLabels = map[string]string{"nvidia.com/gpu.product": "RTX-5090"}
	cpuLabels = map[string]string{"role": "cpu"}
	gpuPool   = metav1.LabelSelector{MatchLabels: gpuLabels}
)

func TestCompute(t *testing.T) {
	nodes := []corev1.Node{
		node("gpu-1", boolPtr(true), gpuLabels),  // free
		node("gpu-2", boolPtr(true), gpuLabels),  // leased, so not free
		node("gpu-3", boolPtr(false), gpuLabels), // not ready, and leased
		node("gpu-4", boolPtr(true), gpuLabels),  // free
		node("cpu-1", boolPtr(true), cpuLabels),  // not matched by the selector
	}
	got, err := Compute(gpuPool, nodes, []string{"gpu-2", "gpu-3"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Total: 4, Free: 2, Reserved: 2, Unhealthy: 1}
	if got != want {
		t.Errorf("Compute() = %+v, want %+v", got, want)
	}
}

func TestComputeTreatsMissingReadyConditionAsUnhealthy(t *testing.T) {
	nodes := []corev1.Node{node("gpu-1", nil, gpuLabels)}
	got, err := Compute(gpuPool, nodes, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unhealthy != 1 || got.Free != 0 || got.Total != 1 {
		t.Errorf("Compute() = %+v, want Total 1, Unhealthy 1, Free 0", got)
	}
}

func TestComputeSupportsMatchExpressions(t *testing.T) {
	selector := metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{"cpu"}},
	}}
	nodes := []corev1.Node{
		node("cpu-1", boolPtr(true), cpuLabels),
		node("gpu-1", boolPtr(true), gpuLabels),
	}
	got, err := Compute(selector, nodes, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.Free != 1 {
		t.Errorf("Compute() = %+v, want Total 1, Free 1", got)
	}
}

func TestComputeRejectsEmptySelector(t *testing.T) {
	nodes := []corev1.Node{node("gpu-1", boolPtr(true), gpuLabels)}
	if _, err := Compute(metav1.LabelSelector{}, nodes, nil, 0); err == nil {
		t.Error("empty selector: want an error, because it would match every node")
	}
}

func TestComputeRejectsInvalidSelector(t *testing.T) {
	selector := metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
		{Key: "role", Operator: "Bogus", Values: []string{"cpu"}},
	}}
	if _, err := Compute(selector, nil, nil, 0); err == nil {
		t.Error("unknown operator: want an error")
	}
}
