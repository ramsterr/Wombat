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

type anthropic struct {
	name    string
	base    string
	key     string
	http    *http.Client
	prepare func(*http.Request)
}

func newAnthropic(name, base, key string) *anthropic {
	return &anthropic{
		name: name,
		base: strings.TrimRight(base, "/"),
		key:  key,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (a *anthropic) Name() string { return a.name }

func (a *anthropic) mapMessages(req Request) ([]map[string]any, error) {
	out := []map[string]any{}
	i := 0
	for i < len(req.Messages) {
		m := req.Messages[i]
		switch m.Role {
		case "user":
			blocks, err := anthropicUserBlocks(m)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{
				"role":    "user",
				"content": blocks,
			})
			i++
		case "assistant":
			blocks := []map[string]any{}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": json.RawMessage(normalizeArgs(tc.Arguments)),
				})
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
			i++
		case "tool":
			results := []map[string]any{}
			for i < len(req.Messages) && req.Messages[i].Role == "tool" {
				tm := req.Messages[i]
				results = append(results, map[string]any{
					"type":        "tool_result",
					"tool_use_id": tm.ToolCallID,
					"content":     tm.Content,
				})
				i++
			}
			out = append(out, map[string]any{"role": "user", "content": results})
		default:
			i++
		}
	}
	return out, nil
}

func anthropicUserBlocks(m Message) ([]map[string]any, error) {
	text := m.Content
	var blocks []map[string]any
	for _, a := range m.Attachments {
		switch a.Kind {
		case "image":
			if !IsImageMIME(a.MIME) {
				return nil, fmt.Errorf("claude: unsupported image type %s (%s)", a.MIME, a.Name)
			}
			data, err := LoadAttachment(a)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": a.MIME,
					"data":       base64.StdEncoding.EncodeToString(data),
				},
			})
		case "audio", "video":
			return nil, fmt.Errorf("claude does not support %s attachments (%s); use a Gemini model", a.Kind, a.Name)
		default:
			content, ok, err := TextAttachment(a)
			if err != nil {
				return nil, err
			}
			if ok {
				text = AppendFileText(text, a, content)
				continue
			}
			if strings.EqualFold(a.MIME, "application/pdf") {
				data, err := LoadAttachment(a)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, map[string]any{
					"type": "document",
					"source": map[string]any{
						"type":       "base64",
						"media_type": "application/pdf",
						"data":       base64.StdEncoding.EncodeToString(data),
					},
				})
				continue
			}
			return nil, fmt.Errorf("claude does not support %s (%s)", a.MIME, a.Name)
		}
	}
	if text != "" || len(blocks) == 0 {
		blocks = append([]map[string]any{{"type": "text", "text": text}}, blocks...)
	}
	return blocks, nil
}

func normalizeArgs(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !json.Valid([]byte(s)) {
		return "{}"
	}
	return s
}

type antEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Message struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (a *anthropic) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	msgs, err := a.mapMessages(req)
	if err != nil {
		return nil, err
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"messages":   msgs,
		"stream":     true,
	}
	if req.System != "" {
		body["system"] = []map[string]any{{
			"type":          "text",
			"text":          req.System,
			"cache_control": map[string]string{"type": "ephemeral"},
		}}
	}
	if len(req.Tools) > 0 {
		tools := []map[string]any{}
		for i, t := range req.Tools {
			tool := map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Schema,
			}
			if i == len(req.Tools)-1 {
				tool["cache_control"] = map[string]string{"type": "ephemeral"}
			}
			tools = append(tools, tool)
		}
		body["tools"] = tools
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if a.prepare != nil {
		a.prepare(httpReq)
	}
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(a.name, resp)
	}

	result := &Result{}
	calls := map[int]*ToolCall{}
	var order []int
	err = readSSE(resp.Body, func(event, data string) error {
		var e antEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return nil
		}
		if e.Type == "error" && e.Error != nil {
			return fmt.Errorf("%s: %s", a.name, e.Error.Message)
		}
		switch e.Type {
		case "message_start":
			u := e.Message.Usage
			result.Usage.In = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			result.Usage.Cached = u.CacheReadInputTokens
		case "content_block_start":
			if e.ContentBlock.Type == "tool_use" {
				calls[e.Index] = &ToolCall{ID: e.ContentBlock.ID, Name: e.ContentBlock.Name}
				order = append(order, e.Index)
			}
		case "content_block_delta":
			if e.Delta.Type == "thinking_delta" && e.Delta.Thinking != "" {
				result.Reasoning += e.Delta.Thinking
				if req.OnReasoning != nil {
					req.OnReasoning(e.Delta.Thinking)
				}
			}
			if e.Delta.Type == "text_delta" && e.Delta.Text != "" {
				result.Content += e.Delta.Text
				if onDelta != nil {
					onDelta(e.Delta.Text)
				}
			}
			if e.Delta.Type == "input_json_delta" {
				if tc, ok := calls[e.Index]; ok {
					tc.Arguments += e.Delta.PartialJSON
				}
			}
		case "message_delta":
			if e.Usage.OutputTokens > 0 {
				result.Usage.Out = e.Usage.OutputTokens
			}
			if e.Delta.StopReason != "" {
				result.StopReason = e.Delta.StopReason
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Ints(order)
	for i, idx := range order {
		tc := calls[idx]
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
		}
		if tc.Arguments == "" {
			tc.Arguments = "{}"
		}
		result.ToolCalls = append(result.ToolCalls, *tc)
	}
	return result, nil
}

func (a *anthropic) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.prepare != nil {
		a.prepare(req)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(a.name, resp)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	return models, nil
}
