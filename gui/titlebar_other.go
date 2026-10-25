//go:build !darwin

package main

import "unsafe"

// titlebarMode tells the page that the window keeps its native title bar.
const titlebarMode = "native"

// hideTitlebar does nothing where the native title bar is kept.
func hideTitlebar(unsafe.Pointer) {}
