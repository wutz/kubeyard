package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func gpuNode(name string, gpus string, ready corev1.ConditionStatus) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				gpuProductKey:                "NVIDIA-GeForce-RTX-5090",
				rolePrefix + "control-plane": "",
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.32.5"},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: ready},
			},
			Allocatable: corev1.ResourceList{
				gpuResource:           resource.MustParse(gpus),
				corev1.ResourceCPU:    resource.MustParse("32"),
				corev1.ResourceMemory: resource.MustParse("128Gi"),
			},
		},
	}
}

func pod(name, node string, phase corev1.PodPhase, c corev1.ResourceRequirements) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "vc-a"},
		Spec: corev1.PodSpec{
			NodeName:   node,
			Containers: []corev1.Container{{Name: "main", Resources: c}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func TestSummarize(t *testing.T) {
	nodes := []corev1.Node{
		gpuNode("gpu-2", "8", corev1.ConditionTrue),
		gpuNode("gpu-1", "8", corev1.ConditionFalse),
	}
	pods := []corev1.Pod{
		pod("train", "gpu-1", corev1.PodRunning, corev1.ResourceRequirements{
			Requests: corev1.ResourceList{gpuResource: resource.MustParse("4")},
		}),
		// GPU limit with no request: the request defaults to the limit.
		pod("infer", "gpu-1", corev1.PodRunning, corev1.ResourceRequirements{
			Limits: corev1.ResourceList{gpuResource: resource.MustParse("2")},
		}),
		pod("waiting", "", corev1.PodPending, corev1.ResourceRequirements{
			Requests: corev1.ResourceList{gpuResource: resource.MustParse("8")},
		}),
		pod("done", "gpu-2", corev1.PodSucceeded, corev1.ResourceRequirements{
			Requests: corev1.ResourceList{gpuResource: resource.MustParse("8")},
		}),
		pod("broken", "gpu-2", corev1.PodFailed, corev1.ResourceRequirements{}),
	}

	ov := Summarize(nodes, pods)

	if ov.NodesReady != 1 {
		t.Errorf("NodesReady = %d, want 1", ov.NodesReady)
	}
	if ov.GPUTotal != 16 {
		t.Errorf("GPUTotal = %d, want 16", ov.GPUTotal)
	}
	if ov.GPURequested != 6 {
		t.Errorf("GPURequested = %d, want 6 (succeeded pods must not count)", ov.GPURequested)
	}
	if ov.PodsRunning != 2 || ov.PodsPending != 1 || ov.PodsUnscheduled != 1 || ov.PodsFailed != 1 {
		t.Errorf("pod counts = running %d pending %d unscheduled %d failed %d",
			ov.PodsRunning, ov.PodsPending, ov.PodsUnscheduled, ov.PodsFailed)
	}
	if got := ov.Nodes[0].Name; got != "gpu-1" {
		t.Errorf("nodes not sorted by name, first = %q", got)
	}
	if got := ov.Nodes[0].GPURequested; got != 6 {
		t.Errorf("gpu-1 GPURequested = %d, want 6", got)
	}
	if got := ov.Nodes[0].Roles; len(got) != 1 || got[0] != "control-plane" {
		t.Errorf("roles = %v, want [control-plane]", got)
	}
	if got := ov.Nodes[0].GPUProduct; got != "NVIDIA-GeForce-RTX-5090" {
		t.Errorf("GPUProduct = %q", got)
	}
}

func TestPodRequestsInitContainersUseMax(t *testing.T) {
	p := corev1.Pod{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{{Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{gpuResource: resource.MustParse("4")},
		}}},
		Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{gpuResource: resource.MustParse("2")},
		}}},
	}}
	if got := podRequests(p).gpu; got != 4 {
		t.Errorf("gpu = %d, want max(init=4, containers=2) = 4", got)
	}
}

func TestLoadWithFakeClientset(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-1"}, Status: gpuNode("gpu-1", "8", corev1.ConditionTrue).Status},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "default"},
			Spec: corev1.PodSpec{NodeName: "gpu-1", Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					gpuResource: resource.MustParse("3"),
				}},
			}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	ov, err := Load(context.Background(), cs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ov.GPUTotal != 8 {
		t.Errorf("GPUTotal = %d, want 8", ov.GPUTotal)
	}
	if ov.GPURequested != 3 || ov.PodsRunning != 1 {
		t.Errorf("GPURequested = %d, PodsRunning = %d; want 3 and 1", ov.GPURequested, ov.PodsRunning)
	}
	if len(ov.Nodes) != 1 || ov.Nodes[0].GPURequested != 3 {
		t.Errorf("node requested = %+v", ov.Nodes)
	}
}
