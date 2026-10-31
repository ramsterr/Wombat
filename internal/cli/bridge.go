package cli

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/commands"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/modelinfo"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/todo"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/tui"
)

// The bridge is the Go side of the opentui frontend. It speaks newline
// delimited JSON on stdout and keeps the terminal for the Node process.
type bridge struct {
	cfg *config.Config
	cwd string

	mu      sync.Mutex
	sess    *session.Session
	ag      *agent.Agent
	prompt  *Prompter
	mode    mode.Mode
	opts    runOpts
	running bool
	stop    context.CancelFunc
	// interrupting marks an in-flight cancellation.
	interrupting bool
	auto         bool
	always       map[string]bool
	showThinking bool

	atts []llm.Attachment

	previews   []bridgePreview
	previewURL string
	refs       []bridgeReference

	cumIn, cumOut, cumCached int
	history, window          int

	permWait     map[string]chan tools.Decision
	questionWait map[string]chan string
	seq          int

	out   *json.Encoder
	outMu sync.Mutex

	runMu sync.Mutex
	cmdMu sync.Mutex
}

type bridgeRequest struct {
	Type     string            `json:"type"`
	Text     string            `json:"text"`
	Line     string            `json:"line"`
	ID       string            `json:"id"`
	Allow    bool              `json:"allow"`
	Always   bool              `json:"always"`
	Feedback string            `json:"feedback"`
	Answer   string            `json:"answer"`
	Action   string            `json:"action"`
	Value    string            `json:"value"`
	Theme    string            `json:"theme"`
	Values   map[string]string `json:"values"`
	Meta     map[string]string `json:"meta"`
}

type bridgePreview struct {
	Ref   string    `json:"ref"`
	Kind  string    `json:"kind"`
	Label string    `json:"label"`
	At    time.Time `json:"at"`
}

type bridgeReference struct {
	Value string    `json:"value"`
	Kind  string    `json:"kind"`
	Added time.Time `json:"added"`
}

type bridgeCommand struct {
	Name    string         `json:"name"`
	Args    string         `json:"args,omitempty"`
	Summary string         `json:"summary"`
	Group   string         `json:"group,omitempty"`
	Options []bridgeOption `json:"options,omitempty"`
}

type bridgeOption struct {
	Value   string `json:"value"`
	Summary string `json:"summary"`
}

type bridgeListItem struct {
	Label   string `json:"label"`
	Detail  string `json:"detail,omitempty"`
	Value   string `json:"value"`
	Current bool   `json:"current,omitempty"`
	Icon    string `json:"icon,omitempty"`
}

var bridgePreviewURL = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?::[0-9]+)?(?:[/?#][^\s"'<>)]*)?`)

// cmdBridge runs the JSON bridge used by the opentui client.
func cmdBridge(args []string) error {
	sessionID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session", "-s":
			if i+1 < len(args) {
				sessionID = args[i+1]
				i++
			}
		}
	}
	protocol := os.Stdout
	// Anything else that prints must not corrupt the JSON stream. The UI
	// points its child stderr at a log file.
	os.Stdout = os.Stderr

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := mode.Parse(cfg.Mode)
	if err != nil {
		m = mode.Create
	}
	b := &bridge{
		cfg:          cfg,
		cwd:          cwd,
		mode:         m,
		always:       map[string]bool{},
		showThinking: cfg.ShowThinking,
		permWait:     map[string]chan tools.Decision{},
		questionWait: map[string]chan string{},
		out:          json.NewEncoder(protocol),
	}
	b.opts = runOpts{mode: m, modeSet: true, profile: cfg.ToolProfile, research: cfg.ResearchMode}
	b.prompt = &Prompter{
		always: map[string]bool{},
		tty:    true,
		perms:  cfg.Permissions,
	}
	b.prompt.permitFn = b.permit
	b.prompt.askFn = b.ask
	b.window = tokens.WindowFor(cfg.Provider, cfg.ActiveModel())
	b.refs = loadBridgeReferences(cwd)

	if sessionID != "" {
		s, openErr := session.Open(sessionID, cwd)
		if openErr != nil {
			b.send(map[string]any{"type": "toast", "message": "could not open session: " + openErr.Error(), "error": true})
		} else {
			b.sess = s
			if s.Meta.Provider != "" {
				b.cfg.Provider = s.Meta.Provider
			}
			if s.Meta.Model != "" {
				b.cfg.Model = s.Meta.Model
			}
			b.opts.agents = append([]string(nil), s.Meta.Agents...)
			b.window = tokens.WindowFor(b.cfg.Provider, b.cfg.ActiveModel())
		}
	}

	b.send(map[string]any{
		"type":     "ready",
		"version":  VersionString(),
		"state":    b.state(),
		"commands": bridgeCommands(),
		"themes":   tui.ThemeCatalog(),
		"history":  b.historyEntries(),
		"previews": b.previews,
		"todos":    b.todoItems(),
		"refs":     b.refs,
	})
	defer b.closeAgent()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req bridgeRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			b.send(map[string]any{"type": "toast", "message": "bad bridge message: " + err.Error(), "error": true})
			continue
		}
		switch req.Type {
		case "submit":
			go b.run(req.Text)
		case "command":
			go b.command(req.Line)
		case "interrupt":
			b.interrupt()
		case "permission":
			b.resolvePerm(req)
		case "question":
			b.resolveQuestion(req)
		case "select":
			go b.selectAction(req.Action, req.Value)
		case "form_submit":
			go b.formAction(req.Action, req.Values, req.Meta)
		case "set_theme":
			b.cmdMu.Lock()
			b.cfg.Theme = req.Theme
			_ = b.cfg.Save()
			b.cmdMu.Unlock()
		case "shutdown":
			b.interrupt()
			return nil
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (b *bridge) send(v any) {
	b.outMu.Lock()
	defer b.outMu.Unlock()
	_ = b.out.Encode(v)
}

func (b *bridge) note(text string, isErr bool) {
	b.send(map[string]any{"type": "note", "text": text, "error": isErr})
}

func (b *bridge) state() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	sessionID := ""
	title := ""
	provider := b.cfg.Provider
	model := b.cfg.ActiveModel()
	if b.sess != nil {
		sessionID = b.sess.ID()
		title = b.sess.Meta.Title
		if b.sess.Meta.Provider != "" {
			provider = b.sess.Meta.Provider
		}
		if b.sess.Meta.Model != "" {
			model = b.sess.Meta.Model
		}
	}
	pending := make([]map[string]any, 0, len(b.atts))
	for _, a := range b.atts {
		pending = append(pending, map[string]any{"name": a.Name, "kind": a.Kind, "size": a.Size, "path": a.Path})
	}
	return map[string]any{
		"provider":    provider,
		"model":       model,
		"mode":        b.mode.Label(),
		"cwd":         b.cwd,
		"branch":      tui.GitBranch(b.cwd),
		"session":     sessionID,
		"title":       title,
		"auto":        b.auto,
		"thinking":    b.showThinking,
		"reasoning":   b.cfg.Reasoning,
		"window":      b.window,
		"missing_key": b.missingKey(),
		"pending":     pending,
		"streaming":   b.running,
		"theme":       b.cfg.Theme,
		"refs":        b.refs,
	}
}

