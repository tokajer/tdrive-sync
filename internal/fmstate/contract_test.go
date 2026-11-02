// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package fmstate

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These tests run a real rclone (TDRIVE_CONTRACT_RCLONE) and check the cache
// facts Cache relies on, so an rclone update that changes them is noticed. No
// FUSE is needed: `serve webdav` uses the same VFS cache as `mount`.

// contractServer is an rclone webdav server whose backend is dataDir and whose
// VFS cache lives in cacheDir.
type contractServer struct {
	base    string
	dataDir string
	cache   Cache
}

// The combine backend gives the cache layout vfs/<remote>/d/<rel>, which is
// what Cache assumes; an alias or plain local remote would put the absolute
// backend path into the cache path.
const contractPrefix = "d/"

func startContractServer(t *testing.T) *contractServer {
	t.Helper()
	bin := os.Getenv("TDRIVE_CONTRACT_RCLONE")
	if bin == "" {
		t.Skip("TDRIVE_CONTRACT_RCLONE not set")
	}
	dataDir, cacheDir := t.TempDir(), t.TempDir()
	conf := filepath.Join(t.TempDir(), "rclone.conf")
	if err := os.WriteFile(conf, []byte("[loc]\ntype = combine\nupstreams = d="+dataDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	cmd := exec.Command(bin, "serve", "webdav", "loc:", "--addr", addr,
		"--vfs-cache-mode", "full", "--cache-dir", cacheDir,
		"--vfs-write-back", "1h", "--vfs-cache-max-age", "9999h",
		"--vfs-read-chunk-size", "1M", "--buffer-size", "1M",
		"--config", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	s := &contractServer{base: "http://" + addr + "/" + contractPrefix, dataDir: dataDir,
		cache: Cache{Dir: cacheDir, Remote: "loc"}}
	waitFor(t, "webdav server to answer", func() bool {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// seed puts size random bytes into the backend and returns them.
func (s *contractServer) seed(t *testing.T, name string, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	rand.Read(b)
	if err := os.WriteFile(filepath.Join(s.dataDir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func (s *contractServer) get(t *testing.T, name, rng string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.base+name, nil)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode/100 != 2 {
		t.Fatalf("GET %s: status %d, err %v", name, resp.StatusCode, err)
	}
	return body
}

func TestContractPartialRead(t *testing.T) {
	s := startContractServer(t)
	s.seed(t, "big.bin", 64<<20)
	if got := s.get(t, "big.bin", "bytes=0-4095"); len(got) != 4096 {
		t.Fatalf("range read returned %d bytes", len(got))
	}
	rel := contractPrefix + "big.bin"
	// rclone writes the metadata with a delay; Found alone is not enough.
	waitFor(t, "cache metadata", func() bool { _, ok := readMeta(s.cache.MetaPath(rel)); return ok })
	if info := s.cache.Inspect(rel); !info.Found || info.Complete || info.Dirty {
		t.Fatalf("partial read: got %+v, want Found and neither Complete nor Dirty", info)
	}
}

func TestContractFullRead(t *testing.T) {
	s := startContractServer(t)
	want := s.seed(t, "full.bin", 8<<20)
	if got := s.get(t, "full.bin", ""); !bytes.Equal(got, want) {
		t.Fatal("full read returned different bytes")
	}
	rel := contractPrefix + "full.bin"
	waitFor(t, "cache to report the file complete", func() bool { return s.cache.Inspect(rel).Complete })
	if info := s.cache.Inspect(rel); !info.Found || info.Dirty {
		t.Fatalf("full read: got %+v, want Found and not Dirty", info)
	}
}

func TestContractDirtyFileSurvivesEvict(t *testing.T) {
	s := startContractServer(t)
	body := make([]byte, 1<<20)
	rand.Read(body)
	req, _ := http.NewRequest(http.MethodPut, s.base+"new.bin", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("PUT: status %d", resp.StatusCode)
	}
	rel := contractPrefix + "new.bin"
	waitFor(t, "cache to report the upload dirty", func() bool { return s.cache.Inspect(rel).Dirty })
	m, ok := readMeta(s.cache.MetaPath(rel))
	if !ok || !m.Dirty || m.Size != int64(len(body)) {
		t.Fatalf("meta = %+v (parsed %v), want Dirty with Size %d", m, ok, len(body))
	}

	freed, kept, err := s.cache.Evict(rel)
	if err != nil || kept != 1 || freed != 0 {
		t.Fatalf("Evict = %d, %d, %v; want 0, 1, nil", freed, kept, err)
	}
	got, err := os.ReadFile(s.cache.DataPath(rel))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("dirty cache copy was damaged by Evict: %v", err)
	}
}

func TestContractEvictCleanFile(t *testing.T) {
	s := startContractServer(t)
	want := s.seed(t, "clean.bin", 4<<20)
	s.get(t, "clean.bin", "")
	rel := contractPrefix + "clean.bin"
	waitFor(t, "cache to report the file complete", func() bool { return s.cache.Inspect(rel).Complete })
	// Metadata is written after the data; Evict only knows a file is clean from it.
	waitFor(t, "cache metadata", func() bool { _, ok := readMeta(s.cache.MetaPath(rel)); return ok })

	if _, kept, err := s.cache.Evict(rel); err != nil || kept != 0 {
		t.Fatalf("Evict: kept %d, err %v", kept, err)
	}
	for _, p := range []string{s.cache.DataPath(rel), s.cache.MetaPath(rel)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after Evict (err %v)", p, err)
		}
	}
	if got := s.get(t, "clean.bin", ""); !bytes.Equal(got, want) {
		t.Fatal("read after Evict returned different bytes")
	}
}
