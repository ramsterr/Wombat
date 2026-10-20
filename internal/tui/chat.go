package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/checkpoint"
	clipboard "github.com/ramsterr/Wombat/internal/clip"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/orchestrator"
	"github.com/ramsterr/Wombat/internal/sandbox"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/trace"
)

const helpLines = `/help             this list
/model [name]     switch model (no name: picker)
/provider [name]  switch provider (no name: picker)
/models           browse models
/sessions         browse sessions
/attach <path>    attach an image, video, audio, or file
/paste            attach an image from the clipboard (ctrl+v)
/detach [n|all]   remove pending attachments
/mouse            toggle mouse: wheel scroll vs text selection
/new              start a fresh session
/compact          compact context now
/cost             token usage
/status           show settings
/yolo             toggle auto-approve
/sandbox          sandbox and network control
/clear            clear the transcript view
/setup            reconfigure providers
/exit             quit`

type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
	entryTool
	entryNote
)

type chatEntry struct {
	kind      entryKind
	text      string
	reasoning string
	atts      []llm.Attachment
	name      string
	detail    string
	result    string
	dur       time.Duration
	running   bool
	ok        bool
	streaming bool
	isErr     bool
	pre       bool
}

type textMsg string
type reasoningMsg string

type toolStartMsg struct {
	name   string
	detail string
}

type toolDoneMsg struct {
	name   string
	result string
	dur    time.Duration
}

type usageMsg struct {
	u       llm.Usage
	history int
}

type compactMsg struct {
	kind   string
	before int
	after  int
}

type compactDoneMsg struct{ err error }

type runDoneMsg struct{ err error }

type clipImageMsg struct {
	path string
	err  error
}

type noteMsg struct {
	text  string
	isErr bool
}

type permAskMsg struct {
	kind   string
	detail string
	reply  chan tools.Decision
}

type chat struct {
	opt Options
	cfg *config.Config

	sess *session.Session
	ag   *agent.Agent

	provName  string
	modelName string

	sandbox      sandbox.Settings
	sandboxState *sandbox.State

	vp viewport.Model
	ta textarea.Model
	sp spinner.Model

	w, h       int
	inputLines int
	entries    []*chatEntry

	streaming    bool
	interrupting bool
	cancel       context.CancelFunc
	follow       bool
	perm         *permAskMsg
	permFeedback bool
	permInput    textinput.Model

	pending []llm.Attachment
	mouse   bool

	auto   bool
	always map[string]bool

	cumIn, cumOut, cumCached int
	history, window          int

	send func(tea.Msg)
}

func newChat(opt Options) (*chat, error) {
	if opt.Cwd == "" {
		opt.Cwd, _ = os.Getwd()
	}
	if opt.Cfg == nil {
		return nil, fmt.Errorf("no config loaded")
	}
	c := &chat{
		opt:    opt,
		cfg:    opt.Cfg,
		mouse:  opt.Mouse,
		always: map[string]bool{},
		follow: true,
	}
	c.provName = c.cfg.Provider
	c.modelName = c.cfg.ActiveModel()

	var err error
	if opt.SessionID != "" {
		c.sess, err = session.Open(opt.SessionID, opt.Cwd)
		if err != nil {
			return nil, err
		}
		if c.sess.Meta.Provider != "" {
			if _, ok := c.cfg.Providers[c.sess.Meta.Provider]; ok {
				c.provName = c.sess.Meta.Provider
			}
		}
		if c.sess.Meta.Model != "" {
			c.modelName = c.sess.Meta.Model
		}
	}

	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = "Ask anything, or /help"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 6
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ta.FocusedStyle.Prompt = styleAccent
	ta.FocusedStyle.Placeholder = styleFaint
	ta.FocusedStyle.Text = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = styleFaint
	ta.BlurredStyle.Placeholder = styleFaint
	ta.BlurredStyle.Text = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	c.ta = ta

	c.vp = viewport.New(80, 20)
	c.sp = spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(styleAccent))
	c.permInput = textinput.New()
	c.permInput.Placeholder = "tell wombat what to do instead…"
	c.permInput.Prompt = "› "
	c.permInput.CharLimit = 500
	c.permInput.PromptStyle = styleKey
	c.permInput.PlaceholderStyle = styleFaint

	if c.sess != nil {
		if err := c.rebuild(false); err != nil {
			return nil, err
		}
		c.loadHistory()
	}
	return c, nil
}

func (c *chat) Init() tea.Cmd {
	return c.ta.Focus()
}

func (c *chat) focusInput() tea.Cmd {
	c.layout()
	return c.ta.Focus()
}

func (c *chat) emit(msg tea.Msg) {
	defer func() { _ = recover() }()
	if c.send != nil {
		c.send(msg)
	}
}