func (b *bridge) emitState() {
	b.send(map[string]any{"type": "state", "state": b.state()})
}

func (b *bridge) missingKey() string {
	pc := b.cfg.Providers[b.cfg.Provider]
	if pc == nil {
		return b.cfg.Provider
	}
	if b.cfg.APIKey(b.cfg.Provider, pc) != "" || bridgeProviderIsLocal(b.cfg.Provider, pc) {
		return ""
	}
	return b.cfg.Provider
}

func bridgeProviderIsLocal(name string, pc *config.Provider) bool {
	if pc == nil {
		return false
	}
	if preset, ok := config.PresetByName(name); ok && preset.Local {
		return true
	}
	base := strings.ToLower(pc.BaseURL)
	return strings.Contains(base, "localhost") || strings.Contains(base, "127.0.0.1") ||
		strings.Contains(base, "0.0.0.0") || strings.Contains(base, "[::1]")
}

func bridgeCommands() []bridgeCommand {
	out := make([]bridgeCommand, 0, len(commands.List))
	for _, c := range commands.List {
		item := bridgeCommand{Name: c.Name, Args: c.Args, Summary: c.Summary, Group: c.Group}
		for _, o := range c.Options {
			item.Options = append(item.Options, bridgeOption{Value: o.Value, Summary: o.Summary})
		}
		out = append(out, item)
	}
	return out
}

func (b *bridge) historyEntries() []map[string]any {
	if b.sess == nil {
		return nil
	}
	var out []map[string]any
	var pending []map[string]any
	for i, m := range b.sess.Messages {
		switch m.Role {
		case "user":
			if i == 0 && strings.HasPrefix(m.Content, "Today: ") {
				continue
			}
			if strings.HasPrefix(m.Content, "[Conversation summary]") {
				out = append(out, map[string]any{
					"kind": "note", "text": "context was compacted (summary above kept verbatim)",
				})
				continue
			}
			out = append(out, map[string]any{
				"kind": "user", "text": m.Content, "attachments": m.Attachments,
			})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" || strings.TrimSpace(m.Reasoning) != "" {
				out = append(out, map[string]any{
					"kind": "assistant", "text": m.Content, "reasoning": m.Reasoning,
				})
			}
			for _, call := range m.ToolCalls {
				entry := map[string]any{
					"kind": "tool", "name": call.Name, "detail": bridgeToolArgsPreview(call.Arguments),
					"result": "", "ok": true, "duration_ms": 0,
				}
				out = append(out, entry)
				pending = append(pending, entry)
			}
		case "tool":
			if len(pending) == 0 {
				continue
			}
			entry := pending[0]
			pending = pending[1:]
			entry["result"] = sanitizeBridge(m.Content)
			entry["ok"] = !strings.HasPrefix(m.Content, "error:")
		}
	}
	return out
}

func bridgeToolArgsPreview(raw string) string {
	var args map[string]any
	if json.Unmarshal([]byte(raw), &args) == nil {
		for _, key := range []string{"command", "path", "file_path", "pattern", "query", "glob"} {
			if value, ok := args[key].(string); ok && value != "" {
				return value
			}
		}
	}
	return clipLine(strings.Join(strings.Fields(raw), " "), 90)
}

func (b *bridge) todoItems() []todo.Item {
	if b.ag != nil && b.ag.Env != nil {
		return b.ag.Env.Todos()
	}
	if b.sess != nil {
		return b.sess.Todos()
	}
	return nil
}

func (b *bridge) emitTodos() {
	b.send(map[string]any{"type": "todos", "items": b.todoItems()})
}

func (b *bridge) ensureSession() error {
	if b.sess != nil {
		return nil
	}
	s, err := session.Create(b.cwd, b.cfg.Provider, b.cfg.ActiveModel(), "")
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.sess = s
	b.mu.Unlock()
	b.emitState()
	return nil
}

