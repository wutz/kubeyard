package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
	"github.com/wutz/kubeyard/pkg/operator/host"
)

// Changing a claim's desired size can take bound nodes from other virtual
// clusters, so the page shows what the change would do first (docs/gui.md §3).
// The preview is the operator's own plan (host.PreviewDesired), and the write
// goes ahead only when the nodes it would take are the ones the admin saw.

// desiredPreviewView is the page's view of host.Preview.
type desiredPreviewView struct {
	Preempt     []preemptionView `json:"preempt"`
	Release     []releaseView    `json:"release"`
	Granted     []string         `json:"granted"`
	Shortfall   int32            `json:"shortfall"`
	Approximate bool             `json:"approximate"`
	// Grace and Force are the notice window and the time to forced deletion, as
	// duration text. They run from the moment the change is applied.
	Grace string `json:"grace"`
	Force string `json:"force"`
}

type preemptionView struct {
	Node string `json:"node"`
	From string `json:"from"`
	For  string `json:"for"`
}

type releaseView struct {
	VirtualCluster string `json:"virtualCluster"`
	Node           string `json:"node"`
}

// durationText writes d for the page, in whole minutes when it is one.
func durationText(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", d/time.Minute)
	}
	return fmt.Sprintf("%d 秒", d/time.Second)
}

func toDesiredPreviewView(p host.Preview) desiredPreviewView {
	out := desiredPreviewView{
		Preempt:     []preemptionView{},
		Release:     []releaseView{},
		Granted:     []string{},
		Shortfall:   int32(p.Shortfall),
		Approximate: p.Approximate,
		Grace:       durationText(p.GracePeriod),
		Force:       durationText(p.ForceAfter),
	}
	for _, v := range p.Preempt {
		out.Preempt = append(out.Preempt, preemptionView{Node: v.Node, From: v.From, For: v.For})
	}
	for _, vc := range slices.Sorted(maps.Keys(p.Release)) {
		for _, node := range p.Release[vc] {
			out.Release = append(out.Release, releaseView{VirtualCluster: vc, Node: node})
		}
	}
	out.Granted = append(out.Granted, p.Grant...)
	return out
}

// affectedNodes lists the nodes the change takes from bound claims: the
// preempted ones and the released ones, sorted.
func affectedNodes(p host.Preview) []string {
	var out []string
	for _, v := range p.Preempt {
		out = append(out, v.Node)
	}
	for _, nodes := range p.Release {
		out = append(out, nodes...)
	}
	slices.Sort(out)
	return out
}

// planDesired previews the desired size of vcName's claim on pool, from the
// cluster's state now.
func planDesired(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, vcName, pool string, desired int32) (host.Preview, error) {
	var np v1alpha1.NodePool
	if err := getKubeyard(ctx, dc, nodePoolResource, pool, &np); err != nil {
		return host.Preview{}, fmt.Errorf("读取资源池 %s：%w", pool, err)
	}
	var vcs v1alpha1.VirtualClusterList
	if err := listKubeyard(ctx, dc, virtualClusterResource, &vcs); err != nil {
		return host.Preview{}, fmt.Errorf("读取虚拟集群：%w", err)
	}
	var leases v1alpha1.NodeLeaseList
	if err := listKubeyard(ctx, dc, nodeLeaseResource, &leases); err != nil && !missingCRD(err) {
		return host.Preview{}, fmt.Errorf("读取节点租约：%w", err)
	}
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return host.Preview{}, fmt.Errorf("读取节点失败：%w", err)
	}
	return host.PreviewDesired(&np, vcs.Items, nodes.Items, leases.Items, vcName, desired, time.Now())
}

// setDesired writes the desired size of vcName's claim on pool. expect lists the
// nodes the admin confirmed in the preview. When the cluster would take a
// different set now, the write is refused, so the confirmation covers exactly
// what was shown.
func setDesired(ctx context.Context, cs kubernetes.Interface, dc dynamic.Interface, vcName, pool string, desired int32, expect []string) error {
	p, err := planDesired(ctx, cs, dc, vcName, pool, desired)
	if err != nil {
		return err
	}
	want := slices.Sorted(slices.Values(expect))
	if got := affectedNodes(p); !slices.Equal(got, want) {
		return fmt.Errorf("预览与当前状态不一致：预览涉及节点 %v，现在涉及 %v，请重新预览", want, got)
	}
	vc, err := readVirtualCluster(ctx, dc, vcName)
	if err != nil {
		return fmt.Errorf("读取虚拟集群 %s：%w", vcName, err)
	}
	pc := claimOf(vc, pool)
	if pc == nil {
		return fmt.Errorf("虚拟集群 %s 未声明资源池 %s", vcName, pool)
	}
	pc.Desired = desired
	return writeVirtualCluster(ctx, dc, vc)
}

// PreviewDesired shows what setting the desired size of a claim would do: the
// nodes it takes from other claims, the nodes it releases, and what it grants.
// It writes nothing.
func (a *App) PreviewDesired(contextName, vc, pool string, desired int32) (desiredPreviewView, error) {
	if contextName == "" {
		return desiredPreviewView{}, errors.New("缺少 context")
	}
	_, cs, dc, err := clients(contextName)
	if err != nil {
		return desiredPreviewView{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	p, err := planDesired(ctx, cs, dc, vc, pool, desired)
	if err != nil {
		return desiredPreviewView{}, err
	}
	return toDesiredPreviewView(p), nil
}

// SetDesired sets the desired size of a claim (docs/gui.md §3). expect is the
// list of nodes the preview showed as taken or released, empty when there were
// none. The change is refused when the cluster now takes a different set.
func (a *App) SetDesired(contextName, vc, pool string, desired int32, expect []string) (string, error) {
	done := fmt.Sprintf("已将 %s 在 %s 的 desired 设为 %d，operator 下一轮分配后生效", vc, pool, desired)
	return a.write(contextName, "调整 desired", done, actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return setDesired(ctx, cs, dc, vc, pool, desired, expect)
		})
}