func (c *chat) hooks() agent.Hooks {
	return agent.Hooks{
		OnText:      func(s string) { c.emit(textMsg(s)) },
		OnReasoning: func(s string) { c.emit(reasoningMsg(s)) },
		OnTool: func(name, detail string) {
			c.emit(toolStartMsg{name: name, detail: detail})
		},
		OnToolResult: func(name, result string, dur time.Duration) {
			c.emit(toolDoneMsg{name: name, result: result, dur: dur})
		},
		OnUsage: func(u llm.Usage, history int) {
			c.emit(usageMsg{u: u, history: history})
		},
		OnCompact: func(kind string, before, after int) {
			c.emit(compactMsg{kind: kind, before: before, after: after})
		},
	}
}

func (c *chat) rebuild(reset bool) error {
	if reset {
		c.provName = c.cfg.Provider
		c.modelName = c.cfg.ActiveModel()
	}
	if c.sess == nil {
		c.window = tokens.Window(c.modelName)
		return nil
	}
	if c.provName == "" {
		c.provName = c.cfg.Provider
	}
	if c.modelName == "" {
		c.modelName = c.cfg.ActiveModel()
	}
	if c.provName == "" {
		return fmt.Errorf("no provider configured — run `wombat setup`")
	}
	pc, ok := c.cfg.Providers[c.provName]
	if !ok {
		return fmt.Errorf("provider %q not found — run `wombat setup`", c.provName)
	}
	if c.modelName == "" {
		return fmt.Errorf("no model selected — pick one with /model")
	}
	prov, err := llm.New(c.provName, pc, c.cfg.APIKey(c.provName, pc))
	if err != nil {
		return err
	}
	cwd := c.sess.Meta.Cwd
	if cwd == "" {
		cwd = c.opt.Cwd
	}
	if c.sandbox.Mode == "" && c.sandbox.Network == false {
		mode, err := sandbox.Parse(c.cfg.Sandbox)
		if err != nil {
			return err
		}
		c.sandbox = sandbox.Settings{Mode: mode, Network: c.cfg.SandboxNetwork}
	}
	if c.sandboxState == nil {
		c.sandboxState = sandbox.NewState(c.sandbox)
	}
	env := &tools.Env{
		Dir:          cwd,
		Permit:       c.permit,
		Sandbox:      c.sandbox.Policy(cwd),
		SandboxState: c.sandboxState,
		DenyNetwork:  !c.sandbox.Network,
	}
	env.SandboxAsk = c.askSandbox
	if c.cfg.Checkpoints {
		cp, err := checkpoint.New(checkpoint.RootForSession(c.sess.Path()), cwd)
		if err != nil {
			return err
		}
		env.Checkpoint = cp
	}
	if c.ag != nil && c.ag.Trace != nil {
		_ = c.ag.Trace.Close()
	}
	c.ag = &agent.Agent{
		Provider:        prov,
		Model:           c.modelName,
		Tools:           tools.Registry(),
		Env:             env,
		Sess:            c.sess,
		MaxSteps:        c.cfg.MaxSteps,
		Compact:         true,
		ContextWindow:   tokens.Window(c.modelName),
		NumCtx:          pc.NumCtx,
		MaxTurnTokens:   c.cfg.MaxTurnTokens,
		MaxTurnCost:     c.cfg.MaxTurnCost,
		MaxTurnDuration: time.Duration(c.cfg.MaxTurnDuration) * time.Second,
		MaxToolCalls:    c.cfg.MaxToolCalls,
		Pricing:         agent.Pricing{InputPerMTok: c.cfg.PriceIn, CachedPerMTok: c.cfg.PriceCached, OutputPerMTok: c.cfg.PriceOut},
		SandboxNote:     c.sandbox.Note(),
	}
	if tr, err := trace.Open(strings.TrimSuffix(c.sess.Path(), ".jsonl") + ".events.jsonl"); err == nil {
		c.ag.Trace = tr
	}
	if c.cfg.Orchestrator {
		if _, err := orchestrator.Attach(c.ag, orchestrator.Config{
			MaxDelegations:        c.cfg.MaxDelegations,
			MaxParallelScouts:     c.cfg.MaxParallelScouts,
			WorkerMaxSteps:        c.cfg.WorkerMaxSteps,
			WorkerTokenBudget:     c.cfg.WorkerTokenBudget,
			TurnWorkerTokenBudget: c.cfg.TurnWorkerTokenBudget,
			MaxParallelWriters:    c.cfg.MaxParallelWriters,
			WriterMaxSteps:        c.cfg.WriterMaxSteps,
			WriterTokenBudget:     c.cfg.WriterTokenBudget,
			TurnWriterTokenBudget: c.cfg.TurnWriterTokenBudget,
			RequireVerification:   c.cfg.RequireVerification,
			EnableWriters:         c.cfg.Writers,
			KeepWorktrees:         c.cfg.KeepWorktrees,
		}); err != nil {
			return err
		}
	}
	c.ag.Hooks = c.hooks()
	c.window = c.ag.ContextWindow
	c.history = c.ag.HistoryTokens()
	c.sess.Meta.Provider = c.provName
	c.sess.Meta.Model = c.modelName
	_ = c.sess.Flush()
	return nil
}