func (b *bridge) ensureAgent() error {
	if b.ag != nil {
		return nil
	}
	if err := b.ensureSession(); err != nil {
		return err
	}
	if name := b.missingKey(); name != "" {
		return fmt.Errorf("no API key configured for %s — run /configure %s", name, name)
	}
	b.opts.model = b.sess.Meta.Model
	b.opts.provider = b.sess.Meta.Provider
	b.opts.mode = b.mode
	b.opts.modeSet = true
	if b.opts.agents == nil {
		b.opts.agents = append([]string(nil), b.sess.Meta.Agents...)
	}
	ag, err := buildAgent(b.cfg, b.opts, b.sess, b.prompt)
	if err != nil {
		return err
	}
	ag.Hooks = agent.Hooks{
		OnText: func(s string) {
			b.send(map[string]any{"type": "text_delta", "text": sanitizeBridge(s)})
		},
		OnReasoning: func(s string) {
			b.send(map[string]any{"type": "reasoning_delta", "text": sanitizeBridge(s)})
		},
		OnTool: func(name, detail string) {
			b.send(map[string]any{"type": "tool_start", "name": name, "detail": sanitizeBridge(detail)})
			b.maybePreviewFromDetail(name, detail)
		},
		OnToolResult: func(name, result string, dur time.Duration) {
			b.send(map[string]any{
				"type": "tool_end", "name": name, "result": sanitizeBridge(result),
				"duration_ms": dur.Milliseconds(), "ok": !strings.HasPrefix(result, "error:"),
			})
			b.detectPreview(name, result)
			b.emitTodos()
		},
		OnUsage: func(u llm.Usage, history int) {
			b.mu.Lock()
			b.cumIn += u.In
			b.cumOut += u.Out
			b.cumCached += u.Cached
			if history >= 0 {
				b.history = history
			}
			in, out, cached, hist, window := b.cumIn, b.cumOut, b.cumCached, b.history, b.window
			b.mu.Unlock()
			b.send(map[string]any{"type": "usage", "in": in, "out": out, "cached": cached, "history": hist, "window": window})
		},
		OnCompact: func(kind string, before, after int) {
			b.note(fmt.Sprintf("%s compaction: %s → %s tokens", kind, humanTokens(before), humanTokens(after)), false)
		},
		OnReset: func() {
			b.send(map[string]any{"type": "assistant_reset"})
		},
		OnRetry: func(attempt int, err error, delay time.Duration) {
			b.send(map[string]any{"type": "retry", "attempt": attempt, "message": err.Error(), "delay_ms": delay.Milliseconds()})
		},
	}
	if ag.Env != nil {
		prev := ag.Env.OnTodosChanged
		ag.Env.OnTodosChanged = func(items []todo.Item) {
			if prev != nil {
				prev(items)
			}
			b.emitTodos()
		}
	}
	b.mu.Lock()
	b.ag = ag
	if ag.ContextWindow > 0 {
		b.window = ag.ContextWindow
	}
	b.history = ag.HistoryTokens()
	b.mu.Unlock()
	b.emitState()
	return nil
}

func (b *bridge) rebuildAgent() error {
	b.closeAgent()
	return b.ensureAgent()
}

func (b *bridge) closeAgent() {
	b.mu.Lock()
	ag := b.ag
	b.ag = nil
	b.mu.Unlock()
	if ag == nil {
		return
	}
	if ag.Trace != nil {
		_ = ag.Trace.Close()
	}
	if ag.Env != nil {
		ag.Env.JobManager().Close()
		ag.Env.CloseLSP()
		ag.Env.CloseBrowser()
		ag.Env.CloseMCP()
	}
}

func (b *bridge) interrupt() {
	b.mu.Lock()
	if b.stop != nil && !b.interrupting {
		b.interrupting = true
		b.stop()
	}
	b.mu.Unlock()
}

func (b *bridge) run(text string) {
	if strings.TrimSpace(text) == "" && len(b.atts) == 0 {
		return
	}
	b.runMu.Lock()
	defer b.runMu.Unlock()

	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		b.note("a run is already in progress — esc to interrupt", true)
		return
	}
	text = sanitizeBridge(text)
	b.mu.Unlock()

	if looksLikeCommand(text) {
		b.command(text)
		return
	}
	if err := b.ensureAgent(); err != nil {
		b.note(err.Error(), true)
		return
	}

	text, mentions := llm.ParseMentions(text)
	atts := append([]llm.Attachment(nil), b.atts...)
	b.mu.Lock()
	b.atts = nil
	b.mu.Unlock()

	for _, path := range mentions {
		a, err := llm.NewAttachment(path)
		if err != nil {
			b.note("error: "+err.Error(), true)
			return
		}
		atts = append(atts, a)
	}
	text = strings.TrimSpace(text)
	if text == "" && len(atts) == 0 {
		return
	}
	if b.sess.Meta.Title == "" {
		title := text
		if title == "" && len(atts) > 0 {
			title = atts[0].Name
		}
		b.sess.Meta.Title = clipLine(title, 80)
		_ = b.sess.Flush()
	}
	stored := make([]llm.Attachment, 0, len(atts))
	for _, a := range atts {
		s, err := b.sess.StoreAttachment(a.Path)
		if err != nil {
			b.note("error: "+err.Error(), true)
			return
		}
		stored = append(stored, s)
	}

	meta := make([]map[string]any, 0, len(stored))
	for _, a := range stored {
		meta = append(meta, map[string]any{"kind": a.Kind, "name": a.Name, "size": a.Size})
	}
	b.send(map[string]any{"type": "user", "text": text, "attachments": meta})
	b.send(map[string]any{"type": "assistant_start"})
	b.mu.Lock()
	b.running = true
	b.interrupting = false
	ctx, cancel := context.WithCancel(context.Background())
	b.stop = cancel
	start := time.Now()
	b.mu.Unlock()
	b.emitState()

	err := b.ag.RunWithAttachments(ctx, text, stored)

	b.mu.Lock()
	b.running = false
	b.interrupting = false
	b.stop = nil
	b.mu.Unlock()
	cancel()
	_ = b.sess.Flush()

	b.send(map[string]any{"type": "assistant_end", "duration_ms": time.Since(start).Milliseconds()})
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		b.note("interrupted", false)
	default:
		b.note("error: "+err.Error(), true)
	}
	if b.ag != nil {
		b.mu.Lock()
		b.history = b.ag.HistoryTokens()
		b.mu.Unlock()
	}
	b.emitTodos()
	b.emitState()
	b.mu.Lock()
	in, out, cached, hist, window := b.cumIn, b.cumOut, b.cumCached, b.history, b.window
	b.mu.Unlock()
	b.send(map[string]any{"type": "usage", "in": in, "out": out, "cached": cached, "history": hist, "window": window})
}

func (b *bridge) resolvePerm(req bridgeRequest) {
	b.mu.Lock()
	ch := b.permWait[req.ID]
	delete(b.permWait, req.ID)
	b.mu.Unlock()
	if ch == nil {
		return
	}
	d := tools.Decision{Allow: req.Allow, Always: req.Always, Feedback: req.Feedback}
	if !d.Allow && d.Feedback == "" {
		d.Feedback = declinedNote()
	}
	ch <- d
}

func (b *bridge) resolveQuestion(req bridgeRequest) {
	b.mu.Lock()
	ch := b.questionWait[req.ID]
	delete(b.questionWait, req.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- req.Answer
	}
}

