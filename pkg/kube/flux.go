package kube

import (
	"context"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var helmReleases = schema.GroupVersionResource{
	Group:    "helm.toolkit.fluxcd.io",
	Version:  "v2",
	Resource: "helmreleases",
}

// maxEvents bounds the events shown for one addon.
const maxEvents = 5

// Condition is the Ready condition of one Flux object.
type Condition struct {
	Kind    string
	Name    string
	Ready   bool
	Message string
}

// Event is a recent event about one of an addon's Flux objects.
type Event struct {
	Object  string
	Type    string
	Reason  string
	Message string
	Time    time.Time
}

// AddonFlux is the Flux state of an installed addon: its Kustomization and
// HelmReleases, and the latest events about them.
type AddonFlux struct {
	Conditions []Condition
	Events     []Event
}

// LoadAddonFlux reads the Ready conditions of an addon's Kustomization and of
// the HelmReleases named in releases, and the latest events about them. An
// object that does not exist is reported as not ready, not as an error.
func LoadAddonFlux(ctx context.Context, dc dynamic.Interface, cs kubernetes.Interface, addon string, releases []string) (AddonFlux, error) {
	var out AddonFlux
	kzName := kustomizationName(addon)
	kz, err := getReady(ctx, dc.Resource(kustomizations).Namespace(CatalogNamespace), "Kustomization", kzName)
	if err != nil {
		return AddonFlux{}, err
	}
	out.Conditions = append(out.Conditions, kz)

	want := map[string]bool{"Kustomization/" + kzName: true}
	for _, name := range releases {
		c, err := getReady(ctx, dc.Resource(helmReleases).Namespace(CatalogNamespace), "HelmRelease", name)
		if err != nil {
			return AddonFlux{}, err
		}
		out.Conditions = append(out.Conditions, c)
		want["HelmRelease/"+name] = true
	}

	events, err := cs.CoreV1().Events(CatalogNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return AddonFlux{}, err
	}
	for _, ev := range events.Items {
		if !want[ev.InvolvedObject.Kind+"/"+ev.InvolvedObject.Name] {
			continue
		}
		out.Events = append(out.Events, Event{
			Object:  ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
			Type:    ev.Type,
			Reason:  ev.Reason,
			Message: ev.Message,
			Time:    eventTime(ev),
		})
	}
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].Time.After(out.Events[j].Time) })
	if len(out.Events) > maxEvents {
		out.Events = out.Events[:maxEvents]
	}
	return out, nil
}

// getReady reads a Flux object and its Ready condition.
func getReady(ctx context.Context, client dynamic.ResourceInterface, kind, name string) (Condition, error) {
	c := Condition{Kind: kind, Name: name}
	obj, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		c.Message = "未找到"
		return c, nil
	}
	if err != nil {
		return Condition{}, err
	}
	conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conds {
		cond, ok := raw.(map[string]any)
		if !ok || cond["type"] != "Ready" {
			continue
		}
		c.Ready = cond["status"] == "True"
		c.Message, _ = cond["message"].(string)
	}
	return c, nil
}

// eventTime picks the most specific timestamp an event has.
func eventTime(ev corev1.Event) time.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.CreationTimestamp.Time
}
