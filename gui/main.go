// Command gui is the Kubeyard desktop client. It shows the UI in a native window
// built with Wails. The page calls the methods of App through Wails bindings,
// and they talk to the API server of the selected kubeconfig context through
// pkg/kube and pkg/catalog. There is no backend service and no network listener.
package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed frontend
var frontend embed.FS

func main() {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	app := NewApp()
	err = wails.Run(&options.App{
		Title:     "Kubeyard",
		Width:     1120,
		Height:    720,
		MinWidth:  960,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind:      []any{app},
		// The page draws the whole window and the traffic lights float over its
		// top left corner, so the title bar is hidden (see app.css).
		Mac: &mac.Options{TitleBar: mac.TitleBarHidden()},
	})
	if err != nil {
		log.Fatal(err)
	}
}
