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

type ollama struct {
	name   string
	base   string
	numCtx int
	http   *http.Client
}

func newOllama(name, base string, numCtx int) *ollama {
	if numCtx <= 0 {
		numCtx = 32768
	}
	return &ollama{
		name:   name,
		base:   strings.TrimRight(base, "/"),
		numCtx: numCtx,
		http:   &http.Client{Timeout: 10 * time.Minute},
	}
}

func (o *ollama) Name() string { return o.name }

func (o *ollama) mapMessages(req Request) ([]map[string]any, error) {
	out := []map[string]any{}
	if req.System != "" {
		out = append(out, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			msg := map[string]any{"role": "assistant", "content": m.Content}
			if len(m.ToolCalls) > 0 {
				tcs := []map[string]any{}
				for _, tc := range m.ToolCalls {
					var args any = map[string]any{}
					if err := json.Unmarshal([]byte(normalizeArgs(tc.Arguments)), &args); err != nil {
						args = map[string]any{}
					}
					tcs = append(tcs, map[string]any{
						"function": map[string]any{"name": tc.Name, "arguments": args},
					})
				}
				msg["tool_calls"] = tcs
			}
			out = append(out, msg)
		case "tool":
			out = append(out, map[string]any{
				"role":      "tool",
				"content":   m.Content,
				"tool_name": m.Name,
			})
		default:
			msg := map[string]any{"role": m.Role, "content": m.Content}
			var images []string
			text := m.Content
			for _, a := range m.Attachments {
				switch a.Kind {
				case "image":
					if !IsImageMIME(a.MIME) {
						return nil, fmt.Errorf("ollama: unsupported image type %s (%s)", a.MIME, a.Name)
					}
					data, err := LoadAttachment(a)
					if err != nil {
						return nil, err
					}
					images = append(images, base64.StdEncoding.EncodeToString(data))
				case "file":
					content, ok, err := TextAttachment(a)
					if err != nil {
						return nil, err
					}
					if !ok {
						return nil, fmt.Errorf("ollama does not support %s (%s)", a.MIME, a.Name)
					}
					text = AppendFileText(text, a, content)
				default:
					return nil, fmt.Errorf("ollama does not support %s attachments (%s)", a.Kind, a.Name)
				}
			}
			msg["content"] = text
			if len(images) > 0 {
				msg["images"] = images
			}
			out = append(out, msg)
		}
	}
	return out, nil
}

type ollamaChunk struct {
	Message struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Thinking  string `json:"thinking"`
		ToolCalls []struct {
			Function struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

func (o *ollama) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	msgs, err := o.mapMessages(req)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   true,
		"options": map[string]any{
			"num_ctx": o.numCtx,
		},
	}
	if req.Temperature > 0 {
		body["options"].(map[string]any)["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := []map[string]any{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Schema,
				},
			})
		}
		body["tools"] = tools
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(o.name, resp)
	}

	result := &Result{}
	n := 0
	err = readNDJSON(resp.Body, func(line []byte) error {
		var c ollamaChunk
		if err := json.Unmarshal(line, &c); err != nil {
			return nil
		}
		if c.Error != "" {
			return fmt.Errorf("%s: %s", o.name, c.Error)
		}
		if c.Message.Thinking != "" && req.OnReasoning != nil {
			req.OnReasoning(c.Message.Thinking)
		}
		if c.Message.Content != "" {
			result.Content += c.Message.Content
			if onDelta != nil {
				onDelta(c.Message.Content)
			}
		}
		for _, tc := range c.Message.ToolCalls {
			args := "{}"
			if len(tc.Function.Arguments) > 0 {
				args = string(tc.Function.Arguments)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:        fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), n),
				Name:      tc.Function.Name,
				Arguments: args,
			})
			n++
		}
		if c.Done {
			result.Usage.In = c.PromptEvalCount
			result.Usage.Out = c.EvalCount
			result.StopReason = c.DoneReason
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (o *ollama) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/api/tags", nil)
	if err != nil {
		return nil, err
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
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		models = append(models, m.Name)
	}
	sort.Strings(models)
	return models, nil
}
