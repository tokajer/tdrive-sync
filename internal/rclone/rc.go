package rclone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// RC talks to a running `rclone mount --rc` control server.
type RC struct {
	addr string
	user string
	pass string
	http *http.Client
}

// NewRC returns a control client for the given "host:port" address,
// authenticating with the given credentials (matching the mount's
// --rc-user/--rc-pass).
func NewRC(addr, user, pass string) *RC {
	return &RC{addr: addr, user: user, pass: pass, http: &http.Client{Timeout: 30 * time.Second}}
}

func (r *RC) call(ctx context.Context, path string, in map[string]any) (map[string]any, error) {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/%s", r.addr, path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.user != "" {
		req.SetBasicAuth(r.user, r.pass)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("rc %s: status %d", path, resp.StatusCode)
	}
	return out, nil
}

// Ping reports whether the control server is reachable.
func (r *RC) Ping(ctx context.Context) bool {
	_, err := r.call(ctx, "rc/noop", nil)
	return err == nil
}

// Refresh re-reads a directory's listing from Drive (recursively).
func (r *RC) Refresh(ctx context.Context, dir string) error {
	_, err := r.call(ctx, "vfs/refresh", map[string]any{"dir": dir, "recursive": "true"})
	return err
}

// Forget evicts a directory from the VFS cache (used when unpinning offline).
func (r *RC) Forget(ctx context.Context, dir string) error {
	_, err := r.call(ctx, "vfs/forget", map[string]any{"dir": dir})
	return err
}

// ForgetFile evicts a single file from the VFS cache. rclone distinguishes the
// two by parameter name, so a file must not be passed to Forget.
func (r *RC) ForgetFile(ctx context.Context, file string) error {
	_, err := r.call(ctx, "vfs/forget", map[string]any{"file": file})
	return err
}

// Stats holds a snapshot of transfer activity.
type Stats struct {
	Transferring int
	Checking     int
	Bytes        int64
	Speed        float64
	Errors       int64
	LastError    string
}

// ResetStats clears rclone's accumulated transfer statistics (incl. error count).
func (r *RC) ResetStats(ctx context.Context) error {
	_, err := r.call(ctx, "core/stats-reset", nil)
	return err
}

// CoreStats returns the current transfer statistics from the mount.
func (r *RC) CoreStats(ctx context.Context) (Stats, error) {
	out, err := r.call(ctx, "core/stats", nil)
	if err != nil {
		return Stats{}, err
	}
	var s Stats
	if v, ok := out["bytes"].(float64); ok {
		s.Bytes = int64(v)
	}
	if v, ok := out["speed"].(float64); ok {
		s.Speed = v
	}
	if v, ok := out["errors"].(float64); ok {
		s.Errors = int64(v)
	}
	if v, ok := out["lastError"].(string); ok {
		s.LastError = v
	}
	if arr, ok := out["transferring"].([]any); ok {
		s.Transferring = len(arr)
	}
	if arr, ok := out["checking"].([]any); ok {
		s.Checking = len(arr)
	}
	return s, nil
}
