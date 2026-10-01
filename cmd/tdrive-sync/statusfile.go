// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"

	"tdrive-sync/internal/config"
	"tdrive-sync/internal/fsutil"
	"tdrive-sync/internal/manager"
)

// statusFile mirrors every status change to status.json, so external tooling
// can monitor the sync without talking to the HTTP API.
//
// Status updates arrive every few seconds, so a snapshot identical to the last
// one written is skipped, and a persistent write failure (a full disk, say) is
// logged once per distinct error rather than on every update. It is a status
// observer and therefore only ever called from one goroutine at a time.
type statusFile struct {
	errorf  func(string, ...any)
	last    []byte
	lastErr string
}

// write is the observer handed to manager.Subscribe.
func (f *statusFile) write(s manager.Status) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		f.errorf("could not encode status.json: %v", err)
		return
	}
	if bytes.Equal(data, f.last) {
		return
	}
	if err := writeStatusFile(data); err != nil {
		if msg := err.Error(); msg != f.lastErr {
			f.lastErr = msg
			f.errorf("could not write status.json: %v", err)
		}
		return
	}
	f.lastErr = ""
	f.last = data
}

// writeStatusFile atomically replaces status.json with data.
func writeStatusFile(data []byte) error {
	path, err := config.StatusPath()
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, data, 0o644)
}
