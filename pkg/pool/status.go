// Package pool computes the status of a NodePool from the nodes its selector
// matches and the NodeLeases that bind those nodes to virtual clusters. The
// operator writes the result to NodePool.status; the GUI shows it on the
// resource-pool page.
package pool

import (
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// Status is the NodePool status.
type Status struct {
	// Total counts the nodes the selector matches.
	Total int
	// Free counts matched nodes that are Ready and not leased to a virtual cluster.
	Free int
	// Reserved is the sum of min over every virtual cluster claim on the pool.
	Reserved int
	// Unhealthy counts matched nodes whose Ready condition is not True.
	Unhealthy int
}

// Compute derives the pool status.
//
// leased names the nodes with an active NodeLease on this pool; the caller
// filters the leases to the pool. reserved is the sum of min across the pool's
// claims. A selector with no requirements is an error, because it would match
// every node in the cluster.
func Compute(selector metav1.LabelSelector, nodes []corev1.Node, leased []string, reserved int) (Status, error) {
	if len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0 {
		return Status{}, errors.New("nodeSelector 为空，会匹配集群中的全部节点")
	}
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		return Status{}, err
	}
	leasedNodes := make(map[string]bool, len(leased))
	for _, name := range leased {
		leasedNodes[name] = true
	}

	s := Status{Reserved: reserved}
	for i := range nodes {
		n := &nodes[i]
		if !sel.Matches(labels.Set(n.Labels)) {
			continue
		}
		s.Total++
		if !ready(n) {
			s.Unhealthy++
			continue
		}
		if !leasedNodes[n.Name] {
			s.Free++
		}
	}
	return s, nil
}

// ready reports whether the node's Ready condition is True. A node without the
// condition is not ready.
func ready(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
