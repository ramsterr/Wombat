package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/release"
	"github.com/ramsterr/Wombat/internal/session"
)

func TestUpgradeBinaryReplacesTarget(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new-wombat\n")
	archive := tarGz(t, "wombat", payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.tar.gz":
			_, _ = w.Write(archive)
		case "/checksums.txt":
			sum := sha256.Sum256(archive)
			fmt.Fprintf(w, "%s  wombat_1.2.3_darwin_arm64.tar.gz\n", hex.EncodeToString(sum[:]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := release.Release{Tag: "v1.2.3"}
	asset := release.Asset{Name: "wombat_1.2.3_darwin_arm64.tar.gz", URL: srv.URL + "/asset.tar.gz"}
	sumAsset := release.Asset{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"}

	dir := t.TempDir()
	target := filepath.Join(dir, "wombat")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := upgradeBinary(context.Background(), rel, asset, sumAsset, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("target = %q, want %q", got, payload)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("target mode = %v, want executable", info.Mode().Perm())
	}
}

func TestUpgradeBinaryRejectsBadChecksum(t *testing.T) {
	archive := tarGz(t, "wombat", []byte("payload"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.tar.gz":
			_, _ = w.Write(archive)
		case "/checksums.txt":
			_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  wombat_1.2.3_darwin_arm64.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := release.Release{Tag: "v1.2.3"}
	asset := release.Asset{Name: "wombat_1.2.3_darwin_arm64.tar.gz", URL: srv.URL + "/asset.tar.gz"}
	sumAsset := release.Asset{Name: "checksums.txt", URL: srv.URL + "/checksums.txt"}

	dir := t.TempDir()
	target := filepath.Join(dir, "wombat")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := upgradeBinary(context.Background(), rel, asset, sumAsset, target)
	if err == nil {
		t.Fatal("expected checksum failure")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old" {
		t.Fatalf("target was modified: %q", got)
	}
}

func TestDetectInstallMethod(t *testing.T) {
	if got := detectInstallMethod(); got != "script" && got != "go" && got != "brew" {
		t.Fatalf("detectInstallMethod = %q", got)
	}
}

func TestInstallMethodFor(t *testing.T) {
	cases := map[string]string{
		"/opt/homebrew/Cellar/wombat/0.2.5/bin/wombat": "brew",
		"/home/linuxbrew/bin/wombat":                   "brew",
		"/usr/local/go/bin/wombat":                     "go",
		"/home/user/go/bin/wombat":                     "go",
		"/home/user/.local/bin/wombat":                 "script",
		"/usr/local/bin/wombat":                        "script",
	}
	for path, want := range cases {
		if got := installMethodFor(path); got != want {
			t.Errorf("installMethodFor(%q) = %q, want %q", path, got, want)
		}
	}
}

func tarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := data
	hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildAgentUsesSandboxOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cfg := &config.Config{
		Provider:  "openai",
		Model:     "test-model",
		Mode:      "create",
		Providers: map[string]*config.Provider{"openai": {Kind: "openai", BaseURL: "http://127.0.0.1:1/v1"}},
	}
	sess, err := session.CreateAt(home, t.TempDir(), "openai", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	opts := runOpts{}
	if _, err := buildAgent(cfg, opts, sess, newPrompter(false, false, cfg)); err != nil {
		t.Fatal(err)
	}
}