func (b *bridge) permit(kind, detail, preview string) tools.Decision {
	if v, ok := tools.PermissionValue(b.cfg.Permissions, kind); ok {
		switch v {
		case "allow":
			return tools.Decision{Allow: true}
		case "deny":
			b.note("denied by config: "+kind, false)
			return tools.Decision{Feedback: declinedNote()}
		}
	}
	b.mu.Lock()
	if b.auto || b.always[kind] {
		b.mu.Unlock()
		return tools.Decision{Allow: true}
	}
	b.seq++
	id := fmt.Sprintf("perm-%d", b.seq)
	ch := make(chan tools.Decision, 1)
	b.permWait[id] = ch
	b.mu.Unlock()

	b.send(map[string]any{
		"type": "permission", "id": id, "kind": kind, "perm": string(tools.PermKindFor(kind)),
		"detail": sanitizeBridge(detail), "preview": sanitizeBridge(preview),
	})
	d := <-ch
	b.mu.Lock()
	if d.Always {
		b.always[kind] = true
	}
	b.mu.Unlock()
	return d
}

func (b *bridge) ask(q tools.Question) (string, error) {
	b.mu.Lock()
	b.seq++
	id := fmt.Sprintf("question-%d", b.seq)
	ch := make(chan string, 1)
	b.questionWait[id] = ch
	b.mu.Unlock()

	b.send(map[string]any{
		"type": "question", "id": id, "header": sanitizeBridge(q.Header),
		"question": sanitizeBridge(q.Question), "options": q.Options,
	})
	answer := <-ch
	return answer, nil
}

func (b *bridge) command(line string) {
	name := ""
	if parts := strings.Fields(strings.TrimSpace(line)); len(parts) > 0 {
		name = parts[0]
	}
	b.mu.Lock()
	running := b.running
	b.mu.Unlock()
	if running && !bridgeCommandSafeDuringRun(name) {
		b.note("a run is already in progress — esc to interrupt", true)
		return
	}
	b.cmdMu.Lock()
	defer b.cmdMu.Unlock()
	b.commandLocked(line)
}

func bridgeCommandSafeDuringRun(name string) bool {
	switch name {
	case "/exit", "/quit", "/q", "/help", "/keys", "/status", "/cost", "/todos",
		"/toolset", "/mcp", "/sources", "/history", "/previews", "/preview",
		"/theme", "/themes", "/mouse", "/yolo", "/thinking", "/copy", "/attach",
		"/detach", "/reference", "/ref", "/doctor", "/usage", "/skill", "/skills":
		return true
	}
	return false
}

func (b *bridge) commandLocked(line string) {
	line = strings.TrimSpace(sanitizeBridge(line))
	if line == "" {
		return
	}
	parts := strings.Fields(line)
	name := parts[0]
	arg := strings.TrimSpace(strings.TrimPrefix(line, name))

	switch name {
	case "/exit", "/quit", "/q":
		b.send(map[string]any{"type": "exit"})
		return
	case "/help", "/keys":
		b.send(map[string]any{"type": "text", "title": "help", "body": helpText()})
		return
	case "/theme", "/themes":
		if arg == "" {
			b.send(map[string]any{"type": "picker", "kind": "theme", "title": "themes", "value": b.cfg.Theme})
			return
		}
		b.cfg.Theme = arg
		_ = b.cfg.Save()
		b.send(map[string]any{"type": "theme", "name": arg})
		return
	case "/model":
		if arg == "" {
			b.listModels()
			return
		}
		b.setModel(arg)
		return
	case "/models":
		b.listModels()
		return
	case "/configure", "/provider", "/providers":
		if arg == "" {
			b.listProviders()
			return
		}
		b.openProviderForm(strings.Fields(arg)[0])
		return
	case "/sessions":
		b.listSessions()
		return
	case "/reasoning":
		if arg == "" {
			b.listReasoning()
			return
		}
		b.setReasoning(strings.ToLower(arg))
		return
	case "/thinking":
		b.setThinking(arg)
		return
	case "/yolo":
		b.mu.Lock()
		b.auto = !b.auto
		auto := b.auto
		b.mu.Unlock()
		b.note(fmt.Sprintf("auto-approve: %v", auto), false)
		b.emitState()
		return
	case "/mode", "/modes":
		b.setMode(line)
		return
	case "/status":
		b.send(map[string]any{"type": "text", "title": "status", "body": b.statusText()})
		return
	case "/cost":
		b.send(map[string]any{"type": "text", "title": "cost", "body": b.costText()})
		return
	case "/compact":
		b.compact()
		return
	case "/todos":
		if items := b.todoItems(); len(items) == 0 {
			b.note("no todos yet", false)
		} else {
			b.send(map[string]any{"type": "text", "title": "todos", "body": todo.Format(items)})
		}
		return
	case "/toolset":
		if b.ag == nil {
			b.send(map[string]any{"type": "text", "title": "tools", "body": "no agent yet — send a message first"})
			return
		}
		b.send(map[string]any{"type": "text", "title": "tools", "body": toolsetText(b.ag, b.mode, b.cfg.Permissions)})
		return
	case "/mcp":
		b.send(map[string]any{"type": "text", "title": "mcp", "body": b.mcpText()})
		return
	case "/sources":
		b.send(map[string]any{"type": "text", "title": "sources", "body": sourcesText(b.sess)})
		return
	case "/history":
		b.send(map[string]any{"type": "text", "title": "history", "body": historyText(b.sess)})
		return
	case "/clear":
		b.send(map[string]any{"type": "clear"})
		return
	case "/new":
		b.newSession()
		return
	case "/attach":
		b.attach(arg)
		return
	case "/detach":
		b.detach(arg)
		return
	case "/paste":
		b.note("paste an image into the terminal, or use /attach <path>", false)
		return
	case "/previews":
		b.listPreviews()
		return
	case "/preview":
		b.openPreview(arg)
		return
	case "/reference", "/ref":
		b.reference(arg)
		return
	case "/agents", "/agent", "/team":
		b.agentsCommand(arg)
		return
	case "/skill", "/skills":
		b.skillsCommand(arg)
		return
	case "/mouse":
		b.note("mouse selection is native in this interface — drag to select and the text is copied", false)
		return
	case "/setup":
		b.send(map[string]any{"type": "setup"})
		return
	case "/doctor":
		go b.doctor()
		return
	case "/usage":
		b.send(map[string]any{"type": "text", "title": "usage", "body": b.usageText()})
		return
	}
	b.send(map[string]any{"type": "toast", "message": "unknown command " + name + " — /help", "error": true})
}

