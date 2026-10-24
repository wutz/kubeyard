package main

import (
	"encoding/json"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	nodeName = "kubeyard-fake-node"
	// fakeKubelet is deliberately not a real release, so the window cannot be
	// mistaken for a live cluster.
	fakeKubelet = "v0.0.0-fake"
)

// handler routes the fake API. Each response is fixed, so two requests return
// the same bytes. A path the fake does not serve gets a 404 Status, the answer a
// real API server gives for a resource it does not have.
func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metav1.APIVersions{
			TypeMeta:                   metav1.TypeMeta{Kind: "APIVersions", APIVersion: "v1"},
			Versions:                   []string{"v1"},
			ServerAddressByClientCIDRs: []metav1.ServerAddressByClientCIDR{},
		})
	})
	mux.HandleFunc("GET /api/v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metav1.APIResourceList{
			TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"},
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "nodes", Kind: "Node", Namespaced: false, Verbs: metav1.Verbs{"get", "list"}},
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
			},
		})
	})
	mux.HandleFunc("GET /apis", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, metav1.APIGroupList{
			TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"},
			Groups:   []metav1.APIGroup{},
		})
	})
	mux.HandleFunc("GET /api/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fakeNodes())
	})
	mux.HandleFunc("GET /api/v1/pods", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fakePods())
	})
	mux.HandleFunc("/", notFound)
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Message:  "the server could not find the requested resource",
		Reason:   metav1.StatusReasonNotFound,
		Code:     http.StatusNotFound,
	})
}

// fakeNodes returns one node that is Ready.
func fakeNodes() corev1.NodeList {
	return corev1.NodeList{
		TypeMeta: metav1.TypeMeta{Kind: "NodeList", APIVersion: "v1"},
		Items: []corev1.Node{{
			TypeMeta: metav1.TypeMeta{Kind: "Node", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:            nodeName,
				ResourceVersion: "1",
				Labels: map[string]string{
					"kubernetes.io/hostname":                nodeName,
					"node-role.kubernetes.io/control-plane": "",
				},
			},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
					corev1.ResourcePods:   resource.MustParse("110"),
				},
				Conditions: []corev1.NodeCondition{{
					Type:    corev1.NodeReady,
					Status:  corev1.ConditionTrue,
					Reason:  "KubeletReady",
					Message: "kubelet is posting ready status",
				}},
				NodeInfo: corev1.NodeSystemInfo{KubeletVersion: fakeKubelet},
			},
		}},
	}
}

// fakePods returns two Running pods on the fake node.
func fakePods() corev1.PodList {
	return corev1.PodList{
		TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
		Items: []corev1.Pod{
			fakePod("kube-system", "coredns-fake", "100m", "70Mi"),
			fakePod("kubeyard-fake", "web-0", "200m", "128Mi"),
		},
	}
}

func fakePod(namespace, name, cpu, memory string) corev1.Pod {
	return corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: "1",
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
			Containers: []corev1.Container{{
				Name:  name,
				Image: "fake",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpu),
						corev1.ResourceMemory: resource.MustParse(memory),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}
