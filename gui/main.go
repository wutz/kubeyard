// Command gui is the Kubeyard desktop client. M1 covers connecting to
// kubeconfig contexts, a read-only cluster overview, and a read-only list of
// the catalog's components with the ones already in the cluster marked. It
// talks to the API server directly; there is no backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/catalog"
	"github.com/wutz/kubeyard/pkg/kube"
)

const (
	loadTimeout = 15 * time.Second

	// The catalog source Kubeyard creates in every cluster it manages.
	sourceNamespace = "kubeyard-system"
	sourceName      = "catalog-default"
)

type page int

const (
	pageOverview page = iota
	pageCatalog
)

// loaded is what one background load reads from a cluster.
type loaded struct {
	overview kube.Overview
	catalog  catalogState
}

// catalogState is the catalog served by the cluster's source, and which of
// its components the cluster already has.
type catalogState struct {
	source    kube.Source
	hasSource bool
	cat       *catalog.Catalog
	status    map[string]catalog.Status
	// note is muted text for a state that is not an error, such as no source.
	note string
	// err explains why a source exists but its catalog cannot be shown.
	err string
}

// app is the state the window shows. Background loads write to it through
// win.Update, so the fields are only touched on the main thread.
type app struct {
	win      *mygo.Window
	contexts []string
	selected string
	page     page

	loadedFor string
	loading   bool
	err       string
	data      loaded
	nodes     ui.ListState

	addons   ui.ListState
	selAddon int
}

// view builds the interface from the state on the main thread.
func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(16).Children(func() {
		a.toolbar(c)
		if a.err != "" {
			ui.Text(c, a.err).TextColor(t.Danger)
		}
		if a.page == pageCatalog {
			a.catalogPage(c)
			return
		}
		a.overviewPage(c)
	})
}

func (a *app) overviewPage(c *ui.Context) {
	ov := a.data.overview
	ui.Row(c).Gap(12).Children(func() {
		card(c, "节点就绪", fmt.Sprintf("%d / %d", ov.NodesReady, len(ov.Nodes)), "Ready 的节点数")
		card(c, "GPU 分配", fmt.Sprintf("%d / %d", ov.GPURequested, ov.GPUTotal), "requests / allocatable")
		card(c, "Pod 运行中", fmt.Sprint(ov.PodsRunning), fmt.Sprintf("Pending %d · 失败 %d", ov.PodsPending, ov.PodsFailed))
	})
	a.nodeTable(c)
}

func (a *app) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Text(c, "集群").Bold()
		if len(a.contexts) == 0 {
			ui.Text(c, "未找到 kubeconfig context，请检查 KUBECONFIG 或 ~/.kube/config").TextColor(t.TextMuted)
			return
		}
		if ui.Select(c, &a.selected, a.contexts).Changed() {
			a.refresh()
		}
		if ui.Button(c.Key("refresh"), "刷新").Disabled(a.loading).Clicked() {
			a.refresh()
		}
		if ui.Button(c.Key("tab-overview"), "概览").Disabled(a.page == pageOverview).Clicked() {
			a.page = pageOverview
		}
		if ui.Button(c.Key("tab-catalog"), "组件").Disabled(a.page == pageCatalog).Clicked() {
			a.page = pageCatalog
		}
		ui.Spacer(c)
		status := ""
		switch {
		case a.loading:
			status = "加载中…"
		case a.data.overview.Nodes != nil:
			ov := a.data.overview
			status = fmt.Sprintf("Pods 共 %d · Pending 无节点 %d", ov.PodsRunning+ov.PodsPending, ov.PodsUnscheduled)
		}
		ui.Text(c, status).TextColor(t.TextMuted)
	})
}

func (a *app) nodeTable(c *ui.Context) {
	nodes := a.data.overview.Nodes
	cols := []ui.TableColumn{
		{Title: "节点", Width: 220},
		{Title: "状态", Width: 80},
		{Title: "角色", Width: 140},
		{Title: "GPU 型号", Width: 200},
		{Title: "GPU 分配", Width: 110, Align: ui.End},
		{Title: "CPU", Width: 100, Align: ui.End},
		{Title: "内存", Width: 180, Align: ui.End},
		{Title: "Kubelet", Align: ui.Start},
	}
	a.nodes.Selected = nil
	ui.Table(c, &a.nodes, cols, len(nodes), func(row, col int) {
		n := nodes[row]
		switch col {
		case 0:
			ui.Text(c, n.Name).SingleLine()
		case 1:
			if n.Ready {
				ui.Text(c, "Ready").TextColor(c.Theme().Success)
			} else {
				ui.Text(c, "NotReady").TextColor(c.Theme().Danger)
			}
		case 2:
			ui.Text(c, join(n.Roles)).SingleLine()
		case 3:
			ui.Text(c, n.GPUProduct).SingleLine()
		case 4:
			if n.GPUTotal == 0 {
				ui.Text(c, "—")
				return
			}
			ui.Text(c, fmt.Sprintf("%d / %d", n.GPURequested, n.GPUTotal))
		case 5:
			ui.Text(c, fmt.Sprintf("%.1f / %d", float64(n.CPUReqMilli)/1000, n.CPUMilli/1000))
		case 6:
			ui.Text(c, fmt.Sprintf("%s / %s", gib(n.MemReqBytes), gib(n.MemBytes)))
		case 7:
			ui.Text(c, n.Version).SingleLine()
		}
	}).Grow(1)
}

