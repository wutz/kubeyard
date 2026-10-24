// Package kube reads kubeconfig contexts and summarizes clusters for the GUI.
// It only reads from the API server; nothing here writes to a cluster.
package kube

import (
	"fmt"
	"sort"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Context is one entry of a kubeconfig file.
type Context struct {
	Name    string
	Cluster string
	Server  string
	Current bool
}

// Contexts lists the contexts of the default kubeconfig loading rules
// (KUBECONFIG, then ~/.kube/config), sorted by name.
func Contexts() ([]Context, error) {
	raw, err := loadingRules().Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	out := make([]Context, 0, len(raw.Contexts))
	for name, ctx := range raw.Contexts {
		item := Context{Name: name, Cluster: ctx.Cluster, Current: name == raw.CurrentContext}
		if cluster, ok := raw.Clusters[ctx.Cluster]; ok {
			item.Server = cluster.Server
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Connect builds a clientset for the named context. Requests time out after
// ten seconds so a dead cluster cannot freeze the window.
func Connect(contextName string) (*kubernetes.Clientset, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules(), &clientcmd.ConfigOverrides{CurrentContext: contextName},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("context %q: %w", contextName, err)
	}
	cfg.Timeout = 10 * time.Second
	return kubernetes.NewForConfig(cfg)
}

func loadingRules() *clientcmd.ClientConfigLoadingRules {
	return clientcmd.NewDefaultClientConfigLoadingRules()
}