func (c *chat) openSession(id string) error {
	if c.streaming {
		return fmt.Errorf("stop the current run first (esc)")
	}
	s, err := session.Open(id, c.opt.Cwd)
	if err != nil {
		return err
	}
	c.sess = s
	c.entries = nil
	c.cumIn, c.cumOut, c.cumCached, c.history = 0, 0, 0, 0
	if s.Meta.Provider != "" {
		if _, ok := c.cfg.Providers[s.Meta.Provider]; ok {
			c.provName = s.Meta.Provider
		}
	}
	if s.Meta.Model != "" {
		c.modelName = s.Meta.Model
	}
	buildErr := c.rebuild(false)
	c.loadHistory()
	c.follow = true
	c.layout()
	c.refresh()
	if buildErr != nil {
		c.note("error: "+buildErr.Error(), true)
	}
	return nil
}

func (c *chat) permit(kind, detail string) tools.Decision {
	if v, ok := c.cfg.Permissions[kind]; ok {
		switch v {
		case "allow":
			return tools.Decision{Allow: true}
		case "deny":
			c.emit(noteMsg{text: "[permission] " + kind + " is denied by config: " + clip(oneline(detail), 100), isErr: true})
			return tools.Decision{}
		}
	}
	if c.auto || c.always[kind] {
		return tools.Decision{Allow: true}
	}
	reply := make(chan tools.Decision, 1)
	if c.send == nil {
		return tools.Decision{}
	}
	c.emit(permAskMsg{kind: kind, detail: detail, reply: reply})
	select {
	case d := <-reply:
		return d
	case <-time.After(10 * time.Minute):
		return tools.Decision{}
	}
}

// currentSandbox returns the live sandbox settings of the session.
func (c *chat) currentSandbox() sandbox.Settings {
	if c.sandboxState != nil {
		return c.sandboxState.Settings()
	}
	return c.sandbox
}

// askSandbox asks the user whether a command blocked by the sandbox may run.
func (c *chat) askSandbox(kind, command string) tools.SandboxDecision {
	if c.send == nil {
		return tools.SandboxDeny
	}
	what := "network access"
	if kind == tools.SandboxWrite {
		what = "write access outside the sandbox"
	}
	reply := make(chan tools.Decision, 1)
	c.emit(permAskMsg{
		kind:   "sandbox: " + what,
		detail: "the sandbox blocked this command — " + command,
		reply:  reply,
	})
	select {
	case d := <-reply:
		switch {
		case d.Allow && d.Always:
			return tools.SandboxAllowSession
		case d.Allow:
			return tools.SandboxAllowOnce
		}
		return tools.SandboxDeny
	case <-time.After(10 * time.Minute):
		return tools.SandboxDeny
	}
}

func (c *chat) loadHistory() {
	c.entries = nil
	var pending []*chatEntry
	for i, m := range c.sess.Messages {
		switch m.Role {
		case "user":
			if i == 0 && strings.HasPrefix(m.Content, "Today: ") {
				continue
			}
			if strings.HasPrefix(m.Content, "[Conversation summary]") {
				c.entries = append(c.entries, &chatEntry{kind: entryNote, text: "context was compacted (summary above kept verbatim)"})
				continue
			}
			c.entries = append(c.entries, &chatEntry{kind: entryUser, text: m.Content, atts: m.Attachments})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" || m.Reasoning != "" {
				c.entries = append(c.entries, &chatEntry{kind: entryAssistant, text: m.Content, reasoning: m.Reasoning})
			}
			for _, tc := range m.ToolCalls {
				e := &chatEntry{kind: entryTool, name: tc.Name, detail: toolArgsPreview(tc.Arguments), running: true}
				c.entries = append(c.entries, e)
				pending = append(pending, e)
			}
		case "tool":
			if len(pending) == 0 {
				continue
			}
			e := pending[0]
			pending = pending[1:]
			e.running = false
			e.ok = !strings.HasPrefix(m.Content, "error:")
			e.result = clip(oneline(m.Content), 120)
			e.dur = 0
		}
	}
	c.follow = true
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "<1ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func toolArgsPreview(raw string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) == nil {
		for _, k := range []string{"command", "path", "file_path", "pattern", "query", "glob"} {
			if v, ok := m[k].(string); ok && v != "" {
				return v
			}
		}
	}
	return clip(oneline(raw), 90)
}

func (c *chat) resize(w, h int) {
	if w == c.w && h == c.h {
		return
	}
	c.w, c.h = w, h
	c.layout()
	c.refresh()
}

func (c *chat) layout() {
	if c.w <= 0 || c.h <= 0 {
		return
	}
	inner := c.w - 4
	if inner < 20 {
		inner = 20
	}
	c.ta.SetWidth(inner - 2)
	c.permInput.Width = inner - 2
	lines := 1
	if c.perm != nil {
		lines = 3
	} else {
		lines = strings.Count(c.ta.Value(), "\n") + 1
		if lines < 1 {
			lines = 1
		}
		if lines > 6 {
			lines = 6
		}
	}
	c.inputLines = lines
	c.ta.SetHeight(lines)
	extra := 0
	if len(c.pending) > 0 {
		extra = 1
	}
	vh := c.h - 1 - (lines + 2) - 1 - extra
	if vh < 3 {
		vh = 3
	}
	c.vp.Width = c.w
	c.vp.Height = vh
}

