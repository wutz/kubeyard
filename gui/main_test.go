package main

import "testing"

func TestCheckLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "127.0.0.1:7788", "localhost:7788", "[::1]:7788"} {
		if err := checkLoopback(addr); err != nil {
			t.Errorf("checkLoopback(%q) = %v, want nil", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:7788", ":7788", "192.168.1.10:7788", "[::]:7788", "example.com:7788", "127.0.0.1"} {
		if err := checkLoopback(addr); err == nil {
			t.Errorf("checkLoopback(%q) = nil, want an error", addr)
		}
	}
}
