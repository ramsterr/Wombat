package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type google struct {
	name    string
	base    string
	key     string
	http    *http.Client
	prepare func(*http.Request)
}

func newGoogle(name, base, key string) *google {
	return &google{
		name: name,
		base: strings.TrimRight(base, "/"),
		key:  key,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (g *google) Name() string { return g.name }

func isGoogleThinkingModel(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "gemini-2.5") || strings.Contains(m, "gemini-3") ||
		strings.Contains(m, "thinking")
}

func (g *google) mapContents(msgs []Message) ([]map[string]any, error) {
	out := []map[string]any{}
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case "user":
			parts, err := googleUserParts(m)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"role":  "user",
				"parts": parts,
			})
			i++
		case "assistant":
			parts := []map[string]any{}
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args any = map[string]any{}
				if err := json.Unmarshal([]byte(normalizeArgs(tc.Arguments)), &args); err != nil {
					args = map[string]any{}
				}
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{"name": tc.Name, "args": args},
				})
			}
			out = append(out, map[string]any{"role": "model", "parts": parts})
			i++
		case "tool":
			parts := []map[string]any{}
			for i < len(msgs) && msgs[i].Role == "tool" {
				tm := msgs[i]
				parts = append(parts, map[string]any{
					"functionResponse": map[string]any{
						"name":     tm.Name,
						"response": map[string]any{"output": tm.Content},
					},
				})
				i++
			}
			out = append(out, map[string]any{"role": "user", "parts": parts})
		default:
			i++
		}
	}
	return out, nil
}

func googleUserParts(m Message) ([]map[string]any, error) {
	text := m.Content
	var parts []map[string]any
	var inlineBytes int64
	for _, a := range m.Attachments {
		if a.Kind == "file" && IsTextMIME(a.MIME) {
			content, ok, err := TextAttachment(a)
			if err != nil {
				return nil, err
			}
			if ok {
				text = AppendFileText(text, a, content)
				continue
			}
		}
		inlineBytes += a.Size
		if inlineBytes > 14<<20 {
			return nil, fmt.Errorf("gemini inline attachment limit is about 14 MB; split or shrink the files")
		}
		data, err := LoadAttachment(a)
		if err != nil {
			return nil, err
		}
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": a.MIME,
				"data":      base64.StdEncoding.EncodeToString(data),
			},
		})
	}
	if text != "" || len(parts) == 0 {
		parts = append([]map[string]any{{"text": text}}, parts...)
	}
	return parts, nil
}

type googleChunk struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text         string `json:"text"`
				Thought      bool   `json:"thought"`
				FunctionCall *struct {
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (g *google) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	contents, err := g.mapContents(req.Messages)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"contents": contents,
	}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": req.System}},
		}
	}
	if len(req.Tools) > 0 {
		decls := []map[string]any{}
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Schema,
			})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	genCfg := map[string]any{}
	if req.MaxTokens > 0 {
		genCfg["maxOutputTokens"] = req.MaxTokens
	}
	if isGoogleThinkingModel(req.Model) {
		genCfg["thinkingConfig"] = map[string]any{"includeThoughts": true}
	}
	if len(genCfg) > 0 {
		body["generationConfig"] = genCfg
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := g.base + "/v1beta/models/" + req.Model + ":streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.key != "" {
		httpReq.Header.Set("x-goog-api-key", g.key)
	}
	if g.prepare != nil {
		g.prepare(httpReq)
	}
	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(g.name, resp)
	}

	result := &Result{}
	n := 0
	err = readSSE(resp.Body, func(_, data string) error {
		var c googleChunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return nil
		}
		if c.Error != nil {
			return fmt.Errorf("%s: %s", g.name, c.Error.Message)
		}
		if c.UsageMetadata != nil {
			result.Usage.In = c.UsageMetadata.PromptTokenCount
			result.Usage.Out = c.UsageMetadata.CandidatesTokenCount
			result.Usage.Cached = c.UsageMetadata.CachedContentTokenCount
		}
		for _, cand := range c.Candidates {
			if cand.FinishReason != "" {
				result.StopReason = strings.ToLower(cand.FinishReason)
			}
			for _, p := range cand.Content.Parts {
				if p.Text != "" {
					if p.Thought {
						if req.OnReasoning != nil {
							req.OnReasoning(p.Text)
						}
					} else {
						result.Content += p.Text
						if onDelta != nil {
							onDelta(p.Text)
						}
					}
				}
				if p.FunctionCall != nil {
					args := "{}"
					if len(p.FunctionCall.Args) > 0 {
						args = normalizeArgs(string(p.FunctionCall.Args))
					}
					result.ToolCalls = append(result.ToolCalls, ToolCall{
						ID:        fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), n),
						Name:      p.FunctionCall.Name,
						Arguments: args,
					})
					n++
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (g *google) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+"/v1beta/models", nil)
	if err != nil {
		return nil, err
	}
	if g.key != "" {
		req.Header.Set("x-goog-api-key", g.key)
	}
	if g.prepare != nil {
		g.prepare(req)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(g.name, resp)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, strings.TrimPrefix(m.Name, "models/"))
	}
	sort.Strings(models)
	return models, nil
}
