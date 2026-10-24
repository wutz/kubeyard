// Command gui is the Kubeyard desktop client. M1 covers connecting to
// kubeconfig contexts, a cluster overview, and the catalog's components: their
// parameters, installing and uninstalling the ones Kubeyard manages, and their
// Flux status. It talks to the API server directly; there is no backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/catalog"
	"github.com/wutz/kubeyard/pkg/kube"
)

const (
	loadTimeout   = 15 * time.Second
	actionTimeout = 30 * time.Second
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

// catalogState is the catalog served by the cluster's source, and what the
// cluster already runs of it.
type catalogState struct {
	source    kube.Source
	hasSource bool
	cat       *catalog.Catalog
	// status and managed are keyed by addon name. managed means Kubeyard
	// installed the addon, so its Kustomization exists.
	status  map[string]catalog.Status
	managed map[string]bool
	// flux is the Flux state of each managed addon.
	flux map[string]kube.AddonFlux
	// note is muted text for a state that is not an error, such as no source.
	note string
	// err explains why a source exists but its catalog cannot be shown.
	err string
	// fluxErr explains why some Flux state could not be read.
	fluxErr string
}

// paramField is the form input for one parameter. Booleans use on; strings
// and integers use text, where empty means the schema default applies.
type paramField struct {
	text string
	on   bool
}

// action is a cluster write. It runs off the main thread.
type action func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error

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

	// formFor is the addon the form fields belong to. The form resets when
	// another addon is selected.
	formFor string
	fields  map[string]*paramField
	// confirm names the action waiting for its second click: "install" or
	// "uninstall".
	confirm string
	busy    bool
	// notice and actionErr report the outcome of the last cluster write.
	notice    string
	actionErr string
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
		ui.Text(c, "安装与卸载只作用于 Kubeyard 管理的组件；已存在的组件不会被重复安装。").TextColor(t.TextMuted)
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
	a.resetForm(ad)
	ui.Column(c).Grow(1).Padding(14).Gap(8).Background(t.Surface).Radius(10).Border(1, t.Border).Children(func() {
		ui.Text(c, ad.DisplayName).FontSize(16).Bold()
		ui.Text(c, ad.Description).TextColor(t.TextMuted)
		ui.Text(c, fmt.Sprintf("命名空间 %s · 健康检查 %s", ad.Namespace, healthText(ad.Health))).FontSize(12).TextColor(t.TextMuted)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				a.fluxView(c, cs, ad)
				a.paramForm(c, ad)
			})
		})
		a.actions(c, cs, ad)
	})
}

// fluxView shows the Flux state of an addon Kubeyard manages: the Ready
// conditions of its Kustomization and HelmReleases, then the latest events.
func (a *app) fluxView(c *ui.Context, cs catalogState, ad catalog.Addon) {
	t := c.Theme()
	if !cs.managed[ad.Name] {
		if cs.status[ad.Name] == catalog.Installed {
			ui.Text(c, "由其他方式安装，Kubeyard 不管理它，也不显示 Flux 状态").FontSize(12).TextColor(t.TextMuted)
		}
		return
	}
	fx, ok := cs.flux[ad.Name]
	if !ok {
		ui.Text(c, orDash(cs.fluxErr)).FontSize(12).TextColor(t.TextMuted)
		return
	}
	for _, cond := range fx.Conditions {
		text, color := "Ready", t.Success
		if !cond.Ready {
			text, color = "未就绪", t.Danger
		}
		ui.Text(c, fmt.Sprintf("%s/%s  %s  %s", cond.Kind, cond.Name, text, cond.Message)).FontSize(12).TextColor(color).SingleLine()
	}
	for _, ev := range fx.Events {
		ui.Text(c, fmt.Sprintf("%s  %s  %s  %s", ev.Time.Local().Format("15:04:05"), ev.Reason, ev.Object, ev.Message)).FontSize(12).TextColor(t.TextMuted).SingleLine()
	}
}

