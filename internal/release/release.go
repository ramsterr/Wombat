// Package release talks to the GitHub releases API so the CLI can check for
// and install new wombat builds. It is deliberately dependency free: the CLI
// already ships as a single static binary and must keep working offline.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// Owner and Repo identify the GitHub repository that publishes releases.
	Owner = "ramsterr"
	Repo  = "Wombat"

	// DefaultAPIBase is the public GitHub API root.
	DefaultAPIBase = "https://api.github.com"

	// ProjectName is the binary name used in release assets.
	ProjectName = "wombat"
)

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a published wombat release.
type Release struct {
	Tag        string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Version returns the release version without a leading "v".
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Asset returns the archive for the given platform.
func (r Release) Asset(goos, goarch string) (Asset, bool) {
	name := ArchiveName(r.Version(), goos, goarch)
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Checksums returns the checksum manifest asset.
func (r Release) Checksums() (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == ChecksumName {
			return a, true
		}
	}
	return Asset{}, false
}

// Client fetches release metadata.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a client pointed at the public GitHub API. The
// WOMBAT_RELEASE_API environment variable overrides the API root, which is
// useful for GitHub Enterprise proxies and for tests.
func NewClient() *Client {
	base := DefaultAPIBase
	if v := strings.TrimSpace(os.Getenv("WOMBAT_RELEASE_API")); v != "" {
		base = v
	}
	return &Client{
		BaseURL: base,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Latest returns the newest published release. The rolling "stable" release
// is skipped so it never shadows a real versioned release.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	releases, err := c.list(ctx, "/repos/"+Owner+"/"+Repo+"/releases?per_page=30")
	if err != nil {
		return Release{}, err
	}
	var best Release
	found := false
	for _, rel := range releases {
		if rel.Draft || rel.Prerelease || rel.Tag == "" || rel.Tag == RollingTag {
			continue
		}
		if !found {
			best = rel
			found = true
			continue
		}
		if Compare(rel.Version(), best.Version()) > 0 {
			best = rel
		}
	}
	if !found {
		return Release{}, fmt.Errorf("no published release found for %s/%s", Owner, Repo)
	}
	return best, nil
}

// RollingTag is the tag of the rolling release that always points at the
// newest stable version.
const RollingTag = "stable"

// Release returns the release for a specific tag.
func (c *Client) Release(ctx context.Context, tag string) (Release, error) {
	return c.fetch(ctx, "/repos/"+Owner+"/"+Repo+"/releases/tags/"+tag)
}

func (c *Client) list(ctx context.Context, path string) ([]Release, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultAPIBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", ProjectName+"-release-check")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api: http %d", resp.StatusCode)
	}
	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	return releases, nil
}

func (c *Client) fetch(ctx context.Context, path string) (Release, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultAPIBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", ProjectName+"-release-check")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("no release found for %s/%s", Owner, Repo)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github api: http %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("github api: %w", err)
	}
	if rel.Tag == "" {
		return Release{}, errors.New("github api: release has no tag")
	}
	return rel, nil
}

// ArchiveName is the release asset name for a platform.
func ArchiveName(version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s%s", ProjectName, version, goos, goarch, ArchiveExt(goos))
}

// ArchiveExt is the archive extension used for a platform.
func ArchiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// ChecksumName is the name of the checksum manifest in a release.
const ChecksumName = "checksums.txt"

// Download writes an asset to dir and returns the path.
func Download(ctx context.Context, asset Asset, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ProjectName+"-release-check")
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: http %d", asset.Name, resp.StatusCode)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, asset.Name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return path, nil
}

// VerifyChecksum checks a file against a goreleaser style checksums manifest.
func VerifyChecksum(path string, manifest []byte) error {
	sum, err := FileSHA256(path)
	if err != nil {
		return err
	}
	want, ok := manifestEntry(manifest, filepath.Base(path))
	if !ok {
		return fmt.Errorf("no checksum for %s in manifest", filepath.Base(path))
	}
	if !strings.EqualFold(want, sum) {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", filepath.Base(path), want, sum)
	}
	return nil
}

func manifestEntry(manifest []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			return fields[0], true
		}
	}
	return "", false
}

// FileSHA256 returns the hex sha256 of a file.
func FileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Normalize strips a leading "v" and surrounding whitespace from a version.
func Normalize(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// Compare returns -1 when a is older than b, 0 when they are equal, and 1 when
// a is newer than b. It understands simple semver with optional pre-release
// suffixes and ignores build metadata.
func Compare(a, b string) int {
	an, ap := splitVersion(a)
	bn, bp := splitVersion(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return comparePrerelease(ap, bp)
}

func splitVersion(v string) ([]int, string) {
	v = Normalize(v)
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out, pre
}

func comparePrerelease(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if x == y {
			continue
		}
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn < yn {
				return -1
			}
			return 1
		case xerr == nil:
			return -1
		case yerr == nil:
			return 1
		default:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

// AssetFor returns the release asset for the current platform.
func AssetFor(rel Release) (Asset, bool) {
	return rel.Asset(runtime.GOOS, runtime.GOARCH)
}
