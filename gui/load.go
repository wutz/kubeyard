package main

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// action is a cluster write against one context. runAction calls it with
// clients for that context and a context that ends after the action's timeout.
type action func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error

// clients builds the REST config and the typed and dynamic clients of a context.
func clients(contextName string) (*rest.Config, kubernetes.Interface, dynamic.Interface, error) {
	cfg, err := kube.RESTConfig(contextName)
	if err != nil {
		return nil, nil, nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, cs, dc, nil
}

// runAction runs fn against a context. The context ends after timeout.
func runAction(contextName string, timeout time.Duration, fn action) error {
	cfg, cs, dc, err := clients(contextName)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx, cfg, cs, dc)
}

// loadCatalog reads the catalog served by the context's source, and the state
// of each addon in the cluster. Problems with the source are returned in the
// view, not as an error, so the page can explain them.
func loadCatalog(ctx context.Context, contextName string) (catalogView, error) {
	cfg, cs, dc, err := clients(contextName)
	if err != nil {
		return catalogView{}, err
	}
	inv, err := kube.LoadInventory(ctx, cfg)
	if err != nil {
		return catalogView{}, err
	}
	out := catalogView{
		FluxInstalled:   inv.FluxInstalled(),
		FluxInstallable: inv.FluxInstallable(),
	}
	src, err := kube.LoadSource(ctx, cfg, kube.CatalogNamespace, kube.CatalogSource)
	if errors.Is(err, kube.ErrNoSource) {
		out.Note = fmt.Sprintf("集群中没有 catalog 源 %s/%s", kube.CatalogNamespace, kube.CatalogSource)
		if !out.FluxInstalled {
			out.Note = fmt.Sprintf("未检测到 Flux 控制器（%s/source-controller），先安装 Flux 才能添加 catalog 源", kube.FluxNamespace)
		}
		return out, nil
	}
	if err != nil {
		out.Error = "读取 catalog 源失败：" + err.Error()
		return out, nil
	}
	out.Source = toSourceView(src)
	cat, err := verifiedCatalog(ctx, cfg, src)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}

	statuses := cat.Statuses(inv.Has)
	out.Addons = make([]addonView, 0, len(cat.Addons))
	for _, ad := range cat.Addons {
		managed := inv.Managed(ad.Name)
		view := addonBase(cat, ad, statuses, managed)
		if managed {
			helm, substitute, err := kube.LoadAddonParams(ctx, cs, dc, ad.Name)
			if err != nil {
				if out.Error == "" {
					out.Error = "读取已安装参数失败：" + err.Error()
				}
			} else {
				view.Current = ad.Current(helm, substitute)
			}
			fx, err := kube.LoadAddonFlux(ctx, dc, cs, ad.Name, helmReleaseNames(ad))
			if err != nil {
				view.FluxError = "读取 Flux 状态失败：" + err.Error()
			} else {
				view.Flux = toFluxView(fx)
			}
		}
		out.Addons = append(out.Addons, view)
	}
	return out, nil
}

// sourceCatalog reads the catalog a write is checked against. A write refuses
// to run when the source is missing or has not been verified.
func sourceCatalog(ctx context.Context, cfg *rest.Config) (*catalog.Catalog, error) {
	src, err := kube.LoadSource(ctx, cfg, kube.CatalogNamespace, kube.CatalogSource)
	if errors.Is(err, kube.ErrNoSource) {
		return nil, errors.New("集群中没有 catalog 源")
	}
	if err != nil {
		return nil, err
	}
	return verifiedCatalog(ctx, cfg, src)
}

// verifiedCatalog reads the catalog at the revision the source's Flux
// OCIRepository verified. Nothing from an unverified source is read.
func verifiedCatalog(ctx context.Context, cfg *rest.Config, src kube.Source) (*catalog.Catalog, error) {
	switch {
	case !src.Ready && src.Message == "":
		// A source Flux has only just created has no message yet. Say that
		// instead of ending the sentence with nothing after the colon.
		return nil, errors.New("catalog 源还在就绪中，Flux 拉取完成后刷新即可")
	case !src.Ready:
		return nil, errors.New("catalog 源未就绪：" + src.Message)
	case !src.Verified:
		return nil, errors.New("catalog 源未通过 cosign 校验，不显示组件")
	case src.Ref() == "":
		return nil, errors.New("catalog 源还没有解析出版本")
	}
	fsys, err := catalog.Pull(ctx, src.Ref())
	if err != nil {
		return nil, fmt.Errorf("下载 catalog 失败：%w", err)
	}
	cat, err := catalog.Parse(fsys)
	if err != nil {
		return nil, fmt.Errorf("解析 catalog 失败：%w", err)
	}
	return cat, nil
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
