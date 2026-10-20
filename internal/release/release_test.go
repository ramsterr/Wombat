package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "10.0.0", -1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0", "1.0.0", 0},
		{"0.2.0", "0.10.0", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("0.2.0", "darwin", "arm64"); got != "wombat_0.2.0_darwin_arm64.tar.gz" {
		t.Errorf("ArchiveName = %q", got)
	}
	if got := ArchiveName("0.2.0", "windows", "amd64"); got != "wombat_0.2.0_windows_amd64.zip" {
		t.Errorf("ArchiveName = %q", got)
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(sum + "  asset.bin\ndeadbeef  other.bin\n")
	if err := VerifyChecksum(path, manifest); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
	bad := []byte("0000  asset.bin\n")
	if err := VerifyChecksum(path, bad); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestLatestSkipsStableAndPrerelease(t *testing.T) {
	body := `[
	  {"tag_name":"stable","prerelease":false,"assets":[]},
	  {"tag_name":"v0.3.0-rc1","prerelease":true,"assets":[]},
	  {"tag_name":"v0.2.0","prerelease":false,"assets":[
	    {"name":"wombat_0.2.0_darwin_arm64.tar.gz","browser_download_url":"https://example.com/a.tar.gz","size":10},
	    {"name":"checksums.txt","browser_download_url":"https://example.com/checksums.txt","size":5}]},
	  {"tag_name":"v0.1.0","prerelease":false,"assets":[]}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/ramsterr/Wombat/releases" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	rel, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version() != "0.2.0" {
		t.Fatalf("Latest = %q, want 0.2.0", rel.Version())
	}
	asset, ok := rel.Asset("darwin", "arm64")
	if !ok || asset.Name != "wombat_0.2.0_darwin_arm64.tar.gz" {
		t.Fatalf("asset = %+v ok=%v", asset, ok)
	}
	if _, ok := rel.Checksums(); !ok {
		t.Fatal("checksums asset missing")
	}
}

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path, err := Download(context.Background(), Asset{Name: "a.txt", URL: srv.URL + "/a.txt"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "payload" {
		t.Fatalf("data = %q err = %v", data, err)
	}
}