func (c *chat) refresh() {
	if c.w <= 0 || c.h <= 0 {
		return
	}
	if len(c.entries) == 0 {
		c.vp.SetContent(c.welcome())
	} else {
		var blocks []string
		for _, e := range c.entries {
			if lines := c.entryLines(e); len(lines) > 0 {
				blocks = append(blocks, strings.Join(lines, "\n"))
			}
		}
		content := strings.Join(blocks, "\n\n") + "\n"
		if pad := c.vp.Height - strings.Count(content, "\n"); pad > 0 {
			content = strings.Repeat("\n", pad) + content
		}
		c.vp.SetContent(content)
	}
	if c.follow {
		c.vp.GotoBottom()
	}
}

func (c *chat) welcome() string {
	cwd := c.opt.Cwd
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && !strings.HasPrefix(rel, "..") {
			cwd = "~/" + rel
		}
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + styleAccent.Bold(true).Render("◆ wombat") + "  " + styleDim.Render(c.opt.Version) + "\n\n")
	b.WriteString("  " + styleTitle.Render("a lightweight coding agent") + "\n\n")
	b.WriteString("  " + styleDim.Render("provider ") + c.provName + "/" + c.modelName + "\n")
	b.WriteString("  " + styleDim.Render("session  ") + c.sessionID() + "\n")
	b.WriteString("  " + styleDim.Render("dir      ") + clip(cwd, max(10, c.w-12)) + "\n\n")
	b.WriteString("  " + styleFaint.Render("type a message to begin · /help for commands") + "\n")
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (c *chat) note(text string, isErr bool) {
	c.entries = append(c.entries, &chatEntry{kind: entryNote, text: text, isErr: isErr})
	c.refresh()
}

func (c *chat) notePre(text string) {
	c.entries = append(c.entries, &chatEntry{kind: entryNote, text: text, pre: true})
	c.refresh()
}

func (c *chat) currentAssistant() *chatEntry {
	if n := len(c.entries); n > 0 {
		if e := c.entries[n-1]; e.kind == entryAssistant && e.streaming {
			return e
		}
	}
	e := &chatEntry{kind: entryAssistant, streaming: true}
	c.entries = append(c.entries, e)
	return e
}

func (c *chat) finishAssistant() {
	if n := len(c.entries); n > 0 {
		if e := c.entries[n-1]; e.kind == entryAssistant {
			e.streaming = false
		}
	}
}

func (c *chat) ensureSession() error {
	if c.sess != nil {
		return nil
	}
	s, err := session.Create(c.opt.Cwd, c.provName, c.modelName, "")
	if err != nil {
		return err
	}
	c.sess = s
	return c.rebuild(false)
}

func (c *chat) sessionID() string {
	if c.sess == nil {
		return "new"
	}
	return c.sess.ID()
}

func (c *chat) submit(text string) tea.Cmd {
	c.ta.Reset()
	c.layout()
	if looksLikeCommand(text) {
		cmd := c.command(text)
		c.refresh()
		return cmd
	}
	if c.autoAttachPath(text) {
		c.refresh()
		return nil
	}
	text, mentions := llm.ParseMentions(text)
	for _, path := range mentions {
		if err := c.addAttachment(path); err != nil {
			c.note("error: "+err.Error(), true)
			return nil
		}
	}
	text = strings.TrimSpace(text)
	if text == "" && len(c.pending) == 0 {
		return nil
	}
	if err := c.ensureSession(); err != nil {
		c.note("error: "+err.Error(), true)
		return nil
	}
	if c.ag == nil {
		c.note("no model configured — run `wombat setup`", true)
		return nil
	}
	if c.sess.Meta.Title == "" {
		title := oneline(text)
		if title == "" && len(c.pending) > 0 {
			title = c.pending[0].Name
		}
		c.sess.Meta.Title = clip(title, 80)
		_ = c.sess.Flush()
	}
	atts := make([]llm.Attachment, 0, len(c.pending))
	for _, a := range c.pending {
		stored, err := c.sess.StoreAttachment(a.Path)
		if err != nil {
			c.note("error: "+err.Error(), true)
			return nil
		}
		atts = append(atts, stored)
	}
	c.pending = nil
	c.layout()
	c.entries = append(c.entries, &chatEntry{kind: entryUser, text: text, atts: atts})
	c.entries = append(c.entries, &chatEntry{kind: entryAssistant, streaming: true})
	c.streaming = true
	c.follow = true
	c.refresh()

	ag := c.ag
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	return tea.Batch(func() tea.Msg {
		go func() { c.emit(runDoneMsg{err: ag.RunWithAttachments(ctx, text, atts)}) }()
		return nil
	}, c.sp.Tick)
}

func (c *chat) pasteImage() tea.Cmd {
	return func() tea.Msg {
		path, err := clipboard.Image()
		return clipImageMsg{path: path, err: err}
	}
}

func (c *chat) autoAttachPath(text string) bool {
	path, ok := llm.MediaPath(text)
	if !ok {
		return false
	}
	_ = c.addAttachment(path)
	return true
}

