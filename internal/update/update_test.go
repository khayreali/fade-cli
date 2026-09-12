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
	"runtime"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture() Release {
	name, _ := AssetName(runtime.GOOS, runtime.GOARCH)
	r := Release{Tag: "v0.3.0"}
	for _, name := range []string{name, "SHA256SUMS"} {
		r.Assets = append(r.Assets, Asset{Name: name, URL: repository + "/releases/download/" + r.Tag + "/" + name})
	}
	return r
}

func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func downloadClient(r Release, binary, checksumBinary string) Client {
	return Client{HTTP: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		switch req.URL.String() {
		case latestURL:
			b, _ := json.Marshal(r)
			return reply(string(b)), nil
		case r.Assets[0].URL:
			return reply(binary), nil
		case r.Assets[1].URL:
			h := sha256.Sum256([]byte(checksumBinary))
			return reply(hex.EncodeToString(h[:]) + "  " + r.Assets[0].Name + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected URL %s", req.URL)
		}
	})}}
}

func TestStableVersionsAreComparedNumerically(t *testing.T) {
	for _, tc := range []struct {
		tag, current string
		want         bool
	}{
		{"v0.3.0", "0.2.0", true}, {"v0.10.0", "0.9.0", true}, {"v1.0.0", "0.99.0", true},
		{"v0.3.0", "0.3.0", false}, {"v0.2.0", "0.3.0", false}, {"v0.4.0-rc1", "0.3.0", false},
		{"garbage", "0.3.0", false}, {"v0.3.0", "dev", false}, {"v00.3.0", "0.2.0", false},
	} {
		if got := (Release{Tag: tc.tag}).Newer(tc.current); got != tc.want {
			t.Errorf("%s > %s = %v", tc.tag, tc.current, got)
		}
	}
}

func TestLatestRequiresStableCompleteTrustedAssets(t *testing.T) {
	for _, change := range []func(*Release){
		func(r *Release) { r.Draft = true }, func(r *Release) { r.Prerelease = true },
		func(r *Release) { r.Assets = r.Assets[:1] }, func(r *Release) { r.Assets[0].URL = "https://example.com/other-binary" },
		func(r *Release) { r.Tag = "v0.3.0/../../other" },
	} {
		r := fixture()
		change(&r)
		c := downloadClient(r, "new", "new")
		if _, err := c.Latest(context.Background()); err == nil {
			t.Fatalf("invalid release accepted: %+v", r)
		}
	}
}

func TestInstallVerifiesThenReplacesAndPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fade-cli")
	link := filepath.Join(dir, "launcher")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	c := downloadClient(fixture(), "new binary", "new binary")
	if err := c.Install(context.Background(), fixture(), link); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "new binary" {
		t.Fatalf("got %q", b)
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatal("launcher symlink replaced")
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0755 {
		t.Fatalf("mode %v", info.Mode())
	}
	assertNoTemp(t, dir)
}

func TestFailedOrCanceledInstallLeavesOriginalUntouched(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "fade-cli")
			_ = os.WriteFile(target, []byte("old"), 0755)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			c := downloadClient(fixture(), "corrupt", "expected")
			if err := c.Install(ctx, fixture(), target); err == nil {
				t.Fatal("invalid download installed")
			}
			b, _ := os.ReadFile(target)
			if string(b) != "old" {
				t.Fatal("original was changed")
			}
			assertNoTemp(t, dir)
		})
	}
}

func TestChecksumMatchesExactAssetName(t *testing.T) {
	h := sha256.Sum256([]byte("x"))
	sum := hex.EncodeToString(h[:])
	if _, err := checksum([]byte(sum+"  fade-cli_linux_arm64"), "fade-cli_linux_amd64"); err == nil {
		t.Fatal("wrong platform checksum accepted")
	}
	if got, err := checksum([]byte(sum+" *fade-cli_linux_arm64"), "fade-cli_linux_arm64"); err != nil || got != sum {
		t.Fatalf("binary-format checksum: %s %v", got, err)
	}
}

type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelOnRead) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestCancellationDuringDownloadNeverReplacesExecutable(t *testing.T) {
	r := fixture()
	c := downloadClient(r, "new", "new")
	base := c.HTTP.Transport
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(req)
		if err == nil && req.URL.String() == r.Assets[0].URL {
			resp.Body = io.NopCloser(cancelOnRead{Reader: resp.Body, cancel: cancel})
		}
		return resp, err
	})
	dir := t.TempDir()
	target := filepath.Join(dir, "fade-cli")
	_ = os.WriteFile(target, []byte("old"), 0755)
	if err := c.Install(ctx, r, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old" {
		t.Fatal("cancellation replaced original")
	}
	assertNoTemp(t, dir)
}

func TestCacheAvoidsRepeatedNetworkChecksButExplicitLatestDoesNot(t *testing.T) {
	r := fixture()
	calls := 0
	b, _ := json.Marshal(r)
	c := Client{HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; return reply(string(b)), nil })}}
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := c.CachedLatest(context.Background(), dir); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("cache made %d requests", calls)
	}
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("explicit check didn't refresh")
	}
}

func TestNoPublishedReleaseAndOfflineAreNotUpToDate(t *testing.T) {
	c := Client{HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	if _, err := c.Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Fatal(err)
	}
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	if _, err := c.CachedLatest(context.Background(), t.TempDir()); err == nil {
		t.Fatal("network error suppressed by updater")
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, ".fade-cli-update-*"))
	if len(paths) != 0 {
		t.Fatalf("temporary binaries leaked: %v", paths)
	}
}
