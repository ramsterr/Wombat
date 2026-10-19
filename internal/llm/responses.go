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

type responses struct {
	name    string
	base    string
	key     string
	http    *http.Client
	prepare func(*http.Request)
}

func newResponses(name, base, key string) *responses {
	return &responses{
		name: name,
		base: strings.TrimRight(base, "/"),
		key:  key,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (r *responses) Name() string { return r.name }

func (r *responses) mapInput(msgs []Message) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			content, err := responsesUserContent(m)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"role": "user", "content": content})
		case "assistant":
			if m.Content != "" {
				out = append(out, map[string]any{"role": "assistant", "content": m.Content})
			}
			for _, tc := range m.ToolCalls {
				out = append(out, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Name,
					"arguments": normalizeArgs(tc.Arguments),
				})
			}
		case "tool":
			out = append(out, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		}
	}
	return out, nil
}

func responsesUserContent(m Message) (any, error) {
	if len(m.Attachments) == 0 {
		return m.Content, nil
	}
	text := m.Content
	var parts []map[string]any
	for _, a := range m.Attachments {
		switch a.Kind {
		case "image":
			if !IsImageMIME(a.MIME) {
				return nil, fmt.Errorf("openai responses: unsupported image type %s (%s)", a.MIME, a.Name)
			}
			data, err := LoadAttachment(a)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{
				"type":      "input_image",
				"image_url": DataURI(a.MIME, data),
			})
		case "audio":
			format, ok := AudioFormat(a.MIME)
			if !ok {
				return nil, fmt.Errorf("openai responses: unsupported audio format %s (%s)", a.MIME, a.Name)
			}
			data, err := LoadAttachment(a)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{
				"type":        "input_audio",
				"input_audio": map[string]any{"data": base64.StdEncoding.EncodeToString(data), "format": format},
			})
		case "video":
			return nil, fmt.Errorf("openai responses does not support video (%s); use a Gemini model", a.Name)
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
				parts = append(parts, map[string]any{
					"type":      "input_file",
					"filename":  a.Name,
					"file_data": DataURI(a.MIME, data),
				})
				continue
			}
			return nil, fmt.Errorf("openai responses does not support %s (%s)", a.MIME, a.Name)
		}
	}
	if text != "" {
		parts = append([]map[string]any{{"type": "input_text", "text": text}}, parts...)
	}
	return parts, nil
}

func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
}

type respItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
}

type respEvent struct {
	Type      string   `json:"type"`
	ItemID    string   `json:"item_id"`
	Delta     string   `json:"delta"`
	Arguments string   `json:"arguments"`
	Name      string   `json:"name"`
	Item      respItem `json:"item"`
	Resp      *struct {
		Status string     `json:"status"`
		Output []respItem `json:"output"`
		Usage  *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (r *responses) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = 8192
	}
	input, err := r.mapInput(req.Messages)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":             req.Model,
		"input":             input,
		"stream":            true,
		"store":             false,
		"max_output_tokens": maxOut,
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	if isReasoningModel(req.Model) {
		body["reasoning"] = map[string]any{"summary": "auto"}
	}
	if len(req.Tools) > 0 {
		tools := []map[string]any{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Schema,
			})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/responses", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if r.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.key)
	}
	if r.prepare != nil {
		r.prepare(httpReq)
	}
	resp, err := r.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(r.name, resp)
	}

	result := &Result{}
	calls := map[string]*ToolCall{}
	var order []string
	err = readSSE(resp.Body, func(_, data string) error {
		var e respEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return nil
		}
		switch e.Type {
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if e.Delta != "" && req.OnReasoning != nil {
				req.OnReasoning(e.Delta)
			}
		case "response.output_text.delta":
			if e.Delta != "" {
				result.Content += e.Delta
				if onDelta != nil {
					onDelta(e.Delta)
				}
			}
		case "response.output_item.added":
			if e.Item.Type == "function_call" {
				if _, ok := calls[e.Item.ID]; !ok {
					order = append(order, e.Item.ID)
				}
				calls[e.Item.ID] = &ToolCall{ID: e.Item.CallID, Name: e.Item.Name}
			}
		case "response.function_call_arguments.delta":
			if tc, ok := calls[e.ItemID]; ok {
				tc.Arguments += e.Delta
			}
		case "response.function_call_arguments.done":
			if tc, ok := calls[e.ItemID]; ok {
				if e.Arguments != "" {
					tc.Arguments = e.Arguments
				}
				if e.Name != "" {
					tc.Name = e.Name
				}
			}
		case "response.output_item.done":
			if e.Item.Type == "function_call" {
				tc, ok := calls[e.Item.ID]
				if !ok {
					order = append(order, e.Item.ID)
					tc = &ToolCall{}
					calls[e.Item.ID] = tc
				}
				if e.Item.CallID != "" {
					tc.ID = e.Item.CallID
				}
				if e.Item.Name != "" {
					tc.Name = e.Item.Name
				}
				if e.Item.Arguments != "" {
					tc.Arguments = e.Item.Arguments
				}
			}
		case "response.completed":
			if e.Resp != nil {
				if e.Resp.Usage != nil {
					result.Usage.In = e.Resp.Usage.InputTokens
					result.Usage.Out = e.Resp.Usage.OutputTokens
					result.Usage.Cached = e.Resp.Usage.InputTokensDetails.CachedTokens
				}
				var final []*ToolCall
				for _, it := range e.Resp.Output {
					if it.Type != "function_call" {
						continue
					}
					tc, ok := calls[it.ID]
					if !ok {
						tc = &ToolCall{}
						calls[it.ID] = tc
					}
					if it.CallID != "" {
						tc.ID = it.CallID
					}
					if it.Name != "" {
						tc.Name = it.Name
					}
					if it.Arguments != "" {
						tc.Arguments = it.Arguments
					}
					final = append(final, tc)
				}
				if len(final) > 0 {
					result.ToolCalls = result.ToolCalls[:0]
					for _, tc := range final {
						result.ToolCalls = append(result.ToolCalls, *tc)
					}
					order = nil
				}
			}
		case "error", "response.failed":
			msg := ""
			if e.Error != nil {
				msg = e.Error.Message
			}
			if msg == "" && e.Resp != nil && e.Resp.Error != nil {
				msg = e.Resp.Error.Message
			}
			if msg == "" {
				msg = e.Type
			}
			return fmt.Errorf("%s: %s", r.name, msg)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(order) > 0 {
		for i, id := range order {
			tc := calls[id]
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
			}
			if tc.Arguments == "" {
				tc.Arguments = "{}"
			}
			result.ToolCalls = append(result.ToolCalls, *tc)
		}
	}
	return result, nil
}

func (r *responses) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if r.key != "" {
		req.Header.Set("Authorization", "Bearer "+r.key)
	}
	if r.prepare != nil {
		r.prepare(req)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(r.name, resp)
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
