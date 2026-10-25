package host

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
)

// preemptEvents lists the Preempting Events written to the victim VirtualCluster.
func preemptEvents(t *testing.T, c client.Client, victim string) []corev1.Event {
	t.Helper()
	var list corev1.EventList
	if err := c.List(context.Background(), &list, client.InNamespace(metav1.NamespaceDefault)); err != nil {
		t.Fatal(err)
	}
	var out []corev1.Event
	for _, ev := range list.Items {
		if ev.Reason == preemptingEvent && ev.InvolvedObject.Name == victim {
			out = append(out, ev)
		}
	}
	return out
}

func TestPreemptionNoticesVictim(t *testing.T) {
	c := preemptionScene(t, interceptor.Funcs{})
	reconcileAt(t, c, t0)

	events := preemptEvents(t, c, "low")
	if len(events) != 1 {
		t.Fatalf("Preempting events on low = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Type != corev1.EventTypeWarning || !strings.Contains(ev.Message, "n1") || !strings.Contains(ev.Message, "转给 high") {
		t.Errorf("event = %q (%s), want a warning naming n1 and high", ev.Message, ev.Type)
	}
	if ev.InvolvedObject.Kind != "VirtualCluster" || ev.InvolvedObject.Namespace != "" {
		t.Errorf("involvedObject = %+v, want a cluster-scoped VirtualCluster", ev.InvolvedObject)
	}

	var low v1alpha1.VirtualCluster
	get(t, c, "low", &low)
	cond := meta.FindStatusCondition(low.Status.Conditions, PreemptingCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != preemptingReason {
		t.Fatalf("low conditions = %+v, want Preempting=True", low.Status.Conditions)
	}
	if !strings.Contains(cond.Message, "1 个节点") || !strings.Contains(cond.Message, "：n1") {
		t.Errorf("condition message = %q, want the count and the node", cond.Message)
	}

	var high v1alpha1.VirtualCluster
	get(t, c, "high", &high)
	if len(high.Status.Conditions) != 0 {
		t.Errorf("claimant conditions = %+v, want none", high.Status.Conditions)
	}
}

func TestPreemptionNoticeIsWrittenOnce(t *testing.T) {
	c := preemptionScene(t, interceptor.Funcs{})
	reconcileAt(t, c, t0)
	reconcileAt(t, c, t0.Add(10*time.Second))

	if n := len(preemptEvents(t, c, "low")); n != 1 {
		t.Errorf("Preempting events after a second pass = %d, want 1", n)
	}
}

func TestPreemptionNoticeClearsAfterHandover(t *testing.T) {
	c := preemptionScene(t, interceptor.Funcs{})
	reconcileAt(t, c, t0)
	reconcileAt(t, c, t0.Add(time.Minute))

	var low v1alpha1.VirtualCluster
	get(t, c, "low", &low)
	if meta.FindStatusCondition(low.Status.Conditions, PreemptingCondition) != nil {
		t.Errorf("low conditions = %+v, want the notice cleared after the handover", low.Status.Conditions)
	}
}
