// Command fakeapiserver serves the Kubernetes API paths the Kubeyard overview
// reads, with one Ready node and two Running pods. CI runs it on loopback so
// the native window can show real API responses on every OS without a cluster.
// It is a test double, not an API server: it answers a fixed set of paths, does
// not store writes, and must not be used against anything but a test.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7792", "loopback address to serve the fake API on")
	flag.Parse()

	if err := checkLoopback(*addr); err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fake API server listening on http://%s", ln.Addr())
	log.Fatal(http.Serve(ln, handler()))
}

// checkLoopback refuses listen addresses that other machines could reach.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-addr %q must bind a loopback address such as 127.0.0.1", addr)
	}
	return nil
}
