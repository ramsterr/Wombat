package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/images"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageGenerateSavesAndAttaches(t *testing.T) {
	pngData := testPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()
	dir := t.TempDir()
	env := &Env{
		Dir:    dir,
		Permit: func(string, string) Decision { return Decision{Allow: true} },
		Images: images.New(images.Config{BaseURL: server.URL, APIKey: "test"}),
	}
	var media []Media
	env.SetMediaSink(&media)
	out := run(t, &ImageGen{}, env, `{"action":"generate","prompt":"a crab icon","path":"assets/crab"}`)
	if !strings.Contains(out, "generated image") || !strings.Contains(out, "attached") {
		t.Fatalf("output = %q", out)
	}
	path := filepath.Join(dir, "assets", "crab.png")
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, pngData) {
		t.Fatalf("image = %d bytes, %v", len(data), err)
	}
	if len(media) != 1 || media[0].Path != path || media[0].MIME != "image/png" {
		t.Fatalf("media = %+v", media)
	}
}

func TestImageEditSendsInputAndMask(t *testing.T) {
	pngData := testPNG(t)
	var imageSeen, maskSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Error(err)
			http.Error(w, "bad form", 400)
			return
		}
		imageSeen = r.FormValue("prompt") == "make it blue"
		_, _, err := r.FormFile("image")
		if err != nil {
			t.Error(err)
		}
		_, _, err = r.FormFile("mask")
		maskSeen = err == nil
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.png"), pngData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mask.png"), pngData, 0o644); err != nil {
		t.Fatal(err)
	}
	env := &Env{
		Dir:    dir,
		Permit: func(string, string) Decision { return Decision{Allow: true} },
		Images: images.New(images.Config{BaseURL: server.URL}),
	}
	out := run(t, &ImageGen{}, env, `{"action":"edit","prompt":"make it blue","image":"input.png","mask":"mask.png","path":"out.png"}`)
	if !strings.Contains(out, "edited image") {
		t.Fatalf("output = %q", out)
	}
	if !imageSeen || !maskSeen {
		t.Fatalf("multipart fields: image=%v mask=%v", imageSeen, maskSeen)
	}
}

func TestImageRequiresConfiguration(t *testing.T) {
	env := testEnv(t.TempDir())
	err := runErr(t, &ImageGen{}, env, `{"prompt":"x","path":"x.png"}`)
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v", err)
	}
}
