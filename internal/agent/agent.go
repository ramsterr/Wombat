package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tokens"
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

	ModeNote string
	TmpNote  string
	// TrimReasoning drops the reasoning of older turns from the request.
	// Some gateways require it to be passed back, so it is off by default.
	TrimReasoning bool
	// Reasoning asks the provider to think less or more: off, low, medium,
	// high, or "" for the provider default.
	Reasoning string

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
	// No step limit: the agent works until it is done or the doom-loop
	// guard stops it.
	maxSteps := a.MaxSteps
	// Files a tool hands over (an image the agent downloaded) are attached to
	// the next user message so the model can actually look at them.
	var media []tools.Media
	if a.Env != nil {
		a.Env.SetMediaSink(&media)
		a.Env.SetSpillDir(a.spillDir())
	}
	// Repeating the same call with the same arguments means the model is
	// stuck; stop it before it burns the budget.
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
	for step := 0; maxSteps <= 0 || step < maxSteps; step++ {
		if a.Compact {
			a.maybeCompact(ctx)
		}
		a.Trace.Emit("model_start", map[string]any{"step": step + 1})
		// On the last step the model gets no tools, so it has to answer in
		// text instead of running out of steps mid-thought.
		defs := a.toolDefs()
		if maxSteps > 0 && step == maxSteps-1 {
			defs = nil
		}
		res, err := a.Provider.Chat(ctx, a.request(system, defs), a.Hooks.OnText)
		if err != nil && llm.InvalidRequest(err) {
			// The provider rejected the request itself. The history is the
			// problem, so repair it and try again: otherwise every later
			// prompt in this session would fail the same way.
			for attempt := 0; attempt < 2 && err != nil && llm.InvalidRequest(err); attempt++ {
				var repaired bool
				var note string
				if attempt == 0 {
					repaired, note = a.repairHistory()
					if reasoningRejected(err) {
						// The provider requires reasoning replay. Stop trimming
						// older turns for this session and repair any turns
						// whose reasoning was never captured.
						a.TrimReasoning = false
						repaired = true
						note = joinNotes(note, "The provider requires reasoning replay, so I stopped trimming older reasoning. Please continue.")
						if r, n := a.repairMissingReasoning(); r {
							note = joinNotes(note, n)
						}
					}
					if !repaired {
						repaired, note = a.dropLastExchange()
					}
				} else {
					repaired, note = a.dropLastExchange()
				}
				if !repaired {
					break
				}
				_ = a.Sess.Append(llm.Message{Role: "user", Content: note})
				res, err = a.Provider.Chat(ctx, a.request(system, defs), a.Hooks.OnText)
			}
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
			// The media is attached after every tool result of this turn: a
			// user message in the middle of a tool_call/tool_result pair
			// breaks the API's pairing rules.
			a.Trace.Emit("tool_end", map[string]any{
				"name": tc.Name, "duration_ms": r.duration.Milliseconds(),
				"result_bytes": len(r.content),
			})
			if a.Hooks.OnToolResult != nil {
				a.Hooks.OnToolResult(tc.Name, r.content, r.duration)
			}
		}
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
	// The step limit is not a failure: the model got its last word in
	// without tools, so the turn ends normally.
	return nil
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
	// Announce calls in the order the model asked for them. The calls
	// themselves may finish out of order, but the hook stream stays ordered.
	if a.Hooks.OnTool != nil {
		for i, tc := range toolCalls {
			if !blocked[i] {
				a.Hooks.OnTool(tc.Name, toolDetail(tc))
			}
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
	case "websearch", "webfetch":
		// Network reads have no shared state; run them alongside other reads.
		return false
	}
	if tc.Name == "bash" {
		// A bash call that only inspects is safe to run alongside others.
		return tools.MayMutate(toolArg(tc, "command"))
	}
	for _, t := range a.Tools {
		if t.Name() != tc.Name {
			continue
		}
		// Every read-only tool, including symbols, batch, and job_output,
		// can run alongside the others. The prompt paths still serialize
		// when a sensitive file needs approval.
		return t.Class() != "read"
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
	a.Trace.Emit("tool_start", map[string]any{"name": tc.Name, "detail": tools.RedactSecrets(tools.Truncate(toolDetail(tc), 0, 0, 500))})
	start := time.Now()
	var content string
	if ctx.Err() != nil {
		content = "error: " + interruptionMessage(ctx)
	} else {
		out, err := a.execTool(ctx, tc)
		switch {
		case ctx.Err() != nil:
			content = "error: " + interruptionMessage(ctx)
		case err != nil:
			content = "error: " + err.Error()
		default:
			content = out
		}
	}
	// Credentials never enter the conversation, even if a command printed
	// them or the user approved a read of a secrets file.
	content = tools.RedactSecrets(content)
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

// request builds the provider request for this turn.
func (a *Agent) request(system string, tools []llm.Tool) llm.Request {
	return llm.Request{
		Model:       a.Model,
		System:      system,
		Messages:    a.requestMessages(),
		Tools:       tools,
		NumCtx:      a.NumCtx,
		Reasoning:   a.Reasoning,
		SessionID:   a.Sess.ID(),
		OnReasoning: a.Hooks.OnReasoning,
	}
}

// reasoningRejected reports whether the provider rejected the request
// because an assistant turn is missing reasoning_content. This is specific to
// thinking-mode APIs; not every provider needs it.
func reasoningRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return (strings.Contains(msg, "reasoning_content") || strings.Contains(msg, "reasoning content")) &&
		(strings.Contains(msg, "must be passed back") || strings.Contains(msg, "thinking mode"))
}

// repairMissingReasoning removes assistant turns that have no reasoning text.
// Some thinking-mode gateways require the original reasoning to be replayed
// and cannot accept a turn without it. Removing the turn (and any tool results
// belonging to it) is the only safe repair when the text was never captured.
func (a *Agent) repairMissingReasoning() (bool, string) {
	msgs := a.Sess.Messages
	removedCalls := map[string]bool{}
	kept := make([]llm.Message, 0, len(msgs))
	removed := 0
	for _, m := range msgs {
		if m.Role == "assistant" && strings.TrimSpace(m.Reasoning) == "" && len(m.ReasoningItems) == 0 {
			removed++
			for _, tc := range m.ToolCalls {
				removedCalls[tc.ID] = true
			}
			continue
		}
		if m.Role == "tool" && removedCalls[m.ToolCallID] {
			continue
		}
		kept = append(kept, m)
	}
	if removed == 0 {
		return false, ""
	}
	a.Sess.Messages = kept
	_ = a.Sess.Flush()
	return true, fmt.Sprintf("The provider requires reasoning_content to be replayed, so I removed %d assistant turn(s) whose reasoning was not captured. Please continue.", removed)
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " " + b
	}
}

// repairHistory fixes a history the provider refused, so the session is usable
// again instead of failing the same way on every later prompt.
//
// It removes what a provider commonly rejects: images sent to a text-only
// model, an assistant turn whose tool calls were never answered, and tool
// results with no matching call.
func (a *Agent) repairHistory() (bool, string) {
	msgs := a.Sess.Messages
	changed := false
	notes := make([]string, 0, 3)

	// 1. images a text-only model cannot take
	vision := a.supportsImages()
	for i := range msgs {
		if len(msgs[i].Attachments) == 0 {
			continue
		}
		changed = true
		if vision {
			continue
		}
		names := make([]string, 0, len(msgs[i].Attachments))
		for _, at := range msgs[i].Attachments {
			names = append(names, at.Name)
		}
		notes = append(notes, "removed "+strings.Join(names, ", ")+" from the conversation because this model cannot see images")
		msgs[i].Attachments = nil
		msgs[i].Content = strings.TrimSpace(msgs[i].Content + " (the image was removed: this model cannot see images)")
	}

	// 2. tool calls without results, and results without calls
	answered := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			answered[tc.ID] = false
		}
	}
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if _, ok := answered[m.ToolCallID]; !ok {
			continue
		}
		answered[m.ToolCallID] = true
	}
	kept := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			if state, ok := answered[m.ToolCallID]; !ok || !state {
				changed = true
				notes = append(notes, "dropped a tool result that did not match any call")
				continue
			}
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			missing := false
			for _, tc := range m.ToolCalls {
				if !answered[tc.ID] {
					missing = true
				}
			}
			if missing {
				changed = true
				notes = append(notes, "dropped an unfinished tool call")
				continue
			}
		}
		kept = append(kept, m)
	}
	if changed {
		a.Sess.Messages = kept
		_ = a.Sess.Flush()
	}
	if len(notes) == 0 {
		return false, ""
	}
	return true, "The previous request was rejected and I repaired the conversation (" +
		strings.Join(notes, "; ") + "). Please continue."
}