func (b *bridge) setMode(line string) {
	newMode, msg, changed, err := mode.Apply(line, b.mode)
	if err != nil {
		b.note(err.Error(), true)
		return
	}
	if !changed {
		b.note(msg, false)
		return
	}
	b.mu.Lock()
	b.mode = newMode
	b.opts.mode = newMode
	b.opts.modeSet = true
	b.mu.Unlock()
	if err := b.rebuildAgent(); err != nil {
		b.note("could not switch mode: "+err.Error(), true)
		return
	}
	b.note(msg, false)
	b.emitState()
}

func (b *bridge) setThinking(arg string) {
	settings, msg, changed, err := commands.ThinkingCommand("/thinking "+arg, b.showThinking)
	if err != nil {
		b.note(err.Error(), true)
		return
	}
	if changed {
		b.showThinking = settings
		b.cfg.ShowThinking = settings
		_ = b.cfg.Save()
	}
	b.note(msg, false)
	b.emitState()
}

func (b *bridge) setReasoning(level string) {
	if level == "" {
		level = "medium"
	}
	valid := false
	for _, l := range []string{"off", "low", "medium", "high", "max"} {
		if strings.EqualFold(l, level) {
			level = l
			valid = true
		}
	}
	if !valid {
		b.note("reasoning must be off, low, medium, high, or max", true)
		return
	}
	b.cfg.Reasoning = level
	_ = b.cfg.Save()
	if err := b.rebuildAgent(); err != nil {
		b.note("could not apply reasoning: "+err.Error(), true)
	}
	b.note("reasoning: "+level, false)
	b.emitState()
}

func (b *bridge) listReasoning() {
	levels := []string{"off", "low", "medium", "high", "max"}
	current := strings.ToLower(strings.TrimSpace(b.cfg.Reasoning))
	if current == "" {
		current = "medium"
	}
	var items []bridgeListItem
	for _, l := range levels {
		items = append(items, bridgeListItem{Label: l, Value: l, Current: l == current})
	}
	b.send(map[string]any{"type": "list", "id": "reasoning", "title": "reasoning", "subtitle": "choose how much the model thinks", "action": "set_reasoning", "items": items})
}

func (b *bridge) setModel(name string) {
	if name == "" {
		b.listModels()
		return
	}
	b.cfg.Model = name
	_ = b.cfg.Save()
	if b.sess != nil {
		b.sess.Meta.Model = name
		_ = b.sess.Flush()
	}
	if err := b.rebuildAgent(); err != nil {
		b.note("could not switch model: "+err.Error(), true)
		return
	}
	b.note("model: "+b.cfg.Provider+"/"+name, false)
	b.emitState()
	b.maybeReasoning()
}

func (b *bridge) listModels() {
	provider := b.cfg.Provider
	pc := b.cfg.Providers[provider]
	if pc == nil || provider == "" {
		b.note("no provider configured — run /configure", true)
		return
	}
	b.send(map[string]any{"type": "loading", "message": "loading models from " + provider + "…"})
	go func() {
		prov, pname, sel, err := buildProvider(context.Background(), b.cfg, provider, "")
		if err != nil {
			b.note("could not load models: "+err.Error(), true)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		models, err := prov.ListModels(ctx)
		if err != nil {
			if pc := b.cfg.Providers[pname]; pc != nil && len(pc.Models) > 0 {
				models = append([]string(nil), pc.Models...)
			} else {
				b.note("could not load models: "+err.Error(), true)
				return
			}
		}
		sort.Strings(models)
		if len(models) == 0 {
			b.note("the provider returned no models — try /model <name>", true)
			return
		}
		if len(models) > 0 {
			pc := b.cfg.Providers[pname]
			if pc != nil {
				pc.Models = models
				_ = b.cfg.Save()
			}
		}
		items := make([]bridgeListItem, 0, len(models))
		for _, m := range models {
			detail := ""
			if w := tokens.WindowFor(pname, m); w > 0 {
				detail = "context " + humanTokens(w)
			}
			items = append(items, bridgeListItem{Label: m, Detail: detail, Value: m, Current: m == sel})
		}
		b.send(map[string]any{"type": "list", "id": "models", "title": "models", "subtitle": pname, "action": "set_model", "items": items})
	}()
}

func (b *bridge) maybeReasoning() {
	info, ok := modelinfo.Lookup(b.cfg.Provider, b.cfg.Model)
	supported := ok && info.Reasoning
	if !ok {
		lower := strings.ToLower(b.cfg.Model)
		supported = strings.Contains(lower, "o1") || strings.Contains(lower, "o3") ||
			strings.Contains(lower, "o4") || strings.Contains(lower, "gpt-5") || strings.Contains(lower, "reason")
	}
	if supported {
		b.listReasoning()
	}
}

func (b *bridge) listProviders() {
	names := map[string]bool{}
	for _, p := range config.Presets {
		names[p.Name] = true
	}
	for name := range b.cfg.Providers {
		if name != "" {
			names[name] = true
		}
	}
	var list []string
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	items := make([]bridgeListItem, 0, len(list))
	for _, name := range list {
		pc := b.cfg.Providers[name]
		detail := ""
		if pc != nil {
			detail = pc.Kind + " · " + pc.BaseURL
		}
		items = append(items, bridgeListItem{Label: name, Detail: detail, Value: name, Current: name == b.cfg.Provider})
	}
	b.send(map[string]any{"type": "list", "id": "providers", "title": "providers", "subtitle": "choose a provider to configure", "action": "configure_provider", "items": items})
}

func (b *bridge) openProviderForm(name string) {
	pc := b.cfg.Providers[name]
	base := ""
	if pc != nil {
		base = pc.BaseURL
	}
	if base == "" {
		if preset, ok := config.PresetByName(name); ok {
			base = preset.Base
		}
	}
	b.send(map[string]any{
		"type": "form", "id": "provider", "title": "configure " + name,
		"action": "save_provider",
		"fields": []map[string]any{
			{"name": "base_url", "label": "base URL", "value": base},
			{"name": "api_key", "label": "API key", "value": "", "secret": true},
		},
		"meta": map[string]string{"provider": name},
	})
}

func (b *bridge) listSessions() {
	metas, err := session.List(b.cwd)
	if err != nil {
		b.note("could not list sessions: "+err.Error(), true)
		return
	}
	if len(metas) == 0 {
		b.note("no sessions for this directory yet", false)
		return
	}
	items := make([]bridgeListItem, 0, len(metas))
	for _, m := range metas {
		title := m.Title
		if title == "" {
			title = "(untitled)"
		}
		items = append(items, bridgeListItem{
			Label: title, Detail: m.Updated.Format("2006-01-02 15:04") + " · " + m.Model,
			Value: m.ID, Current: b.sess != nil && b.sess.ID() == m.ID,
		})
	}
	b.send(map[string]any{"type": "list", "id": "sessions", "title": "sessions", "subtitle": b.cwd, "action": "open_session", "items": items})
}

func (b *bridge) openSession(id string) {
	s, err := session.Open(id, b.cwd)
	if err != nil {
		b.note("could not open session: "+err.Error(), true)
		return
	}
	b.closeAgent()
	b.mu.Lock()
	b.sess = s
	b.cumIn, b.cumOut, b.cumCached, b.history = 0, 0, 0, 0
	if s.Meta.Provider != "" {
		b.cfg.Provider = s.Meta.Provider
	}
	if s.Meta.Model != "" {
		b.cfg.Model = s.Meta.Model
	}
	b.opts.agents = append([]string(nil), s.Meta.Agents...)
	b.window = tokens.WindowFor(b.cfg.Provider, b.cfg.ActiveModel())
	b.mu.Unlock()
	b.send(map[string]any{"type": "clear"})
	b.send(map[string]any{"type": "history", "items": b.historyEntries()})
	b.emitState()
	b.emitTodos()
}

func (b *bridge) newSession() {
	b.closeAgent()
	s, err := session.Create(b.cwd, b.cfg.Provider, b.cfg.ActiveModel(), "")
	if err != nil {
		b.note("could not create session: "+err.Error(), true)
		return
	}
	b.mu.Lock()
	b.sess = s
	b.cumIn, b.cumOut, b.cumCached, b.history = 0, 0, 0, 0
	b.opts.agents = nil
	b.mu.Unlock()
	b.send(map[string]any{"type": "clear"})
	b.emitState()
	b.emitTodos()
}

func (b *bridge) attach(path string) {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		b.note("usage: /attach <path> — or type @path in a message", true)
		return
	}
	a, err := llm.NewAttachment(path)
	if err != nil {
		b.note("error: "+err.Error(), true)
		return
	}
	b.mu.Lock()
	b.atts = append(b.atts, a)
	b.mu.Unlock()
	b.note(fmt.Sprintf("attached %s (%s, %s)", a.Name, a.Kind, llm.HumanSize(a.Size)), false)
	b.emitState()
}

