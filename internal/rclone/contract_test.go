// SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
// SPDX-License-Identifier: GPL-3.0-or-later

package rclone

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These tests run a real rclone (TDRIVE_CONTRACT_RCLONE) so that an rclone
// update which drops a flag or changes an output format is noticed before it
// ships.

func contractBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("TDRIVE_CONTRACT_RCLONE")
	if bin == "" {
		t.Skip("TDRIVE_CONTRACT_RCLONE not set")
	}
	return bin
}

func TestContractFlagsKnown(t *testing.T) {
	bin := contractBinary(t)
	c := &Client{remote: "r", conf: "c"}
	var argSets [][]string
	argSets = append(argSets, c.MountArgs("/mnt", "127.0.0.1:1", "/cache"))
	for _, manual := range []bool{true, false} {
		for _, resync := range []string{"", "newer"} {
			argSets = append(argSets, c.BisyncArgs("/local", "/work", manual, resync))
		}
	}
	// Flags are parsed before --help takes effect, so nothing is mounted or
	// synced, and an unknown flag fails. Searching the help text would not do:
	// flags are also mentioned in the descriptions of other flags.
	for _, args := range argSets {
		help := append(append([]string{}, args...), "--help")
		if out, err := exec.Command(bin, help...).CombinedOutput(); err != nil {
			t.Errorf("rclone rejected %v: %v\n%s", args, err, out)
		}
	}
	bad := append(append([]string{}, argSets[0]...), "--no-such-flag", "--help")
	if err := exec.Command(bin, bad...).Run(); err == nil {
		t.Error("an unknown flag was accepted, so this test proves nothing")
	}
}

// localClient returns a Client for a plain local remote "loc" and a directory
// with a known layout.
func localClient(t *testing.T) (*Client, string) {
	t.Helper()
	t.Setenv("TDRIVE_RCLONE", contractBinary(t))
	root := t.TempDir()
	conf := filepath.Join(t.TempDir(), "rclone.conf")
	if err := os.WriteFile(conf, []byte("[loc]\ntype = local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New("loc", conf, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return c, root
}

func TestContractList(t *testing.T) {
	c, root := localClient(t)
	entries, err := c.List(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "docs" || !entries[0].IsDir ||
		entries[1].Name != "a.txt" || entries[1].IsDir || entries[1].Size != 5 {
		t.Fatalf("List = %+v, want dir docs then file a.txt (5 bytes)", entries)
	}
}

func TestContractListDirs(t *testing.T) {
	c, root := localClient(t)
	dirs, err := c.ListDirs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{root + "/docs": true, root + "/docs/sub": true}
	if len(dirs) != len(want) {
		t.Fatalf("ListDirs = %v, want %v", dirs, want)
	}
	for _, d := range dirs {
		if !want[d] {
			t.Fatalf("ListDirs = %v, want %v", dirs, want)
		}
	}
}

func TestContractRC(t *testing.T) {
	bin := contractBinary(t)
	conf := filepath.Join(t.TempDir(), "rclone.conf")
	if err := os.WriteFile(conf, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	cmd := exec.Command(bin, "rcd", "--rc-addr", addr, "--rc-user", "u", "--rc-pass", "p", "--config", conf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	ctx := context.Background()
	rc := NewRC(addr, "u", "p")
	deadline := time.Now().Add(20 * time.Second)
	for !rc.Ping(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the control server")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if NewRC(addr, "u", "wrong").Ping(ctx) {
		t.Error("Ping succeeded with a wrong password")
	}
	if _, err := rc.CoreStats(ctx); err != nil {
		t.Errorf("CoreStats: %v", err)
	}
	if err := rc.ResetStats(ctx); err != nil {
		t.Errorf("ResetStats: %v", err)
	}
}