// paramForm shows one input per parameter, in the order the schema lists them.
func (a *app) paramForm(c *ui.Context, ad catalog.Addon) {
	t := c.Theme()
	if len(ad.Params) == 0 {
		ui.Text(c, "无参数").FontSize(12).TextColor(t.TextMuted)
		return
	}
	for _, p := range ad.Params {
		f := a.fields[p.Path]
		ui.Row(c.Key(p.Path)).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, paramLabel(p)).FontSize(12).SingleLine()
			if p.Type == "boolean" {
				ui.Checkbox(c.Key(p.Path), &f.on, p.Title)
				return
			}
			ui.TextInput(c.Key(p.Path), &f.text).Placeholder(placeholder(p)).Width(320)
		})
	}
}

// actions shows install and uninstall, each behind a second click. A button
// is disabled when the guard refuses, and the reason is shown below.
func (a *app) actions(c *ui.Context, cs catalogState, ad catalog.Addon) {
	t := c.Theme()
	installErr := ad.Installable(cs.status[ad.Name], cs.status)
	uninstallErr := cs.cat.Uninstallable(ad.Name, cs.status, cs.managed[ad.Name])
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			installLabel := "安装"
			if a.confirm == "install" {
				installLabel = "确认安装"
			}
			if ui.Button(c.Key("install"), installLabel).Disabled(installErr != nil || a.busy).Clicked() {
				if a.confirm == "install" {
					a.confirm = ""
					a.install(cs.cat, ad)
				} else {
					a.confirm = "install"
				}
			}
			uninstallLabel := "卸载"
			if a.confirm == "uninstall" {
				uninstallLabel = "确认卸载"
			}
			if ui.Button(c.Key("uninstall"), uninstallLabel).Disabled(uninstallErr != nil || a.busy).Clicked() {
				if a.confirm == "uninstall" {
					a.confirm = ""
					a.uninstall(cs.cat, ad.Name)
				} else {
					a.confirm = "uninstall"
				}
			}
			if a.confirm != "" {
				if ui.Button(c.Key("cancel"), "取消").Clicked() {
					a.confirm = ""
				}
			}
			if a.busy {
				ui.Text(c, "正在提交到集群…").TextColor(t.TextMuted)
			}
		})
		if a.actionErr != "" {
			ui.Text(c, a.actionErr).FontSize(12).TextColor(t.Danger)
		}
		if a.notice != "" {
			ui.Text(c, a.notice).FontSize(12).TextColor(t.Success)
		}
		if installErr != nil {
			ui.Text(c, "安装："+installErr.Error()).FontSize(12).TextColor(t.TextMuted)
		}
		if uninstallErr != nil {
			ui.Text(c, "卸载："+uninstallErr.Error()).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// install validates the form, then writes the addon in the background. The
// guard is checked again against the cluster before anything is written.
func (a *app) install(cat *catalog.Catalog, ad catalog.Addon) {
	values, err := a.formValues(ad)
	if err != nil {
		a.actionErr = err.Error()
		return
	}
	helm, substitute, err := ad.Render(values)
	if err != nil {
		a.actionErr = err.Error()
		return
	}
	a.act("安装", "已提交安装，Flux 正在协调，状态会在刷新后更新", func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
		inv, err := kube.LoadInventory(ctx, cfg)
		if err != nil {
			return err
		}
		statuses := cat.Statuses(inv.Has)
		if err := ad.Installable(statuses[ad.Name], statuses); err != nil {
			return err
		}
		return kube.Install(ctx, cs, dc, kube.Addon{
			Name:       ad.Name,
			Namespace:  ad.Namespace,
			DependsOn:  ad.DependsOn,
			Values:     helm,
			Substitute: substitute,
		})
	})
}

// uninstall removes an addon Kubeyard installed. The guard is checked again
// against the cluster before anything is deleted.
func (a *app) uninstall(cat *catalog.Catalog, name string) {
	a.act("卸载", "已提交卸载，Flux 回收完成后状态会更新", func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
		inv, err := kube.LoadInventory(ctx, cfg)
		if err != nil {
			return err
		}
		if err := cat.Uninstallable(name, cat.Statuses(inv.Has), inv.Managed(name)); err != nil {
			return err
		}
		return kube.Uninstall(ctx, cs, dc, name)
	})
}

