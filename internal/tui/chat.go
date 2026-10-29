package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/checkpoint"
	clipboard "github.com/ramsterr/Wombat/internal/clip"
	"github.com/ramsterr/Wombat/internal/commands"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/embeddings"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mcp"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/orchestrator"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/todo"
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
	started   time.Time
	running   bool
	ok        bool
	streaming bool
	isErr     bool
	pre       bool
}

type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalTheme
	modalReference
	modalSources
	modalPreviews
	modalCopy
	modalReasoning
	modalAgents
	modalAgentForm
	modalSkills
)

type textMsg string
type reasoningMsg string

type resetStreamMsg struct{}

type retryMsg struct {
	attempt int
	err     error
	delay   time.Duration
}

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
	kind    string
	perm    tools.PermKind
	detail  string
	preview string
	reply   chan tools.Decision
}

// questionMsg asks the user to choose between options, the way opencode
// does. The answer comes back as free text: a chosen option's label, or
// whatever the user typed.
type questionMsg struct {
	question tools.Question
	reply    chan string
}

// permColor returns the colour that marks a permission kind. Requests that
// go beyond the sandbox get their own colour so they stand out.
func permColor(kind tools.PermKind) lipgloss.TerminalColor {
	switch kind {
	case tools.PermNetwork, tools.PermSandboxWrite:
		return cSandbox
	case tools.PermOutside:
		return cOutside
	case tools.PermMCP:
		return cSandbox
	case tools.PermDangerous, tools.PermSensitive:
		return cRed
	}
	return cWarn
}

// permTitle names the prompt.
func permTitle(kind tools.PermKind) string {
	switch kind {
	case tools.PermNetwork, tools.PermSandboxWrite:
		return "sandbox"
	case tools.PermDangerous:
		return "dangerous"
	case tools.PermSensitive:
		return "credentials file"
	case tools.PermOutside:
		return "outside workspace"
	case tools.PermMCP:
		return "MCP tool"
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
	branch    string

	mode mode.Mode

	// showThinking controls whether the model's thinking is displayed.
	showThinking bool
	// imageGen is opt-in per session. Without it the imagegen tool is not
	// even offered to the model.
	imageGen bool
	// missingKey names the provider whose API key is absent. It is shown in
	// the header and blocks chat with a configuration hint.
	missingKey string

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
	retryNotice  string
	cancel       context.CancelFunc
	follow       bool
	perm         *permAskMsg
	permFeedback bool
	permInput    textinput.Model
	permMu       sync.Mutex

	question       *tools.Question
	questionText   textinput.Model
	questionTyping bool
	questionSel    int
	askReply       chan string

	pending []llm.Attachment
	mouse   bool

	// showBanner keeps the session-opening banner at the top of the
	// transcript for the whole session. It scrolls up as chat happens.
	showBanner bool

	// previewURL is the newest preview the agent produced. previews keeps the
	// full, de-duplicated list shown in the inspector and /previews.
	previewURL string
	previews   []preview
	previewSel int

	auto   bool
	always map[string]bool

	cumIn, cumOut, cumCached int
	history, window          int

	send func(tea.Msg)

	// panel is the right-hand inspector.
	panel bool

	// todos is the session anchor shown in the inspector.
	todos *todo.Store

	// startedAt scopes default verification to files touched by this run.
	startedAt time.Time

	// live run status
	runStart time.Time
	runTools int

	// modal overlays
	modal     modalKind
	themeSel  int
	themeOrig string
	helpTop   int

	// prompt history (ctrl+p / ctrl+n)
	prompts   []string
	histIdx   int
	histDraft string

	// project references shown in the inspector
	refs     []reference
	refInput textinput.Model

	// custom agents selected for this session, and the picker state
	agentDefs     []agents.Loaded
	agentSel      []string
	agentProblems []agents.Problem
	agentRows     []agentRow
	agentCursor   int
	agentDraft    map[string]bool
	wizard        *agentWizard

	// skills browser
	skillRows     []skillRow
	skillCursor   int
	skillExpanded bool
	skillProblems []agents.Problem

	// reasoning picker
	reasoningOptions []string
	reasoningSel     int

	// copy picker
	copyItems []copyItem
	copySel   int

	// sites and files browser
	sourceItems []sourceItem
	sourceTab   int
	sourceSel   int
	sourceSites int
	sourceFiles int

	// @-file completion
	fileActive bool
	fileList   []fileEntry
	fileSel    int
	fileTop    int
	fileQuery  string
	fileStart  int
	fileEnd    int
	fileCache  []fileEntry
	fileCacheT time.Time
}

type fileEntry struct {
	path  string
	isDir bool
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
		panel:  opt.Cfg.Panel,
		always: map[string]bool{},
		follow: true,
	}
	applyTheme(c.cfg.Theme)
	c.provName = c.cfg.Provider
	c.modelName = c.cfg.ActiveModel()
	c.startedAt = time.Now()
	c.branch = gitBranch(c.opt.Cwd)
	c.refs = loadReferences(c.opt.Cwd)
	c.missingKey = c.missingProviderKey()

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
		c.agentSel = normalizeAgentSelection(c.sess.Meta.Agents)
	}

	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = "Ask anything, or /help"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 6
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	c.ta = ta

	c.vp = viewport.New(80, 20)
	c.sp = spinner.New(spinner.WithSpinner(spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    80 * time.Millisecond,
	}))
	c.permInput = textinput.New()
	c.permInput.Placeholder = "tell wombat what to do instead…"
	c.permInput.Prompt = "› "
	c.permInput.CharLimit = 500
	c.applyThemeStyles()

	if c.sess != nil {
		if err := c.rebuild(false); err != nil {
			return nil, err
		}
		c.loadHistory()
		c.refreshSourceCounts()
	}
	c.showBanner = len(c.entries) == 0
	return c, nil
}

// applyThemeStyles refreshes the widgets that keep their own copy of the
// theme. Package-level styles update immediately; textarea and textinput
// styles are values, so they need to be copied again.
func (c *chat) applyThemeStyles() {
	c.ta.FocusedStyle.Prompt = styleAccent
	c.ta.FocusedStyle.Placeholder = styleFaint
	c.ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(cText)
	c.ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	c.ta.BlurredStyle.Prompt = styleFaint
	c.ta.BlurredStyle.Placeholder = styleFaint
	c.ta.BlurredStyle.Text = lipgloss.NewStyle().Foreground(cText)
	c.ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	c.ta.Cursor.Style = styleAccent
	c.sp.Style = styleAccent
	c.permInput.PromptStyle = styleAccent
	c.permInput.PlaceholderStyle = styleFaint
	c.permInput.TextStyle = lipgloss.NewStyle().Foreground(cText)
	c.permInput.Cursor.Style = styleAccent
	if c.questionText.Placeholder != "" {
		c.questionText.PromptStyle = styleAccent
		c.questionText.PlaceholderStyle = styleFaint
		c.questionText.TextStyle = lipgloss.NewStyle().Foreground(cText)
		c.questionText.Cursor.Style = styleAccent
	}
	if c.refInput.Placeholder != "" {
		c.refInput.PromptStyle = styleAccent
		c.refInput.PlaceholderStyle = styleFaint
		c.refInput.TextStyle = lipgloss.NewStyle().Foreground(cText)
		c.refInput.Cursor.Style = styleAccent
	}
}

