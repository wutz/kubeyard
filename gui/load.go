package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/api/v1alpha1"
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

// loadOverview reads the nodes and pods of a context.
func loadOverview(ctx context.Context, contextName string) (kube.Overview, error) {
	_, cs, _, err := clients(contextName)
	if err != nil {
		return kube.Overview{}, err
	}
	return kube.Load(ctx, cs)
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

// The Kubeyard resources the resource pool page reads. Both are cluster-scoped.
var (
	nodePoolResource       = v1alpha1.SchemeGroupVersion.WithResource("nodepools")
	virtualClusterResource = v1alpha1.SchemeGroupVersion.WithResource("virtualclusters")
)

// loadPools reads the NodePools of a context and the VirtualClusters that claim
// them. A cluster without the Kubeyard CRDs is not an error: the view reports
// that the operator is not installed.
func loadPools(ctx context.Context, contextName string) (poolsView, error) {
	_, _, dc, err := clients(contextName)
	if err != nil {
		return poolsView{}, err
	}
	var pools v1alpha1.NodePoolList
	if err := listKubeyard(ctx, dc, nodePoolResource, &pools); err != nil {
		if missingCRD(err) {
			return toPoolsView(false, nil, nil), nil
		}
		return poolsView{}, fmt.Errorf("读取 NodePool 失败：%w", err)
	}
	var claims v1alpha1.VirtualClusterList
	if err := listKubeyard(ctx, dc, virtualClusterResource, &claims); err != nil && !missingCRD(err) {
		return poolsView{}, fmt.Errorf("读取 VirtualCluster 失败：%w", err)
	}
	return toPoolsView(true, pools.Items, claims.Items), nil
}

// loadVirtualClusters reads the VirtualClusters of a context, with the pool
// display names their claims refer to. A cluster without the Kubeyard CRDs is
// not an error: the view reports that the operator is not installed.
func loadVirtualClusters(ctx context.Context, contextName string) (virtualClustersView, error) {
	_, _, dc, err := clients(contextName)
	if err != nil {
		return virtualClustersView{}, err
	}
	var vcs v1alpha1.VirtualClusterList
	if err := listKubeyard(ctx, dc, virtualClusterResource, &vcs); err != nil {
		if missingCRD(err) {
			return toVirtualClustersView(false, nil, nil), nil
		}
		return virtualClustersView{}, fmt.Errorf("读取 VirtualCluster 失败：%w", err)
	}
	var pools v1alpha1.NodePoolList
	if err := listKubeyard(ctx, dc, nodePoolResource, &pools); err != nil && !missingCRD(err) {
		return virtualClustersView{}, fmt.Errorf("读取 NodePool 失败：%w", err)
	}
	return toVirtualClustersView(true, vcs.Items, pools.Items), nil
}

// listKubeyard lists a cluster-scoped resource and decodes the result into list,
// a pointer to one of the pkg/api list types.
func listKubeyard(ctx context.Context, dc dynamic.Interface, gvr schema.GroupVersionResource, list any) error {
	items, err := dc.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	return runtime.DefaultUnstructuredConverter.FromUnstructured(items.UnstructuredContent(), list)
}

// missingCRD reports whether the API server does not serve the resource, which
// is how a cluster without the Kubeyard operator answers.
func missingCRD(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
}