// act runs a cluster write for the selected context in the background, then
// reloads the context. done is shown when the write succeeds.
func (a *app) act(label, done string, fn action) {
	name := a.selected
	a.busy = true
	a.actionErr = ""
	a.notice = ""
	go func() {
		err := runAction(name, fn)
		a.win.Update(func() {
			a.busy = false
			if err != nil {
				a.actionErr = label + "失败：" + err.Error()
				return
			}
			a.notice = done
			a.refresh()
		})
	}()
}

// formValues reads the form into parameter values keyed by path, for
// catalog.Addon.Render. An empty optional field is left out, so the schema
// default applies.
func (a *app) formValues(ad catalog.Addon) (map[string]any, error) {
	values := make(map[string]any, len(ad.Params))
	for _, p := range ad.Params {
		f := a.fields[p.Path]
		if p.Type == "boolean" {
			values[p.Path] = f.on
			continue
		}
		text := strings.TrimSpace(f.text)
		if text == "" {
			if p.Required {
				return nil, fmt.Errorf("%s 必填", p.Path)
			}
			continue
		}
		if p.Type == "integer" {
			n, err := strconv.Atoi(text)
			if err != nil {
				return nil, fmt.Errorf("%s 必须是整数", p.Path)
			}
			values[p.Path] = n
			continue
		}
		values[p.Path] = text
	}
	return values, nil
}

// resetForm starts the form over when another addon is selected. Booleans
// start at their schema default; other fields start empty.
func (a *app) resetForm(ad catalog.Addon) {
	if a.formFor == ad.Name {
		return
	}
	a.formFor = ad.Name
	a.fields = make(map[string]*paramField, len(ad.Params))
	a.confirm = ""
	a.actionErr = ""
	a.notice = ""
	for _, p := range ad.Params {
		f := &paramField{}
		f.on, _ = p.Default.(bool)
		a.fields[p.Path] = f
	}
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
	dc, err := dynamic.NewForConfig(cfg)
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
	return loaded{overview: ov, catalog: loadCatalog(ctx, cfg, cs, dc, inv)}, nil
}

// loadCatalog reads the catalog at the revision the source's Flux
// OCIRepository verified. Nothing from an unverified source is shown.
func loadCatalog(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface, inv kube.Inventory) catalogState {
	var out catalogState
	src, err := kube.LoadSource(ctx, cfg, kube.CatalogNamespace, kube.CatalogSource)
	if errors.Is(err, kube.ErrNoSource) {
		out.note = fmt.Sprintf("集群中没有 catalog 源 %s/%s", kube.CatalogNamespace, kube.CatalogSource)
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
	out.status = cat.Statuses(inv.Has)
	out.managed = make(map[string]bool, len(cat.Addons))
	out.flux = make(map[string]kube.AddonFlux)
	for _, ad := range cat.Addons {
		out.managed[ad.Name] = inv.Managed(ad.Name)
		if !out.managed[ad.Name] {
			continue
		}
		fx, err := kube.LoadAddonFlux(ctx, dc, cs, ad.Name, helmReleaseNames(ad))
		if err != nil {
			out.fluxErr = "读取 Flux 状态失败：" + err.Error()
			continue
		}
		out.flux[ad.Name] = fx
	}
	return out
}

// runAction builds clients for a context and runs fn with a bounded context.
func runAction(contextName string, fn action) error {
	cfg, err := kube.RESTConfig(contextName)
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	return fn(ctx, cfg, cs, dc)
}

// helmReleaseNames lists the HelmReleases an addon's health checks name.
func helmReleaseNames(ad catalog.Addon) []string {
	var names []string
	for _, h := range ad.Health {
		if h.Kind == "HelmRelease" {
			names = append(names, h.Name)
		}
	}
	return names
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

// paramLabel is a form row's label: the parameter path, marked when required.
func paramLabel(p catalog.Param) string {
	if p.Required {
		return p.Path + " *"
	}
	return p.Path
}

// placeholder is the hint an empty field shows: the schema default, or the
// parameter's title when it has no default.
func placeholder(p catalog.Param) string {
	if p.Default == nil {
		return p.Title
	}
	return "默认 " + defaultText(p.Default)
}

// defaultText renders a schema default for display.
func defaultText(v any) string {
	if d, ok := v.(float64); ok && d == math.Trunc(d) {
		return strconv.FormatInt(int64(d), 10)
	}
	return fmt.Sprint(v)
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