// setTheme applies and persists a theme. It returns false for an unknown
// name without changing anything.
// updatePlaceholder keeps the input hint in step with the current mode.
func (c *chat) updatePlaceholder() {
	switch {
	case c.mode == mode.Chat:
		c.ta.Placeholder = "Ask about the code, or /help"
	case c.mode == mode.Create:
		c.ta.Placeholder = "Describe what to build, or /help"
	default:
		c.ta.Placeholder = "Ask anything, or /help"
	}
	if c.ta.Placeholder == "" {
		c.ta.Placeholder = "Ask anything, or /help"
	}
}

func (c *chat) setTheme(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, ok := themeCatalog[name]; !ok {
		return activeTheme, false
	}
	applyTheme(name)
	c.applyThemeStyles()
	if c.cfg != nil {
		c.cfg.Theme = name
		_ = c.cfg.Save()
	}
	c.layout()
	c.refresh()
	return name, true
}

// cycleTheme rotates through the built-in themes and reports the new one.
func (c *chat) cycleTheme() string {
	name := nextTheme(activeTheme, false)
	c.setTheme(name)
	return name
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
		OnReset: func() { c.emit(resetStreamMsg{}) },
		OnRetry: func(attempt int, err error, delay time.Duration) {
			c.emit(retryMsg{attempt: attempt, err: err, delay: delay})
		},
	}
}

func (c *chat) rebuild(reset bool) error {
	if reset {
		c.provName = c.cfg.Provider
		c.modelName = c.cfg.ActiveModel()
	}
	if c.sess == nil {
		c.window = tokens.WindowFor(c.provName, c.modelName)
		c.missingKey = c.missingProviderKey()
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
	c.showThinking = c.cfg.ShowThinking
	if c.mode == "" {
		if m, err := mode.Parse(c.cfg.Mode); err == nil {
			c.mode = m
		} else {
			c.mode = mode.Create
		}
	}
	c.updatePlaceholder()
	key := c.cfg.APIKey(c.provName, pc)
	if key == "" && !providerIsLocal(c.provName, pc) {
		c.missingKey = c.provName
		c.closeAgent()
		c.window = tokens.WindowFor(c.provName, c.modelName)
		c.history = 0
		c.sess.Meta.Provider = c.provName
		c.sess.Meta.Model = c.modelName
		_ = c.sess.Flush()
		return nil
	}
	c.missingKey = ""
	prov, err := llm.New(c.provName, pc, key)
	if err != nil {
		return err
	}
	if len(c.cfg.FallbackModels) > 0 {
		prov = llm.WithFallback(prov, c.cfg.FallbackModels, nil)
	}
	cwd := c.sess.Meta.Cwd
	if cwd == "" {
		cwd = c.opt.Cwd
	}
	todoPath := todo.PathForSession(c.sess.Path())
	if c.todos == nil || c.todos.Path() != todoPath {
		c.todos = todo.New(todoPath)
	}
	env := &tools.Env{
		Dir:              cwd,
		Permit:           c.permit,
		PermitPreview:    c.permitPreview,
		DenyMutate:       !c.mode.AllowsWrite(),
		Mode:             c.mode.Label(),
		Todos:            c.todos,
		SessionStarted:   c.startedAt,
		AllowScreenshots: c.cfg.BrowserScreenshots,
	}
	env.AskUser = c.askUser
	env.Embeddings = embeddings.FromConfig(c.cfg)
	env.IndexDir = filepath.Join(config.Home(), "index")
	if c.cfg.Checkpoints {
		cp, err := checkpoint.New(checkpoint.RootForSession(c.sess.Path()), cwd)
		if err != nil {
			return err
		}
		env.Checkpoint = cp
	}
	c.closeAgent()
	allTools := availableTools(c.imageGen)
	if len(c.cfg.MCPServers) > 0 {
		manager := mcp.Connect(context.Background(), c.cfg.MCPServers, cwd, c.opt.Version)
		env.SetMCPManager(manager)
		allTools = append(allTools, tools.MCPTools(manager)...)
	}
	c.agentDefs = agents.Load(c.cfg, config.Home(), cwd)
	var team []*agents.Member
	var agentProblems []agents.Problem
	if c.cfg.Orchestrator && len(c.agentSel) > 0 {
		team, agentProblems = agents.Prepare(agents.PrepareOptions{
			Cfg:           c.cfg,
			Cwd:           cwd,
			Home:          config.Home(),
			BaseProvider:  prov,
			BaseModel:     c.modelName,
			AllTools:      allTools,
			Defs:          c.agentDefs,
			Selected:      c.agentSel,
			ForceReadOnly: !c.mode.AllowsWrite(),
		})
	}
	c.agentProblems = agentProblems
	c.ag = &agent.Agent{
		Provider:      prov,
		Model:         c.modelName,
		Tools:         allTools,
		Env:           env,
		Sess:          c.sess,
		Compact:       true,
		ContextWindow: tokens.WindowFor(c.provName, c.modelName),
		NumCtx:        pc.NumCtx,
		Reasoning:     c.cfg.Reasoning,
		ModeNote:      c.mode.Note(),
	}
	if tr, err := trace.Open(strings.TrimSuffix(c.sess.Path(), ".jsonl") + ".events.jsonl"); err == nil {
		c.ag.Trace = tr
	}
	if c.cfg.Orchestrator {
		if _, err := orchestrator.Attach(c.ag, orchestrator.Config{
			MaxParallelScouts:     c.cfg.MaxParallelScouts,
			MaxParallelAgents:     c.cfg.MaxParallelAgents,
			Team:                  team,
			ScoutModel:            c.cfg.ScoutModel,
			ParallelAgents:        c.cfg.ParallelAgents,
			MaxParallelWriters:    c.cfg.MaxParallelWriters,
			WorkerMaxSteps:        c.cfg.WorkerMaxSteps,
			WorkerTokenBudget:     c.cfg.WorkerTokenBudget,
			TurnWorkerTokenBudget: c.cfg.TurnWorkerTokenBudget,
			WriterMaxSteps:        c.cfg.WriterMaxSteps,
			WriterTokenBudget:     c.cfg.WriterTokenBudget,
			TurnWriterTokenBudget: c.cfg.TurnWriterTokenBudget,
			RequireVerification:   c.cfg.RequireVerification,
			EnableWriters:         c.cfg.Writers,
			KeepWorktrees:         c.cfg.KeepWorktrees,
			ReviewWriters:         c.cfg.ReviewWriters,
			ReviewModel:           c.cfg.ReviewModel,
			FullWorkers:           c.cfg.FullWorkers(),
		}); err != nil {
			env.CloseMCP()
			return err
		}
	}
	c.ag.Hooks = c.hooks()
	// Keep every earlier section: the orchestrator installs its planner
	// prompt here, and a later assignment must compose with it, never replace
	// it.
	prevExtra := c.ag.SystemExtra
	c.ag.SystemExtra = func() string {
		var parts []string
		if prevExtra != nil {
			if s := strings.TrimSpace(prevExtra()); s != "" {
				parts = append(parts, s)
			}
		}
		if !c.imageGen {
			parts = append(parts, "Image generation is disabled. Do not generate or edit images unless the user enables it with /imagen.")
		}
		return strings.Join(parts, "\n\n")
	}
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
	c.startedAt = time.Now()
	c.entries = nil
	c.imageGen = false
	c.previews = nil
	c.previewURL = ""
	c.refs = loadReferences(c.opt.Cwd)
	c.cumIn, c.cumOut, c.cumCached, c.history = 0, 0, 0, 0
	if s.Meta.Provider != "" {
		if _, ok := c.cfg.Providers[s.Meta.Provider]; ok {
			c.provName = s.Meta.Provider
		}
	}
	if s.Meta.Model != "" {
		c.modelName = s.Meta.Model
	}
	c.agentSel = normalizeAgentSelection(s.Meta.Agents)
	c.agentDraft = nil
	buildErr := c.rebuild(false)
	c.loadHistory()
	c.showBanner = len(c.entries) == 0
	c.refreshSourceCounts()
	c.follow = true
	c.layout()
	c.refresh()
	if buildErr != nil {
		c.note("error: "+buildErr.Error(), true)
	}
	for _, problem := range c.agentProblems {
		c.note("agent: "+problem.String(), true)
	}
	return nil
}

func (c *chat) permit(kind, detail string) tools.Decision {
	return c.permitPreview(kind, detail, "")
}

func (c *chat) permitPreview(kind, detail, preview string) tools.Decision {
	c.permMu.Lock()
	defer c.permMu.Unlock()
	if v, ok := tools.PermissionValue(c.cfg.Permissions, kind); ok {
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
	c.emit(permAskMsg{kind: kind, perm: tools.PermKindFor(kind), detail: detail, preview: preview, reply: reply})
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
	if hasArg && (name == "/agents" || name == "/agent" || name == "/team") {
		opts := c.agentPaletteOptions(arg)
		if len(opts) > 0 {
			c.cmdCommand = "/agents"
			c.cmdOptions = opts
			c.cmdList = optionEntries(opts, "")
			if c.cmdSel >= len(c.cmdList) {
				c.cmdSel = 0
			}
			c.cmdTop = 0
			return
		}
	}
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
	matches := commands.Match(prefix)
	if len(matches) == 0 && len(prefix) > 2 {
		matches = fuzzyCommands(prefix)
	}
	entries := make([]paletteEntry, 0, len(matches))
	for _, cmd := range matches {
		entries = append(entries, paletteEntry{
			label:      cmd.Usage(),
			summary:    cmd.Summary,
			insert:     cmd.Name,
			group:      cmd.Group,
			isCommand:  true,
			hasOptions: len(cmd.Options) > 0,
		})
	}
	sortPaletteByGroup(entries)
	c.cmdList = entries
	if c.cmdSel >= len(c.cmdList) {
		c.cmdSel = 0
	}
	c.cmdTop = 0
}

// sortPaletteByGroup keeps related commands together while preserving the
// canonical order inside each group.
func sortPaletteByGroup(entries []paletteEntry) {
	order := map[string]int{
		"Session": 1, "Models": 2, "Context": 3, "Tools": 4, "Skills": 5,
		"Files": 6, "Appearance": 7, "Safety": 8, "Help": 9, "System": 10,
	}
	sort.SliceStable(entries, func(i, j int) bool {
		gi, gj := order[entries[i].group], order[entries[j].group]
		if gi == 0 {
			gi = 99
		}
		if gj == 0 {
			gj = 99
		}
		return gi < gj
	})
}

// splitCommand splits an input like "/sandbox net" into the command and the
// argument.
func splitCommand(text string) (name string, arg string, hasArg bool) {
	raw := strings.TrimLeft(text, " \t")
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		name = raw[:i]
		arg = strings.TrimSpace(raw[i+1:])
		return name, arg, true
	}
	return strings.TrimSpace(raw), "", false
}

// fuzzyCommands falls back to a subsequence match when no command starts
// with the typed prefix. Ties keep the canonical command order.
func fuzzyCommands(query string) []commands.Command {
	q := strings.ToLower(strings.TrimPrefix(query, "/"))
	if q == "" {
		return nil
	}
	var out []commands.Command
	for _, cmd := range commands.List {
		name := strings.ToLower(strings.TrimPrefix(cmd.Name, "/"))
		qi := 0
		for _, r := range name {
			if qi < len(q) && byte(r) == q[qi] {
				qi++
			}
		}
		if qi == len(q) {
			out = append(out, cmd)
		}
	}
	return out
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
		if strings.TrimSpace(answer) != "" {
			c.emit(noteMsg{text: "you: " + answer, isErr: false})
		}
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
			c.entries = append(c.entries, &chatEntry{kind: entryUser, text: sanitizeTerminalText(m.Content), atts: m.Attachments})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" || m.Reasoning != "" {
				c.entries = append(c.entries, &chatEntry{
					kind: entryAssistant,
					text: sanitizeTerminalText(m.Content), reasoning: sanitizeTerminalText(m.Reasoning),
				})
			}
			for _, tc := range m.ToolCalls {
				detail := toolArgsPreview(tc.Arguments)
				c.detectPreviewFromDetail(tc.Name, detail)
				e := &chatEntry{kind: entryTool, name: tc.Name, detail: detail, running: true}
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
			e.result = clip(strings.TrimRight(sanitizeTerminalText(m.Content), "\n"), 700)
			e.dur = 0
			c.detectToolPreview(e.name, m.Content)
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
		extra++
	}
	if c.previewURL != "" {
		extra++
	}
	overlay := 0
	if c.modal == modalNone {
		overlay = c.commandOverlayRows() + c.fileOverlayRows()
	}
	vh := c.h - 2 - (lines + 2) - 1 - extra - overlay
	if vh < 3 {
		vh = 3
	}
	c.vp.Width = c.contentWidth()
	c.vp.Height = vh
}

