// Package updater implements self-updating for the AppImage build. It checks the
// project's GitHub releases for a newer version (optionally including
// prereleases), downloads the matching .AppImage asset and atomically replaces
// the running AppImage file. Applying an update requires a restart to take
// effect.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub repository the releases are published under.
const (
	defaultOwner = "tokajer"
	defaultRepo  = "tdrive-sync"
	apiBase      = "https://api.github.com"
)

// State is the coarse updater state shown in the UI.
type State string

const (
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateAvailable   State = "available"
	StateDownloading State = "downloading"
	StateReady       State = "ready" // installed, restart pending
	StateUpToDate    State = "uptodate"
	StateError       State = "error"
	StateUnsupported State = "unsupported" // not running as an AppImage
)

// asset is one GitHub release asset.
type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// ghRelease mirrors the fields we use from the GitHub releases API.
type ghRelease struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	HTMLURL    string  `json:"html_url"`
	Assets     []asset `json:"assets"`
}

// Release describes an applicable update found on GitHub.
type Release struct {
	Version    string
	Tag        string
	Name       string
	URL        string
	Prerelease bool
	asset      asset
}

// Status is an immutable snapshot for the UI / API.
type Status struct {
	State         State     `json:"state"`
	Current       string    `json:"current"`
	Available     string    `json:"available"`
	Tag           string    `json:"tag"`
	ReleaseURL    string    `json:"release_url"`
	Prerelease    bool      `json:"prerelease"`
	IncludePre    bool      `json:"include_prerelease"`
	CanSelfUpdate bool      `json:"can_self_update"`
	Progress      int       `json:"progress"`
	Message       string    `json:"message"`
	LastCheck     time.Time `json:"last_check"`
}

// Updater checks for and applies updates. All methods are safe for concurrent
// use.
type Updater struct {
	owner    string
	repo     string
	apiBase  string
	current  string // normalized, for comparison
	display  string // raw version string, for display
	appImage string
	client   *http.Client
	logf     func(string, ...any)

	mu         sync.Mutex
	status     Status
	latest     *Release
	includePre bool
}

// New creates an Updater for the given current version. includePre selects
// whether prereleases are considered. logf may be nil.
func New(current string, includePre bool, logf func(string, ...any)) *Updater {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	app := os.Getenv("APPIMAGE")
	display := strings.TrimSpace(current)
	if display == "" {
		display = "local-dev-build"
	}
	u := &Updater{
		owner:      defaultOwner,
		repo:       defaultRepo,
		apiBase:    apiBase,
		current:    normalize(current),
		display:    display,
		appImage:   app,
		client:     &http.Client{Timeout: 30 * time.Second},
		logf:       logf,
		includePre: includePre,
	}
	st := Status{
		State:         StateIdle,
		Current:       display,
		IncludePre:    includePre,
		CanSelfUpdate: app != "",
		Message:       "Noch nicht geprüft",
	}
	if app == "" {
		st.State = StateUnsupported
		st.Message = "Selbstupdate nur in der AppImage-Version verfügbar"
	}
	u.status = st
	return u
}

// Status returns the current snapshot.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

// SetIncludePrerelease changes whether prereleases are considered.
func (u *Updater) SetIncludePrerelease(on bool) {
	u.mu.Lock()
	u.includePre = on
	u.status.IncludePre = on
	u.mu.Unlock()
}

// Check queries GitHub and updates the status. It returns the applicable
// release when a newer version is available, or nil when up to date.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	u.set(func(s *Status) { s.State = StateChecking; s.Message = "Suche nach Updates…" })

	rels, err := u.fetchReleases(ctx)
	if err != nil {
		u.set(func(s *Status) { s.State = StateError; s.Message = "Update-Prüfung fehlgeschlagen: " + err.Error() })
		return nil, err
	}

	u.mu.Lock()
	includePre := u.includePre
	u.mu.Unlock()
	best := pick(rels, includePre)

	now := time.Now()
	if best == nil || compareVersions(best.Version, u.current) <= 0 {
		u.mu.Lock()
		u.latest = nil
		u.status.State = StateUpToDate
		u.status.Available = ""
		u.status.Tag = ""
		u.status.ReleaseURL = ""
		u.status.Prerelease = false
		u.status.Progress = 0
		u.status.Message = "Aktuell – keine neuere Version"
		u.status.LastCheck = now
		u.mu.Unlock()
		return nil, nil
	}

	u.mu.Lock()
	u.latest = best
	u.status.State = StateAvailable
	u.status.Available = best.Version
	u.status.Tag = best.Tag
	u.status.ReleaseURL = best.URL
	u.status.Prerelease = best.Prerelease
	u.status.Progress = 0
	u.status.Message = "Update verfügbar: " + best.Tag
	u.status.LastCheck = now
	u.mu.Unlock()
	u.logf("Update verfügbar: %s (installiert %s)", best.Tag, u.current)
	return best, nil
}

