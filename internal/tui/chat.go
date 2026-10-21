package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/ramsterr/Wombat/internal/commands"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/orchestrator"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tokens"
	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/trace"
)

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

type transcriptDoneMsg struct{ err error }

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
	perm   tools.PermKind
	detail string
	reply  chan tools.Decision
}

// questionMsg asks the user to choose between options, the way opencode
// does. The answer comes back as free text: a chosen option's label, or
// whatever the user typed.
type questionMsg struct {
	question tools.Question
	reply    chan string
}

// permColor returns the colour that marks a permission kind. Requests that
// reach the network or go outside the workspace stand out.
func permColor(kind tools.PermKind) lipgloss.TerminalColor {
	switch kind {
	case tools.PermNetwork:
		return cSandbox
	case tools.PermOutside:
		return cOutside
	case tools.PermDangerous:
		return cRed
	}
	return cWarn
}

// permTitle names the prompt.
func permTitle(kind tools.PermKind) string {
	switch kind {
	case tools.PermNetwork:
		return "network"
	case tools.PermDangerous:
		return "dangerous"
	case tools.PermOutside:
		return "outside workspace"
	}
	return "permission"
}

type chat struct {
	opt Options
	cfg *config.Config

	sess *session.Session
	ag   *agent.Agent

	provName  string
	modelName string

	mode mode.Mode

	// showThinking controls whether the model's thinking is displayed.
	showThinking bool
	// scrollback is true while the alternate screen is off and the terminal's
	// own scrollback is available.
	scrollback bool

	// cmdList is the palette of matching commands or options. cmdSel is the
	// highlighted entry and cmdTop is the first visible one.
	cmdList []paletteEntry
	cmdSel  int
	cmdTop  int

	// cmdOptions holds the options of the command being edited.
	cmdOptions []commands.Option
	cmdCommand string

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

	question       *tools.Question
	questionText   textinput.Model
	questionTyping bool
	questionSel    int
	askReply       chan string

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
		OnText: func(s string) { c.emit(textMsg(s)) },
		OnReasoning: func(s string) {
			if c.showThinking {
				c.emit(reasoningMsg(s))
			}
		},
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
		OnNotice: func(s string) {
			c.emit(noteMsg{text: s})
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
	c.showThinking = c.cfg.ShowThinking
	if c.mode == "" {
		if m, err := mode.Parse(c.cfg.Mode); err == nil {
			c.mode = m
		} else {
			c.mode = mode.Create
		}
	}
	vision := llm.SupportsImages(c.modelName)
	env := &tools.Env{
		Dir:        cwd,
		Permit:     c.permit,
		DenyMutate: !c.mode.AllowsWrite(),
		Mode:       c.mode.Label(),
		NoImages:   !vision,
		Warn: func(s string) {
			c.emit(noteMsg{text: "checkpoint skipped: " + s})
		},
	}
	env.AskUser = c.askUser
	if c.cfg.Checkpoints {
		cp, err := checkpoint.New(checkpoint.RootForSession(c.sess.Path()), cwd)
		if err != nil {
			return err
		}
		cp.SetKeep(c.cfg.CheckpointKeep)
		env.Checkpoint = cp
	}
	if c.ag != nil && c.ag.Trace != nil {
		_ = c.ag.Trace.Close()
	}
	c.ag = &agent.Agent{
		Provider:      prov,
		Model:         c.modelName,
		Tools:         tools.RegistryWithQuestion(),
		Env:           env,
		Sess:          c.sess,
		Compact:       true,
		ContextWindow: tokens.Window(c.modelName),
		NumCtx:        pc.NumCtx,
		Reasoning:     c.cfg.Reasoning,
		TrimReasoning: c.cfg.TrimReasoning,
		NoVision:      !vision,
		ModeNote:      c.mode.Note(),
	}
	if tr, err := trace.Open(strings.TrimSuffix(c.sess.Path(), ".jsonl") + ".events.jsonl"); err == nil {
		c.ag.Trace = tr
	}
	if c.cfg.Orchestrator {
		if _, err := orchestrator.Attach(c.ag, orchestrator.Config{
			ScoutModel:     c.cfg.ScoutModel,
			ParallelAgents: c.cfg.ParallelAgents,
			EnableWriters:  c.cfg.Writers,
			KeepWorktrees:  c.cfg.KeepWorktrees,
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
	c.emit(permAskMsg{kind: kind, perm: tools.PermKindFor(kind), detail: detail, reply: reply})
	select {
	case d := <-reply:
		return d
	case <-time.After(10 * time.Minute):
		return tools.Decision{}
	}
}

// updatePalette refreshes the slash-command palette for the current input.
// When the input names a command that offers options, the palette shows
// those options instead.
func (c *chat) updatePalette() {
	text := c.ta.Value()
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		c.clearPalette()
		return
	}
	name, arg, hasArg := splitCommand(text)
	cmd, known := commands.Find(name)
	if known && len(cmd.Options) > 0 && hasArg {
		// the user is typing an argument: offer the command's options
		c.cmdCommand = name
		c.cmdOptions = cmd.Options
		c.cmdList = optionEntries(cmd.Options, arg)
		if c.cmdSel >= len(c.cmdList) {
			c.cmdSel = 0
		}
		c.cmdTop = 0
		return
	}
	c.cmdCommand = ""
	c.cmdOptions = nil
	prefix := name
	entries := make([]paletteEntry, 0, len(commands.Match(prefix)))
	for _, cmd := range commands.Match(prefix) {
		entries = append(entries, paletteEntry{
			label:      cmd.Usage(),
			summary:    cmd.Summary,
			insert:     cmd.Name,
			isCommand:  true,
			hasOptions: len(cmd.Options) > 0,
		})
	}
	c.cmdList = entries
	if c.cmdSel >= len(c.cmdList) {
		c.cmdSel = 0
	}
	c.cmdTop = 0
}

// splitCommand splits an input like "/thinking off" into the command and the
// argument.
func splitCommand(text string) (name string, arg string, hasArg bool) {
	name = strings.TrimSpace(text)
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		arg = strings.TrimSpace(name[i+1:])
		name = name[:i]
		hasArg = true
	}
	return name, arg, hasArg
}

// optionEntries turns a command's options into palette rows.
func optionEntries(options []commands.Option, filter string) []paletteEntry {
	entries := make([]paletteEntry, 0, len(options))
	for _, o := range options {
		if filter != "" && !strings.HasPrefix(o.Value, filter) {
			continue
		}
		entries = append(entries, paletteEntry{
			label:   o.Value,
			summary: o.Summary,
			insert:  o.Value,
		})
	}
	return entries
}

// paletteEnter acts on the highlighted palette row: a command with options
// opens its options, an option is filled in and run, and a plain command runs
// straight away.
func (c *chat) paletteEnter() tea.Cmd {
	if len(c.cmdList) == 0 {
		return nil
	}
	entry := c.cmdList[min(c.cmdSel, len(c.cmdList)-1)]
	if entry.isCommand && entry.hasOptions {
		// open the options of the command
		found, ok := commands.Find(entry.insert)
		if !ok || len(found.Options) == 0 {
			return c.submit(entry.insert)
		}
		c.ta.SetValue(entry.insert + " ")
		c.ta.CursorEnd()
		c.cmdCommand = entry.insert
		c.cmdOptions = found.Options
		c.cmdList = optionEntries(found.Options, "")
		c.cmdSel = 0
		c.cmdTop = 0
		c.layout()
		c.refresh()
		return nil
	}
	if !entry.isCommand && c.cmdCommand != "" {
		full := c.cmdCommand + " " + entry.insert
		c.ta.SetValue(full)
		c.clearPalette()
		return c.submit(full)
	}
	// a plain command runs as it is
	c.ta.SetValue(entry.insert)
	c.clearPalette()
	return c.submit(entry.insert)
}

// clearPalette closes the palette.
func (c *chat) clearPalette() {
	c.cmdList = nil
	c.cmdSel = 0
	c.cmdTop = 0
	c.cmdOptions = nil
	c.cmdCommand = ""
}

// paletteOpen reports whether the palette is showing.
func (c *chat) paletteOpen() bool { return len(c.cmdList) > 0 }

// completeCommand replaces the input with the selected command.
func (c *chat) completeCommand() {
	if len(c.cmdList) == 0 {
		return
	}
	sel := c.cmdList[min(c.cmdSel, len(c.cmdList)-1)]
	text := c.ta.Value()
	if i := strings.IndexAny(text, " \t"); i >= 0 {
		text = text[:i]
	}
	c.ta.SetValue(sel.insert + " ")
	c.ta.CursorEnd()
	c.clearPalette()
}

// askUser asks the user a question and waits for the answer.
func (c *chat) askUser(q tools.Question) (string, error) {
	if c.send == nil {
		return "", fmt.Errorf("the user cannot be asked here")
	}
	reply := make(chan string, 1)
	c.askReply = reply
	c.emit(questionMsg{question: q, reply: reply})
	select {
	case answer := <-reply:
		return answer, nil
	case <-time.After(30 * time.Minute):
		return "", fmt.Errorf("the user did not answer")
	}
}

// initQuestionInput prepares the free-text input for a question.
func (c *chat) initQuestionInput() {
	if c.questionText.Placeholder == "" {
		c.questionText = textinput.New()
		c.questionText.Placeholder = "type your own answer"
		c.questionText.CharLimit = 400
	}
}

// questionKey handles the keys while a question is open.
func (c *chat) questionKey(msg tea.KeyMsg) tea.Cmd {
	if c.questionTyping {
		switch msg.String() {
		case "enter":
			text := strings.TrimSpace(c.questionText.Value())
			if text == "" {
				c.questionTyping = false
				c.questionText.Blur()
				c.layout()
				c.refresh()
				return nil
			}
			c.sendQuestion(text)
			return nil
		case "esc":
			c.questionTyping = false
			c.questionText.Blur()
			c.layout()
			c.refresh()
			return nil
		}
		var cmd tea.Cmd
		c.questionText, cmd = c.questionText.Update(msg)
		return cmd
	}
	switch msg.String() {
	case "enter":
		// Enter takes the highlighted option.
		if c.questionSel < len(c.question.Options) {
			c.sendQuestion(c.question.Options[c.questionSel].Label)
		}
		return nil
	case "up", "ctrl+p":
		if c.questionSel > 0 {
			c.questionSel--
		}
		c.layout()
		c.refresh()
		return nil
	case "down", "ctrl+n":
		if c.questionSel < len(c.question.Options)-1 {
			c.questionSel++
		}
		c.layout()
		c.refresh()
		return nil
	case "f", "F":
		c.initQuestionInput()
		c.questionTyping = true
		c.questionText.Focus()
		c.layout()
		c.refresh()
		return nil
	case "esc":
		c.sendQuestion("")
		return nil
	case "ctrl+c":
		c.sendQuestion("")
		c.interrupt()
		return nil
	}
	if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
		idx := int(msg.Runes[0] - '1')
		if idx < len(c.question.Options) {
			c.questionSel = idx
			c.sendQuestion(c.question.Options[idx].Label)
			return nil
		}
	}
	return nil
}

// sendQuestion delivers the answer and closes the question.
func (c *chat) sendQuestion(answer string) {
	if c.question == nil {
		return
	}
	c.question = nil
	c.questionTyping = false
	c.questionText.Reset()
	c.questionText.Blur()
	c.layout()
	c.refresh()
	if c.askReply != nil {
		c.askReply <- answer
		c.askReply = nil
	}
}

// answerQuestion delivers the user's answer.
func (c *chat) answerQuestion(answer string) {
	if c.question == nil {
		return
	}
	q := c.question
	c.question = nil
	c.questionTyping = false
	c.questionText.Reset()
	c.questionText.Blur()
	c.layout()
	c.refresh()
	_ = q
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
	b.WriteString("  " + styleDim.Render("mode     ") + c.modeLabel() + styleFaint.Render("  (/mode chat to explore without changing anything)") + "\n")
	b.WriteString("  " + styleDim.Render("session  ") + c.sessionID() + "\n")
	b.WriteString("  " + styleDim.Render("dir      ") + clip(cwd, max(10, c.w-12)) + "\n\n")
	b.WriteString("  " + styleFaint.Render("type a message to begin · type / for commands") + "\n")
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
	c.cmdList = nil
	c.cmdSel = 0
	c.cmdTop = 0
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
		c.note(commands.Help(), false)
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
	case "/undo":
		c.cmdUndo(arg)
	case "/prune":
		c.cmdPrune(arg)
	case "/transcript":
		return c.openTranscript()
	case "/cost":
		c.note(fmt.Sprintf("in %s (cache %s) · out %s · context %s/%s",
			humanN(c.cumIn), humanN(c.cumCached), humanN(c.cumOut), humanN(c.history), humanN(c.window)), false)
	case "/status":
		cwd := c.opt.Cwd
		if c.sess != nil {
			cwd = c.sess.Meta.Cwd
		}
		c.note(fmt.Sprintf("provider=%s model=%s session=%s\ndir=%s\nmode=%s  auto-approve=%v",
			c.provName, c.modelName, c.sessionID(), cwd, c.modeLabel(), c.auto), false)
	case "/yolo":
		c.auto = !c.auto
		c.note(fmt.Sprintf("auto-approve: %v", c.auto), false)
	case "/mode", "/modes":
		newMode, msg, changed, err := mode.Apply(line, c.mode)
		if err != nil {
			c.note(err.Error(), true)
			return nil
		}
		if !changed {
			c.note(msg, false)
			return nil
		}
		c.mode = newMode
		if err := c.rebuild(false); err != nil {
			c.note("could not switch mode: "+err.Error(), true)
			return nil
		}
		c.note(msg, false)
	case "/thinking":
		settings, msg, changed, err := commands.ThinkingCommand(line, c.showThinking)
		if err != nil {
			c.note(err.Error(), true)
			return nil
		}
		if changed {
			c.showThinking = settings
			c.cfg.ShowThinking = settings
			_ = c.cfg.Save()
		}
		c.note(msg, false)
	case "/toolset":
		c.note(c.toolsetView(), false)
	case "/sources":
		c.note(c.sourcesView(), false)
	case "/history":
		c.note(c.historyView(), false)
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
	case questionMsg:
		q := msg.question
		c.question = &q
		c.questionSel = 0
		c.questionTyping = false
		c.initQuestionInput()
		c.layout()
		c.refresh()
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
	case transcriptDoneMsg:
		if msg.err != nil {
			c.note("transcript: "+msg.err.Error(), true)
		}
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
	c.updatePalette()
	c.layout()
	if c.follow {
		c.vp.GotoBottom()
	}
	return cmd
}

func (c *chat) key(msg tea.KeyMsg) tea.Cmd {
	if c.question != nil {
		return c.questionKey(msg)
	}
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
	// The command palette takes over the arrow keys while it is open, so the
	// list can be walked without moving the cursor in the input.
	if c.paletteOpen() && !c.streaming {
		switch msg.String() {
		case "up", "ctrl+p":
			if c.cmdSel > 0 {
				c.cmdSel--
			}
			c.syncPaletteWindow(c.paletteLimit())
			c.layout()
			c.refresh()
			return nil
		case "down", "ctrl+n":
			if c.cmdSel < len(c.cmdList)-1 {
				c.cmdSel++
			}
			c.syncPaletteWindow(c.paletteLimit())
			c.layout()
			c.refresh()
			return nil
		case "tab":
			c.completeCommand()
			c.layout()
			c.refresh()
			return nil
		case "esc":
			c.cmdList = nil
			c.cmdSel = 0
			c.cmdTop = 0
			c.layout()
			c.refresh()
			return nil
		}
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
	case "ctrl+o":
		return c.openTranscript()
	case "ctrl+g":
		c.scrollback = !c.scrollback
		if c.scrollback {
			c.note("terminal scrollback on — use the terminal scrollbar or wheel; ctrl+g to return", false)
			return tea.Batch(
				func() tea.Msg { return tea.DisableMouse() },
				func() tea.Msg { return tea.ExitAltScreen() },
			)
		}
		c.note("full-screen mode", false)
		cmds := []tea.Cmd{func() tea.Msg { return tea.EnterAltScreen() }}
		if c.mouse {
			cmds = append(cmds, func() tea.Msg { return tea.EnableMouseCellMotion() })
		}
		return tea.Batch(cmds...)
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
		// With the palette open, Enter acts on the highlighted row: it runs
		// the command, or offers the options when the command has some.
		if c.paletteOpen() {
			return c.paletteEnter()
		}
		return c.submit(text)
	}
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)
	c.updatePalette()
	c.layout()
	if c.follow {
		c.vp.GotoBottom()
	}
	return cmd
}

