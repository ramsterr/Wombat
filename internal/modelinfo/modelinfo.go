// Package modelinfo resolves model capabilities from the public models.dev
// catalogue, so modality, context, and reasoning support do not have to be
// hard-coded in Wombat.
package modelinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SourceURL is the machine-readable model catalogue published by models.dev,
// the same data OpenCode and other harnesses use for model metadata.
const SourceURL = "https://models.dev/api.json"

// maxAge is how long a cached catalogue stays fresh before a refresh is
// attempted. A stale cache is still used when the network is unavailable.
const maxAge = 24 * time.Hour

// maxDownload bounds the catalogue download.
const maxDownload = 32 << 20

// Info describes what a provider says a model can do. Zero values mean the
// catalogue did not say; callers fall back to their built-in assumptions.
type Info struct {
	Context   int      // context window in tokens
	Output    int      // maximum output tokens
	Input     []string // input modalities: text, image, video, audio, pdf
	Vision    bool
	Reasoning bool
	ToolCall  bool
	Protocol  string // openai, messages, responses, or google
	API       string // the provider endpoint from the catalogue
}

// HasModality reports whether the model accepts the given input modality.
func (i Info) HasModality(kind string) bool {
	for _, in := range i.Input {
		if strings.EqualFold(strings.TrimSpace(in), kind) {
			return true
		}
	}
	return false
}

var (
	mu      sync.RWMutex
	catalog map[string]map[string]Info

	loadOnce sync.Once

	client    = &http.Client{Timeout: 15 * time.Second}
	sourceURL = SourceURL
	cacheFile = defaultCacheFile()
)

func defaultCacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".wombat")
	}
	return filepath.Join(dir, "wombat", "models-dev.json")
}

// Load reads the cached catalogue from disk. It never touches the network:
// callers that can wait use Ensure instead. A missing or unreadable cache
// simply leaves the catalogue empty.
func Load() {
	loadOnce.Do(func() {
		data, err := os.ReadFile(cacheFile)
		if err != nil {
			return
		}
		c, err := parse(data)
		if err != nil {
			return
		}
		set(c)
	})
}

// Ensure loads the cache and refreshes it when it is missing or older than a
// day. The refresh is bounded by ctx so a slow or offline network cannot
// block startup: a stale cache and the built-in model families are used
// instead.
func Ensure(ctx context.Context) {
	Load()
	if fresh() {
		return
	}
	Refresh(ctx)
}

// Refresh downloads the catalogue and replaces the in-memory copy and the
// on-disk cache. Errors are deliberately ignored by callers: capability
// detection degrades to the built-in heuristics when the catalogue is not
// available.
func Refresh(ctx context.Context) {
	data, err := fetch(ctx)
	if err != nil {
		return
	}
	c, err := parse(data)
	if err != nil {
		return
	}
	set(c)
	writeCache(data)
}

// Lookup returns the catalogue entry for provider/model. The second result
// is false when the catalogue has no entry for the pair.
func Lookup(provider, model string) (Info, bool) {
	mu.RLock()
	loaded := catalog != nil
	mu.RUnlock()
	if !loaded {
		Load()
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	if p == "" || m == "" {
		return Info{}, false
	}
	mu.RLock()
	defer mu.RUnlock()
	info, ok := catalog[p][m]
	return info, ok
}

func fresh() bool {
	info, err := os.Stat(cacheFile)
	return err == nil && time.Since(info.ModTime()) < maxAge
}

func fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "wombat/modelinfo")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

func writeCache(data []byte) {
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o755); err != nil {
		return
	}
	tmp := cacheFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, cacheFile)
}

func set(c map[string]map[string]Info) {
	mu.Lock()
	catalog = c
	mu.Unlock()
}

type rawCatalog map[string]rawProvider

type rawProvider struct {
	Models map[string]rawModel `json:"models"`
}

type rawModel struct {
	ID         string `json:"id"`
	Attachment bool   `json:"attachment"`
	Reasoning  bool   `json:"reasoning"`
	ToolCall   bool   `json:"tool_call"`
	Modalities struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Provider struct {
		API string `json:"api"`
	} `json:"provider"`
}

func parse(data []byte) (map[string]map[string]Info, error) {
	var raw rawCatalog
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]Info, len(raw))
	for provider, p := range raw {
		models := make(map[string]Info, len(p.Models))
		for key, m := range p.Models {
			info := infoOf(m)
			if k := strings.ToLower(strings.TrimSpace(key)); k != "" {
				models[k] = info
			}
			if id := strings.ToLower(strings.TrimSpace(m.ID)); id != "" {
				models[id] = info
			}
		}
		if p := strings.ToLower(strings.TrimSpace(provider)); p != "" {
			out[p] = models
		}
	}
	return out, nil
}

func infoOf(m rawModel) Info {
	input := make([]string, 0, len(m.Modalities.Input))
	for _, in := range m.Modalities.Input {
		if in = strings.ToLower(strings.TrimSpace(in)); in != "" {
			input = append(input, in)
		}
	}
	return Info{
		Context:   m.Limit.Context,
		Output:    m.Limit.Output,
		Input:     input,
		Vision:    seesImages(input, m.Attachment),
		Reasoning: m.Reasoning,
		ToolCall:  m.ToolCall,
		Protocol:  protocolOf(m.Provider.API),
		API:       m.Provider.API,
	}
}

func seesImages(input []string, attachment bool) bool {
	if len(input) == 0 {
		return attachment
	}
	for _, in := range input {
		if in == "image" {
			return true
		}
	}
	return false
}

// protocolOf maps a catalogue endpoint to the wire protocol Wombat talks to
// that provider with.
func protocolOf(api string) string {
	a := strings.ToLower(api)
	switch {
	case strings.Contains(a, "/anthropic"):
		return "messages"
	case strings.Contains(a, "/google"):
		return "google"
	case strings.Contains(a, "/responses"):
		return "responses"
	case strings.Contains(a, "/openai"):
		return "openai"
	}
	return ""
}
