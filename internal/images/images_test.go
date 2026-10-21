package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGenerateBase64(t *testing.T) {
	pngData := tinyPNG(t)
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, APIKey: "secret", Model: "test-image"})
	result, err := client.Generate(context.Background(), "a crab icon", Options{Size: "512x512", Quality: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Data, pngData) || result.MIME != "image/png" {
		t.Fatalf("result = %s, %d bytes", result.MIME, len(result.Data))
	}
	if got["model"] != "test-image" || got["prompt"] != "a crab icon" || got["size"] != "512x512" || got["quality"] != "high" {
		t.Fatalf("request = %#v", got)
	}
}

func TestGenerateDownloadsURL(t *testing.T) {
	pngData := tinyPNG(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/generations" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"url": server.URL + "/download"}},
			})
			return
		}
		if r.URL.Path == "/download" {
			_, _ = w.Write(pngData)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL})
	result, err := client.Generate(context.Background(), "texture", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Data, pngData) {
		t.Fatal("downloaded bytes differ")
	}
}

func TestEditSendsMultipart(t *testing.T) {
	pngData := tinyPNG(t)
	var prompt, model, filename string
	var imageBytes []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Error(err)
			http.Error(w, "bad form", 400)
			return
		}
		prompt = r.FormValue("prompt")
		model = r.FormValue("model")
		file, header, err := r.FormFile("image")
		if err != nil {
			t.Error(err)
			http.Error(w, "missing image", 400)
			return
		}
		defer file.Close()
		filename = header.Filename
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(file)
		imageBytes = buf.Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: "edit-model"})
	result, err := client.Edit(context.Background(), "make it blue", pngData, "icon.png", nil, "", Options{Size: "1024x1024"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "make it blue" || model != "edit-model" || filename != "icon.png" {
		t.Fatalf("form = prompt %q model %q file %q", prompt, model, filename)
	}
	if !bytes.Equal(imageBytes, pngData) || len(result.Data) == 0 {
		t.Fatal("image data was not preserved")
	}
}

func TestGenerateReportsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"prompt rejected"}}`))
	}))
	defer server.Close()
	_, err := New(Config{BaseURL: server.URL}).Generate(context.Background(), "x", Options{})
	if err == nil || !strings.Contains(err.Error(), "prompt rejected") {
		t.Fatalf("error = %v", err)
	}
}