func (c *chat) checkpointManager() (*checkpoint.Manager, int, error) {
	if c.sess == nil {
		return nil, 0, fmt.Errorf("no session yet")
	}
	cwd := c.sess.Meta.Cwd
	if cwd == "" {
		cwd = c.opt.Cwd
	}
	mgr, err := checkpoint.New(checkpoint.RootForSession(c.sess.Path()), cwd)
	if err != nil {
		return nil, 0, err
	}
	list, err := mgr.List()
	if err != nil {
		return mgr, 0, err
	}
	return mgr, len(list), nil
}

func (c *chat) cmdUndo(arg string) {
	mgr, count, err := c.checkpointManager()
	if err != nil {
		c.note("undo: "+err.Error(), true)
		return
	}
	if count == 0 {
		c.note("undo: no checkpoints for this session", true)
		return
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		c.note(fmt.Sprintf("undo: use /undo N, where N is 1-%d. The conversation is unchanged.", count), false)
		return
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		c.note(fmt.Sprintf("undo: N must be a number from 1 to %d", count), true)
		return
	}
	if n > count {
		n = count
	}
	c.note(fmt.Sprintf("undo: reverting the last %d change(s) from checkpoints; those checkpoints are then removed. The conversation stays as it is.", n), false)
	done, err := mgr.Undo(n)
	if err != nil {
		c.note("undo failed: "+err.Error(), true)
		return
	}
	c.note(fmt.Sprintf("undo: restored %d change(s)", done), false)
}

