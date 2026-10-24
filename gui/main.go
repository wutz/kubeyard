// Command gui is the Kubeyard desktop client. It serves the UI on a loopback
// port and shows it in a native webview window. The API talks to the API server
// of the selected kubeconfig context directly through pkg/kube and pkg/catalog;
// there is no backend service.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"

	webview "github.com/webview/webview_go"
)

func main() {
	headless := flag.Bool("headless", false, "serve the UI without a window and print its URL")
	addr := flag.String("addr", "127.0.0.1:0", "loopback address to serve the UI on")
	debug := flag.Bool("debug", false, "enable the webview developer tools")
	flag.Parse()

	if err := checkLoopback(*addr); err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	srv, err := newServer(ln.Addr())
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		log.Fatal(http.Serve(ln, srv.handler()))
	}()
	url := "http://" + ln.Addr().String() + "/"

	if *headless {
		fmt.Println(url)
		select {}
	}

	w := webview.New(*debug)
	defer w.Destroy()
	w.SetTitle("Kubeyard")
	w.SetSize(1120, 720, webview.HintNone)
	w.SetSize(960, 600, webview.HintMin)
	w.Navigate(url)
	w.Run()
}

// checkLoopback refuses listen addresses that other machines could reach. The
// API trusts only loopback callers, so the UI must never leave the machine.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("-addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-addr %q must bind a loopback address such as 127.0.0.1", addr)
	}
	return nil
}