// looksLikeCommand distinguishes "/model" from an absolute file path.
func looksLikeCommand(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false
	}
	tok := line
	if i := strings.IndexAny(line, " \t\n"); i >= 0 {
		tok = line[:i]
	}
	return len(tok) > 1 && !strings.ContainsAny(tok[1:], "/.~")
}

func (c *chat) addAttachment(path string) error {
	a, err := llm.NewAttachment(path)
	if err != nil {
		return err
	}
	for _, p := range c.pending {
		if p.Path == a.Path {
			c.note("already attached: "+a.Name, false)
			return nil
		}
	}
	c.pending = append(c.pending, a)
	c.layout()
	c.refresh()
	c.note(fmt.Sprintf("attached %s (%s, %s)", a.Name, a.Kind, llm.HumanSize(a.Size)), false)
	return nil
}

func (c *chat) detach(arg string) {
	if len(c.pending) == 0 {
		c.note("no pending attachments", true)
		return
	}
	if arg == "" || arg == "all" {
		c.pending = nil
		c.note("detached all", false)
		c.layout()
		c.refresh()
		return
	}
	idx := -1
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(c.pending) {
			idx = n - 1
		}
	} else {
		for i, a := range c.pending {
			if a.Name == arg || a.Path == arg || filepath.Base(a.Path) == filepath.Base(arg) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		c.note("no attachment matches "+arg, true)
		return
	}
	name := c.pending[idx].Name
	c.pending = append(c.pending[:idx], c.pending[idx+1:]...)
	c.note("detached "+name, false)
	c.layout()
	c.refresh()
}

func (c *chat) interrupt() {
	if c.perm != nil {
		c.answerPerm(tools.Decision{})
	}
	if c.cancel == nil || c.interrupting {
		return
	}
	c.interrupting = true
	c.cancel()
}

func (c *chat) command(line string) tea.Cmd {
	parts := strings.Fields(line)
	name := parts[0]
	arg := strings.TrimSpace(strings.TrimPrefix(line, name))
	busy := func() bool {
		if c.streaming {
			c.note("stop the current run first (esc)", true)
			return true
		}
		return false
	}
	switch name {
	case "/exit", "/quit", "/q":
		if busy() {
			return nil
		}
		return exit("quit", "")
	case "/help":
		c.notePre(helpLines)
	case "/model":
		if busy() {
			return nil
		}
		if arg == "" {
			return openScreen(scrModels)
		}
		c.cfg.Model = arg
		_ = c.cfg.Save()
		c.provName = c.cfg.Provider
		c.modelName = arg
		if err := c.rebuild(false); err != nil {
			c.note("error: "+err.Error(), true)
		} else {
			c.note("model: "+c.provName+"/"+c.modelName, false)
		}
	case "/provider":
		if busy() {
			return nil
		}
		if arg == "" {
			return openScreen(scrModels)
		}
		if _, ok := c.cfg.Providers[arg]; !ok {
			c.note("provider "+arg+" not in config — run `wombat setup`", true)
			break
		}
		c.cfg.Provider = arg
		c.cfg.Model = ""
		_ = c.cfg.Save()
		c.provName = arg
		c.modelName = ""
		return openScreen(scrModels)
	case "/models":
		return openScreen(scrModels)
	case "/sessions":
		return openScreen(scrSessions)
	case "/paste":
		return c.pasteImage()
	case "/mouse":
		c.mouse = !c.mouse
		c.cfg.Mouse = c.mouse
		_ = c.cfg.Save()
		if c.mouse {
			c.note("mouse on — wheel scrolls; hold Option/Shift to select text", false)
			return func() tea.Msg { return tea.EnableMouseCellMotion() }
		}
		c.note("mouse off — select and copy normally; pgup/pgdn scroll", false)
		return func() tea.Msg { return tea.DisableMouse() }
	case "/attach":
		path := strings.TrimSpace(strings.Trim(arg, `"'`))
		if path == "" {
			c.note("usage: /attach <path> — or type @path in a message", true)
			break
		}
		if err := c.addAttachment(path); err != nil {
			c.note("error: "+err.Error(), true)
		}
	case "/detach":
		c.detach(arg)
	case "/doctor":
		return openScreen(scrDoctor)
	case "/usage":
		return openScreen(scrUsage)
	case "/new":
		if busy() {
			return nil
		}
		ns, err := session.Create(c.opt.Cwd, c.provName, c.modelName, "")
		if err != nil {
			c.note("error: "+err.Error(), true)
			break
		}
		c.sess = ns
		c.entries = nil
		c.pending = nil
		c.cumIn, c.cumOut, c.cumCached, c.history = 0, 0, 0, 0
		if err := c.rebuild(false); err != nil {
			c.note("error: "+err.Error(), true)
		}
		c.note("new session "+ns.ID(), false)
	case "/compact":
		if busy() || c.ag == nil {
			return nil
		}
		ag := c.ag
		c.note("compacting context…", false)
		return func() tea.Msg {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer cancel()
				c.emit(compactDoneMsg{err: ag.CompactNow(ctx)})
			}()
			return nil
		}
	case "/cost":
		c.note(fmt.Sprintf("in %s (cache %s) · out %s · context %s/%s",
			humanN(c.cumIn), humanN(c.cumCached), humanN(c.cumOut), humanN(c.history), humanN(c.window)), false)
	case "/status":
		cwd := c.opt.Cwd
		if c.sess != nil {
			cwd = c.sess.Meta.Cwd
		}
		c.note(fmt.Sprintf("provider=%s model=%s session=%s\ndir=%s\nauto-approve=%v\nsandbox: %s",
			c.provName, c.modelName, c.sessionID(), cwd, c.auto, c.currentSandbox().Status()), false)
	case "/yolo":
		c.auto = !c.auto
		c.note(fmt.Sprintf("auto-approve: %v", c.auto), false)
	case "/sandbox":
		current := c.sandbox
		if c.sandboxState != nil {
			current = c.sandboxState.Settings()
		}
		settings, msg, changed, err := sandbox.ApplyCommand(line, current)
		if err != nil {
			c.note(err.Error(), true)
			return nil
		}
		if !changed {
			c.note(msg, false)
			return nil
		}
		c.sandbox = settings
		if c.sandboxState != nil {
			c.sandboxState.Update(func(s *sandbox.Settings) { *s = settings })
		}
		if err := c.rebuild(false); err != nil {
			c.note("could not apply the sandbox change: "+err.Error(), true)
			return nil
		}
		c.note(msg, false)
	case "/clear":
		c.entries = nil
	case "/setup":
		if busy() {
			return nil
		}
		return exit("configure", "")
	default:
		c.note("unknown command "+name+" — /help", true)
	}
	return nil
}

