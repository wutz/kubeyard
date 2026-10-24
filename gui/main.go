// Command gui is the Kubeyard desktop client. M1 covers connecting to
// kubeconfig contexts and showing a read-only cluster overview. It talks to
// the API server directly; there is no backend.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/wutz/kubeyard/pkg/kube"
)

const loadTimeout = 15 * time.Second

// app is the state the window shows. Background loads write to it through
// win.Update, so the fields are only touched on the main thread.
type app struct {
	win      *mygo.Window
	contexts []string
	selected string

	loadedFor string
	loading   bool
	err       string
	overview  kube.Overview
	nodes     ui.ListState
}

// view builds the interface from the state on the main thread.
func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(16).Children(func() {
		a.toolbar(c)
		if a.err != "" {
			ui.Text(c, a.err).TextColor(t.Danger)
		}
		ui.Row(c).Gap(12).Children(func() {
			card(c, "节点就绪", fmt.Sprintf("%d / %d", a.overview.NodesReady, len(a.overview.Nodes)), "Ready 的节点数")
			card(c, "GPU 分配", fmt.Sprintf("%d / %d", a.overview.GPURequested, a.overview.GPUTotal), "requests / allocatable")
			card(c, "Pod 运行中", fmt.Sprint(a.overview.PodsRunning), fmt.Sprintf("Pending %d · 失败 %d", a.overview.PodsPending, a.overview.PodsFailed))
		})
		a.nodeTable(c)
	})
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
		ui.Spacer(c)
		status := ""
		switch {
		case a.loading:
			status = "加载中…"
		case a.overview.Nodes != nil:
			status = fmt.Sprintf("Pods 共 %d · Pending 无节点 %d", a.overview.PodsRunning+a.overview.PodsPending, a.overview.PodsUnscheduled)
		}
		ui.Text(c, status).TextColor(t.TextMuted)
	})
}

func (a *app) nodeTable(c *ui.Context) {
	nodes := a.overview.Nodes
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

// refresh loads the overview of the selected context in the background.
func (a *app) refresh() {
	name := a.selected
	a.loadedFor = name
	a.loading = true
	a.err = ""
	go func() {
		ov, err := load(name)
		a.win.Update(func() {
			if a.loadedFor != name {
				// The user picked another context while this one loaded.
				return
			}
			a.loading = false
			if err != nil {
				a.err = err.Error()
				a.overview = kube.Overview{}
				return
			}
			a.overview = ov
		})
	}()
}

func load(contextName string) (kube.Overview, error) {
	cs, err := kube.Connect(contextName)
	if err != nil {
		return kube.Overview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	return kube.Load(ctx, cs)
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
