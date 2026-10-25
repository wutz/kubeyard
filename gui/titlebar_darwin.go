//go:build darwin

package main

// #cgo LDFLAGS: -framework Cocoa
// void hideTitlebar(void *window);
import "C"

import "unsafe"

// titlebarMode tells the page that the window has no title bar, so the page
// keeps the traffic lights clear (see app.css).
const titlebarMode = "hidden"

// hideTitlebar removes the title bar of the window. The page then draws the
// full window, and the traffic lights float over its top left corner. window
// is the NSWindow that webview returns; it must be called on the main thread.
func hideTitlebar(window unsafe.Pointer) {
	C.hideTitlebar(window)
}