func (a *app) catalogPage(c *ui.Context) {
	t := c.Theme()
	cs := a.data.catalog
	ui.Column(c).Grow(1).Gap(12).Children(func() {
		ui.Text(c, sourceLine(cs)).TextColor(t.TextMuted).SingleLine()
		if cs.note != "" {
			ui.Text(c, cs.note).TextColor(t.TextMuted)
		}
		if cs.err != "" {
			ui.Text(c, cs.err).TextColor(t.Danger)
		}
		if cs.cat == nil {
			return
		}
		ui.Text(c, "只读：安装、升级与卸载在后续版本加入。已存在的组件不会被重复安装。").TextColor(t.TextMuted)
		a.addonTable(c, cs)
		a.addonDetail(c, cs)
	})
}

func (a *app) addonTable(c *ui.Context, cs catalogState) {
	addons := cs.cat.Addons
	cols := []ui.TableColumn{
		{Title: "组件", Width: 190},
		{Title: "分类", Width: 80},
		{Title: "版本", Width: 110},
		{Title: "适用范围", Width: 130},
		{Title: "依赖", Width: 150},
		{Title: "状态", Width: 150},
		{Title: "说明", Align: ui.Start},
	}
	a.addons.Key = func(row int) any { return addons[row].Name }
	a.addons.Selected = &a.selAddon
	ui.Table(c, &a.addons, cols, len(addons), func(row, col int) {
		t := c.Theme()
		ad := addons[row]
		switch col {
		case 0:
			ui.Text(c, ad.Name).SingleLine()
		case 1:
			ui.Text(c, ad.Category).SingleLine()
		case 2:
			ui.Text(c, ad.Version).SingleLine()
		case 3:
			ui.Text(c, join(ad.Scope)).SingleLine()
		case 4:
			ui.Text(c, orDash(join(ad.DependsOn))).SingleLine()
		case 5:
			label, color := statusLabel(cs.status[ad.Name], t.Success, t.TextMuted)
			ui.Text(c, label).TextColor(color)
		case 6:
			ui.Text(c, ad.DisplayName).SingleLine()
		}
	}).Grow(1)
}

func (a *app) addonDetail(c *ui.Context, cs catalogState) {
	t := c.Theme()
	addons := cs.cat.Addons
	if a.selAddon < 0 || a.selAddon >= len(addons) {
		return
	}
	ad := addons[a.selAddon]
	ui.Column(c).Padding(14).Gap(6).Background(t.Surface).Radius(10).Border(1, t.Border).Children(func() {
		ui.Text(c, ad.DisplayName).FontSize(16).Bold()
		ui.Text(c, ad.Description).TextColor(t.TextMuted)
		ui.Text(c, fmt.Sprintf("命名空间 %s · 健康检查 %s", ad.Namespace, healthText(ad.Health))).FontSize(12).TextColor(t.TextMuted)
		if len(ad.Params) == 0 {
			ui.Text(c, "无参数").FontSize(12).TextColor(t.TextMuted)
			return
		}
		for _, p := range ad.Params {
			ui.Text(c, paramLine(p)).FontSize(12).SingleLine()
		}
	})
}

// refresh loads the selected context in the background.
func (a *app) refresh() {
	name := a.selected
	a.loadedFor = name
	a.loading = true
	a.err = ""
	go func() {
		data, err := load(name)
		a.win.Update(func() {
			if a.loadedFor != name {
				// The user picked another context while this one loaded.
				return
			}
			a.loading = false
			if err != nil {
				a.err = err.Error()
				a.data = loaded{}
				return
			}
			a.data = data
			a.selAddon = 0
		})
	}()
}