// dropLastExchange removes the newest assistant turn and the results that
// belong to it. It is the last resort when the provider still refuses the
// history: the session stays usable, and the next prompt starts from
// something the provider accepts.
func (a *Agent) dropLastExchange() (bool, string) {
	msgs := a.Sess.Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		end := i + 1
		for end < len(msgs) && msgs[end].Role == "tool" {
			end++
		}
		if end < len(msgs) && msgs[end].Role == "user" && len(msgs[end].Attachments) > 0 {
			end++
		}
		a.Sess.Messages = append(append([]llm.Message{}, msgs[:i]...), msgs[end:]...)
		_ = a.Sess.Flush()
		return true, "The last exchange was rejected by the provider, so I dropped it. Please continue from where we left off."
	}
	return false, ""
}

// requestMessages returns the history to send. The reasoning of older turns
// is only dropped when the user asked for it: some gateways reject a
// request whose thinking turns are missing their reasoning.
func (a *Agent) requestMessages() []llm.Message {
	if !a.TrimReasoning {
		return a.Sess.Messages
	}
	return llm.TrimStaleReasoning(a.Sess.Messages)
}

// supportsImages reports whether the active provider/model can see images.
// The provider's published metadata wins over the built-in model families.
func (a *Agent) supportsImages() bool {
	name := ""
	if a.Provider != nil {
		name = a.Provider.Name()
	}
	return tokens.SupportsImagesFor(name, a.Model)
}

