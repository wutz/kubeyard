package alloc

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

func leases(at time.Time, nodes ...string) []Lease {
	var out []Lease
	for _, n := range nodes {
		out = append(out, Lease{Node: n, BoundAt: at})
	}
	return out
}

func TestWant(t *testing.T) {
	tests := []struct {
		name  string
		claim Claim
		want  int
	}{
		{"manual clamps desired up to min", Claim{Min: 2, Max: 4, Desired: 0}, 2},
		{"manual clamps desired down to max", Claim{Min: 1, Max: 4, Desired: 9}, 4},
		{"manual uses desired in range", Claim{Min: 1, Max: 4, Desired: 3}, 3},
		{"autoscale adds pending and removes idle", Claim{Autoscale: true, Min: 1, Max: 8, Current: leases(t0, "a", "b", "c"), Pending: 2, Idle: 1}, 4},
		{"autoscale clamps to max", Claim{Autoscale: true, Min: 1, Max: 8, Current: leases(t0, "a", "b", "c", "d", "e", "f", "g"), Pending: 5}, 8},
		{"autoscale floors at min", Claim{Autoscale: true, Min: 2, Max: 8}, 2},
		{"autoscale ignores desired", Claim{Autoscale: true, Min: 1, Max: 8, Desired: 7}, 1},
		{"pinned nodes raise want above desired", Claim{Min: 0, Max: 4, Desired: 1, Pinned: []string{"a", "b", "c"}}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Want(tt.claim); got != tt.want {
				t.Fatalf("Want() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		claims  []Claim
		healthy int
		wantErr bool
	}{
		{"min above max", []Claim{{VC: "a", Min: 3, Max: 2}}, 10, true},
		{"negative min", []Claim{{VC: "a", Min: -1, Max: 2}}, 10, true},
		{"manual desired below min", []Claim{{VC: "a", Min: 2, Max: 4, Desired: 1}}, 10, true},
		{"manual desired above max", []Claim{{VC: "a", Min: 2, Max: 4, Desired: 5}}, 10, true},
		{"autoscale ignores desired", []Claim{{VC: "a", Autoscale: true, Min: 2, Max: 4, Desired: 0}}, 10, false},
		{"pinned above max", []Claim{{VC: "a", Min: 0, Max: 1, Pinned: []string{"x", "y"}}}, 10, true},
		{"node pinned by two claims", []Claim{
			{VC: "a", Min: 1, Max: 2, Desired: 1, Pinned: []string{"x"}},
			{VC: "b", Min: 1, Max: 2, Desired: 1, Pinned: []string{"x"}},
		}, 10, true},
		{"sum of mins above healthy", []Claim{
			{VC: "a", Min: 3, Max: 4, Desired: 3},
			{VC: "b", Min: 3, Max: 4, Desired: 3},
		}, 5, true},
		{"sum of mins equal to healthy", []Claim{
			{VC: "a", Min: 3, Max: 4, Desired: 3},
			{VC: "b", Min: 2, Max: 4, Desired: 2},
		}, 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.claims, tt.healthy)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReconcileFreeNodesByPriority(t *testing.T) {
	plan, err := Reconcile([]string{"n1", "n2", "n3", "n4"}, []Claim{
		{VC: "low", Priority: 5, Created: t0, Min: 0, Max: 3, Desired: 3},
		{VC: "high", Priority: 10, Created: t0.Add(time.Hour), Min: 0, Max: 3, Desired: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Grant["high"]; !reflect.DeepEqual(got, []string{"n1", "n2", "n3"}) {
		t.Fatalf("high grant = %v", got)
	}
	if got := plan.Grant["low"]; !reflect.DeepEqual(got, []string{"n4"}) {
		t.Fatalf("low grant = %v", got)
	}
	if got := plan.Shortfall["low"]; got != 2 {
		t.Fatalf("low shortfall = %d, want 2", got)
	}
	if len(plan.Preempt) != 0 {
		t.Fatalf("unexpected preemption: %v", plan.Preempt)
	}
}

func TestReconcileSameLevelByCreation(t *testing.T) {
	plan, err := Reconcile([]string{"n1", "n2", "n3"}, []Claim{
		{VC: "later", Priority: 5, Created: t0.Add(time.Hour), Min: 0, Max: 2, Desired: 2},
		{VC: "earlier", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Grant["earlier"]; !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("earlier grant = %v", got)
	}
	if got := plan.Grant["later"]; !reflect.DeepEqual(got, []string{"n3"}) {
		t.Fatalf("later grant = %v", got)
	}
}

func TestReconcileMinBeforeExtra(t *testing.T) {
	plan, err := Reconcile([]string{"n1", "n2"}, []Claim{
		{VC: "greedy", Priority: 10, Created: t0, Min: 0, Max: 2, Desired: 2},
		{VC: "guaranteed", Priority: 1, Created: t0, Min: 1, Max: 1, Desired: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Grant["guaranteed"]; !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("guaranteed grant = %v, want its min first", got)
	}
	if got := plan.Grant["greedy"]; !reflect.DeepEqual(got, []string{"n2"}) {
		t.Fatalf("greedy grant = %v", got)
	}
}

func TestReconcilePinnedGrantedFirst(t *testing.T) {
	plan, err := Reconcile([]string{"n1", "n2", "n3"}, []Claim{
		{VC: "a", Priority: 10, Created: t0, Min: 0, Max: 1, Desired: 1, Pinned: []string{"n3"}},
		{VC: "b", Priority: 20, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Grant["a"]; !reflect.DeepEqual(got, []string{"n3"}) {
		t.Fatalf("a grant = %v, want its pinned node", got)
	}
	if got := plan.Grant["b"]; !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("b grant = %v", got)
	}
}

func TestReconcilePinnedBoundElsewhereIsError(t *testing.T) {
	_, err := Reconcile(nil, []Claim{
		{VC: "a", Priority: 1, Min: 0, Max: 1, Desired: 1, Pinned: []string{"n1"}},
		{VC: "b", Priority: 1, Min: 0, Max: 1, Desired: 1, Current: leases(t0, "n1")},
	})
	if err == nil {
		t.Fatal("want error for a pinned node still bound to another claim")
	}
}

func TestReconcilePreemptsLowerPriorityOnly(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 0, Max: 2, Desired: 2, Current: leases(t0, "n1", "n2")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 2 {
		t.Fatalf("preempt = %v, want two nodes from low", plan.Preempt)
	}
	for _, v := range plan.Preempt {
		if v.From != "low" || v.ForVC != "high" {
			t.Fatalf("victim = %+v", v)
		}
	}
	if got, ok := plan.Shortfall["high"]; ok {
		t.Fatalf("high shortfall = %d, want none", got)
	}
}

func TestReconcileNoPreemptionAtEqualPriority(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "owner", Priority: 5, Created: t0, Preemptible: true, Min: 0, Max: 2, Desired: 2, Current: leases(t0, "n1", "n2")},
		{VC: "other", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 0 {
		t.Fatalf("preempt = %v, want none at equal priority", plan.Preempt)
	}
	if got := plan.Shortfall["other"]; got != 2 {
		t.Fatalf("other shortfall = %d, want 2", got)
	}
}

func TestReconcileNoPreemptionBelowMin(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 2, Max: 2, Desired: 2, Current: leases(t0, "n1", "n2")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 0 {
		t.Fatalf("preempt = %v, want none below min", plan.Preempt)
	}
	if got := plan.Shortfall["high"]; got != 2 {
		t.Fatalf("high shortfall = %d, want 2", got)
	}
}

func TestReconcileMinGapFilledByPreemption(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 0, Max: 1, Desired: 1, Current: leases(t0, "n1")},
		{VC: "high", Priority: 5, Created: t0, Min: 1, Max: 1, Desired: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 1 || plan.Preempt[0].Node != "n1" {
		t.Fatalf("preempt = %v, want n1 for the min of high", plan.Preempt)
	}
}

func TestReconcilePinnedNodeNotVictim(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 0, Max: 2, Desired: 2, Pinned: []string{"n1"}, Current: leases(t0, "n1", "n2")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 1 || plan.Preempt[0].Node != "n2" {
		t.Fatalf("preempt = %v, want only the unpinned n2", plan.Preempt)
	}
	if got := plan.Shortfall["high"]; got != 1 {
		t.Fatalf("high shortfall = %d, want 1", got)
	}
}

func TestReconcileNonPreemptibleNotVictim(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "fixed", Priority: 1, Created: t0, Preemptible: false, Min: 0, Max: 2, Desired: 2, Current: leases(t0, "n1", "n2")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 0 {
		t.Fatalf("preempt = %v, want none from a non-preemptible claim", plan.Preempt)
	}
}

func TestReconcileTakesOnlyTheGap(t *testing.T) {
	plan, err := Reconcile(nil, []Claim{
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 0, Max: 3, Desired: 3, Current: leases(t0, "n1", "n2", "n3")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 2, Desired: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 2 {
		t.Fatalf("preempt = %v, want exactly the 2-node gap", plan.Preempt)
	}
	if plan.Preempt[0].Node == plan.Preempt[1].Node {
		t.Fatalf("same victim chosen twice: %v", plan.Preempt)
	}
}

func TestReconcileVictimOrderPriorityThenExcessThenNewest(t *testing.T) {
	// Lower priority goes first even when it has less excess.
	plan, err := Reconcile(nil, []Claim{
		{VC: "mid", Priority: 2, Created: t0, Preemptible: true, Min: 0, Max: 3, Desired: 3, Current: leases(t0, "m1", "m2", "m3")},
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 2, Max: 3, Desired: 3, Current: leases(t0, "l1", "l2", "l3")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 1, Desired: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 1 || plan.Preempt[0].From != "low" {
		t.Fatalf("preempt = %v, want from the lowest priority claim", plan.Preempt)
	}

	// Within one priority, the claim with more excess goes first.
	plan, err = Reconcile(nil, []Claim{
		{VC: "small", Priority: 1, Created: t0, Preemptible: true, Min: 2, Max: 3, Desired: 3, Current: leases(t0, "s1", "s2", "s3")},
		{VC: "big", Priority: 1, Created: t0, Preemptible: true, Min: 0, Max: 4, Desired: 4, Current: leases(t0, "b1", "b2", "b3", "b4")},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 1, Desired: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 1 || plan.Preempt[0].From != "big" {
		t.Fatalf("preempt = %v, want from the claim with more excess", plan.Preempt)
	}

	// Within one claim, the most recently bound lease goes first.
	plan, err = Reconcile(nil, []Claim{
		{VC: "low", Priority: 1, Created: t0, Preemptible: true, Min: 0, Max: 2, Desired: 2, Current: []Lease{
			{Node: "old", BoundAt: t0},
			{Node: "new", BoundAt: t0.Add(time.Hour)},
		}},
		{VC: "high", Priority: 5, Created: t0, Min: 0, Max: 1, Desired: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Preempt) != 1 || plan.Preempt[0].Node != "new" {
		t.Fatalf("preempt = %v, want the newest lease", plan.Preempt)
	}
}
