package modelinfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const fixture = `{
  "opencode-go": {
    "models": {
      "deepseek-v4.1-flash": {
        "id": "deepseek-v4.1-flash",
        "reasoning": true,
        "tool_call": true,
        "modalities": {"input": ["text", "image"], "output": ["text"]},
        "limit": {"context": 1000000, "output": 384000},
        "provider": {"api": "https://opencode.ai/inference/go/openai/v1"}
      },
      "deepseek-v4-pro": {
        "id": "deepseek-v4-pro",
        "reasoning": true,
        "tool_call": true,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 1000000, "output": 384000},
        "provider": {"api": "https://opencode.ai/inference/go/openai/v1"}
      },
      "claude-haiku-5-5": {
        "id": "claude-haiku-5-5",
        "reasoning": true,
        "tool_call": true,
        "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]},
        "limit": {"context": 200000, "output": 64000},
        "provider": {"api": "https://opencode.ai/inference/go/anthropic/v1"}
      }
    }
  },
  "local": {
    "models": {
      "my-model": {
        "id": "custom-alias",
        "attachment": true
      }
    }
  }
}`

func TestParseAndLookup(t *testing.T) {
	c, err := parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	set(c)
	t.Cleanup(func() { set(nil) })

	tests := []struct {
		provider string
		model    string
		vision   bool
		known    bool
	}{
		{"opencode-go", "deepseek-v4.1-flash", true, true},
		{"opencode-go", "DeepSeek-V4.1-Flash", true, true},
		{"opencode-go", "deepseek-v4-pro", false, true},
		{"opencode-go", "claude-haiku-5-5", true, true},
		{"local", "my-model", true, true},
		{"local", "custom-alias", true, true},
		{"opencode-go", "unknown", false, false},
		{"unknown-provider", "deepseek-v4.1-flash", false, false},
		{"", "deepseek-v4.1-flash", false, false},
	}
	for _, tt := range tests {
		info, ok := Lookup(tt.provider, tt.model)
		if ok != tt.known || info.Vision != tt.vision {
			t.Errorf("Lookup(%q, %q) = %+v, %v; want vision %v, %v", tt.provider, tt.model, info, ok, tt.vision, tt.known)
		}
	}

	info, _ := Lookup("opencode-go", "deepseek-v4.1-flash")
	if info.Context != 1000000 || !info.Reasoning || !info.ToolCall || info.Protocol != "openai" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if !info.HasModality("image") || info.HasModality("video") {
		t.Fatalf("unexpected modalities: %+v", info.Input)
	}
	info, _ = Lookup("opencode-go", "claude-haiku-5-5")
	if info.Protocol != "messages" {
		t.Fatalf("protocol = %q, want messages", info.Protocol)
	}
}

func TestSeesImagesFallsBackToAttachment(t *testing.T) {
	if !seesImages(nil, true) {
		t.Fatal("attachment without modalities should count as image capable")
	}
	if seesImages([]string{"text"}, true) {
		t.Fatal("text-only modalities must win over attachment")
	}
}

func TestRefreshFetchesAndCaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	oldURL, oldClient, oldCache := sourceURL, client, cacheFile
	sourceURL = srv.URL + "/api.json"
	client = srv.Client()
	cacheFile = filepath.Join(t.TempDir(), "models-dev.json")
	set(nil)
	t.Cleanup(func() {
		sourceURL, client, cacheFile = oldURL, oldClient, oldCache
		set(nil)
	})

	Ensure(context.Background())

	if info, ok := Lookup("opencode-go", "deepseek-v4.1-flash"); !ok || !info.Vision {
		t.Fatalf("catalogue not refreshed: %+v, %v", info, ok)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
}

func TestRefreshKeepsStaleCatalogueOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "offline", http.StatusBadGateway)
	}))
	defer srv.Close()

	c, err := parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	set(c)
	t.Cleanup(func() { set(nil) })

	oldURL, oldClient, oldCache := sourceURL, client, cacheFile
	sourceURL = srv.URL + "/api.json"
	client = srv.Client()
	cacheFile = filepath.Join(t.TempDir(), "missing.json")
	t.Cleanup(func() {
		sourceURL, client, cacheFile = oldURL, oldClient, oldCache
		set(nil)
	})

	Ensure(context.Background())

	if info, ok := Lookup("opencode-go", "deepseek-v4.1-flash"); !ok || !info.Vision {
		t.Fatalf("stale catalogue was lost: %+v, %v", info, ok)
	}
	if _, err := os.Stat(cacheFile); err == nil {
		t.Fatal("a failed refresh must not write a cache")
	}
}