// Apply downloads the last-found release's AppImage asset and replaces the
// running AppImage. A restart is required afterwards.
func (u *Updater) Apply(ctx context.Context) error {
	u.mu.Lock()
	rel := u.latest
	target := u.appImage
	u.mu.Unlock()

	if target == "" {
		return fmt.Errorf("Selbstupdate nur in der AppImage-Version verfügbar")
	}
	if rel == nil {
		return fmt.Errorf("kein Update verfügbar")
	}

	u.set(func(s *Status) {
		s.State = StateDownloading
		s.Progress = 0
		s.Message = "Lade Update " + rel.Tag + "…"
	})

	tmp, err := u.download(ctx, rel.asset, target)
	if err != nil {
		u.set(func(s *Status) { s.State = StateError; s.Message = "Download fehlgeschlagen: " + err.Error() })
		return err
	}

	// Replace the running AppImage. tmp lives in the same directory, so the
	// rename is atomic; the running process keeps the old inode until restart.
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		u.set(func(s *Status) { s.State = StateError; s.Message = "Ersetzen fehlgeschlagen: " + err.Error() })
		return err
	}

	u.set(func(s *Status) {
		s.State = StateReady
		s.Progress = 100
		s.Message = "Update " + rel.Tag + " installiert – bitte neu starten"
	})
	u.logf("Update %s installiert – Neustart erforderlich", rel.Tag)
	return nil
}

func (u *Updater) download(ctx context.Context, a asset, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tdrive-sync-updater")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".tdrive-sync-update-*.AppImage")
	if err != nil {
		return "", fmt.Errorf("Zielordner nicht beschreibbar: %w", err)
	}
	tmpPath := tmp.Name()
	total := resp.ContentLength
	if a.Size > 0 {
		total = a.Size
	}

	var written int64
	buf := make([]byte, 256*1024)
	for {
		if ctx.Err() != nil {
			cleanup(tmp, tmpPath)
			return "", ctx.Err()
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				cleanup(tmp, tmpPath)
				return "", werr
			}
			written += int64(n)
			if total > 0 {
				u.setProgress(int(written * 100 / total))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			cleanup(tmp, tmpPath)
			return "", rerr
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if a.Size > 0 && written != a.Size {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("unvollständiger Download (%d/%d Bytes)", written, a.Size)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func cleanup(f *os.File, path string) {
	_ = f.Close()
	_ = os.Remove(path)
}

func (u *Updater) fetchReleases(ctx context.Context) ([]ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=30", u.apiBase, u.owner, u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tdrive-sync-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	return rels, nil
}

// pick chooses the highest-versioned, non-draft release that ships a matching
// AppImage asset, honouring the includePre flag.
func pick(rels []ghRelease, includePre bool) *Release {
	var best *Release
	for _, r := range rels {
		if r.Draft {
			continue
		}
		if r.Prerelease && !includePre {
			continue
		}
		a := pickAsset(r.Assets)
		if a == nil {
			continue
		}
		ver := normalize(r.TagName)
		if ver == "" {
			continue
		}
		cand := &Release{
			Version:    ver,
			Tag:        r.TagName,
			Name:       r.Name,
			URL:        r.HTMLURL,
			Prerelease: r.Prerelease,
			asset:      *a,
		}
		if best == nil || compareVersions(cand.Version, best.Version) > 0 {
			best = cand
		}
	}
	return best
}

// pickAsset selects the .AppImage asset, preferring one matching the running
// architecture.
func pickAsset(assets []asset) *asset {
	archTok := archToken()
	var fallback *asset
	for i := range assets {
		a := &assets[i]
		if !strings.HasSuffix(strings.ToLower(a.Name), ".appimage") {
			continue
		}
		if fallback == nil {
			fallback = a
		}
		if archTok != "" && strings.Contains(a.Name, archTok) {
			return a
		}
	}
	return fallback
}

func archToken() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return ""
	}
}

func (u *Updater) set(fn func(*Status)) {
	u.mu.Lock()
	fn(&u.status)
	u.mu.Unlock()
}

func (u *Updater) setProgress(p int) {
	if p < 0 {
		p = 0
	} else if p > 100 {
		p = 100
	}
	u.mu.Lock()
	u.status.Progress = p
	u.mu.Unlock()
}

// normalize strips a leading "v" and surrounding whitespace from a version tag.
func normalize(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

// compareVersions compares two semver-ish versions, returning -1, 0 or 1.
// It handles a numeric major.minor.patch core plus an optional prerelease
// suffix, where a version with a prerelease sorts below the same core without.
func compareVersions(a, b string) int {
	ac, ap := splitPre(a)
	bc, bp := splitPre(b)
	if c := compareCore(ac, bc); c != 0 {
		return c
	}
	// Equal cores: no prerelease outranks a prerelease.
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	default:
		return comparePre(ap, bp)
	}
}

func splitPre(v string) (core, pre string) {
	// Ignore build metadata.
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func compareCore(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		if c := atoi(part(as, i)) - atoi(part(bs, i)); c != 0 {
			if c < 0 {
				return -1
			}
			return 1
		}
	}
	return 0
}

func comparePre(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, aok := as[i], i < len(as)
		bi, bok := bs[i], i < len(bs)
		// Fewer identifiers => lower precedence.
		if !aok {
			return -1
		}
		if !bok {
			return 1
		}
		an, aNum := toNum(ai)
		bn, bNum := toNum(bi)
		switch {
		case aNum && bNum:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aNum != bNum:
			// Numeric identifiers have lower precedence than non-numeric.
			if aNum {
				return -1
			}
			return 1
		default:
			if ai != bi {
				if ai < bi {
					return -1
				}
				return 1
			}
		}
	}
	return 0
}

func part(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "0"
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func toNum(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
