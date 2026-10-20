package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/trace"
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
	SystemExtra   func() string
	RunStart      func()
	Hooks         Hooks
	Trace         *trace.Logger

	SandboxNote string
	ModeNote    string

	mu    sync.Mutex
	usage UsageStats
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
	if a.RunStart != nil {
		a.RunStart()
	}
	a.markStarted()
	traceStart := time.Now()
	traceUsage := a.Usage()
	a.Trace.Emit("run_start", map[string]any{"model": a.Model, "max_steps": a.MaxSteps})
	defer func() {
		u := a.Usage()
		a.Trace.Emit("run_end", map[string]any{
			"duration_ms": time.Since(traceStart).Milliseconds(),
			"in":          u.In - traceUsage.In,
			"cached":      u.Cached - traceUsage.Cached,
			"out":         u.Out - traceUsage.Out,
		})
	}()
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
	// Files a tool hands over (an image the agent downloaded) are attached to
	// the next user message so the model can actually look at them.
	var media []tools.Media
	if a.Env != nil {
		a.Env.SetMediaSink(&media)
	}
	system := buildSystem(a.Env.Dir)
	if note := strings.TrimSpace(a.ModeNote); note != "" {
		system += "\n\n" + note
	}
	if note := strings.TrimSpace(a.SandboxNote); note != "" {
		system += "\n\n" + note
	}
	if a.SystemExtra != nil {
		if extra := strings.TrimSpace(a.SystemExtra()); extra != "" {
			system += "\n\n" + extra
		}
	}
	for step := 0; step < maxSteps; step++ {
		if a.Compact {
			a.maybeCompact(ctx)
		}
		a.Trace.Emit("model_start", map[string]any{"step": step + 1})
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
		a.recordUsage(res.Usage)
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
			a.Trace.Emit("tool_start", map[string]any{"name": tc.Name, "detail": tools.Truncate(toolDetail(tc), 0, 0, 500)})
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
			if len(media) > 0 {
				if err := a.appendMedia(media); err != nil {
					return err
				}
				media = media[:0]
			}
			a.Trace.Emit("tool_end", map[string]any{
				"name": tc.Name, "duration_ms": time.Since(start).Milliseconds(),
				"result_bytes": len(content),
			})
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

// appendMedia attaches files a tool produced to a user message, so the model
// receives them as images the way a pasted image arrives.
func (a *Agent) appendMedia(media []tools.Media) error {
	names := make([]string, 0, len(media))
	attachments := make([]llm.Attachment, 0, len(media))
	for _, m := range media {
		names = append(names, m.Name)
		attachments = append(attachments, llm.Attachment{Kind: "image", MIME: m.MIME, Name: m.Name, Path: m.Path})
	}
	content := "I read " + strings.Join(names, ", ") + ". Look at the attached image and continue."
	return a.Sess.Append(llm.Message{Role: "user", Content: content, Attachments: attachments})
}

func (a *Agent) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	for _, tool := range a.Tools {
		if tool.Name() == tc.Name {
			return tool.Run(ctx, json.RawMessage(tc.Arguments), a.Env)
		}
	}
	return "", fmt.Errorf("unknown tool %q", tc.Name)
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