func (c *chat) cmdPrune(arg string) {
	mgr, count, err := c.checkpointManager()
	if err != nil {
		c.note("prune: "+err.Error(), true)
		return
	}
	arg = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(arg), "keep"))
	arg = strings.TrimSpace(arg)
	if arg == "" {
		c.note(fmt.Sprintf("prune: %d checkpoint(s). Use /prune keep N, where N is 0-%d (0 deletes all). Files and session history stay.", count, count), false)
		return
	}
	keep, err := strconv.Atoi(arg)
	if err != nil || keep < 0 {
		c.note(fmt.Sprintf("prune: N must be a number from 0 to %d", count), true)
		return
	}
	c.note(fmt.Sprintf("prune: keeping the newest %d checkpoint(s); older undo points will be deleted. Your files and session history are untouched.", keep), false)
	removed, err := mgr.Prune(keep)
	if err != nil {
		c.note("prune failed: "+err.Error(), true)
		return
	}
	c.note(fmt.Sprintf("prune: removed %d checkpoint(s)", removed), false)
}

func (c *chat) transcriptText() string {
	var blocks []string
	for _, e := range c.entries {
		if lines := c.entryLines(e); len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func (c *chat) openTranscript() tea.Cmd {
	text := c.transcriptText()
	if text == "" {
		c.note("transcript: nothing to show yet", false)
		return nil
	}
	f, err := os.CreateTemp("", "wombat-transcript-*.txt")
	if err != nil {
		c.note("transcript: "+err.Error(), true)
		return nil
	}
	path := f.Name()
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		os.Remove(path)
		c.note("transcript: "+err.Error(), true)
		return nil
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		c.note("transcript: "+err.Error(), true)
		return nil
	}
	args := []string{"-R", "+G", path}
	name := "less"
	if _, err := exec.LookPath(name); err != nil {
		name = "cat"
		args = []string{path}
	}
	cmd := exec.Command(name, args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		os.Remove(path)
		return transcriptDoneMsg{err: err}
	})
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

// modeLabel is the short mode tag shown in the header.
func (c *chat) modeLabel() string {
	if c.mode == "" {
		return "create"
	}
	return c.mode.Label()
}

func (c *chat) header() string {
	left := " " + styleAccent.Bold(true).Render("◆ wombat")
	if c.streaming {
		left += " " + c.sp.View()
	}
	modeTag := styleDim.Render("[" + c.modeLabel() + "]")
	right := styleDim.Render(c.provName+"/"+c.modelName) + " " + modeTag + "  " + styleFaint.Render(c.sessionID())
	if c.w <= 0 {
		return left
	}
	gap := c.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		right = styleDim.Render(clip(c.provName+"/"+c.modelName, 30)) + " " + modeTag
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
		left = lipgloss.NewStyle().Foreground(permColor(c.perm.perm)).Render("waiting for " + c.perm.kind)
	case c.interrupting:
		left = styleWarn.Render("interrupting…")
	case c.streaming:
		left = styleDim.Render("esc interrupt · wheel/pgup scroll · ctrl+o transcript · working…")
	default:
		left = styleFaint.Render("enter send · alt+enter newline · ctrl+v image · wheel/pgup scroll · ctrl+o transcript")
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
	color := permColor(p.perm)
	title := lipgloss.NewStyle().Bold(true).Foreground(color).Render(permTitle(p.perm)) +
		"  " + styleTitle.Render(p.kind)
	detail := styleDim.Render(clip(oneline(p.detail), max(10, c.w-16)))
	if c.permFeedback {
		return title + "\n" + detail + "\n" + c.permInput.View()
	}
	keys := styleKey.Render(" y ") + " allow   " +
		styleKey.Render(" a ") + " always " + p.kind + "   " +
		styleKey.Render(" n ") + " deny   " +
		styleKey.Render(" f ") + " feedback"
	if p.perm.Outside() {
		keys = styleKey.Render(" y ") + " allow once   " +
			styleKey.Render(" a ") + " allow for the session   " +
			styleKey.Render(" n ") + " deny   " +
			styleKey.Render(" f ") + " feedback"
	}
	return title + "\n" + detail + "\n" + keys
}

// questionView renders the question box.
func (c *chat) questionView() string {
	q := c.question
	if q == nil {
		return ""
	}
	var b strings.Builder
	if q.Header != "" {
		b.WriteString(styleTitle.Render(q.Header) + "\n")
	}
	b.WriteString(styleAccent.Render(q.Question) + "\n")
	for i, o := range q.Options {
		marker := "  "
		style := styleDim
		if c.questionSel == i {
			marker = styleKey.Render(" > ")
			style = styleAccent
		}
		b.WriteString(marker + style.Render(fmt.Sprintf("%d) %s", i+1, o.Label)))
		if o.Description != "" {
			b.WriteString(styleDim.Render(" - " + o.Description))
		}
		b.WriteString("\n")
	}
	if c.questionTyping {
		b.WriteString("  " + c.questionText.View() + "\n")
		b.WriteString(styleFaint.Render("  enter send · esc cancel"))
	} else {
		b.WriteString(styleFaint.Render("  1-9 pick · f type your own · esc skip"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *chat) view() string {
	if c.w <= 0 || c.h <= 0 {
		return ""
	}
	var box string
	if c.question != nil {
		box = styleInputIdle.BorderForeground(cAccent).Width(c.w - 4).Render(c.questionView())
	} else if c.perm != nil {
		box = styleInputIdle.BorderForeground(permColor(c.perm.perm)).Width(c.w - 4).Render(c.permView())
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
	if pal := c.paletteView(); pal != "" {
		parts = append(parts, pal)
	}
	parts = append(parts, box, c.hint())
	return strings.Join(parts, "\n")
}

// paletteEntry is one row of the command palette: a command, or one of the
// options a command offers.
type paletteEntry struct {
	// label is what the row shows.
	label string
	// summary is the one-line description.
	summary string
	// insert is the text placed in the input when the row is chosen.
	insert string
	// isCommand is true for a command row, false for an option.
	isCommand bool
	// hasOptions is true when picking this command should offer its options
	// instead of running it.
	hasOptions bool
}

// paletteLimit is how many entries the palette shows at once.
func (c *chat) paletteLimit() int {
	limit := 8
	if c.h > 0 {
		if room := (c.h - 12) / 2; room < limit {
			limit = room
		}
	}
	if limit < 3 {
		limit = 3
	}
	return limit
}

// syncPaletteWindow scrolls the visible window so the selected command is
// always on screen.
func (c *chat) syncPaletteWindow(limit int) {
	if len(c.cmdList) == 0 {
		c.cmdTop = 0
		return
	}
	if limit > len(c.cmdList) {
		limit = len(c.cmdList)
	}
	if c.cmdSel < c.cmdTop {
		c.cmdTop = c.cmdSel
	}
	if c.cmdSel >= c.cmdTop+limit {
		c.cmdTop = c.cmdSel - limit + 1
	}
	if c.cmdTop < 0 {
		c.cmdTop = 0
	}
	if c.cmdTop+limit > len(c.cmdList) {
		c.cmdTop = len(c.cmdList) - limit
	}
}

// paletteView renders the list of slash commands that match the input. The
// window follows the selection, so the highlighted command is always visible.
func (c *chat) paletteView() string {
	if len(c.cmdList) == 0 {
		return ""
	}
	limit := c.paletteLimit()
	if limit > len(c.cmdList) {
		limit = len(c.cmdList)
	}
	c.syncPaletteWindow(limit)

	var b strings.Builder
	if c.cmdTop > 0 {
		b.WriteString(styleFaint.Render(fmt.Sprintf("  … %d more above", c.cmdTop)) + "\n")
	}
	end := c.cmdTop + limit
	if end > len(c.cmdList) {
		end = len(c.cmdList)
	}
	if c.cmdCommand != "" {
		b.WriteString(styleFaint.Render("  options for "+c.cmdCommand) + "\n")
	}
	for i := c.cmdTop; i < end; i++ {
		entry := c.cmdList[i]
		marker := "  "
		style := styleDim
		if i == c.cmdSel {
			marker = styleKey.Render(" > ")
			style = lipgloss.NewStyle().Foreground(cAccent)
		}
		b.WriteString(marker + style.Render(pad(entry.label, 30)) + styleDim.Render(entry.summary) + "\n")
	}
	if end < len(c.cmdList) {
		b.WriteString(styleFaint.Render(fmt.Sprintf("  … %d more below", len(c.cmdList)-end)) + "\n")
	}
	b.WriteString(styleFaint.Render("  ↑↓ select · tab complete · esc dismiss"))
	return strings.TrimRight(b.String(), "\n")
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s + "  "
	}
	return s + strings.Repeat(" ", n-len(s))
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
			if c.showThinking {
				lines = append(lines, "   "+styleFaint.Render("thinking"))
				for _, ln := range wrap(e.reasoning, max(10, w-2)) {
					lines = append(lines, "   "+styleThought.Render(ln))
				}
			} else {
				lines = append(lines, "   "+styleFaint.Render(fmt.Sprintf("thought · /thinking to show (%d chars)", len(e.reasoning))))
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