// appendMedia attaches files a tool produced to a user message, so the model
// receives them as images the way a pasted image arrives.
//
// A model that cannot see images must not be sent one: the provider rejects
// the whole request. For those, the file is described in words instead.
func (a *Agent) appendMedia(media []tools.Media) error {
	if !a.supportsImages() {
		parts := make([]string, 0, len(media))
		for _, m := range media {
			note := ""
			if m.Note != "" {
				note = " (" + m.Note + ")"
			}
			location := ""
			if m.Path != "" {
				location = " at " + m.Path
			}
			parts = append(parts, fmt.Sprintf("%s is an image (%s, %d bytes)%s%s", m.Name, m.MIME, mediaSize(m), location, note))
		}
		content := "I read " + strings.Join(parts, ", and ") + ". This model cannot see images, so I cannot " +
			"look at them. Use the file directly (a tool that reads its metadata, or a command) if you need facts about it."
		return a.Sess.Append(llm.Message{Role: "user", Content: content})
	}
	names := make([]string, 0, len(media))
	attachments := make([]llm.Attachment, 0, len(media))
	for _, m := range media {
		names = append(names, m.Name)
		attachments = append(attachments, llm.Attachment{
			Kind: "image", MIME: m.MIME, Name: m.Name, Path: m.Path,
			Size: mediaSize(m), Data: m.Data,
		})
	}
	content := "I read " + strings.Join(names, ", ") + ". Look at the attached image and continue."
	return a.Sess.Append(llm.Message{Role: "user", Content: content, Attachments: attachments})
}

// mediaSize is the byte length of a tool image, whether it is in memory or on
// disk.
func mediaSize(m tools.Media) int64 {
	if m.Size > 0 {
		return m.Size
	}
	if len(m.Data) > 0 {
		return int64(len(m.Data))
	}
	return fileSize(m.Path)
}

// fileSize is the size of a file, or zero when it cannot be read.
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func (a *Agent) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	for _, tool := range a.Tools {
		if tool.Name() == tc.Name {
			return tool.Run(ctx, json.RawMessage(tc.Arguments), a.Env)
		}
	}
	names := make([]string, 0, len(a.Tools))
	for _, tool := range a.Tools {
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
	case "write", "edit", "read", "apply_patch", "symbols", "lsp", "rename", "imagegen", "verify":
		if v := pick("path"); v != "" {
			return v
		}
		return pick("action", "kind", "name")
	case "browser":
		return pick("action", "url", "selector")
	case "glob", "grep":
		return pick("pattern")
	case "job_output", "job_kill":
		return pick("id")
	default:
		return ""
	}
}

// interruptionMessage distinguishes a real user interrupt from a worker
// timeout or a budget-driven cancellation.
func interruptionMessage(ctx context.Context) string {
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		return "interrupted by user"
	}
	return cause.Error()
}
