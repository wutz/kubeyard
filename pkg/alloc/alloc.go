// Package alloc decides how the nodes of one NodePool are shared among the
// virtual clusters that claim it. It implements the expected-size and allocation
// rules of docs/operator.md §3. The operator calls it once per pool, under the
// pool's lock, and then applies the result to NodeLeases.
package alloc

import (
	"fmt"
	"slices"
	"sort"
	"time"
)

// Lease is a node currently bound to a claim.
type Lease struct {
	Node    string
	BoundAt time.Time
	// Draining is set while the node is being emptied for another claim. It
	// still counts toward its claim's size, but it is never a victim, never
	// released again, and never free.
	Draining bool
}

// Claim is one virtual cluster's claim on a pool.
type Claim struct {
	VC          string
	Priority    int
	Created     time.Time
	Preemptible bool
	// Autoscale selects the autoscale rule; otherwise Desired is used.
	Autoscale bool
	Min       int
	Max       int
	Desired   int
	// Pending is the extra nodes the virtual cluster's pending pods need.
	Pending int
	// Idle is how many bound nodes have had no workload for scaleDownIdle.
	Idle int
	// Pinned nodes count toward the claim's size and are never preempted.
	Pinned []string
	// Current holds the nodes bound to this claim now.
	Current []Lease
}

// Victim is a bound node taken from one claim so that another claim can use it.
type Victim struct {
	Node  string
	From  string
	ForVC string
}

// Plan is the outcome of one pool reconcile.
type Plan struct {
	// Grant lists the nodes each claim receives, free or preempted, by VC name.
	Grant map[string][]string
	// Preempt lists the bound nodes taken from lower-priority claims.
	Preempt []Victim
	// Release lists, by VC name, the nodes a claim gives up because it holds
	// more than its Want. Released nodes are not granted in the same plan.
	Release map[string][]string
	// Shortfall is how many nodes each claim still lacks of its Want after
	// granting and preempting.
	Shortfall map[string]int
}

// Want is the number of nodes a claim should hold (docs/operator.md §3.1).
func Want(c Claim) int {
	var want int
	if c.Autoscale {
		want = clamp(len(c.Current)+c.Pending-c.Idle, c.Min, c.Max)
	} else {
		want = clamp(c.Desired, c.Min, c.Max)
	}
	return max(want, len(c.Pinned))
}

// Validate checks the admission invariants that the webhook enforces
// (docs/operator.md §3.2). healthyNodes is the pool's total minus its
// unhealthy nodes. Reconcile assumes the claims passed Validate.
func Validate(claims []Claim, healthyNodes int) error {
	sumMin := 0
	pinned := map[string]string{}
	for _, c := range claims {
		if c.Min < 0 || c.Min > c.Max {
			return fmt.Errorf("%s: min %d 必须在 0 与 max %d 之间", c.VC, c.Min, c.Max)
		}
		if !c.Autoscale && (c.Desired < c.Min || c.Desired > c.Max) {
			return fmt.Errorf("%s: desired %d 必须在 min %d 与 max %d 之间", c.VC, c.Desired, c.Min, c.Max)
		}
		if len(c.Pinned) > c.Max {
			return fmt.Errorf("%s: pinnedNodes 数量 %d 超过 max %d", c.VC, len(c.Pinned), c.Max)
		}
		for _, n := range c.Pinned {
			if owner, ok := pinned[n]; ok {
				return fmt.Errorf("节点 %s 同时被 %s 与 %s 固定", n, owner, c.VC)
			}
			pinned[n] = c.VC
		}
		sumMin += c.Min
	}
	if sumMin > healthyNodes {
		return fmt.Errorf("min 之和 %d 超过资源池的健康节点数 %d", sumMin, healthyNodes)
	}
	return nil
}

