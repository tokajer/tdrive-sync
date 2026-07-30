// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !cgo

package window

import "fmt"

// Available reports that no native-window implementation is compiled in.
const Available = false

// Open is a no-op stub used when the binary is built without cgo.
func Open(title, url string) error {
	return fmt.Errorf("built without cgo – native window not available")
}
