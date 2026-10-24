package kube

import (
	"context"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	gpuResource   = corev1.ResourceName("nvidia.com/gpu")
	gpuProductKey = "nvidia.com/gpu.product"
	rolePrefix    = "node-role.kubernetes.io/"
)

// Node is the part of a node the overview shows.
type Node struct {
	Name         string
	Version      string
	Ready        bool
	Roles        []string
	GPUProduct   string
	GPUTotal     int64
	GPURequested int64
	CPUMilli     int64
	CPUReqMilli  int64
	MemBytes     int64
	MemReqBytes  int64
}

// Overview aggregates nodes and pods of one cluster.
type Overview struct {
	Nodes           []Node
	NodesReady      int
	GPUTotal        int64
	GPURequested    int64
	PodsRunning     int
	PodsPending     int
	PodsUnscheduled int
	PodsFailed      int
}

// Load reads nodes and pods and returns the overview.
func Load(ctx context.Context, cs kubernetes.Interface) (Overview, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Overview{}, err
	}
	pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Overview{}, err
	}
	return Summarize(nodes.Items, pods.Items), nil
}

// Summarize computes the overview from raw objects. It does not call the API.
func Summarize(nodes []corev1.Node, pods []corev1.Pod) Overview {
	var ov Overview
	index := make(map[string]int, len(nodes))
	for _, n := range nodes {
		node := Node{
			Name:       n.Name,
			Version:    n.Status.NodeInfo.KubeletVersion,
			Ready:      isReady(n),
			Roles:      roles(n.Labels),
			GPUProduct: n.Labels[gpuProductKey],
			GPUTotal:   quantity(n.Status.Allocatable[gpuResource]),
			CPUMilli:   n.Status.Allocatable.Cpu().MilliValue(),
			MemBytes:   n.Status.Allocatable.Memory().Value(),
		}
		if node.Ready {
			ov.NodesReady++
		}
		ov.GPUTotal += node.GPUTotal
		index[n.Name] = len(ov.Nodes)
		ov.Nodes = append(ov.Nodes, node)
	}

	for _, p := range pods {
		switch p.Status.Phase {
		case corev1.PodSucceeded:
			continue
		case corev1.PodFailed:
			ov.PodsFailed++
			continue
		case corev1.PodRunning:
			ov.PodsRunning++
		case corev1.PodPending:
			ov.PodsPending++
			if p.Spec.NodeName == "" {
				ov.PodsUnscheduled++
			}
		}
		i, ok := index[p.Spec.NodeName]
		if !ok || p.Spec.NodeName == "" {
			continue
		}
		req := podRequests(p)
		node := &ov.Nodes[i]
		node.GPURequested += req.gpu
		node.CPUReqMilli += req.cpu
		node.MemReqBytes += req.mem
		ov.GPURequested += req.gpu
	}

	sort.Slice(ov.Nodes, func(i, j int) bool { return ov.Nodes[i].Name < ov.Nodes[j].Name })
	return ov
}

type requests struct{ gpu, cpu, mem int64 }

// podRequests sums container requests of pods holding a node. Init containers
// count at their maximum, as the scheduler accounts for them.
func podRequests(p corev1.Pod) requests {
	var sum, initMax requests
	for _, c := range p.Spec.Containers {
		sum = add(sum, containerRequests(c))
	}
	for _, c := range p.Spec.InitContainers {
		r := containerRequests(c)
		initMax.gpu = max(initMax.gpu, r.gpu)
		initMax.cpu = max(initMax.cpu, r.cpu)
		initMax.mem = max(initMax.mem, r.mem)
	}
	return requests{
		gpu: max(sum.gpu, initMax.gpu),
		cpu: max(sum.cpu, initMax.cpu),
		mem: max(sum.mem, initMax.mem),
	}
}

func containerRequests(c corev1.Container) requests {
	req := c.Resources.Requests
	if _, ok := req[gpuResource]; !ok {
		// Extended resources default their request to the limit.
		if lim, ok := c.Resources.Limits[gpuResource]; ok {
			req = corev1.ResourceList{gpuResource: lim}
		}
	}
	return requests{
		gpu: quantity(req[gpuResource]),
		cpu: req.Cpu().MilliValue(),
		mem: req.Memory().Value(),
	}
}

func add(a, b requests) requests {
	return requests{gpu: a.gpu + b.gpu, cpu: a.cpu + b.cpu, mem: a.mem + b.mem}
}

func isReady(n corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func roles(labels map[string]string) []string {
	var out []string
	for k := range labels {
		if strings.HasPrefix(k, rolePrefix) {
			out = append(out, strings.TrimPrefix(k, rolePrefix))
		}
	}
	sort.Strings(out)
	return out
}

func quantity(q resource.Quantity) int64 { return q.Value() }
