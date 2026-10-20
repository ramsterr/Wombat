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

	"github.com/ramsterr/Wombat/internal/release"
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