// commandOverlayRows estimates the height the slash palette adds below the
// transcript, so the chat view always fits the terminal.
func (c *chat) commandOverlayRows() int {
	if !c.paletteOpen() {
		return 0
	}
	if c.cmdCommand == "/theme" {
		return (len(c.cmdList)+1)/2 + 4
	}
	limit := min(c.paletteLimit(), len(c.cmdList))
	c.syncPaletteWindow(limit)
	n := limit
	rows := n + 4
	if c.h >= 26 && c.cmdCommand == "" && strings.TrimSpace(c.ta.Value()) == "/" {
		end := min(len(c.cmdList), c.cmdTop+limit)
		rows += paletteGroupCount(c.cmdList[c.cmdTop:end])
	}
	if c.selectedCommandHasOptions() {
		if c.selectedCommandInsert() == "/theme" {
			rows += 3
		} else {
			rows++
		}
	}
	return rows
}

// paletteGroupCount is the number of group headings the palette can draw.
func paletteGroupCount(entries []paletteEntry) int {
	seen := map[string]bool{}
	for _, e := range entries {
		if e.group != "" {
			seen[e.group] = true
		}
	}
	return len(seen)
}

func (c *chat) selectedCommandInsert() string {
	if c.cmdCommand != "" || len(c.cmdList) == 0 {
		return ""
	}
	sel := c.cmdSel
	if sel < 0 || sel >= len(c.cmdList) {
		return ""
	}
	return c.cmdList[sel].insert
}

func (c *chat) selectedCommandHasOptions() bool {
	if c.cmdCommand != "" || len(c.cmdList) == 0 {
		return false
	}
	sel := c.cmdSel
	if sel < 0 || sel >= len(c.cmdList) {
		return false
	}
	return c.cmdList[sel].hasOptions
}

// fileOverlayRows is the same estimate for @-file completion.
func (c *chat) fileOverlayRows() int {
	if !c.fileActive {
		return 0
	}
	if len(c.fileList) == 0 {
		return 1
	}
	return min(c.fileLimit(), len(c.fileList)) + 3
}