func (b *bridge) detach(arg string) {
	b.mu.Lock()
	if len(b.atts) == 0 {
		b.mu.Unlock()
		b.note("no pending attachments", true)
		return
	}
	if arg == "" || arg == "all" {
		b.atts = nil
		b.mu.Unlock()
		b.emitState()
		b.note("detached all", false)
		return
	}
	name := ""
	for i, a := range b.atts {
		if a.Name == arg || a.Path == arg || filepath.Base(a.Path) == filepath.Base(arg) {
			name = a.Name
			b.atts = append(b.atts[:i], b.atts[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
	if name == "" {
		b.note("no attachment matches "+arg, true)
		return
	}
	b.emitState()
	b.note("detached "+name, false)
}

func (b *bridge) compact() {
	if b.ag == nil {
		b.note("send a message first, or configure a provider", true)
		return
	}
	b.note("compacting context…", false)
	ag := b.ag
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := ag.CompactNow(ctx); err != nil {
			b.note("error: "+err.Error(), true)
		} else {
			b.note("context compacted", false)
			b.emitState()
		}
	}()
}

func (b *bridge) statusText() string {
	sessionID := ""
	if b.sess != nil {
		sessionID = b.sess.ID()
	}
	return fmt.Sprintf("provider  %s\nmodel     %s\nsession   %s\ndir       %s\nbranch    %s\nmode      %s\nauto      %v\nreasoning %s\ncontext   %s / %s\npending   %d attachments",
		b.cfg.Provider, b.cfg.ActiveModel(), sessionID, b.cwd, tui.GitBranch(b.cwd),
		b.mode.Label(), b.auto, b.cfg.Reasoning, humanTokens(b.history), humanTokens(b.window), len(b.atts))
}

func (b *bridge) costText() string {
	return fmt.Sprintf("in %s · cache %s · out %s\ncontext %s / %s",
		humanTokens(b.cumIn), humanTokens(b.cumCached), humanTokens(b.cumOut), humanTokens(b.history), humanTokens(b.window))
}

func (b *bridge) usageText() string {
	metas, err := session.List(b.cwd)
	if err != nil || len(metas) == 0 {
		return "no sessions recorded for this project yet"
	}
	var sb strings.Builder
	sb.WriteString("recent sessions\n")
	for i, m := range metas {
		if i >= 10 {
			break
		}
		title := m.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&sb, "  %s  %s  %s\n", m.Updated.Format("2006-01-02 15:04"), m.Model, clipLine(title, 60))
	}
	sb.WriteString("\nthe full activity charts are in `wombat classic`")
	return sb.String()
}

func (b *bridge) mcpText() string {
	if b.ag == nil || b.ag.Env == nil {
		return "no agent yet — send a message first"
	}
	manager := b.ag.Env.MCPManager()
	if manager == nil || len(manager.Servers()) == 0 {
		return "no MCP servers configured\nadd one with: wombat mcp add <name> -- <command>"
	}
	var sb strings.Builder
	for _, server := range manager.Servers() {
		if err := server.Err(); err != nil {
			fmt.Fprintf(&sb, "✗ %s  %s\n", server.Name(), tools.RedactSecrets(err.Error()))
			continue
		}
		fmt.Fprintf(&sb, "✓ %s  %d tools", server.Name(), len(server.Tools()))
		if calls := server.Calls(); calls > 0 {
			fmt.Fprintf(&sb, " · %d calls", calls)
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (b *bridge) doctor() {
	exe, err := os.Executable()
	if err != nil {
		b.note("could not locate wombat: "+err.Error(), true)
		return
	}
	cmd := exec.Command(exe, "doctor")
	cmd.Dir = b.cwd
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	text := stripANSI(string(out))
	if err != nil && strings.TrimSpace(text) == "" {
		text = err.Error()
	}
	b.send(map[string]any{"type": "text", "title": "doctor", "body": text})
}

func (b *bridge) reference(arg string) {
	parts := strings.Fields(arg)
	sub := ""
	if len(parts) > 0 {
		sub = parts[0]
	}
	value := strings.TrimSpace(strings.TrimPrefix(arg, sub))
	switch sub {
	case "":
		b.listReferences()
	case "list", "ls":
		b.listReferences()
	case "clear":
		b.refs = nil
		if err := saveBridgeReferences(b.cwd, b.refs); err != nil {
			b.note("error: "+err.Error(), true)
			return
		}
		b.note("references cleared", false)
		b.emitState()
	case "rm", "remove", "delete":
		b.removeReference(value)
	default:
		b.addReference(value)
	}
}

func (b *bridge) listReferences() {
	if len(b.refs) == 0 {
		b.note("no references yet — /reference add <path|url>", false)
		return
	}
	var items []bridgeListItem
	for i, r := range b.refs {
		items = append(items, bridgeListItem{Label: r.Value, Detail: r.Kind, Value: r.Value, Icon: fmt.Sprintf("%d", i+1)})
	}
	b.send(map[string]any{"type": "list", "id": "references", "title": "references", "subtitle": "project notes", "items": items})
}

func (b *bridge) addReference(value string) {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	if value == "" {
		b.note("usage: /reference add <path|url>", true)
		return
	}
	for _, r := range b.refs {
		if r.Value == value {
			b.note("already a reference: "+value, true)
			return
		}
	}
	b.refs = append(b.refs, bridgeReference{Value: value, Kind: bridgeReferenceKind(b.cwd, value), Added: time.Now()})
	if err := saveBridgeReferences(b.cwd, b.refs); err != nil {
		b.note("error: "+err.Error(), true)
		return
	}
	b.note("reference added: "+value, false)
	b.emitState()
}

func (b *bridge) removeReference(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		b.note("usage: /reference rm <n|value>", true)
		return
	}
	for i, r := range b.refs {
		if r.Value == value || fmt.Sprint(i+1) == value {
			b.refs = append(b.refs[:i], b.refs[i+1:]...)
			if err := saveBridgeReferences(b.cwd, b.refs); err != nil {
				b.note("error: "+err.Error(), true)
				return
			}
			b.note("reference removed: "+r.Value, false)
			b.emitState()
			return
		}
	}
	b.note("no reference matches "+value, true)
}

func (b *bridge) agentsCommand(arg string) {
	arg = strings.TrimSpace(arg)
	loaded := agents.Load(b.cfg, config.Home(), b.cwd)
	usable := agents.Usable(loaded)
	if arg == "" || arg == "list" || arg == "ls" {
		selected := map[string]bool{}
		for _, name := range b.opts.agents {
			selected[name] = true
		}
		var items []bridgeListItem
		for _, def := range usable {
			items = append(items, bridgeListItem{Label: def.Name, Detail: def.Description, Value: def.Name, Current: selected[def.Name]})
		}
		if len(items) == 0 {
			b.note("no custom agents found — create one with `wombat agent new <name>`", false)
			return
		}
		b.send(map[string]any{"type": "list", "id": "agents", "title": "agents", "subtitle": "select the team for this session", "action": "toggle_agent", "items": items, "multi": true})
		return
	}
	if arg == "clear" || arg == "none" || arg == "off" {
		b.setAgents(nil)
		return
	}
	if arg == "all" {
		var names []string
		for _, def := range usable {
			names = append(names, def.Name)
		}
		b.setAgents(names)
		return
	}
	names := splitAgentFlags([]string{arg})
	available := map[string]bool{}
	for _, def := range usable {
		available[def.Name] = true
	}
	for _, name := range names {
		if !available[name] {
			b.note("unknown agent: "+name, true)
			return
		}
	}
	b.setAgents(names)
}

func (b *bridge) setAgents(names []string) {
	b.mu.Lock()
	b.opts.agents = names
	if b.sess != nil {
		b.sess.Meta.Agents = append([]string(nil), names...)
		_ = b.sess.Flush()
	}
	b.mu.Unlock()
	if err := b.rebuildAgent(); err != nil {
		b.note("could not apply agents: "+err.Error(), true)
		return
	}
	b.note(fmt.Sprintf("agents: %s", b.agentLabel()), false)
	b.emitState()
}

func (b *bridge) agentLabel() string {
	if len(b.opts.agents) == 0 {
		return "default"
	}
	return strings.Join(b.opts.agents, ", ")
}

func (b *bridge) skillsCommand(arg string) {
	skills, problems := agents.LoadSkills(config.Home(), b.cwd)
	if arg == "list" || arg == "ls" || arg == "" {
		var sb strings.Builder
		for _, s := range skills {
			fmt.Fprintf(&sb, "%s\n  %s\n", s.Name, clipLine(s.Description, 100))
		}
		for _, p := range problems {
			fmt.Fprintf(&sb, "! %s\n", p.String())
		}
		if sb.Len() == 0 {
			sb.WriteString("no skills installed")
		}
		b.send(map[string]any{"type": "text", "title": "skills", "body": strings.TrimRight(sb.String(), "\n")})
		return
	}
	b.send(map[string]any{"type": "text", "title": "skills", "body": "skills are discovered from ~/.wombat/skills and .wombat/skills"})
}

func (b *bridge) selectAction(action, value string) {
	b.cmdMu.Lock()
	defer b.cmdMu.Unlock()
	b.selectActionLocked(action, value)
}

func (b *bridge) selectActionLocked(action, value string) {
	switch action {
	case "set_model":
		b.setModel(value)
	case "open_session":
		b.openSession(value)
	case "configure_provider":
		b.openProviderForm(value)
	case "set_reasoning":
		b.setReasoning(value)
	case "open_preview":
		b.openPreview(value)
	case "toggle_agent":
		b.toggleAgent(value)
	}
}

func (b *bridge) formAction(action string, values, meta map[string]string) {
	b.cmdMu.Lock()
	defer b.cmdMu.Unlock()
	b.formActionLocked(action, values, meta)
}

func (b *bridge) formActionLocked(action string, values, meta map[string]string) {
	switch action {
	case "save_provider":
		name := meta["provider"]
		if name == "" {
			b.note("missing provider name", true)
			return
		}
		pc := b.cfg.Providers[name]
		if pc == nil {
			pc = &config.Provider{Kind: "openai"}
		}
		if v := strings.TrimSpace(values["base_url"]); v != "" {
			pc.BaseURL = v
		}
		if v := strings.TrimSpace(values["api_key"]); v != "" {
			pc.APIKey = v
		}
		if b.cfg.Providers == nil {
			b.cfg.Providers = map[string]*config.Provider{}
		}
		b.cfg.Providers[name] = pc
		b.cfg.Provider = name
		_ = b.cfg.Save()
		b.note("configured "+name, false)
		b.emitState()
		b.closeAgent()
		b.listModels()
	}
}

func (b *bridge) toggleAgent(name string) {
	has := false
	for _, n := range b.opts.agents {
		if n == name {
			has = true
		}
	}
	var names []string
	if has {
		for _, n := range b.opts.agents {
			if n != name {
				names = append(names, n)
			}
		}
	} else {
		names = append(names, b.opts.agents...)
		names = append(names, name)
	}
	b.setAgents(names)
	b.agentsCommand("list")
}

func (b *bridge) listPreviews() {
	if len(b.previews) == 0 {
		b.note("no previews detected yet", true)
		return
	}
	var items []bridgeListItem
	for i, p := range b.previews {
		items = append(items, bridgeListItem{Label: p.Label, Detail: p.Kind, Value: p.Ref, Icon: fmt.Sprintf("%d", i+1)})
	}
	b.send(map[string]any{"type": "list", "id": "previews", "title": "previews", "subtitle": "open something the agent produced", "action": "open_preview", "items": items})
}

func (b *bridge) openPreview(arg string) {
	if len(b.previews) == 0 {
		b.note("no previews detected yet", true)
		return
	}
	i := 0
	if arg != "" {
		fmt.Sscanf(arg, "%d", &i)
		i--
	}
	if i < 0 || i >= len(b.previews) {
		b.note("no preview matches "+arg, true)
		return
	}
	if err := openBridgePreview(b.previews[i]); err != nil {
		b.note("could not open preview: "+err.Error(), true)
		return
	}
	b.note("opened "+b.previews[i].Ref, false)
}

func (b *bridge) maybePreviewFromDetail(name, detail string) {
	switch toolBaseName(name) {
	case "write", "create", "edit", "multi_edit", "replace", "refactor", "rename":
	default:
		return
	}
	detail = strings.TrimSpace(strings.Trim(detail, `"'`))
	if detail == "" || strings.ContainsAny(detail, "\n\r\t{}") {
		return
	}
	if kind := previewKindForPath(detail); kind != "" {
		b.addPreview(bridgePreview{Ref: detail, Kind: kind, Label: detail, At: time.Now()})
	}
}

func (b *bridge) detectPreview(name, text string) {
	base := toolBaseName(name)
	switch base {
	case "browser", "screenshot":
		if ref := bridgePreviewURL.FindString(text); ref != "" {
			b.addPreview(bridgePreview{Ref: strings.TrimRight(ref, ".,;:!?)]}'\"·–—"), Kind: "site", Label: ref, At: time.Now()})
		}
	case "bash", "verify_site":
		if !bridgeServerBanner(text) {
			return
		}
		if ref := bridgePreviewURL.FindString(text); ref != "" {
			ref = strings.TrimRight(ref, ".,;:!?)]}'\"·–—")
			b.addPreview(bridgePreview{Ref: ref, Kind: "site", Label: ref, At: time.Now()})
		}
	}
}

func (b *bridge) addPreview(p bridgePreview) {
	for i, old := range b.previews {
		if old.Ref == p.Ref {
			b.previews = append(b.previews[:i], b.previews[i+1:]...)
			break
		}
	}
	b.previews = append([]bridgePreview{p}, b.previews...)
	if len(b.previews) > 12 {
		b.previews = b.previews[:12]
	}
	b.mu.Lock()
	b.previewURL = p.Ref
	b.mu.Unlock()
	b.send(map[string]any{"type": "previews", "items": b.previews, "current": p.Ref})
}

func bridgeServerBanner(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"local:", "ready in", "serving http", "listening on", "uvicorn running",
		"development server", "running at", "server running", "available at",
		"preview at", "compiled successfully",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func previewKindForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm", ".xhtml":
		return "html"
	case ".pdf":
		return "pdf"
	case ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp":
		return "image"
	case ".mp4", ".webm", ".mov", ".m4v":
		return "video"
	}
	return ""
}

func toolBaseName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func openBridgePreview(p bridgePreview) error {
	ref := p.Ref
	if strings.HasPrefix(ref, "file://") {
		ref = strings.TrimPrefix(ref, "file://")
	}
	var cmd *exec.Cmd
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		cmd = openCommand(ref)
	default:
		if !filepath.IsAbs(ref) {
			if abs, err := filepath.Abs(ref); err == nil {
				ref = abs
			}
		}
		cmd = openCommand(ref)
	}
	return cmd.Start()
}

func openCommand(target string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		return exec.Command("xdg-open", target)
	}
}

func sanitizeBridge(s string) string {
	// Strip terminal escapes. The UI is the only renderer now, so a tool
	// result must never smuggle control sequences into the opentui tree.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func skipEscape(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	case ']':
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(s)
	default:
		return i + 2
	}
}

func stripANSI(s string) string {
	return sanitizeBridge(s)
}

func bridgeReferenceKind(cwd, value string) string {
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return "site"
	}
	p := value
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	if st, err := os.Stat(p); err == nil {
		if st.IsDir() {
			return "dir"
		}
		return "file"
	}
	return "ref"
}

func bridgeReferencesPath(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err == nil {
		cwd = abs
	}
	sum := bridgeReferenceID(cwd)
	return filepath.Join(config.Home(), "references", sum+".json")
}

func loadBridgeReferences(cwd string) []bridgeReference {
	raw, err := os.ReadFile(bridgeReferencesPath(cwd))
	if err != nil {
		return nil
	}
	var refs []bridgeReference
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil
	}
	return refs
}

func saveBridgeReferences(cwd string, refs []bridgeReference) error {
	path := bridgeReferencesPath(cwd)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func bridgeReferenceID(s string) string {
	sum := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", sum[:8])
}