func (c *chat) answerPerm(d tools.Decision) {
	p := c.perm
	c.perm = nil
	c.permFeedback = false
	c.permInput.Reset()
	c.permInput.Blur()
	if p == nil {
		return
	}
	if d.Always {
		c.always[p.kind] = true
	}
	p.reply <- d
	switch {
	case d.Allow && d.Always:
		c.note("always allowing "+p.kind, false)
	case d.Allow:
		c.note("allowed "+p.kind, false)
	case d.Feedback != "":
		c.note("denied "+p.kind+" — feedback sent", false)
	default:
		c.note("denied "+p.kind, false)
	}
	c.layout()
}

func (c *chat) updateAsync(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case textMsg, reasoningMsg, toolStartMsg, toolDoneMsg, usageMsg, compactMsg, compactDoneMsg, noteMsg, runDoneMsg:
		return c.update(msg)
	case spinner.TickMsg:
		if c.streaming {
			return c.update(msg)
		}
	}
	return nil
}

func (c *chat) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !c.streaming {
			return nil
		}
		var cmd tea.Cmd
		c.sp, cmd = c.sp.Update(msg)
		return cmd
	case textMsg:
		c.currentAssistant().text += string(msg)
		c.refresh()
		return nil
	case reasoningMsg:
		c.currentAssistant().reasoning += string(msg)
		c.refresh()
		return nil
	case toolStartMsg:
		c.finishAssistant()
		c.entries = append(c.entries, &chatEntry{kind: entryTool, name: msg.name, detail: msg.detail, running: true})
		c.refresh()
		return nil
	case toolDoneMsg:
		for i := len(c.entries) - 1; i >= 0; i-- {
			e := c.entries[i]
			if e.kind == entryTool && e.running {
				e.running = false
				e.ok = !strings.HasPrefix(msg.result, "error:")
				e.dur = msg.dur
				e.result = clip(oneline(msg.result), 120)
				break
			}
		}
		c.refresh()
		return nil
	case usageMsg:
		c.cumIn += msg.u.In
		c.cumOut += msg.u.Out
		c.cumCached += msg.u.Cached
		if msg.history >= 0 {
			c.history = msg.history
		}
		return nil
	case compactMsg:
		c.note(fmt.Sprintf("%s compaction: %s → %s tokens", msg.kind, humanN(msg.before), humanN(msg.after)), false)
		return nil
	case compactDoneMsg:
		if msg.err != nil {
			c.note("error: "+msg.err.Error(), true)
		}
		return nil
	case noteMsg:
		c.note(msg.text, msg.isErr)
		return nil
	case permAskMsg:
		p := msg
		c.perm = &p
		c.layout()
		c.refresh()
		return nil
	case runDoneMsg:
		c.streaming = false
		c.interrupting = false
		c.cancel = nil
		c.finishAssistant()
		switch {
		case msg.err == nil:
		case errors.Is(msg.err, context.Canceled):
			c.note("interrupted", false)
		default:
			c.note("error: "+msg.err.Error(), true)
		}
		if c.ag != nil {
			c.history = c.ag.HistoryTokens()
		}
		c.refresh()
		return nil
	case clipImageMsg:
		if msg.err != nil {
			if errors.Is(msg.err, clipboard.ErrNoImage) {
				c.note("no image in clipboard", false)
			} else {
				c.note("error: "+msg.err.Error(), true)
			}
			return nil
		}
		if err := c.addAttachment(msg.path); err != nil {
			c.note("error: "+err.Error(), true)
		}
		return nil
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			c.scrollUp(3)
		case tea.MouseButtonWheelDown:
			c.scrollDown(3)
		}
		return nil
	case tea.KeyMsg:
		return c.key(msg)
	}
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)
	c.layout()
	if c.follow {
		c.vp.GotoBottom()
	}
	return cmd
}

