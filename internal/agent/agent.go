package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

type Hooks struct {
	OnText       func(string)
	OnReasoning  func(string)
	OnTool       func(name, detail string)
	OnToolResult func(name, result string, dur time.Duration)
	OnUsage      func(u llm.Usage, historyTokens int)
	OnCompact    func(kind string, before, after int)
}

type Agent struct {
	Provider      llm.Provider
	Model         string
	Tools         []tools.Tool
	Env           *tools.Env
	Sess          *session.Session
	MaxSteps      int
	Compact       bool
	ContextWindow int
	NumCtx        int
	Hooks         Hooks
}

func (a *Agent) toolDefs() []llm.Tool {
	defs := make([]llm.Tool, 0, len(a.Tools))
	for _, t := range a.Tools {
		defs = append(defs, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return defs
}

func (a *Agent) Run(ctx context.Context, input string) error {
	return a.RunWithAttachments(ctx, input, nil)
}

func (a *Agent) RunWithAttachments(ctx context.Context, input string, attachments []llm.Attachment) error {
	defer func() { _ = a.Sess.Flush() }()
	if len(a.Sess.Messages) == 0 {
		if err := a.Sess.Append(llm.Message{Role: "user", Content: startupContext(a.Env.Dir)}); err != nil {
			return err
		}
	}
	if input != "" || len(attachments) > 0 {
		if err := a.Sess.Append(llm.Message{Role: "user", Content: input, Attachments: attachments}); err != nil {
			return err
		}
	}
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 64
	}
	system := buildSystem(a.Env.Dir)
	for step := 0; step < maxSteps; step++ {
		if a.Compact {
			a.maybeCompact(ctx)
		}
		res, err := a.Provider.Chat(ctx, llm.Request{
			Model:       a.Model,
			System:      system,
			Messages:    a.Sess.Messages,
			Tools:       a.toolDefs(),
			NumCtx:      a.NumCtx,
			SessionID:   a.Sess.ID(),
			OnReasoning: a.Hooks.OnReasoning,
		}, a.Hooks.OnText)
		if err != nil {
			return err
		}
		if err := a.Sess.Append(llm.Message{
			Role:           "assistant",
			Content:        res.Content,
			Reasoning:      res.Reasoning,
			ReasoningItems: res.ReasoningItems,
			ToolCalls:      res.ToolCalls,
		}); err != nil {
			return err
		}
		if a.Hooks.OnUsage != nil {
			a.Hooks.OnUsage(res.Usage, a.historyTokens())
		}
		if len(res.ToolCalls) == 0 {
			return nil
		}
		for _, tc := range res.ToolCalls {
			if a.Hooks.OnTool != nil {
				a.Hooks.OnTool(tc.Name, toolDetail(tc))
			}
			start := time.Now()
			var content string
			if ctx.Err() != nil {
				content = "error: interrupted by user"
			} else {
				out, err := a.execTool(ctx, tc)
				switch {
				case ctx.Err() != nil:
					content = "error: interrupted by user"
				case err != nil:
					content = "error: " + err.Error()
				default:
					content = out
				}
			}
			content = tools.Truncate(content, 0, 0, 64*1024)
			if err := a.Sess.Append(llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    content,
			}); err != nil {
				return err
			}
			if a.Hooks.OnToolResult != nil {
				a.Hooks.OnToolResult(tc.Name, content, time.Since(start))
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return fmt.Errorf("stopped after %d steps without completing", maxSteps)
}

func (a *Agent) HistoryTokens() int { return a.historyTokens() }

func (a *Agent) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	tool, ok := tools.ByName(tc.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", tc.Name)
	}
	return tool.Run(ctx, json.RawMessage(tc.Arguments), a.Env)
}

func toolDetail(tc llm.ToolCall) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(tc.Arguments), &m)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch tc.Name {
	case "bash":
		return pick("command")
	case "write", "edit", "read":
		return pick("path")
	case "glob", "grep":
		return pick("pattern")
	default:
		return ""
	}
}