func (c *chat) refresh() {
	if c.w <= 0 || c.h <= 0 {
		return
	}
	var blocks []string
	if c.showBanner {
		if banner := c.welcome(); banner != "" {
			blocks = append(blocks, banner)
		}
	}
	for _, e := range c.entries {
		if lines := c.entryLines(e); len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	if len(blocks) == 0 {
		c.vp.SetContent("")
		return
	}
	content := strings.Join(blocks, "\n\n")
	if !c.showBanner {
		if pad := c.vp.Height - (strings.Count(content, "\n") + 1); pad > 0 {
			content = strings.Repeat("\n", pad) + content
		}
	}
	c.vp.SetContent(content)
	if len(c.entries) == 0 && c.showBanner {
		c.follow = false
		c.vp.GotoTop()
		return
	}
	if c.follow {
		c.vp.GotoBottom()
	}
}

// providerIsLocal reports whether a provider is expected to work without a
// cloud API key.
func providerIsLocal(name string, pc *config.Provider) bool {
	if preset, ok := config.PresetByName(name); ok && preset.Local {
		return true
	}
	if pc == nil {
		return false
	}
	base := strings.ToLower(pc.BaseURL)
	return strings.Contains(base, "localhost") || strings.Contains(base, "127.0.0.1") ||
		strings.Contains(base, "0.0.0.0") || strings.Contains(base, "[::1]")
}

// missingProviderKey returns the active provider when it has no API key.
func (c *chat) missingProviderKey() string {
	if c.cfg == nil || c.provName == "" {
		return ""
	}
	pc := c.cfg.Providers[c.provName]
	if pc == nil {
		return c.provName
	}
	if c.cfg.APIKey(c.provName, pc) == "" && !providerIsLocal(c.provName, pc) {
		return c.provName
	}
	return ""
}

// closeAgent releases the tool processes held by the current agent.
func (c *chat) closeAgent() {
	if c.ag == nil {
		return
	}
	if c.ag.Trace != nil {
		_ = c.ag.Trace.Close()
	}
	if c.ag.Env != nil {
		c.ag.Env.JobManager().Close()
		c.ag.Env.CloseLSP()
		c.ag.Env.CloseBrowser()
		c.ag.Env.ClosePreview()
		c.ag.Env.CloseMCP()
	}
	c.ag = nil
}

// availableTools excludes image generation unless the user opted in.
func availableTools(allowImage bool) []tools.Tool {
	all := tools.RegistryWithQuestion()
	if allowImage {
		return all
	}
	out := make([]tools.Tool, 0, len(all))
	for _, tool := range all {
		if tool.Name() != "imagegen" {
			out = append(out, tool)
		}
	}
	return out
}

// clearStreamingAssistant removes partial text before a provider retry so the
// replacement stream does not duplicate it.
func (c *chat) clearStreamingAssistant() {
	if n := len(c.entries); n > 0 {
		if e := c.entries[n-1]; e.kind == entryAssistant && e.streaming {
			e.text = ""
			e.reasoning = ""
		}
	}
}

func retryDelayLabel(d time.Duration) string {
	if d <= 0 {
		return "a moment"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (c *chat) note(text string, isErr bool) {
	c.entries = append(c.entries, &chatEntry{kind: entryNote, text: sanitizeTerminalText(text), isErr: isErr})
	c.refresh()
}

func (c *chat) notePre(text string) {
	c.entries = append(c.entries, &chatEntry{kind: entryNote, text: sanitizeTerminalText(text), pre: true})
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
	c.startedAt = time.Now()
	c.persistAgents()
	return c.rebuild(false)
}

func (c *chat) sessionID() string {
	if c.sess == nil {
		return "new"
	}
	return c.sess.ID()
}

func (c *chat) submit(text string) tea.Cmd {
	text = sanitizeTerminalText(text)
	c.ta.Reset()
	c.cmdList = nil
	c.cmdSel = 0
	c.cmdTop = 0
	c.closeFilePalette()
	c.recordHistory(text)
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
	if name := c.missingProviderKey(); name != "" {
		c.missingKey = name
		c.note("no API key configured for "+name+" — run /configure "+name, true)
		return nil
	}
	c.missingKey = ""
	if c.ag == nil && c.modelName != "" {
		if err := c.rebuild(false); err != nil {
			c.note("error: "+err.Error(), true)
			return nil
		}
	}
	if c.ag == nil {
		c.note("no model configured — run `configure` or `wombat setup`", true)
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
	c.runStart = time.Now()
	c.runTools = 0
	c.follow = true
	c.refresh()

	ag := c.ag
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	return tea.Batch(func() tea.Msg {
		go func() {
			var runErr error
			defer func() {
				if r := recover(); r != nil {
					runErr = fmt.Errorf("internal error: %v", r)
				}
				c.emit(runDoneMsg{err: runErr})
			}()
			runErr = ag.RunWithAttachments(ctx, text, atts)
		}()
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
	if a.Kind == "image" && c.modelName != "" && !tokens.SupportsImagesFor(c.provName, c.modelName) {
		c.note("this model cannot see images ("+c.modelName+") - the file will be described instead", true)
	}
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
	case "/help", "/keys":
		return c.openHelp()
	case "/panel":
		c.togglePanel()
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
			c.maybeOpenReasoningPicker()
		}
	case "/configure", "/provider", "/providers":
		if busy() {
			return nil
		}
		if arg == "" {
			return openScreen(scrProviders)
		}
		name := strings.Fields(arg)[0]
		return func() tea.Msg { return openConfigureMsg{name: name} }
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
			c.note("mouse on — wheel scrolls; run /mouse again to select text normally", false)
			return tea.EnableMouseCellMotion
		}
		c.note("mouse off — select text normally; run /mouse again for wheel scrolling", false)
		return tea.DisableMouse
	case "/imagen":
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "", "toggle":
			c.imageGen = !c.imageGen
		case "on", "enable":
			c.imageGen = true
		case "off", "disable":
			c.imageGen = false
		default:
			c.imageGen = true
			if c.sess != nil {
				if err := c.rebuild(false); err != nil {
					c.note("error: "+err.Error(), true)
					return nil
				}
			}
			return c.submit("Generate an image with the imagegen tool: " + strings.TrimSpace(arg))
		}
		if c.sess != nil {
			if err := c.rebuild(false); err != nil {
				c.note("error: "+err.Error(), true)
				return nil
			}
		}
		if c.imageGen {
			c.note("image generation enabled for this session; /imagen off disables it", false)
		} else {
			c.note("image generation disabled; /imagen enables it", false)
		}
		return nil
	case "/reasoning":
		if arg != "" {
			for _, level := range reasoningLevels {
				if strings.EqualFold(level, arg) {
					c.cfg.Reasoning = level
					_ = c.cfg.Save()
					c.note("reasoning: "+level, false)
					return nil
				}
			}
			c.note("reasoning must be off, low, medium, high, or max", true)
			return nil
		}
		c.reasoningOptions = reasoningLevels
		c.reasoningSel = 0
		current := strings.ToLower(strings.TrimSpace(c.cfg.Reasoning))
		if current == "" {
			current = "medium"
		}
		for i, level := range reasoningLevels {
			if level == current {
				c.reasoningSel = i
			}
		}
		c.modal = modalReasoning
		c.layout()
		c.refresh()
		return nil
	case "/theme", "/themes":
		if arg == "" {
			return c.openThemePicker()
		}
		name, ok := c.setTheme(arg)
		if !ok {
			c.note("unknown theme "+arg+" — use /theme to list", true)
			break
		}
		c.note("theme: "+themeLabel+" ("+name+") · ctrl+t cycles", false)
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
		c.startedAt = time.Now()
		c.persistAgents()
		c.entries = nil
		c.imageGen = false
		c.showBanner = true
		c.pending = nil
		c.previewURL = ""
		c.previews = nil
		c.previewSel = 0
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
	case "/previews":
		return c.openPreviews()
	case "/copy":
		arg = strings.TrimSpace(arg)
		if strings.EqualFold(arg, "all") {
			return c.copyToClipboard(c.transcriptText(), "transcript")
		}
		if n, err := strconv.Atoi(arg); err == nil && n >= 1 {
			items := c.copyableMessages()
			if n <= len(items) {
				return c.copyToClipboard(items[n-1].text, items[n-1].label)
			}
			c.note("no reply matches "+arg, true)
			return nil
		}
		return c.openCopyPicker()
	case "/preview":
		if arg != "" {
			if n, err := strconv.Atoi(arg); err == nil && n >= 1 && n <= len(c.previews) {
				if err := c.openPreview(c.previews[n-1]); err != nil {
					c.note("could not open preview: "+err.Error(), true)
				} else {
					c.note("opened "+c.previews[n-1].Ref, false)
				}
				break
			}
			c.note("no preview matches "+arg, true)
			break
		}
		if c.previewURL == "" {
			c.note("no preview detected yet", true)
			break
		}
		if err := c.openNewestPreview(); err != nil {
			c.note("could not open preview: "+err.Error(), true)
			break
		}
		c.note("opened "+c.previewURL, false)
	case "/cost":
		c.note(fmt.Sprintf("in %s (cache %s) · out %s · context %s/%s",
			humanN(c.cumIn), humanN(c.cumCached), humanN(c.cumOut), humanN(c.history), humanN(c.window)), false)
	case "/status":
		cwd := c.opt.Cwd
		if c.sess != nil {
			cwd = c.sess.Meta.Cwd
		}
		c.note(fmt.Sprintf("provider=%s model=%s session=%s\ndir=%s\nmode=%s  auto-approve=%v\nagents=%s",
			c.provName, c.modelName, c.sessionID(), cwd, c.modeLabel(), c.auto, c.agentSelLabel()), false)
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
	case "/agents", "/agent", "/team":
		if busy() {
			return nil
		}
		arg = strings.TrimSpace(arg)
		switch {
		case arg == "":
			return c.openAgentPicker()
		case strings.EqualFold(arg, "list"), strings.EqualFold(arg, "ls"):
			c.notePre(c.agentsList())
			return nil
		case strings.EqualFold(arg, "clear"), strings.EqualFold(arg, "none"), strings.EqualFold(arg, "off"):
			return c.setAgents(nil)
		case strings.EqualFold(arg, "all"):
			c.refreshAgentDefs()
			var names []string
			for _, def := range agents.Usable(c.agentDefs) {
				names = append(names, def.Name)
			}
			if len(names) == 0 {
				c.note("no custom agents found — create one with `wombat agent new <name>`", true)
				return nil
			}
			return c.setAgents(names)
		default:
			c.refreshAgentDefs()
			available := map[string]bool{}
			for _, def := range agents.Usable(c.agentDefs) {
				available[def.Name] = true
			}
			names := splitAgentArgs(arg)
			var unknown []string
			for _, name := range names {
				if !available[name] {
					unknown = append(unknown, name)
				}
			}
			if len(unknown) > 0 {
				c.note("unknown agent: "+strings.Join(unknown, ", ")+" — /agents list", true)
				return nil
			}
			return c.setAgents(names)
		}
	case "/skill", "/skills":
		arg = strings.TrimSpace(arg)
		fields := strings.Fields(arg)
		switch {
		case arg == "":
			return c.openSkills()
		case strings.EqualFold(arg, "list"), strings.EqualFold(arg, "ls"):
			c.notePre(c.skillsList())
		case len(fields) >= 2 && (strings.EqualFold(fields[0], "install") || strings.EqualFold(fields[0], "add")):
			source := strings.TrimSpace(arg[len(fields[0]):])
			c.installSkill(strings.Trim(source, `"'`))
		case len(fields) >= 2 && (strings.EqualFold(fields[0], "remove") || strings.EqualFold(fields[0], "rm") || strings.EqualFold(fields[0], "uninstall")):
			c.removeSkill(fields[1])
		case strings.EqualFold(arg, "help"), arg == "?":
			c.notePre("skills\n  /skill                 browser\n  /skill list            names and sources\n  /skill install <src>   path, URL, or registry name\n  /skill remove <name>   delete an installed skill\n  /skill <name>          full instructions")
		default:
			c.notePre(c.skillDetail(arg))
		}
		return nil
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
		c.notePre(c.toolsetView())
	case "/mcp":
		c.notePre(c.mcpView())
	case "/sources":
		return c.openSources()
	case "/reference", "/ref":
		return c.referenceCommand(arg)
	case "/history":
		c.notePre(c.historyView())
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
	case textMsg, reasoningMsg, toolStartMsg, toolDoneMsg, usageMsg, compactMsg, compactDoneMsg, noteMsg, runDoneMsg, resetStreamMsg, retryMsg:
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
		c.retryNotice = ""
		c.currentAssistant().text += sanitizeTerminalText(string(msg))
		c.refresh()
		return nil
	case reasoningMsg:
		c.retryNotice = ""
		c.currentAssistant().reasoning += sanitizeTerminalText(string(msg))
		c.refresh()
		return nil
	case resetStreamMsg:
		c.clearStreamingAssistant()
		c.refresh()
		return nil
	case retryMsg:
		c.retryNotice = "network error — retrying in " + retryDelayLabel(msg.delay)
		c.refresh()
		return nil
	case toolStartMsg:
		c.retryNotice = ""
		c.finishAssistant()
		c.runTools++
		c.detectPreviewFromDetail(msg.name, msg.detail)
		c.entries = append(c.entries, &chatEntry{
			kind: entryTool, name: msg.name, detail: sanitizeTerminalText(msg.detail),
			running: true, started: time.Now(),
		})
		c.refresh()
		return nil
	case toolDoneMsg:
		c.detectToolPreview(msg.name, msg.result)
		for i := len(c.entries) - 1; i >= 0; i-- {
			e := c.entries[i]
			if e.kind == entryTool && e.running {
				e.running = false
				e.ok = !strings.HasPrefix(msg.result, "error:")
				e.dur = msg.dur
				e.result = clip(strings.TrimRight(sanitizeTerminalText(msg.result), "\n"), 700)
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
		if c.modal != modalNone {
			c.restoreAndClose()
		}
		q := msg.question
		c.question = &q
		c.questionSel = 0
		c.questionTyping = false
		c.initQuestionInput()
		if len(q.Options) == 0 {
			c.questionTyping = true
			c.questionText.Focus()
		}
		c.layout()
		c.refresh()
		return nil
	case permAskMsg:
		if c.modal != modalNone {
			c.restoreAndClose()
		}
		p := msg
		if p.preview != "" {
			c.note("proposed change for "+p.detail+":\n"+p.preview, false)
		}
		c.perm = &p
		c.layout()
		c.refresh()
		return nil
	case runDoneMsg:
		c.streaming = false
		c.interrupting = false
		c.retryNotice = ""
		c.cancel = nil
		c.runStart = time.Time{}
		c.runTools = 0
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
		c.refreshSourceCounts()
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
	c.scrubInput()
	c.updatePalette()
	if !c.paletteOpen() {
		c.updateFilePalette()
	} else {
		c.closeFilePalette()
	}
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
	if c.modal != modalNone {
		return c.modalKey(msg)
	}
	// The @-file picker owns the arrow keys while it is open.
	if c.fileActive && !c.streaming {
		if cmd, ok := c.filePaletteKey(msg); ok {
			return cmd
		}
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
			c.updatePalette()
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
	case "ctrl+b":
		c.togglePanel()
		return nil
	case "ctrl+y":
		return c.copyLastReply(false)
	case "ctrl+o":
		if c.previewURL == "" {
			c.note("no preview detected yet", true)
			return nil
		}
		if err := c.openNewestPreview(); err != nil {
			c.note("could not open preview: "+err.Error(), true)
			return nil
		}
		c.note("opened "+c.previewURL, false)
		return nil
	case "ctrl+g":
		return c.openHelp()
	case "ctrl+r":
		return c.openReferencePrompt()
	case "?":
		if c.ta.Value() == "" {
			return c.openHelp()
		}
	case "ctrl+p":
		if c.ta.Value() == "" && len(c.prompts) > 0 {
			c.historyPrev()
			return nil
		}
	case "ctrl+n":
		if c.ta.Value() == "" && len(c.prompts) > 0 {
			c.historyNext()
			return nil
		}
	case "ctrl+t":
		if c.streaming || c.perm != nil || c.question != nil {
			return nil
		}
		name := c.cycleTheme()
		c.note("theme: "+themeLabel+" ("+name+")", false)
		return nil
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
	case "ctrl+up":
		c.scrollUp(1)
		return nil
	case "ctrl+down":
		c.scrollDown(1)
		return nil
	case "ctrl+home":
		c.follow = false
		c.vp.GotoTop()
		return nil
	case "ctrl+end":
		c.follow = true
		c.vp.GotoBottom()
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
	c.scrubInput()
	c.updatePalette()
	if !c.paletteOpen() {
		c.updateFilePalette()
	} else {
		c.closeFilePalette()
	}
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

// modeLabel is the short mode tag shown in the header.
func (c *chat) modeLabel() string {
	if c.mode == "" {
		return "create"
	}
	return c.mode.Label()
}

var previewURLPattern = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?::[0-9]+)?(?:[/?#][^\s"'<>)]*)?(?:$|[^\w.-])`)

func findPreviewURL(text string) string {
	text = sanitizeTerminalText(text)
	matches := previewURLPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return ""
	}
	url := strings.TrimSpace(matches[len(matches)-1])
	url = strings.TrimRight(url, ".,;:!?)]}'\"·–—")
	return url
}

func serverBanner(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"local:", "ready in", "serving http", "listening on",
		"uvicorn running", "development server", "running at",
		"server running", "available at", "preview at", "compiled successfully",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func openLocalURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func (c *chat) header() string {
	left := styleBrand.Render("◈ WOMBAT")
	if c.opt.Version != "" {
		left += " " + styleFaint.Render(clip(c.opt.Version, 18))
	}
	if c.streaming {
		left += " " + c.sp.View()
		if d := c.runDuration(); d > 0 && c.w >= 90 {
			left += " " + styleWarn.Render(elapsedLabel(d))
		}
	}
	cwd := shortPath(c.opt.Cwd, max(12, c.w/3))
	if c.w >= 62 {
		left += "  " + styleDim.Render("in ") + styleDim.Render(cwd)
	}
	if c.branch != "" && c.w >= 120 {
		left += "  " + styleFaint.Render("⎇ "+clip(c.branch, 18))
	}
	model := c.provName + "/" + c.modelName
	mode := modeBadge(c.modeLabel())
	right := styleDim.Render(clip(model, 28))
	if c.missingKey != "" {
		right += "  " + styleWarn.Render("no key")
	}
	if c.auto {
		right += "  " + styleWarn.Render("yolo")
	}
	right += "  " + mode
	if badge := c.agentsBadge(); badge != "" && c.w >= 100 {
		right += "  " + styleFaint.Render(badge)
	}
	if c.w >= 110 {
		right += "  " + styleFaint.Render(clip(c.sessionID(), 18))
	}
	if c.w < 52 {
		right = mode
	}
	return chromeLine(left, right, c.w) + "\n" + accentRule(c.w)
}

func (c *chat) hint() string {
	var left string
	switch {
	case c.modal == modalHelp:
		left = keyHint("j/k", "scroll") + "   " + keyHint("g/G", "top/bottom") + "   " + keyHint("esc", "close")
	case c.modal == modalAgents:
		left = keyHint("space", "toggle") + "   " + keyHint("enter", "apply") + "   " + keyHint("c", "create") + "   " + keyHint("esc", "cancel")
	case c.modal == modalAgentForm:
		if c.wizard != nil && c.wizard.editing && c.wizard.section == wizardPrompt {
			left = keyHint("ctrl+s", "done") + "   " + keyHint("enter", "new line") + "   " + keyHint("esc", "done")
		} else if c.wizard != nil && c.wizard.editing {
			left = keyHint("tab", "next") + "   " + keyHint("space", "toggle") + "   " + keyHint("esc", "back")
		} else {
			left = keyHint("↑↓", "section") + "   " + keyHint("enter", "edit") + "   " + keyHint("ctrl+s", "save") + "   " + keyHint("esc", "cancel")
		}
	case c.modal == modalSkills:
		left = keyHint("↑↓", "browse") + "   " + keyHint("enter", "expand") + "   " + keyHint("esc", "close")
	case c.modal == modalTheme:
		left = keyHint("↑↓", "preview") + "   " + keyHint("enter", "apply") + "   " + keyHint("esc", "restore")
	case c.perm != nil && c.permFeedback:
		left = keyHint("enter", "send feedback") + "   " + keyHint("esc", "back")
	case c.perm != nil:
		left = lipgloss.NewStyle().Foreground(permColor(c.perm.perm)).Render("waiting for " + c.perm.kind)
	case c.question != nil:
		left = keyHint("1-9", "pick") + "   " + keyHint("f", "type") + "   " + keyHint("esc", "skip")
	case c.interrupting:
		left = styleWarn.Render("interrupting…")
	case c.streaming:
		if c.retryNotice != "" {
			left = c.sp.View() + " " + styleWarn.Render(c.retryNotice)
		} else {
			left = c.sp.View() + " " + styleWarn.Render("working "+elapsedLabel(c.runDuration()))
			if c.runTools > 0 {
				left += styleFaint.Render(fmt.Sprintf(" · %d tools", c.runTools))
			}
		}
		left += "   " + keyHint("esc", "interrupt")
	default:
		left = keyHint("enter", "send") + "   " + keyHint("/", "commands") + "   " +
			keyHint("ctrl+y", "copy") + "   " + keyHint("ctrl+g", "help")
		if c.w >= 112 {
			left += "   " + keyHint("/mouse", "select")
		}
	}
	right := ""
	if c.window > 0 {
		if c.w >= 104 {
			right = contextGaugeTokens(c.history, c.window)
		} else {
			right = contextGauge(c.history, c.window)
		}
	}
	if c.w <= 0 {
		return " " + left
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+4 > c.w {
		right = ""
	}
	return chromeLine(left, right, c.w)
}

func (c *chat) permView() string {
	p := c.perm
	color := permColor(p.perm)
	title := lipgloss.NewStyle().Bold(true).Foreground(color).Render(permTitle(p.perm)) +
		"  " + styleTitle.Render(p.kind)
	detail := styleDim.Render(clip(oneline(sanitizeTerminalText(p.detail)), max(10, c.w-16)))
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
	header := sanitizeTerminalText(q.Header)
	question := sanitizeTerminalText(q.Question)
	if header != "" {
		b.WriteString(styleTitle.Render(header) + "\n")
	}
	b.WriteString(styleAccent.Render(question) + "\n")
	for i, o := range q.Options {
		text := fmt.Sprintf("%d) %s", i+1, sanitizeTerminalText(o.Label))
		if o.Description != "" {
			text += "  " + sanitizeTerminalText(o.Description)
		}
		if c.questionSel == i {
			b.WriteString(styleSelected.Render(" > "+pad(text, max(12, c.mainWidth()-8))) + "\n")
		} else {
			b.WriteString("   " + styleDim.Render(text) + "\n")
		}
	}
	if c.questionTyping {
		b.WriteString("  " + c.questionText.View() + "\n")
		b.WriteString(styleFaint.Render("  enter send · esc cancel"))
	} else if len(q.Options) == 0 {
		b.WriteString(styleFaint.Render("  type your answer · enter send · esc skip"))
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
	switch {
	case c.question != nil:
		box = styleInputBox.BorderForeground(cAccent).Width(c.w - 4).Render(c.questionView())
	case c.perm != nil:
		box = styleInputIdle.BorderForeground(permColor(c.perm.perm)).Width(c.w - 4).Render(c.permView())
	case c.streaming:
		box = styleInputBusy.Width(c.w - 4).Render(c.ta.View())
	default:
		box = styleInputBox.Width(c.w - 4).Render(c.ta.View())
	}
	body := c.bodyView()
	if c.modal != modalNone {
		body = c.modalView()
	}
	if c.previewRailVisible() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, c.previewRailView(), inspectorGutter(), body)
	}
	if c.panelVisible() {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, inspectorGutter(), c.panelView())
	}
	parts := []string{c.header()}
	if bar := c.previewBar(); bar != "" {
		parts = append(parts, bar)
	}
	parts = append(parts, body)
	if line := c.pendingView(); line != "" {
		parts = append(parts, line)
	}
	if c.modal == modalNone {
		if pal := c.paletteView(); pal != "" {
			parts = append(parts, pal)
		}
		if files := c.filePaletteView(); files != "" {
			parts = append(parts, files)
		}
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
	// group is the command section shown in the palette.
	group string
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
	if c.cmdCommand == "/theme" {
		return c.themePaletteView()
	}
	limit := c.paletteLimit()
	if limit > len(c.cmdList) {
		limit = len(c.cmdList)
	}
	c.syncPaletteWindow(limit)

	inner := max(20, c.mainWidth()-4)
	showGroups := c.h >= 26 && c.cmdCommand == "" && strings.TrimSpace(c.ta.Value()) == "/"
	var b strings.Builder
	header := " " + styleAccent2.Render("commands") + " " + styleFaint.Render(itoa(len(c.cmdList))+" matches")
	b.WriteString(header + "\n")
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
	lastGroup := ""
	for i := c.cmdTop; i < end; i++ {
		entry := c.cmdList[i]
		if showGroups && entry.group != "" && entry.group != lastGroup {
			b.WriteString(" " + styleSection.Render(strings.ToUpper(entry.group)) + "\n")
			lastGroup = entry.group
		}
		label := pad(entry.label, 26)
		swatch := ""
		if c.cmdCommand == "/theme" {
			if t, ok := themeCatalog[entry.label]; ok {
				swatch = "  " + swatches(t)
			}
		}
		if i == c.cmdSel {
			row := " > " + label + entry.summary + swatch
			b.WriteString(styleSelected.Render(pad(row, inner)) + "\n")
		} else {
			b.WriteString("   " + styleAccent.Render(label) + styleDim.Render(entry.summary) + swatch + "\n")
		}
		if i == c.cmdSel && entry.isCommand && entry.hasOptions {
			if entry.insert == "/theme" {
				b.WriteString(themeStrip(inner) + "\n")
			} else if cmd, ok := commands.Find(entry.insert); ok {
				var opts []string
				for _, o := range cmd.Options {
					opts = append(opts, o.Value)
				}
				hint := "↳ " + strings.Join(opts, " · ") + "  ·  enter to choose"
				b.WriteString("       " + styleFaint.Render(clip(hint, max(8, inner-7))) + "\n")
			}
		}
	}
	if end < len(c.cmdList) {
		b.WriteString(styleFaint.Render(fmt.Sprintf("  … %d more below", len(c.cmdList)-end)) + "\n")
	}
	b.WriteString(styleFaint.Render("  ↑↓ select · tab complete · enter run · esc dismiss"))
	return strings.TrimRight(b.String(), "\n")
}

// themeStrip lists every theme with its swatch under the selected /theme row.
func themeStrip(width int) string {
	var lines []string
	line := "       " + styleAccent2.Render("↳ THEMES  ")
	for _, t := range themeEntries() {
		part := styleDim.Render(t.label) + " " + swatches(t)
		if lipgloss.Width(line)+2+lipgloss.Width(part) > width {
			lines = append(lines, line)
			line = "         "
		}
		if line != "         " {
			line += "  "
		}
		line += part
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

// themePaletteView is the two-column theme option grid. It shows every theme
// at once instead of a scrolling single column.
func (c *chat) themePaletteView() string {
	inner := max(20, c.mainWidth()-4)
	var b strings.Builder
	b.WriteString(" " + styleAccent2.Render("commands") + " " + styleFaint.Render("options for /theme") + "\n")
	colW := max(18, (inner-3)/2)
	rows := (len(c.cmdList) + 1) / 2
	sel := min(c.cmdSel, len(c.cmdList)-1)
	for r := 0; r < rows; r++ {
		var cells []string
		for col := 0; col < 2; col++ {
			i := r*2 + col
			if i >= len(c.cmdList) {
				break
			}
			entry := c.cmdList[i]
			t, ok := themeCatalog[entry.label]
			if !ok {
				continue
			}
			name := padRight(t.label, 14)
			row := "  " + name + " " + swatches(t)
			if i == sel {
				row = " > " + name + " " + swatches(t)
				cells = append(cells, styleSelected.Render(pad(clip(row, colW), colW)))
			} else {
				cells = append(cells, clipANSI(row, colW))
			}
		}
		b.WriteString(" " + strings.Join(cells, "  ") + "\n")
	}
	b.WriteString(styleFaint.Render("  ↑↓ select · tab complete · enter run · esc dismiss"))
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
	w := c.contentWidth() - 6
	if w < 20 {
		w = 20
	}
	switch e.kind {
	case entryUser:
		rail := styleUserRail.Render("▌")
		lines := []string{" " + rail + " " + styleAccent.Bold(true).Render("you")}
		for _, ln := range wrap(e.text, max(12, w-5)) {
			lines = append(lines, " "+rail+"   "+ln)
		}
		for _, a := range e.atts {
			lines = append(lines, " "+rail+"   "+styleFaint.Render("↳ "+a.Kind+" · "+a.Name+" · "+llm.HumanSize(a.Size)))
		}
		return lines
	case entryAssistant:
		lines := []string{" " + styleBrand.Render("◆") + " " + styleAccent.Bold(true).Render("wombat")}
		if e.reasoning != "" {
			if c.showThinking {
				lines = append(lines, "   "+styleFaint.Render("◇ thinking"))
				for _, ln := range wrap(e.reasoning, max(10, w-2)) {
					lines = append(lines, "   "+styleThought.Render(ln))
				}
			} else {
				lines = append(lines, "   "+styleFaint.Render(fmt.Sprintf("◇ thinking · /thinking to show (%d chars)", len(e.reasoning))))
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
		glyph, glyphStyle := toolGlyph(e.name)
		head := "  "
		switch {
		case e.running:
			head += c.sp.View() + " "
		case e.ok:
			head += styleGood.Render("✓") + " "
		default:
			head += styleBad.Render("✗") + " "
		}
		head += glyphStyle.Render(glyph) + " " + styleToolName.Render(e.name)
		if e.detail != "" {
			head += "  " + styleDim.Render(clip(oneline(e.detail), max(8, w-18)))
		}
		switch {
		case e.running && !e.started.IsZero():
			head += "  " + styleFaint.Render(elapsedLabel(time.Since(e.started)))
		case !e.running && e.dur > 0:
			head += "  " + styleFaint.Render(fmtDuration(e.dur))
		}
		lines := []string{head}
		if !e.running && e.result != "" {
			shown := 0
			for _, rl := range strings.Split(e.result, "\n") {
				rl = strings.TrimRight(rl, " \t")
				if strings.TrimSpace(rl) == "" {
					continue
				}
				if shown >= 3 {
					lines = append(lines, "       "+styleFaint.Render("…"))
					break
				}
				prefix := "       "
				if shown == 0 {
					prefix = "     " + styleTree.Render("↳ ")
				}
				lines = append(lines, prefix+resultStyle(rl).Render(clip(rl, max(8, w-14))))
				shown++
			}
		}
		return lines
	default:
		st := styleFaint
		mark := "·"
		if e.isErr {
			st = styleBad
			mark = "!"
		}
		var out []string
		if e.pre {
			out = append(out, "  "+styleFaint.Render(mark))
			for _, ln := range strings.Split(e.text, "\n") {
				if e.isErr {
					ln = st.Render(ln)
				}
				out = append(out, "  "+clipANSI(ln, w+2))
			}
			return out
		}
		for i, ln := range wrap(e.text, max(12, w-2)) {
			if i == 0 {
				out = append(out, "  "+st.Render(mark+" "+ln))
			} else {
				out = append(out, "    "+st.Render(ln))
			}
		}
		return out
	}
}

var inlineLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

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
	s = inlineLinkPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := inlineLinkPattern.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		label := lipgloss.NewStyle().Foreground(cLink).Underline(true).Render(sub[1])
		if sub[1] == sub[2] {
			return label
		}
		return label + styleFaint.Render(" ("+clip(sub[2], 48)+")")
	})
	return s
}

// numberedPrefix recognizes "1. ", "12) " and similar ordered-list markers.
func numberedPrefix(s string) (string, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(s) {
		return "", s, false
	}
	if (s[i] == '.' || s[i] == ')') && s[i+1] == ' ' {
		return s[:i+1] + " ", strings.TrimSpace(s[i+2:]), true
	}
	return "", s, false
}

func isTableSeparator(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.Contains(t, "|") || !strings.Contains(t, "-") {
		return false
	}
	for _, r := range t {
		switch r {
		case '|', '-', ':', ' ', '\t':
		default:
			return false
		}
	}
	return true
}

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// renderTable draws a compact box table. Columns are shrunk from the right
// when the terminal is narrow.
func renderTable(header []string, rows [][]string, width int) []string {
	cols := len(header)
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	for i, h := range header {
		widths[i] = len([]rune(h))
	}
	for _, row := range rows {
		for i := 0; i < cols && i < len(row); i++ {
			if n := len([]rune(row[i])); n > widths[i] {
				widths[i] = n
			}
		}
	}
	// +---+ per column, plus the outer frame
	total := func() int {
		n := cols + 1
		for _, w := range widths {
			n += w + 2
		}
		return n
	}
	for total() > width && cols > 0 {
		big := 0
		for i := 1; i < cols; i++ {
			if widths[i] > widths[big] {
				big = i
			}
		}
		if widths[big] <= 3 {
			break
		}
		widths[big]--
	}
	line := func(left, mid, right string) string {
		var b strings.Builder
		b.WriteString(styleRule.Render(left))
		for i, w := range widths {
			b.WriteString(styleRule.Render(strings.Repeat("─", w+2)))
			if i == cols-1 {
				b.WriteString(styleRule.Render(right))
			} else {
				b.WriteString(styleRule.Render(mid))
			}
		}
		return b.String()
	}
	row := func(cells []string, header bool) string {
		var b strings.Builder
		b.WriteString(styleRule.Render("│"))
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			cell = clip(cell, widths[i])
			padN := widths[i] - len([]rune(cell))
			style := styleDim
			if header {
				style = styleTitle
			}
			b.WriteString(" " + style.Render(cell) + strings.Repeat(" ", padN) + " " + styleRule.Render("│"))
		}
		return b.String()
	}
	out := []string{line("┌", "┬", "┐"), row(header, true), line("├", "┼", "┤")}
	for _, r := range rows {
		out = append(out, row(r, false))
	}
	out = append(out, line("└", "┴", "┘"))
	return out
}

func renderMarkdown(s string, width int) []string {
	lines := strings.Split(s, "\n")
	var out []string
	inCode := false
	codeLang := ""
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				inCode = false
				out = append(out, styleFaint.Render("╰"+strings.Repeat("─", max(4, min(40, width-2)))))
				codeLang = ""
			} else {
				inCode = true
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				label := "code"
				if codeLang != "" {
					label = codeLang
				}
				out = append(out, styleFaint.Render("╭─ ")+styleAccent2.Render(label))
			}
			continue
		}
		if inCode {
			style := styleCode
			if codeLang == "diff" || codeLang == "patch" {
				style = resultStyle(raw)
			}
			out = append(out, styleFaint.Render("│ ")+style.Render(clip(raw, max(4, width-2))))
			continue
		}
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if strings.Contains(trimmed, "|") && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			header := splitTableRow(trimmed)
			j := i + 2
			var rows [][]string
			for j < len(lines) {
				next := strings.TrimSpace(lines[j])
				if next == "" || !strings.Contains(next, "|") {
					break
				}
				rows = append(rows, splitTableRow(next))
				j++
			}
			out = append(out, renderTable(header, rows, max(10, width-2))...)
			i = j - 1
			continue
		}
		if strings.Trim(trimmed, "-*_ ") == "" && len(trimmed) >= 3 {
			out = append(out, rule(max(4, width-2)))
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			text := clip(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), max(8, width-2))
			switch {
			case level <= 1:
				out = append(out, styleTitle.Render(text))
			case level == 2:
				out = append(out, styleTitle.Render(text))
			default:
				out = append(out, styleAccent2.Bold(true).Render(text))
			}
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		pad := strings.Repeat("  ", min(4, indent/2))
		prefix := ""
		body := trimmed
		switch {
		case strings.HasPrefix(trimmed, "- "), strings.HasPrefix(trimmed, "* "), strings.HasPrefix(trimmed, "+ "):
			prefix, body = "• ", strings.TrimSpace(trimmed[2:])
		case strings.HasPrefix(trimmed, "> "):
			prefix, body = "│ ", strings.TrimSpace(trimmed[2:])
		default:
			if p, rest, ok := numberedPrefix(trimmed); ok {
				prefix, body = p, rest
			}
		}
		for k, ln := range wrap(body, max(8, width-2-len(prefix))) {
			ln = inlineStyle(ln)
			if k == 0 {
				out = append(out, " "+pad+styleDim.Render(prefix)+ln)
			} else {
				out = append(out, "  "+pad+strings.Repeat(" ", len(prefix))+ln)
			}
		}
	}
	return out
}