func (c *chat) key(msg tea.KeyMsg) tea.Cmd {
	if c.perm != nil {
		if c.permFeedback {
			switch msg.String() {
			case "ctrl+c":
				c.answerPerm(tools.Decision{})
				c.interrupt()
			case "esc":
				c.permFeedback = false
				c.permInput.Reset()
				c.permInput.Blur()
				c.layout()
				c.refresh()
			case "enter":
				text := strings.TrimSpace(c.permInput.Value())
				if text == "" {
					c.permFeedback = false
					c.permInput.Reset()
					c.permInput.Blur()
					c.layout()
					c.refresh()
					return nil
				}
				c.answerPerm(tools.Decision{Feedback: text})
			default:
				var cmd tea.Cmd
				c.permInput, cmd = c.permInput.Update(msg)
				return cmd
			}
			return nil
		}
		switch msg.String() {
		case "y", "Y":
			c.answerPerm(tools.Decision{Allow: true})
		case "a", "A":
			c.answerPerm(tools.Decision{Allow: true, Always: true})
		case "n", "N", "esc":
			c.answerPerm(tools.Decision{})
		case "f", "F":
			c.permFeedback = true
			c.permInput.Focus()
			c.layout()
			c.refresh()
		case "ctrl+c":
			c.answerPerm(tools.Decision{})
			c.interrupt()
		}
		return nil
	}
	switch msg.String() {
	case "ctrl+c":
		if c.streaming {
			c.interrupt()
			return nil
		}
		return exit("quit", "")
	case "esc":
		if c.streaming {
			c.interrupt()
		}
		return nil
	case "ctrl+v":
		return c.pasteImage()
	case "pgup", "ctrl+u":
		c.scrollUp(max(1, c.vp.Height-2))
		return nil
	case "pgdown", "ctrl+d":
		c.scrollDown(max(1, c.vp.Height-2))
		return nil
	case "up":
		if c.ta.Value() == "" {
			c.scrollUp(1)
			return nil
		}
	case "down":
		if c.ta.Value() == "" {
			c.scrollDown(1)
			return nil
		}
	case "enter":
		if c.streaming {
			return nil
		}
		text := strings.TrimSpace(c.ta.Value())
		if text == "" {
			return nil
		}
		return c.submit(text)
	}
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)
	c.layout()
	if c.follow {
		c.vp.GotoBottom()
	}
	return cmd
}

func (c *chat) scrollUp(n int) {
	c.follow = false
	c.vp.LineUp(n)
}

func (c *chat) scrollDown(n int) {
	c.vp.LineDown(n)
	if c.vp.AtBottom() {
		c.follow = true
	}
}

