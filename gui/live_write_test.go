package main

import (
	"context"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestLiveWritePath runs the cluster writes the page makes, through App, against
// a live cluster: install Flux, add the catalog source, install nfd, change its
// replica count, then uninstall it. It changes the cluster, so it runs only when
// KUBEYARD_LIVE_WRITE names a context, and that context must be disposable.
func TestLiveWritePath(t *testing.T) {
	name := os.Getenv("KUBEYARD_LIVE_WRITE")
	if name == "" {
		t.Skip("KUBEYARD_LIVE_WRITE is not set")
	}
	app := NewApp()
	ctx := context.Background()
	_, cs, _, err := clients(name)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.InstallFlux(name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Flux installed", 15*time.Minute, func() bool {
		cat, err := app.Catalog(name)
		return err == nil && cat.FluxInstalled
	})

	if _, err := app.AddCatalogSource(name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catalog listed", 15*time.Minute, func() bool {
		cat, err := app.Catalog(name)
		return err == nil && len(cat.Addons) > 0
	})

	if _, err := app.InstallAddon(name, "nfd", map[string]any{"master.replicaCount": "2"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "nfd installed", 15*time.Minute, func() bool {
		return addonStatus(t, app, name, "nfd") == "installed"
	})
	t.Logf("nfd uninstall warning while installed: %q", addonField(t, app, name, "nfd").UninstallWarning)

	if _, err := app.UpdateAddon(name, "nfd", map[string]any{"master.replicaCount": "3"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "nfd master scaled to 3", 5*time.Minute, func() bool {
		d, err := cs.AppsV1().Deployments("nfd").Get(ctx, "nfd-node-feature-discovery-master", metav1.GetOptions{})
		return err == nil && d.Spec.Replicas != nil && *d.Spec.Replicas == 3
	})

	if _, err := app.UninstallAddon(name, "nfd"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "nfd absent", 15*time.Minute, func() bool {
		return addonStatus(t, app, name, "nfd") == "absent"
	})
	waitFor(t, "nfd workloads pruned", 5*time.Minute, func() bool {
		_, err := cs.AppsV1().Deployments("nfd").Get(ctx, "nfd-node-feature-discovery-master", metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	})

	// The namespace is created by Helm, so Flux does not prune it. This is the
	// behaviour the uninstall warning describes (roadmap M1, decision B).
	ns, err := cs.CoreV1().Namespaces().Get(ctx, "nfd", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace nfd after uninstall: %v", err)
	}
	if ns.Status.Phase != corev1.NamespaceActive {
		t.Errorf("namespace nfd phase = %s, want Active", ns.Status.Phase)
	}
	t.Logf("namespace nfd kept after uninstall, created %s", ns.CreationTimestamp.Format(time.RFC3339))
}

// addonField returns the page view of one addon of the catalog.
func addonField(t *testing.T, app *App, contextName, addon string) addonView {
	t.Helper()
	cat, err := app.Catalog(contextName)
	if err != nil {
		t.Fatal(err)
	}
	for _, ad := range cat.Addons {
		if ad.Name == addon {
			return ad
		}
	}
	t.Fatalf("addon %s not in the catalog", addon)
	return addonView{}
}

// addonStatus returns the status the page shows for one addon, or "" while the
// catalog cannot be read.
func addonStatus(t *testing.T, app *App, contextName, addon string) string {
	t.Helper()
	cat, err := app.Catalog(contextName)
	if err != nil {
		return ""
	}
	for _, ad := range cat.Addons {
		if ad.Name == addon {
			return ad.Status
		}
	}
	return ""
}

// waitFor polls cond until it holds or the timeout runs out.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Second)
	}
	t.Logf("%s at %s", what, time.Now().Format(time.TimeOnly))
}