// load reads the overview, the Helm releases and workloads that show what is
// already installed, and the catalog served by the cluster's source.
func load(contextName string) (loaded, error) {
	cfg, err := kube.RESTConfig(contextName)
	if err != nil {
		return loaded{}, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return loaded{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()

	ov, err := kube.Load(ctx, cs)
	if err != nil {
		return loaded{}, err
	}
	inv, err := kube.LoadInventory(ctx, cfg)
	if err != nil {
		return loaded{}, err
	}
	return loaded{overview: ov, catalog: loadCatalog(ctx, cfg, inv)}, nil
}

// loadCatalog reads the catalog at the revision the source's Flux
// OCIRepository verified. Nothing from an unverified source is shown.
func loadCatalog(ctx context.Context, cfg *rest.Config, inv kube.Inventory) catalogState {
	var out catalogState
	src, err := kube.LoadSource(ctx, cfg, sourceNamespace, sourceName)
	if errors.Is(err, kube.ErrNoSource) {
		out.note = fmt.Sprintf("集群中没有 catalog 源 %s/%s", sourceNamespace, sourceName)
		return out
	}
	if err != nil {
		out.err = "读取 catalog 源失败：" + err.Error()
		return out
	}
	out.source, out.hasSource = src, true
	switch {
	case !src.Ready:
		out.err = "catalog 源未就绪：" + src.Message
		return out
	case !src.Verified:
		out.err = "catalog 源未通过 cosign 校验，不显示组件"
		return out
	case src.Ref() == "":
		out.err = "catalog 源还没有解析出版本"
		return out
	}
	fsys, err := catalog.Pull(ctx, src.Ref())
	if err != nil {
		out.err = "下载 catalog 失败：" + err.Error()
		return out
	}
	cat, err := catalog.Parse(fsys)
	if err != nil {
		out.err = "解析 catalog 失败：" + err.Error()
		return out
	}
	out.cat = cat
	out.status = make(map[string]catalog.Status, len(cat.Addons))
	for _, ad := range cat.Addons {
		out.status[ad.Name] = ad.Status(inv.Has)
	}
	return out
}

func sourceLine(cs catalogState) string {
	if !cs.hasSource {
		return "catalog 源：未添加"
	}
	verified := "未校验"
	if cs.source.Verified {
		verified = "cosign 已校验"
	}
	return fmt.Sprintf("源 %s/%s · %s · %s", cs.source.Namespace, cs.source.Name, shortRevision(cs.source.Revision), verified)
}

// statusLabel returns the text and color for an addon's installed state.
func statusLabel(s catalog.Status, ok, muted ui.Color) (string, ui.Color) {
	switch s {
	case catalog.Installed:
		return "已安装", ok
	case catalog.Existing:
		return "已存在（非 Helm）", muted
	case catalog.Absent:
		return "未安装", muted
	default:
		return "无法判断", muted
	}
}

func healthText(health []catalog.Health) string {
	if len(health) == 0 {
		return "无"
	}
	out := ""
	for i, h := range health {
		if i > 0 {
			out += ", "
		}
		out += h.Kind + "/" + h.Name
	}
	return out
}

func paramLine(p catalog.Param) string {
	line := fmt.Sprintf("%s  %s  默认 %v", p.Path, p.Type, p.Default)
	if p.Required {
		line += "  必填"
	}
	if p.Title != "" {
		line += "  " + p.Title
	}
	return line
}

// shortRevision shortens the digest in "tag@sha256:…" for display.
func shortRevision(rev string) string {
	const digestLen = 12
	i := strings.Index(rev, "sha256:")
	if i < 0 || len(rev) <= i+len("sha256:")+digestLen {
		return rev
	}
	return rev[:i+len("sha256:")+digestLen] + "…"
}

func card(c *ui.Context, title, value, sub string) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(14).Gap(4).Background(t.Surface).Radius(10).Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).FontSize(24).Bold()
		ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
	})
}

func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func gib(bytes int64) string {
	return fmt.Sprintf("%.0f GiB", float64(bytes)/(1<<30))
}

func main() {
	a := &app{}
	contexts, err := kube.Contexts()
	if err != nil {
		log.Printf("kubeconfig: %v", err)
	}
	for _, ctx := range contexts {
		a.contexts = append(a.contexts, ctx.Name)
		if ctx.Current {
			a.selected = ctx.Name
		}
	}
	if a.selected == "" && len(a.contexts) > 0 {
		a.selected = a.contexts[0]
	}

	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:   "Kubeyard",
			Width:   1120,
			Height:  720,
			Content: ui.View(a.view),
		})
		if a.selected != "" {
			a.refresh()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