func (c *chat) header() string {
	left := " " + styleAccent.Bold(true).Render("◆ wombat")
	if c.streaming {
		left += " " + c.sp.View()
	}
	right := styleDim.Render(c.provName+"/"+c.modelName) + "  " + styleFaint.Render(c.sessionID())
	if c.w <= 0 {
		return left
	}
	gap := c.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		right = styleDim.Render(clip(c.provName+"/"+c.modelName, 30))
		gap = c.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	}
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (c *chat) hint() string {
	var left string
	switch {
	case c.perm != nil && c.permFeedback:
		left = styleDim.Render("enter send feedback · esc back")
	case c.perm != nil:
		left = styleWarn.Render("waiting for permission")
	case c.interrupting:
		left = styleWarn.Render("interrupting…")
	case c.streaming:
		left = styleDim.Render("esc interrupt · wheel/pgup scroll · working…")
	default:
		left = styleFaint.Render("enter send · alt+enter newline · ctrl+v image · pgup scroll · /help")
	}
	if c.cumIn+c.cumOut == 0 || c.w <= 0 {
		return " " + left
	}
	right := styleFaint.Render(fmt.Sprintf("ctx %s/%s", humanN(c.history), humanN(c.window)))
	gap := c.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return " " + left
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (c *chat) permView() string {
	p := c.perm
	title := styleWarn.Bold(true).Render("permission") + "  " + styleTitle.Render(p.kind)
	detail := styleDim.Render(clip(oneline(p.detail), max(10, c.w-16)))
	if c.permFeedback {
		return title + "\n" + detail + "\n" + c.permInput.View()
	}
	keys := styleKey.Render(" y ") + " allow   " +
		styleKey.Render(" a ") + " always " + p.kind + "   " +
		styleKey.Render(" n ") + " deny   " +
		styleKey.Render(" f ") + " feedback"
	return title + "\n" + detail + "\n" + keys
}

func (c *chat) view() string {
	if c.w <= 0 || c.h <= 0 {
		return ""
	}
	var box string
	if c.perm != nil {
		box = styleInputIdle.BorderForeground(cWarn).Width(c.w - 4).Render(c.permView())
	} else {
		style := styleInputIdle
		if !c.streaming {
			style = styleInputBox
		}
		box = style.Width(c.w - 4).Render(c.ta.View())
	}
	parts := []string{c.header(), c.vp.View()}
	if line := c.pendingView(); line != "" {
		parts = append(parts, line)
	}
	parts = append(parts, box, c.hint())
	return strings.Join(parts, "\n")
}

func (c *chat) pendingView() string {
	if len(c.pending) == 0 {
		return ""
	}
	var parts []string
	for i, a := range c.pending {
		if i >= 3 {
			parts = append(parts, styleFaint.Render(fmt.Sprintf("+%d", len(c.pending)-i)))
			break
		}
		parts = append(parts, styleKey.Render(fmt.Sprintf(" %d ", i+1))+
			" "+styleDim.Render(a.Kind)+" "+clip(a.Name, 28))
	}
	return " " + styleDim.Render("attached:") + " " + strings.Join(parts, "  ") +
		styleFaint.Render("  · /detach")
}

func (c *chat) entryLines(e *chatEntry) []string {
	w := c.w - 6
	if w < 20 {
		w = 20
	}
	switch e.kind {
	case entryUser:
		lines := []string{" " + styleAccent.Bold(true).Render("you")}
		for _, ln := range wrap(e.text, w) {
			lines = append(lines, "   "+ln)
		}
		for _, a := range e.atts {
			lines = append(lines, "   "+styleFaint.Render("↳ "+a.Kind+" · "+a.Name+" · "+llm.HumanSize(a.Size)))
		}
		return lines
	case entryAssistant:
		lines := []string{" " + styleAccent.Bold(true).Render("◆ wombat")}
		if e.reasoning != "" {
			lines = append(lines, "   "+styleFaint.Render("thinking"))
			for _, ln := range wrap(e.reasoning, max(10, w-2)) {
				lines = append(lines, "   "+styleThought.Render(ln))
			}
		}
		var body []string
		if strings.TrimSpace(e.text) != "" {
			body = renderMarkdown(e.text, w)
		}
		if len(body) == 0 && e.reasoning == "" {
			if !e.streaming {
				return nil
			}
			body = []string{styleFaint.Render("thinking…")}
		}
		if len(body) > 0 {
			if e.reasoning != "" {
				lines = append(lines, "")
			}
			for _, ln := range body {
				lines = append(lines, "   "+ln)
			}
		}
		if e.streaming {
			lines[len(lines)-1] += styleAccent.Render("▍")
		}
		return lines
	case entryTool:
		head := " "
		switch {
		case e.running:
			head += c.sp.View() + " "
		case e.ok:
			head += styleGood.Render("✓") + " "
		default:
			head += styleBad.Render("✗") + " "
		}
		head += styleTitle.Render(e.name)
		if e.detail != "" {
			head += "  " + styleDim.Render(clip(oneline(e.detail), max(8, w-12)))
		}
		lines := []string{head}
		if !e.running {
			meta := styleFaint.Render("→ " + fmtDuration(e.dur))
			if e.result != "" {
				meta += styleFaint.Render(" · " + clip(e.result, max(8, w-24)))
			}
			lines = append(lines, "   "+meta)
		}
		return lines
	default:
		st := styleFaint
		if e.isErr {
			st = styleBad
		}
		var out []string
		if e.pre {
			for _, ln := range strings.Split(e.text, "\n") {
				out = append(out, " "+st.Render(clip(ln, w+4)))
			}
			return out
		}
		for _, ln := range wrap(e.text, w) {
			out = append(out, " "+st.Render(ln))
		}
		return out
	}
}

func inlineStyle(s string) string {
	parts := strings.Split(s, "`")
	if len(parts) >= 3 {
		for i := range parts {
			if i%2 == 1 {
				parts[i] = styleCode.Render(parts[i])
			}
		}
		s = strings.Join(parts, "")
	}
	parts = strings.Split(s, "**")
	if len(parts) >= 3 {
		for i := range parts {
			if i%2 == 1 {
				parts[i] = styleTitle.Render(parts[i])
			}
		}
		s = strings.Join(parts, "")
	}
	return s
}

func renderMarkdown(s string, width int) []string {
	var out []string
	inCode := false
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			if !inCode {
				out = append(out, "")
			}
			continue
		}
		if inCode {
			out = append(out, styleCode.Render(" "+clip(line, width-2)))
			continue
		}
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			out = append(out, styleTitle.Render(strings.TrimSpace(strings.TrimLeft(trimmed, "#"))))
			continue
		}
		prefix := ""
		body := trimmed
		switch {
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "):
			prefix, body = "• ", trimmed[2:]
		case strings.HasPrefix(trimmed, "> "):
			prefix, body = "│ ", trimmed[2:]
		}
		for i, ln := range wrap(body, max(8, width-2-len(prefix))) {
			ln = inlineStyle(ln)
			if i == 0 {
				out = append(out, " "+styleDim.Render(prefix)+ln)
			} else {
				out = append(out, "  "+strings.Repeat(" ", len(prefix))+ln)
			}
		}
	}
	return out
}
