// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package fmstate

import (
	"os"
	"path/filepath"
	"syscall"
)

// seekHole is Linux's SEEK_HOLE: move to the start of the next hole in a sparse
// file (or to the end of the file if there is none).
const seekHole = 4

// allocatedBytes returns the disk space a file really occupies. rclone's cache
// files are sparse: the file has the size of the whole Drive file from the
// start, but only the downloaded parts are allocated.
func allocatedBytes(fi os.FileInfo) int64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi.Size()
	}
	return st.Blocks * 512
}

// firstHole returns the offset of the first not-yet-downloaded region of a
// cache file, or size when the file has no holes at all.
//
// This is what makes "fully downloaded" detectable even where the allocated
// size is smaller than the file size, as on a filesystem with transparent
// compression.
func firstHole(path string, size int64) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	off, err := f.Seek(0, seekHole)
	if err != nil {
		// No SEEK_HOLE support: fall back to "no holes".
		return size, nil
	}
	return off, nil
}

// DiskUsage returns the disk space occupied by a file or directory tree,
// counting only the parts actually allocated.
func DiskUsage(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += allocatedBytes(fi)
		}
		return nil
	})
	return total
}
