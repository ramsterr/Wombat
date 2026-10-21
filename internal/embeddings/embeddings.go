// Package embeddings turns text into vectors for semantic code search. It
// speaks the OpenAI-compatible /embeddings API, so any provider that exposes
// it (OpenAI, Ollama, LM Studio, vLLM, ...) works without a separate SDK.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBatch keeps requests small enough for local servers and provider limits.
const maxBatch = 32

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

type Client struct {
	base  string
	key   string
	model string
	http  *http.Client
}

func New(cfg Config) *Client {
	httpClient := cfg.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	return &Client{
		base:  strings.TrimRight(cfg.BaseURL, "/"),
		key:   cfg.APIKey,
		model: cfg.Model,
		http:  httpClient,
	}
}

// Model is the embedding model, used in user-facing messages.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// Signature identifies the model and endpoint, so vectors from a different
// embedding space invalidate the cached index.
func (c *Client) Signature() string {
	if c == nil {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(c.base + "\x00" + c.model))
	return c.model + "@" + strconv.FormatUint(h.Sum64(), 16)
}

// Embed returns one vector per input, preserving order. Inputs are batched.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c == nil {
		return nil, fmt.Errorf("no embedding client configured")
	}
	if c.model == "" {
		return nil, fmt.Errorf("no embedding model configured")
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += maxBatch {
		end := start + maxBatch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := c.batch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (c *Client) batch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": c.model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, fmt.Errorf("embeddings: %s: %s", resp.Status, msg)
	}
	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("embeddings: decode response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	vecs := make([][]float32, len(texts))
	ordered := true
	for i, item := range parsed.Data {
		if item.Index != i {
			ordered = false
			break
		}
	}
	dim := 0
	for i, item := range parsed.Data {
		slot := i
		if !ordered {
			slot = item.Index
		}
		if slot < 0 || slot >= len(vecs) {
			return nil, fmt.Errorf("embeddings: response index %d is outside the batch", item.Index)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings: empty vector returned")
		}
		if dim == 0 {
			dim = len(item.Embedding)
		} else if len(item.Embedding) != dim {
			return nil, fmt.Errorf("embeddings: inconsistent vector dimensions")
		}
		vecs[slot] = item.Embedding
	}
	for _, vec := range vecs {
		if len(vec) == 0 {
			return nil, fmt.Errorf("embeddings: response is missing a vector")
		}
	}
	return vecs, nil
}
