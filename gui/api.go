package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/wutz/kubeyard/pkg/kube"
)

// The methods the page calls through Wails bindings. Reads answer with the views
// in view.go. Writes run in the background of the call and answer with a
// message, or with an error the page shows. Wails sends an error to the page as
// its text.

// contextsView lists the kubeconfig contexts. Error explains why the list could
// not be read, when it could not.
type contextsView struct {
	Contexts []contextView `json:"contexts"`
	Error    string        `json:"error,omitempty"`
}

type contextView struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

// Contexts lists the kubeconfig contexts.
func (a *App) Contexts() contextsView {
	out := contextsView{Contexts: []contextView{}}
	list, err := kube.Contexts()
	if err != nil {
		out.Error = err.Error()
	}
	for _, c := range list {
		out.Contexts = append(out.Contexts, contextView{Name: c.Name, Current: c.Current})
	}
	return out
}

// Overview reads the nodes and pods of a context.
func (a *App) Overview(contextName string) (overviewView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	ov, err := loadOverview(ctx, contextName)
	if err != nil {
		return overviewView{}, err
	}
	return toOverviewView(ov), nil
}

// Pools reads the resource pool page of a context.
func (a *App) Pools(contextName string) (poolsView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	return loadPools(ctx, contextName)
}

// VirtualClusters reads the virtual cluster page of a context.
func (a *App) VirtualClusters(contextName string) (virtualClustersView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	return loadVirtualClusters(ctx, contextName)
}

// Catalog reads the component catalog of a context and what its cluster runs.
func (a *App) Catalog(contextName string) (catalogView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	return loadCatalog(ctx, contextName)
}

// InstallFlux installs Flux into the context.
func (a *App) InstallFlux(contextName string) (string, error) {
	return a.write(contextName, "安装 Flux", "已提交安装 Flux，控制器就绪后组件会出现，可稍后刷新", kube.FluxInstallTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return kube.InstallFlux(ctx, contextName, cfg, cs, dc)
		})
}

// AddCatalogSource adds the default catalog source to the context.
func (a *App) AddCatalogSource(contextName string) (string, error) {
	return a.write(contextName, "添加 catalog 源", "已添加 catalog 源，Flux 拉取并校验后组件会出现，可稍后刷新", actionTimeout, addCatalogSource)
}

// InstallAddon installs an addon from the catalog with the form's fields.
func (a *App) InstallAddon(contextName, name string, fields map[string]any) (string, error) {
	return a.write(contextName, "安装", "已提交安装，Flux 协调完成后状态会更新", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return installAddon(ctx, cfg, cs, dc, name, fields)
		})
}

// UpdateAddon rewrites the parameters of an addon Kubeyard installed.
func (a *App) UpdateAddon(contextName, name string, fields map[string]any) (string, error) {
	return a.write(contextName, "更新参数", "已写入参数，Flux 协调完成后状态会更新", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return updateAddon(ctx, cfg, cs, dc, name, fields)
		})
}

// UninstallAddon removes an addon Kubeyard installed.
func (a *App) UninstallAddon(contextName, name string) (string, error) {
	return a.write(contextName, "卸载", "已提交卸载，Flux 回收完成后状态会更新", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return uninstallAddon(ctx, cfg, cs, dc, name)
		})
}

// PinNode pins a node of a pool to the claim of a virtual cluster, so the
// operator grants it to that cluster first (docs/gui.md §3, dragging a node onto
// a virtual cluster). The rules are in checkPin.
func (a *App) PinNode(contextName, vc, pool, node string) (string, error) {
	return a.write(contextName, "固定节点", "已固定节点 "+node+" 到 "+vc+"，operator 下一轮分配后生效", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return setPinned(ctx, cs, dc, vc, pool, node, true)
		})
}

// UnpinNode removes a node from the pinned nodes of a virtual cluster's claim.
// The node stays with the cluster until the operator releases it, like any other
// node the claim holds.
func (a *App) UnpinNode(contextName, vc, pool, node string) (string, error) {
	return a.write(contextName, "解除固定", "已解除节点 "+node+" 的固定", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return setPinned(ctx, cs, dc, vc, pool, node, false)
		})
}

// saveFileDialog is the native save dialog. A variable, so the local harness can
// pick the file without a window; the product always uses the dialog.
var saveFileDialog = runtime.SaveFileDialog

// ExportAdminKubeconfig saves the cluster-admin kubeconfig of a VirtualCluster
// (docs/operator.md §4.1). confirm must repeat the VirtualCluster name. The user
// picks the file in a save dialog first, so the credential is written to the
// file and never returned to the page. The audit Event is written before the
// file, so an export without a record is not possible. The result is the path
// written, or "" when the user cancelled the dialog.
func (a *App) ExportAdminKubeconfig(contextName, name, confirm string) (string, error) {
	if contextName == "" {
		return "", errors.New("缺少 context")
	}
	if confirm != name {
		return "", fmt.Errorf("请输入虚拟集群名称 %s 以确认导出", name)
	}
	path, err := saveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出管理员 kubeconfig",
		DefaultFilename: name + "-admin.kubeconfig",
		Filters:         []runtime.FileFilter{{DisplayName: "kubeconfig", Pattern: "*.kubeconfig;*.yaml"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	_, cs, dc, err := clients(contextName)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	data, err := kube.AdminKubeconfig(ctx, cs, dc, name)
	if err != nil {
		return "", err
	}
	actor := "GUI（context " + contextName + "）"
	if err := kube.RecordAccess(ctx, cs, dc, name, kube.ExportedKubeconfig, "管理员 kubeconfig 已导出，操作来源："+actor); err != nil {
		return "", err
	}
	// The file holds a cluster-admin credential, so only its owner may read it.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// OpenVirtualClusterAdmin reads the nodes and pods of a VirtualCluster as its
// cluster-admin (docs/gui.md §4). The audit Event is written before the
// credential is read, so an open without a record is not possible. The
// credential lives in memory for this call and is never returned: the page gets
// the overview only. confirm must repeat the VirtualCluster name.
func (a *App) OpenVirtualClusterAdmin(contextName, name, confirm string) (overviewView, error) {
	if contextName == "" {
		return overviewView{}, errors.New("缺少 context")
	}
	if confirm != name {
		return overviewView{}, fmt.Errorf("请输入虚拟集群名称 %s 以确认", name)
	}
	_, cs, dc, err := clients(contextName)
	if err != nil {
		return overviewView{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	actor := "GUI（context " + contextName + "，以管理员身份打开）"
	if err := kube.RecordAccess(ctx, cs, dc, name, kube.OpenedAsAdmin, "管理员身份打开，操作来源："+actor); err != nil {
		return overviewView{}, err
	}
	data, err := kube.AdminKubeconfig(ctx, cs, dc, name)
	if err != nil {
		return overviewView{}, err
	}
	_, acs, err := kube.AdminClient(data)
	if err != nil {
		return overviewView{}, err
	}
	ov, err := kube.Load(ctx, acs)
	if err != nil {
		return overviewView{}, fmt.Errorf("以 %s 管理员身份读取失败：%w", name, err)
	}
	return toOverviewView(ov), nil
}

// write runs fn against the context and answers with done. A failure is the
// error, with label in front of it. Writes are serialized, see App.writeMu.
func (a *App) write(contextName, label, done string, timeout time.Duration, fn action) (string, error) {
	if contextName == "" {
		return "", errors.New("缺少 context")
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if err := runAction(contextName, timeout, fn); err != nil {
		return "", fmt.Errorf("%s失败：%w", label, err)
	}
	return done, nil
}
