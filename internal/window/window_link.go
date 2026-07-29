//go:build linux && cgo

package window

// This file only exports a callback for the C code in window_gtk.go, so its
// preamble must stay free of definitions (a cgo rule for files using //export).

import "C"

import "log"

//export goOpenExternalURI
func goOpenExternalURI(uri *C.char) {
	target := C.GoString(uri)
	// Off the GTK main loop: starting a browser must not stall the window.
	go func() {
		if err := OpenExternal(target); err != nil {
			log.Printf("could not open %s in a browser: %v", target, err)
		}
	}()
}
