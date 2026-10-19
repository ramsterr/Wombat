package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type openAI struct {
	name    string
	base    string
	key     string
	numCtx  int
	http    *http.Client
	prepare func(*http.Request)
}

func newOpenAI(name, base, key string, numCtx int) *openAI {
	return &openAI{
		name:   name,
		base:   strings.TrimRight(base, "/"),
		key:    key,
		numCtx: numCtx,
		http:   &http.Client{Timeout: 10 * time.Minute},
	}
}

func (o *openAI) Name() string { return o.name }

type oaiMessage struct {
	Role             string       `json:"role"`
	Content          any          `json:"content"`
	ReasoningContent string       `json:"reasoning_content,omitempty"`
	ToolCalls        []oaiToolCal `json:"tool_calls,omitempty"`
	ToolCallID       string       `json:"tool_call_id,omitempty"`
}

type oaiToolCal struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (o *openAI) mapMessages(req Request) ([]oaiMessage, error) {
	out := make([]oaiMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, oaiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		content, err := oaiContent(m)
		if err != nil {
			return nil, err
		}
		mm := oaiMessage{Role: m.Role, Content: content}
		if m.Role == "assistant" {
			mm.ReasoningContent = m.Reasoning
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				var t oaiToolCal
				t.ID = tc.ID
				t.Type = "function"
				t.Function.Name = tc.Name
				t.Function.Arguments = tc.Arguments
				mm.ToolCalls = append(mm.ToolCalls, t)
			}
		}
		if m.Role == "tool" {
			mm.ToolCallID = m.ToolCallID
		}
		out = append(out, mm)
	}
	return out, nil
}

func oaiContent(m Message) (any, error) {
	if len(m.Attachments) == 0 {
		return m.Content, nil
	}
	text := m.Content
	var parts []map[string]any
	for _, a := range m.Attachments {
		switch a.Kind {
		case "image":
			if !IsImageMIME(a.MIME) {
				return nil, fmt.Errorf("openai: unsupported image type %s (%s)", a.MIME, a.Name)
			}
			data, err := LoadAttachment(a)
			if err != nil {
				return nil, err
			}
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": DataURI(a.MIME, data)},
			})
		case "audio":
			format, ok := AudioFormat(a.MIME)
			if !ok || (format != "wav" && format != "mp3") {
				return nil, fmt.Errorf("openai: unsupported audio format %s (%s)", a.MIME, a.Name)
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
			return nil, fmt.Errorf("openai chat completions does not support video (%s); use a Gemini model", a.Name)
		default:
			content, ok, err := TextAttachment(a)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("openai chat completions does not support %s (%s); use a Responses, Claude, or Gemini model", a.MIME, a.Name)
			}
			text = AppendFileText(text, a, content)
		}
	}
	if text != "" {
		parts = append([]map[string]any{{"type": "text", "text": text}}, parts...)
	}
	return parts, nil
}

func toolSchemas(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Schema,
			},
		})
	}
	return out
}

type oaiChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		PromptTokensDet  struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o *openAI) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	msgs, err := o.mapMessages(req)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if len(req.Tools) > 0 {
		body["tools"] = toolSchemas(req.Tools)
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.key)
	}
	if o.prepare != nil {
		o.prepare(httpReq)
	}
	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(o.name, resp)
	}

	result := &Result{}
	calls := map[int]*ToolCall{}
	var order []int
	sawReasoningContent := false
	err = readSSE(resp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return nil
		}
		var c oaiChunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return nil
		}
		if c.Error != nil {
			return fmt.Errorf("%s: %s", o.name, c.Error.Message)
		}
		if c.Usage != nil {
			result.Usage.In = c.Usage.PromptTokens
			result.Usage.Out = c.Usage.CompletionTokens
			result.Usage.Cached = c.Usage.PromptTokensDet.CachedTokens
		}
		if len(c.Choices) == 0 {
			return nil
		}
		ch := c.Choices[0]
		if reasoning := ch.Delta.ReasoningContent; reasoning != "" {
			sawReasoningContent = true
			result.Reasoning += reasoning
			if req.OnReasoning != nil {
				req.OnReasoning(reasoning)
			}
		} else if reasoning := ch.Delta.Reasoning; reasoning != "" && !sawReasoningContent {
			result.Reasoning += reasoning
			if req.OnReasoning != nil {
				req.OnReasoning(reasoning)
			}
		}
		if ch.Delta.Content != "" {
			result.Content += ch.Delta.Content
			if onDelta != nil {
				onDelta(ch.Delta.Content)
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			cur, ok := calls[tc.Index]
			if !ok {
				cur = &ToolCall{}
				calls[tc.Index] = cur
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				if cur.Name == "" {
					cur.Name = tc.Function.Name
				} else if !strings.HasSuffix(cur.Name, tc.Function.Name) {
					cur.Name += tc.Function.Name
				}
			}
			cur.Arguments += tc.Function.Arguments
		}
		if ch.FinishReason != "" {
			result.StopReason = ch.FinishReason
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

func (o *openAI) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	if o.prepare != nil {
		o.prepare(req)
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(o.name, resp)
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

func statusError(name string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s: http %d: %s", name, resp.StatusCode, msg)
}
