package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchSerperImagesParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "k" {
			t.Error("missing api key")
		}
		fmt.Fprint(w, `{"images":[
			{"title":"Wombat photo","imageUrl":"https://cdn.example/wombat.jpg","imageWidth":1600,"imageHeight":900,"thumbnailUrl":"https://cdn.example/thumb.jpg","link":"https://example.com/wombat","source":"Example"},
			{"title":"Second","imageUrl":"https://cdn.example/two.png","imageWidth":800,"imageHeight":800,"link":"https://example.com/two"}
		]}`)
	}))
	defer srv.Close()
	old := serperImagesURL
	serperImagesURL = srv.URL
	defer func() { serperImagesURL = old }()
	got, err := searchSerperImages(context.Background(), "k", "wombat", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ImageURL != "https://cdn.example/wombat.jpg" || got[0].Width != 1600 {
		t.Fatalf("results = %+v", got)
	}
}

func TestRankImageResultsFiltersAdultAndBoostsTrusted(t *testing.T) {
	batches := []imageBatch{{engine: "test", results: []ImageResult{
		{Title: "scikit-learn logo", PageURL: "https://scikit-learn.org/stable/", ImageURL: "https://scikit-learn.org/logo.png", Width: 512, Height: 512},
		{Title: "naked porn image", PageURL: "https://pornhub.com/x", ImageURL: "https://pornhub.com/x.jpg", Width: 800, Height: 600},
		{Title: "random image", PageURL: "https://random.example/a", ImageURL: "https://random.example/a.jpg", Width: 400, Height: 300},
	}}}
	got := rankImageResults("scikit-learn logo", batches, 8, 0, 0, "all")
	if len(got) == 0 {
		t.Fatal("no results")
	}
	if got[0].ImageURL != "https://scikit-learn.org/logo.png" {
		t.Fatalf("top image = %+v", got[0])
	}
	for _, r := range got {
		if strings.Contains(r.ImageURL, "pornhub") {
			t.Fatalf("adult image survived: %+v", r)
		}
	}
}

func TestRankImageResultsOrientation(t *testing.T) {
	batches := []imageBatch{{results: []ImageResult{
		{Title: "wide", PageURL: "https://a.example", ImageURL: "https://a.example/wide.jpg", Width: 1200, Height: 600},
		{Title: "tall", PageURL: "https://b.example", ImageURL: "https://b.example/tall.jpg", Width: 600, Height: 1200},
	}}}
	got := rankImageResults("image", batches, 8, 0, 0, "portrait")
	if len(got) != 1 || got[0].Title != "tall" {
		t.Fatalf("portrait results = %+v", got)
	}
}

func TestDownloadImage(t *testing.T) {
	// 1x1 transparent PNG
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer srv.Close()
	data, ext, err := downloadImage(context.Background(), srv.URL+"/x.png")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || ext != ".png" {
		t.Fatalf("download = %d bytes, ext %q", len(data), ext)
	}
}

func TestImageSlug(t *testing.T) {
	if got := imageSlug("Wombat: cute & fluffy / 2026"); got != "wombat-cute-fluffy-2026" {
		t.Fatalf("slug = %q", got)
	}
}
