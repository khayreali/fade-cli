// Package update checks published releases and replaces an installed binary.
// GitHub source commits are not releases: only complete stable releases are offered.
package update

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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repository = "https://github.com/khayreali/fade-cli"
	latestURL  = "https://api.github.com/repos/khayreali/fade-cli/releases/latest"
	maxBinary  = 64 << 20
)

var ErrNoRelease = errors.New("no stable release is published yet")
var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }
func (r Release) Page() string    { return repository + "/releases/tag/" + r.Tag }

// Newer compares stable versions numerically, never lexically or by commit date.
func (r Release) Newer(current string) bool {
	a, b := versionPattern.FindStringSubmatch(r.Tag), versionPattern.FindStringSubmatch(current)
	if len(a) != 4 || len(b) != 4 || r.Draft || r.Prerelease {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, e1 := strconv.ParseUint(a[i], 10, 64)
		y, e2 := strconv.ParseUint(b[i], 10, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func AssetName(goos, arch string) (string, error) {
	if (goos != "darwin" && goos != "linux") || (arch != "arm64" && arch != "amd64") {
		return "", fmt.Errorf("no prebuilt update for %s/%s; update from source with make install", goos, arch)
	}
	return "fade-cli_" + goos + "_" + arch, nil
}

func (r Release) asset(name string) (string, error) {
	if !versionPattern.MatchString(r.Tag) || r.Draft || r.Prerelease {
		return "", errors.New("invalid stable release")
	}
	want := repository + "/releases/download/" + r.Tag + "/" + name
	for _, a := range r.Assets {
		if a.Name == name && a.URL == want {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("release %s is missing %s", r.Tag, name)
}

type Client struct{ HTTP *http.Client }

func (c Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fade-cli-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == 404 && url == latestURL {
			return nil, ErrNoRelease
		}
		return nil, fmt.Errorf("update request returned HTTP %d; try again later", resp.StatusCode)
	}
	return resp, nil
}

func (c Client) Latest(ctx context.Context) (Release, error) {
	resp, err := c.get(ctx, latestURL)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, err
	}
	name, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Release{}, err
	}
	if _, err = r.asset(name); err != nil {
		return Release{}, err
	}
	if _, err = r.asset("SHA256SUMS"); err != nil {
		return Release{}, err
	}
	return r, nil
}

// CachedLatest keeps startup checks to at most once an hour. Failures remain
// silent at startup; an explicit update/check always calls Latest directly.
func (c Client) CachedLatest(ctx context.Context, dir string) (Release, error) {
	path := filepath.Join(dir, "update.json")
	var cache struct {
		Checked time.Time `json:"checked"`
		Release Release   `json:"release"`
	}
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &cache) == nil {
		age := time.Since(cache.Checked)
		if age >= 0 && age < time.Hour && versionPattern.MatchString(cache.Release.Tag) {
			return cache.Release, nil
		}
	}
	r, err := c.Latest(ctx)
	if err != nil {
		return Release{}, err
	}
	cache.Checked, cache.Release = time.Now(), r
	if os.MkdirAll(dir, 0o700) == nil {
		if f, err := os.CreateTemp(dir, ".update-*"); err == nil {
			tmp := f.Name()
			defer os.Remove(tmp)
			if json.NewEncoder(f).Encode(cache) == nil && f.Close() == nil {
				_ = os.Rename(tmp, path)
			} else {
				_ = f.Close()
			}
		}
	}
	return r, nil
}

// Executable resolves symlinks so a launcher link continues to work after updating.
func Executable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

// Install writes beside the existing executable and verifies the complete
// download before an atomic rename. Errors and cancellation leave it intact.
func (c Client) Install(ctx context.Context, r Release, target string) error {
	name, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	binaryURL, err := r.asset(name)
	if err != nil {
		return err
	}
	sumsURL, err := r.asset("SHA256SUMS")
	if err != nil {
		return err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("the installed executable is not a regular file")
	}
	resp, err := c.get(ctx, sumsURL)
	if err != nil {
		return err
	}
	sums, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	resp.Body.Close()
	if err != nil {
		return err
	}
	if len(sums) > 64<<10 {
		return errors.New("release checksums are too large")
	}
	want, err := checksum(sums, name)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".fade-cli-update-*")
	if err != nil {
		return fmt.Errorf("cannot update %s: %w", target, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	resp, err = c.get(ctx, binaryURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, maxBinary+1))
	if err != nil {
		return err
	}
	if n == 0 || n > maxBinary {
		return errors.New("invalid update size")
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return errors.New("update checksum mismatch; your installed version was not changed")
	}
	if err = f.Chmod(0o755); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(tmp, target); err != nil {
		return fmt.Errorf("replacing %s: %w", target, err)
	}
	return nil
}

func checksum(data []byte, name string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == name {
			b, err := hex.DecodeString(parts[0])
			if err == nil && len(b) == sha256.Size {
				return strings.ToLower(parts[0]), nil
			}
		}
	}
	return "", errors.New("release has no valid checksum for this platform")
}