// Reconcile computes the plan for one pool. free lists the healthy nodes that
// are unbound; the nodes bound to claims are read from Claim.Current.
//
// The steps follow §3.3. Pinned nodes are granted first. Then, for the min and
// then for Want, each claim takes free nodes in priority order and, if it still
// lacks nodes, preempts elastic nodes of lower-priority claims.
func Reconcile(free []string, claims []Claim) (Plan, error) {
	ordered := byPriority(claims)
	plan := Plan{
		Grant:     map[string][]string{},
		Release:   map[string][]string{},
		Shortfall: map[string]int{},
	}
	pool := newNodeSet(free)
	owners := boundOwners(claims)

	held := map[string]int{}
	mins := map[string]int{}
	for _, c := range claims {
		held[c.VC] = len(c.Current)
		mins[c.VC] = c.Min
	}
	grant := func(vc, node string) {
		plan.Grant[vc] = append(plan.Grant[vc], node)
		held[vc]++
	}

	for _, c := range ordered {
		for _, n := range c.Pinned {
			if owner, ok := owners[n]; ok && owner != c.VC {
				return Plan{}, fmt.Errorf("节点 %s 固定给 %s，但仍绑定在 %s 上", n, c.VC, owner)
			}
			if pool.take(n) {
				grant(c.VC, n)
			}
		}
	}

	victims := victimOrder(claims)
	for _, target := range []func(Claim) int{minOf, Want} {
		for _, c := range ordered {
			for held[c.VC] < target(c) {
				n, ok := pool.pop()
				if !ok {
					break
				}
				grant(c.VC, n)
			}
		}
		for _, c := range ordered {
			for held[c.VC] < target(c) {
				i := nextVictim(victims, held, mins, c.Priority)
				if i < 0 {
					break
				}
				v := victims[i]
				victims = slices.Delete(victims, i, i+1)
				held[v.VC]--
				plan.Preempt = append(plan.Preempt, Victim{Node: v.Node, From: v.VC, ForVC: c.VC})
				grant(c.VC, v.Node)
			}
		}
	}

	// A claim above its Want gives back its newest releasable leases. Pinned and
	// draining leases are kept. The caller picks which idle nodes to free when
	// Want is lower because of idleness; this only enforces the count.
	for _, c := range ordered {
		releasable := releasableLeases(c)
		for held[c.VC] > Want(c) && len(releasable) > 0 {
			plan.Release[c.VC] = append(plan.Release[c.VC], releasable[0].Node)
			releasable = releasable[1:]
			held[c.VC]--
		}
	}

	for _, c := range ordered {
		if short := Want(c) - held[c.VC]; short > 0 {
			plan.Shortfall[c.VC] = short
		}
	}
	return plan, nil
}

// releasableLeases lists the leases a claim may give back, newest first.
func releasableLeases(c Claim) []Lease {
	pinned := map[string]bool{}
	for _, n := range c.Pinned {
		pinned[n] = true
	}
	var out []Lease
	for _, l := range c.Current {
		if !pinned[l.Node] && !l.Draining {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].BoundAt.After(out[j].BoundAt) })
	return out
}

func minOf(c Claim) int { return c.Min }

// candidate is a bound, unpinned node of a preemptible claim.
type candidate struct {
	Node    string
	VC      string
	Prio    int
	Excess  int
	BoundAt time.Time
}

// victimOrder lists the candidates in the order they should be taken: priority
// ascending, then the claim with the most nodes above its min, then the most
// recently bound lease first (docs/operator.md §3.3).
func victimOrder(claims []Claim) []candidate {
	var out []candidate
	for _, c := range claims {
		if !c.Preemptible {
			continue
		}
		pinned := map[string]bool{}
		for _, n := range c.Pinned {
			pinned[n] = true
		}
		for _, l := range c.Current {
			if pinned[l.Node] || l.Draining {
				continue
			}
			out = append(out, candidate{Node: l.Node, VC: c.VC, Prio: c.Priority, Excess: len(c.Current) - c.Min, BoundAt: l.BoundAt})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Prio != out[j].Prio {
			return out[i].Prio < out[j].Prio
		}
		if out[i].Excess != out[j].Excess {
			return out[i].Excess > out[j].Excess
		}
		return out[i].BoundAt.After(out[j].BoundAt)
	})
	return out
}

// nextVictim returns the index of the first candidate that a claim of the given
// priority may take: its owner has a strictly lower priority and still holds
// more nodes than its min. It returns -1 when there is none.
func nextVictim(victims []candidate, held, mins map[string]int, priority int) int {
	for i, v := range victims {
		if v.Prio < priority && held[v.VC] > mins[v.VC] {
			return i
		}
	}
	return -1
}

func byPriority(claims []Claim) []Claim {
	out := append([]Claim(nil), claims...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out
}

func boundOwners(claims []Claim) map[string]string {
	owners := map[string]string{}
	for _, c := range claims {
		for _, l := range c.Current {
			owners[l.Node] = c.VC
		}
	}
	return owners
}

// nodeSet hands out free nodes in input order, once each.
type nodeSet struct {
	order []string
	left  map[string]bool
}

func newNodeSet(nodes []string) *nodeSet {
	s := &nodeSet{left: map[string]bool{}}
	for _, n := range nodes {
		if !s.left[n] {
			s.order = append(s.order, n)
			s.left[n] = true
		}
	}
	return s
}

// take removes a specific node if it is still free.
func (s *nodeSet) take(name string) bool {
	if !s.left[name] {
		return false
	}
	delete(s.left, name)
	return true
}

// pop removes the next free node in input order.
func (s *nodeSet) pop() (string, bool) {
	for len(s.order) > 0 {
		n := s.order[0]
		s.order = s.order[1:]
		if s.left[n] {
			delete(s.left, n)
			return n, true
		}
	}
	return "", false
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
