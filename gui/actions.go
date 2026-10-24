package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/wutz/kubeyard/pkg/catalog"
	"github.com/wutz/kubeyard/pkg/kube"
)

// The cluster writes. Each one checks its guard against the cluster before it
// writes, because the page's view may be stale. Nothing is written when a guard
// refuses.

// addCatalogSource adds the default catalog source. It only creates objects.
func addCatalogSource(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface) error {
	inv, err := kube.LoadInventory(ctx, cfg)
	if err != nil {
		return err
	}
	if !inv.FluxInstalled() {
		return errors.New("未检测到 Flux，无法添加 catalog 源")
	}
	_, err = kube.LoadSource(ctx, cfg, kube.CatalogNamespace, kube.CatalogSource)
	if err == nil {
		return errors.New("catalog 源已存在")
	}
	if !errors.Is(err, kube.ErrNoSource) {
		return err
	}
	return kube.AddCatalogSource(ctx, cs, dc, kube.DefaultCatalogURL, kube.CatalogPublicKey)
}

// installAddon installs an addon from the catalog the source serves, with the
// parameters of the install form.
func installAddon(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface, name string, fields map[string]any) error {
	inv, err := kube.LoadInventory(ctx, cfg)
	if err != nil {
		return err
	}
	cat, err := sourceCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	ad, helm, substitute, err := renderAddon(cat, name, fields)
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
}

// updateAddon rewrites the parameters of an addon Kubeyard installed. Flux
// reconciles the change.
func updateAddon(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface, name string, fields map[string]any) error {
	inv, err := kube.LoadInventory(ctx, cfg)
	if err != nil {
		return err
	}
	if !inv.Managed(name) {
		return errors.New("不是由 Kubeyard 安装的，不会更新参数")
	}
	cat, err := sourceCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	ad, helm, substitute, err := renderAddon(cat, name, fields)
	if err != nil {
		return err
	}
	return kube.Install(ctx, cs, dc, kube.Addon{
		Name:       ad.Name,
		Namespace:  ad.Namespace,
		DependsOn:  ad.DependsOn,
		Values:     helm,
		Substitute: substitute,
	})
}

// uninstallAddon removes an addon Kubeyard installed. Addons from other
// installs are left alone.
func uninstallAddon(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, dc dynamic.Interface, name string) error {
	inv, err := kube.LoadInventory(ctx, cfg)
	if err != nil {
		return err
	}
	cat, err := sourceCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	if err := cat.Uninstallable(name, cat.Statuses(inv.Has), inv.Managed(name)); err != nil {
		return err
	}
	return kube.Uninstall(ctx, cs, dc, name)
}

// renderAddon finds an addon in the catalog and renders the form's fields into
// the Helm values and Flux substitutions that install it.
func renderAddon(cat *catalog.Catalog, name string, fields map[string]any) (catalog.Addon, map[string]any, map[string]string, error) {
	ad, ok := cat.Addon(name)
	if !ok {
		return catalog.Addon{}, nil, nil, fmt.Errorf("目录中没有组件 %s", name)
	}
	values, err := buildValues(ad.Params, fields)
	if err != nil {
		return catalog.Addon{}, nil, nil, err
	}
	helm, substitute, err := ad.Render(values)
	if err != nil {
		return catalog.Addon{}, nil, nil, err
	}
	return ad, helm, substitute, nil
}

// buildValues turns the form's fields into parameter values keyed by path, for
// catalog.Addon.Render. Text is trimmed. An empty optional field is left out,
// so the schema default applies; an empty required field is an error.
func buildValues(params []catalog.Param, fields map[string]any) (map[string]any, error) {
	values := make(map[string]any, len(params))
	for _, p := range params {
		if p.Type == "boolean" {
			on, _ := fields[p.Path].(bool)
			values[p.Path] = on
			continue
		}
		text, err := fieldText(p.Path, fields[p.Path])
		if err != nil {
			return nil, err
		}
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

// fieldText reads a text field. A missing field is empty.
func fieldText(path string, v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	default:
		return "", fmt.Errorf("%s 必须是文本", path)
	}
}
