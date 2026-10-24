package main

import (
	"context"
	"net/http"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/kube"
)

// The handlers for the reads and writes the page makes. Reads answer with the
// views in view.go. Writes run in the background of the request and answer
// with a message, or with an error the page shows.

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

func (s *server) contexts(w http.ResponseWriter, r *http.Request) {
	out := contextsView{Contexts: []contextView{}}
	list, err := kube.Contexts()
	if err != nil {
		out.Error = err.Error()
	}
	for _, c := range list {
		out.Contexts = append(out.Contexts, contextView{Name: c.Name, Current: c.Current})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), loadTimeout)
	defer cancel()
	ov, err := loadOverview(ctx, r.URL.Query().Get("context"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, toOverviewView(ov))
}

func (s *server) pools(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), loadTimeout)
	defer cancel()
	pv, err := loadPools(ctx, r.URL.Query().Get("context"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (s *server) catalog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), loadTimeout)
	defer cancel()
	cat, err := loadCatalog(ctx, r.URL.Query().Get("context"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, cat)
}

func (s *server) installFlux(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeWrite(w, r)
	if !ok {
		return
	}
	s.write(w, req.Context, "安装 Flux", "已提交安装 Flux，控制器就绪后组件会出现，可稍后刷新", kube.FluxInstallTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return kube.InstallFlux(ctx, req.Context, cfg, cs, dc)
		})
}

func (s *server) addSource(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeWrite(w, r)
	if !ok {
		return
	}
	s.write(w, req.Context, "添加 catalog 源", "已添加 catalog 源，Flux 拉取并校验后组件会出现，可稍后刷新", actionTimeout, addCatalogSource)
}

func (s *server) installAddon(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeWrite(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	s.write(w, req.Context, "安装", "已提交安装，Flux 正在协调，状态会在刷新后更新", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return installAddon(ctx, cfg, cs, dc, name, req.Fields)
		})
}

func (s *server) updateAddon(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeWrite(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	s.write(w, req.Context, "更新参数", "已写入参数，状态会在刷新后更新", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return updateAddon(ctx, cfg, cs, dc, name, req.Fields)
		})
}

func (s *server) uninstallAddon(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeWrite(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	s.write(w, req.Context, "卸载", "已提交卸载，Flux 回收完成后状态会更新", actionTimeout,
		func(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
			return uninstallAddon(ctx, cfg, cs, dc, name)
		})
}
