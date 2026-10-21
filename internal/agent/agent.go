package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	OnNotice     func(string)
}

type Agent struct {
	Provider      llm.Provider
	Model         string
	Tools         []tools.Tool
	Env           *tools.Env
	Sess          *session.Session
	Compact       bool
	ContextWindow int
	NumCtx        int
	SystemExtra   func() string
	RunStart      func()
	Hooks         Hooks
	Trace         *trace.Logger

	ModeNote string
	TmpNote  string
	// Reasoning asks the provider to think less or more: off, low, medium,
	// high, or "" for the provider default.
	Reasoning string
	// NoVision drops image/video/audio attachments before each request. It is
	// set for text-only models, and after a provider says it cannot see
	// images.
	NoVision bool
	// TrimReasoning drops older non-tool reasoning from requests. It is on by
	// default; a provider that requires reasoning replay turns it off for the
	// session automatically.
	TrimReasoning bool

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
	a.Trace.Emit("run_start", map[string]any{"model": a.Model})
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
	prior := append([]llm.Message(nil), a.Sess.Messages...)
	var inputMsg *llm.Message
	if input != "" || len(attachments) > 0 {
		msg := llm.Message{Role: "user", Content: input, Attachments: attachments}
		if err := a.Sess.Append(msg); err != nil {
			return err
		}
		inputMsg = &msg
	}
	// Files a tool hands over (an image the agent downloaded) are attached to
	// the next user message so the model can actually look at them.
	var media []tools.Media
	if a.Env != nil {
		a.Env.SetMediaSink(&media)
		a.Env.SetSpillDir(a.spillDir())
	}
	// Repeating the same call with the same arguments means the model is
	// stuck; stop it before it burns the turn.
	calls := map[string]int{}
	system := buildSystem(a.Env.Dir)
	if note := strings.TrimSpace(a.ModeNote); note != "" {
		system += "\n\n" + note
	}
	if note := strings.TrimSpace(a.TmpNote); note != "" {
		system += "\n\n" + note
	}
	if a.SystemExtra != nil {
		if extra := strings.TrimSpace(a.SystemExtra()); extra != "" {
			system += "\n\n" + extra
		}
	}
	for step := 0; ; step++ {
		if a.Compact {
			a.maybeCompact(ctx)
		}
		a.Trace.Emit("model_start", map[string]any{"step": step + 1})
		defs := a.toolDefs()
		messages := a.Sess.Messages
		if a.TrimReasoning {
			messages = llm.TrimStaleReasoning(messages)
		}
		messages = llm.NormalizeToolCalls(messages)
		if a.NoVision {
			messages = llm.TextOnlyMessages(messages)
		}
		req := llm.Request{
			Model:       a.Model,
			System:      system,
			Messages:    messages,
			Tools:       defs,
			NumCtx:      a.NumCtx,
			Reasoning:   a.Reasoning,
			SessionID:   a.Sess.ID(),
			OnReasoning: a.Hooks.OnReasoning,
		}
		res, err := a.Provider.Chat(ctx, req, a.Hooks.OnText)
		if err != nil && a.TrimReasoning && llm.RequiresReasoning(err) {
			// The gateway wants the older reasoning back. Keep it for the rest
			// of this session and retry the same request without trimming.
			a.TrimReasoning = false
			if a.Hooks.OnNotice != nil {
				a.Hooks.OnNotice("provider requires older reasoning; keeping it for this session (set trim_reasoning false to make it permanent)")
			}
			req.Messages = llm.NormalizeToolCalls(a.Sess.Messages)
			if a.NoVision {
				req.Messages = llm.TextOnlyMessages(req.Messages)
			}
			res, err = a.Provider.Chat(ctx, req, a.Hooks.OnText)
		}
		if err != nil && !a.NoVision && llm.IsImageUnsupported(err) {
			// The model is text-only even though its name did not say so.
			// Retry once without the image attachments and remember it.
			a.NoVision = true
			if a.Env != nil {
				a.Env.NoImages = true
			}
			a.Trace.Emit("vision_disabled", map[string]any{"error": tools.Truncate(err.Error(), 0, 0, 500)})
			req.Messages = llm.TextOnlyMessages(messages)
			res, err = a.Provider.Chat(ctx, req, a.Hooks.OnText)
		}
		if err != nil && llm.IsInvalidRequest(err) && inputMsg != nil {
			// The provider rejected the conversation itself (bad tool
			// ordering, an oversized context, an unsupported message). Try
			// once with only the latest request, so a poisoned history does
			// not make every future prompt fail the same way.
			res, err = a.recoverInvalidRequest(ctx, system, prior, *inputMsg, err)
		}
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
		// Run the tool calls. Calls that cannot change anything run
		// concurrently; anything that mutates runs on its own, in order, so
		// the workspace stays consistent. Results are always committed in
		// the order the model asked for them.
		results, err := a.runToolCalls(ctx, res.ToolCalls, calls)
		if err != nil {
			return err
		}
		for i, tc := range res.ToolCalls {
			r := results[i]
			if err := a.Sess.Append(llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    r.content,
			}); err != nil {
				return err
			}
			a.Trace.Emit("tool_end", map[string]any{
				"name": tc.Name, "duration_ms": r.duration.Milliseconds(),
				"result_bytes": len(r.content),
			})
			if a.Hooks.OnToolResult != nil {
				a.Hooks.OnToolResult(tc.Name, r.content, r.duration)
			}
		}
		// Every tool message for this assistant turn must be committed before
		// any user message. Tools that hand back an image append to `media`;
		// attaching it inside the loop would put a user message between two
		// tool results, which providers reject.
		if len(media) > 0 {
			if err := a.appendMedia(media); err != nil {
				return err
			}
			media = media[:0]
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// recoverInvalidRequest retries the latest request from a fresh context. If
// even that fails, it restores the history before the failed prompt and drops
// that prompt, so the next prompt is not poisoned by it.
func (a *Agent) recoverInvalidRequest(ctx context.Context, system string, prior []llm.Message, input llm.Message, cause error) (*llm.Result, error) {
	fresh := make([]llm.Message, 0, 2)
	for _, m := range prior {
		if m.Role == "user" && strings.HasPrefix(m.Content, "Today: ") {
			fresh = append(fresh, m)
			break
		}
	}
	fresh = append(fresh, input)
	backup, _ := a.Sess.Backup("recovered")
	if err := a.Sess.ReplaceMessages(fresh); err != nil {
		return nil, cause
	}
	req := llm.Request{
		Model: a.Model,
		System: system + "\n\nNote: the earlier conversation was rejected by the provider and dropped. " +
			"Answer the user's latest request from scratch.",
		Tools:       a.toolDefs(),
		Messages:    llm.NormalizeToolCalls(fresh),
		NumCtx:      a.NumCtx,
		Reasoning:   a.Reasoning,
		SessionID:   a.Sess.ID(),
		OnReasoning: a.Hooks.OnReasoning,
	}
	if a.NoVision {
		req.Messages = llm.TextOnlyMessages(req.Messages)
	}
	res, err := a.Provider.Chat(ctx, req, a.Hooks.OnText)
	if err == nil {
		msg := "provider rejected the old conversation; retried your latest request from a fresh context"
		if backup != "" {
			msg += " (backup: " + backup + ")"
		}
		a.notice(msg)
		return res, nil
	}
	if llm.IsImageUnsupported(err) && !a.NoVision {
		a.NoVision = true
		if a.Env != nil {
			a.Env.NoImages = true
		}
		req.Messages = llm.TextOnlyMessages(req.Messages)
		res, err = a.Provider.Chat(ctx, req, a.Hooks.OnText)
		if err == nil {
			a.notice("provider rejected images; retried the latest request as text")
			return res, nil
		}
	}
	// Even a fresh context failed. Restore the last known-good conversation
	// and drop the failed prompt, so the next prompt starts clean.
	_ = a.Sess.ReplaceMessages(prior)
	msg := "provider rejected the request; removed the failed turn so your next prompt starts clean"
	if backup != "" {
		msg += " (backup: " + backup + ")"
	}
	a.notice(msg)
	return nil, err
}

func (a *Agent) notice(s string) {
	a.Trace.Emit("notice", map[string]any{"text": tools.Truncate(s, 0, 0, 500)})
	if a.Hooks.OnNotice != nil {
		a.Hooks.OnNotice(s)
	}
}

func (a *Agent) HistoryTokens() int { return a.historyTokens() }

// toolResult is the outcome of one tool call.
// parallelToolLimit is how many read-only tool calls run at once.
const parallelToolLimit = 6

// maxRepeatedToolCall is how many times the same call may repeat in a turn
// before the agent is told to stop.
const maxRepeatedToolCall = 3

type toolResult struct {
	content  string
	duration time.Duration
}

// runToolCalls runs the tool calls of one assistant turn. Read-only calls
// run concurrently - tool execution is a large part of an agent's wall-clock
// time - while calls that can change the workspace run alone, in order.
func (a *Agent) runToolCalls(ctx context.Context, toolCalls []llm.ToolCall, seen map[string]int) ([]toolResult, error) {
	results := make([]toolResult, len(toolCalls))
	exclusive := make([]bool, len(toolCalls))
	blocked := make([]bool, len(toolCalls))
	for i, tc := range toolCalls {
		exclusive[i] = a.exclusiveTool(tc)
		// Count repeats before dispatching, so the map is never touched
		// from the worker goroutines.
		key := tc.Name + " " + tc.Arguments
		seen[key]++
		if seen[key] > maxRepeatedToolCall {
			blocked[i] = true
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallelToolLimit)
	for i, tc := range toolCalls {
		if exclusive[i] {
			continue
		}
		wg.Add(1)
		go func(i int, tc llm.ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = a.callTool(ctx, tc, blocked[i])
		}(i, tc)
	}
	wg.Wait()
	for i, tc := range toolCalls {
		if !exclusive[i] {
			continue
		}
		results[i] = a.callTool(ctx, tc, blocked[i])
	}
	return results, nil
}

// exclusiveTool reports whether a call must run on its own. Anything that can
// change the filesystem or the repository is exclusive.
func (a *Agent) exclusiveTool(tc llm.ToolCall) bool {
	switch tc.Name {
	case "read", "glob", "grep":
		return false
	case "bash":
		// A bash call that only inspects is safe to run alongside others.
		return tools.MayMutate(toolArg(tc, "command"))
	}
	return true
}

// callTool runs one tool call and captures its result.
func (a *Agent) callTool(ctx context.Context, tc llm.ToolCall, blocked bool) toolResult {
	if blocked {
		return toolResult{
			content: fmt.Sprintf("error: %s was called with the same arguments %d times in this turn. "+
				"Stop repeating it: change the arguments, use a different tool, or ask the user what to do.",
				tc.Name, maxRepeatedToolCall+1),
		}
	}
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
	return toolResult{content: tools.Truncate(content, 0, 0, 64*1024), duration: time.Since(start)}
}

// toolArg returns one argument of a tool call.
func toolArg(tc llm.ToolCall, key string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(tc.Arguments), &m)
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// spillDir is where large tool results are written for the model to read.
func (a *Agent) spillDir() string {
	if a.Env == nil || a.Env.Dir == "" {
		return ""
	}
	return filepath.Join(a.Env.Dir, ".wombat", "spill")
}

// appendMedia attaches files a tool produced to a user message, so the model
// receives them as images the way a pasted image arrives.
func (a *Agent) appendMedia(media []tools.Media) error {
	if a.NoVision || len(media) == 0 {
		return nil
	}
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
	names := make([]string, 0, len(a.Tools))
	for _, tool := range a.Tools {
		if tool.Name() == tc.Name {
			return tool.Run(ctx, json.RawMessage(tc.Arguments), a.Env)
		}
		names = append(names, tool.Name())
	}
	return "", fmt.Errorf("unknown tool %q; available tools: %s", tc.Name, strings.Join(names, ", "))
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
