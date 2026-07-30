// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package fmstate

import (
	"os"
	"path/filepath"
)

// allocatedBytes falls back to the plain file size where sparse-file details are
// not available.
func allocatedBytes(fi os.FileInfo) int64 { return fi.Size() }

// firstHole reports "no holes", so completeness is decided by the recorded
// ranges and the allocated size alone.
func firstHole(_ string, size int64) (int64, error) { return size, nil }

// DiskUsage returns the space occupied by a file or directory tree.
func DiskUsage(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}
