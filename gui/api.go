package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

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
