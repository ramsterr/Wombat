package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tools"
)

func testChat(t *testing.T) *chat {
	t.Helper()
	cfg := config.Defaults()
	cfg.Provider = "openai"
	cfg.Model = "gpt-5"
	cfg.Theme = "wombat"
	cfg.Panel = true
	c, err := newChat(Options{Version: "test", Cwd: t.TempDir(), Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	c.mode = mode.Create
	c.provName = "openai"
	c.modelName = "gpt-5"
	c.window = 200_000
	c.history = 12_000
	return c
}

func TestThemeCatalogIsComplete(t *testing.T) {
	for _, name := range themeOrder {
		th, ok := themeCatalog[name]
		if !ok {
			t.Fatalf("theme %q is in the order but not the catalog", name)
		}
		if th.label == "" || th.blurb == "" {
			t.Errorf("theme %q is missing a label or blurb", name)
		}
		for field, color := range map[string]lipgloss.TerminalColor{
			"ink": th.ink, "surface": th.surface, "panel": th.panel, "border": th.border,
			"text": th.text, "dim": th.dim, "faint": th.faint, "accent": th.accent,
			"accent2": th.accent2, "green": th.green, "red": th.red, "warn": th.warn,
			"link": th.link, "code": th.code, "sandbox": th.sandbox, "outside": th.outside,
		} {
			if color == nil {
				t.Errorf("theme %q has no %s colour", name, field)
			}
		}
		if got := applyTheme(name); got != name {
			t.Errorf("applyTheme(%q) = %q", name, got)
		}
		if swatches(th) == "" {
			t.Errorf("theme %q has no swatches", name)
		}
	}
	applyTheme("definitely-not-a-theme")
	if activeTheme != themeDefault {
		t.Errorf("unknown themes must fall back to %q, got %q", themeDefault, activeTheme)
	}
}

func TestViewFitsTerminal(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		set  func(*chat)
	}{
		{"welcome with inspector", 120, 34, func(c *chat) {}},
		{"narrow welcome", 62, 20, func(c *chat) {}},
		{"short welcome", 80, 13, func(c *chat) {}},
		{"transcript", 130, 36, func(c *chat) {
			c.branch = "feature/terminal-ui"
			c.refs = []reference{
				{Value: "docs/spec.md", Kind: "file"},
				{Value: "https://example.com/design", Kind: "site"},
			}
			c.sourceSites, c.sourceFiles = 4, 7
			c.entries = []*chatEntry{
				{kind: entryUser, text: "add a health endpoint", atts: []llm.Attachment{{Name: "spec.md", Kind: "file", Size: 99}}},
				{kind: entryAssistant, text: "## Plan\n\n1. read the server\n2. add the route\n\n| file | change |\n|---|---|\n| main.go | route |\n"},
				{kind: entryTool, name: "edit", detail: "main.go", ok: true, result: "@@ -1 +1 @@\n-old\n+new"},
				{kind: entryNote, text: "noted", isErr: false},
			}
			c.previewURL = "http://localhost:5173/"
			c.previews = []preview{
				{Ref: "http://localhost:5173/", Kind: "site", Label: "http://localhost:5173/"},
				{Ref: "report.pdf", Kind: "pdf", Label: "report.pdf"},
			}
		}},
		{"palette", 120, 34, func(c *chat) {
			c.ta.SetValue("/")
			c.updatePalette()
		}},
		{"file picker", 120, 34, func(c *chat) {
			c.ta.SetValue("read @")
			c.updatePalette()
			c.updateFilePalette()
		}},
		{"help", 120, 36, func(c *chat) { c.openHelp() }},
		{"theme picker", 100, 30, func(c *chat) { c.openThemePicker() }},
		{"references", 120, 34, func(c *chat) {
			c.refs = []reference{{Value: "docs/spec.md", Kind: "file"}}
			c.openReferencePrompt()
		}},
		{"preview picker", 120, 34, func(c *chat) {
			c.previews = []preview{
				{Ref: "http://localhost:5173/", Kind: "site", Label: "http://localhost:5173/"},
				{Ref: "report.pdf", Kind: "pdf", Label: "report.pdf"},
			}
			c.openPreviews()
		}},
		{"sources browser", 120, 34, func(c *chat) {
			c.sourceItems = []sourceItem{
				{kind: "site", value: "https://example.com/spec", calls: 3},
				{kind: "file", value: "main.go", calls: 2},
			}
			c.modal = modalSources
		}},
		{"tiny", 44, 12, func(c *chat) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testChat(t)
			c.resize(tc.w, tc.h)
			tc.set(c)
			c.layout()
			c.refresh()
			view := c.view()
			lines := strings.Split(view, "\n")
			if len(lines) > tc.h {
				t.Errorf("view is %d rows, terminal is %d", len(lines), tc.h)
			}
			for _, line := range lines {
				if got := lipgloss.Width(line); got > tc.w {
					t.Errorf("line is %d columns, terminal is %d: %q", got, tc.w, clipANSI(line, tc.w+10))
				}
			}
		})
	}
}

func TestInspectorLayout(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	if !c.panelVisible() {
		t.Fatal("the inspector should be visible at 120 columns")
	}
	if got := c.mainWidth() + c.panelWidth() + 1; got != c.w {
		t.Errorf("panes take %d columns, want %d", got, c.w)
	}
	c.resize(80, 24)
	if c.panelVisible() {
		t.Fatal("the inspector must not squeeze a narrow terminal")
	}
	if c.mainWidth() != c.w {
		t.Errorf("mainWidth = %d, want %d", c.mainWidth(), c.w)
	}
}

func TestPaletteGroupsCommands(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.ta.SetValue("/")
	c.updatePalette()
	if len(c.cmdList) < 2 {
		t.Fatal("not enough commands")
	}
	if c.cmdList[0].insert != "/mode" || c.cmdList[1].insert != "/modes" {
		t.Fatalf("the first two commands are %q and %q", c.cmdList[0].insert, c.cmdList[1].insert)
	}
	view := c.paletteView()
	for _, want := range []string{"SESSION", "MODELS", "↳ chat · create", "more below"} {
		if !strings.Contains(view, want) {
			t.Errorf("palette is missing %q:\n%s", want, view)
		}
	}
}

func TestFilePickerCompletesAPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "main.go"), []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := testChat(t)
	c.opt.Cwd = dir
	c.ta.SetValue("read @RE")
	c.updateFilePalette()
	if !c.fileActive || len(c.fileList) == 0 {
		t.Fatalf("the file picker did not match: %+v", c.fileList)
	}
	if got := c.fileList[0].path; got != "README.md" {
		t.Errorf("first match = %q", got)
	}
	c.filePaletteKey(keyMsg("tab"))
	if got := c.ta.Value(); got != "read @README.md" {
		t.Errorf("completed value = %q", got)
	}
	if c.fileActive {
		t.Error("the picker should close after choosing a file")
	}
}

func TestFilePickerOffersDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "wombat"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := testChat(t)
	c.opt.Cwd = dir
	c.ta.SetValue("@cm")
	c.updateFilePalette()
	if len(c.fileList) == 0 || c.fileList[0].path != "cmd/" {
		t.Fatalf("directory match = %+v", c.fileList)
	}
	c.filePaletteKey(keyMsg("tab"))
	if got := c.ta.Value(); got != "@cmd/" {
		t.Errorf("directory completion = %q", got)
	}
	if !c.fileActive {
		t.Error("the picker should stay open inside a directory")
	}
}

func TestPromptHistory(t *testing.T) {
	c := testChat(t)
	c.recordHistory("first")
	c.recordHistory("second")
	c.ta.SetValue("")
	c.historyPrev()
	if got := c.ta.Value(); got != "second" {
		t.Errorf("prev = %q", got)
	}
	c.historyPrev()
	if got := c.ta.Value(); got != "first" {
		t.Errorf("prev twice = %q", got)
	}
	c.historyNext()
	c.historyNext()
	if got := c.ta.Value(); got != "" {
		t.Errorf("forward past the end = %q", got)
	}
}

func TestThemePickerRestoresOnEscape(t *testing.T) {
	c := testChat(t)
	applyTheme("wombat")
	c.openThemePicker()
	c.themeSel = 1
	c.previewTheme(themeOrder[1])
	if activeTheme != themeOrder[1] {
		t.Fatalf("preview did not apply: %q", activeTheme)
	}
	c.modalKey(keyMsg("esc"))
	if activeTheme != "wombat" {
		t.Errorf("escape left theme %q, want wombat", activeTheme)
	}
	if c.modal != modalNone {
		t.Error("escape did not close the picker")
	}
}

func TestConfigDefaultsForCopy(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Mouse {
		t.Error("mouse reporting should be off by default so text selection works")
	}
	if !cfg.PaintBackground {
		t.Error("the terminal background should follow the theme by default")
	}
}

func TestReferencesPersist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	c := testChat(t)
	c.opt.Cwd = t.TempDir()
	if err := os.WriteFile(filepath.Join(c.opt.Cwd, "notes.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.addReference("notes.md"); err != nil {
		t.Fatal(err)
	}
	if err := c.addReference("https://example.com/spec"); err != nil {
		t.Fatal(err)
	}
	if len(c.refs) != 2 {
		t.Fatalf("refs = %+v", c.refs)
	}
	if c.refs[0].Kind != "file" || c.refs[1].Kind != "site" {
		t.Errorf("kinds = %q, %q", c.refs[0].Kind, c.refs[1].Kind)
	}
	if err := c.addReference("notes.md"); err == nil {
		t.Error("duplicate references should be refused")
	}
	stored := loadReferences(c.opt.Cwd)
	if len(stored) != 2 {
		t.Fatalf("stored refs = %+v", stored)
	}
	if err := c.removeReference("1"); err != nil {
		t.Fatal(err)
	}
	if len(loadReferences(c.opt.Cwd)) != 1 {
		t.Error("remove did not persist")
	}
	if err := c.clearReferences(); err != nil {
		t.Fatal(err)
	}
	if len(loadReferences(c.opt.Cwd)) != 0 {
		t.Error("clear did not persist")
	}
}

func TestSourcesBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cwd := t.TempDir()
	sess, err := session.CreateAt(home, cwd, "openai", "test-model", "sources")
	if err != nil {
		t.Fatal(err)
	}
	tracePath := strings.TrimSuffix(sess.Path(), ".jsonl") + ".events.jsonl"
	events := `{"kind":"tool_start","name":"webfetch","detail":"https://example.com/spec"}
{"kind":"tool_start","name":"read","detail":"main.go"}
`
	if err := os.WriteFile(tracePath, []byte(events), 0o600); err != nil {
		t.Fatal(err)
	}
	c := testChat(t)
	c.opt.Cwd = cwd
	c.sess = sess
	if cmd := c.openSources(); cmd != nil {
		t.Fatal("openSources should not start a command")
	}
	if c.modal != modalSources {
		t.Fatal("the sources browser did not open")
	}
	if c.sourceSites != 1 || c.sourceFiles != 1 {
		t.Errorf("counts = %d sites, %d files", c.sourceSites, c.sourceFiles)
	}
	if len(c.visibleSources()) != 2 {
		t.Errorf("all sources = %+v", c.visibleSources())
	}
	c.sourceKey(keyMsg("tab"))
	if got := c.visibleSources(); len(got) != 1 || got[0].kind != "site" {
		t.Errorf("sites tab = %+v", got)
	}
	c.sourceKey(keyMsg("tab"))
	if got := c.visibleSources(); len(got) != 1 || got[0].kind != "file" {
		t.Errorf("files tab = %+v", got)
	}
	c.sourceKey(keyMsg("esc"))
	if c.modal != modalNone {
		t.Error("escape did not close the browser")
	}
}

func TestScrollbarAppearsForLongTranscripts(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.vp.SetContent(strings.Repeat("a line of transcript\n", 200))
	c.vp.GotoTop()
	bars := c.scrollbarLines(c.vp.Height)
	if len(bars) != c.vp.Height {
		t.Fatalf("bars = %d, want %d", len(bars), c.vp.Height)
	}
	joined := strings.Join(bars, "")
	if !strings.Contains(joined, "┃") || !strings.Contains(joined, "│") {
		t.Errorf("scrollbar has no thumb/track: %q", joined)
	}
	c.vp.GotoBottom()
	atBottom := strings.Join(c.scrollbarLines(c.vp.Height), "")
	if atBottom == joined {
		t.Error("the thumb did not move to the bottom")
	}
}

func TestEveryThemeWelcomeFits(t *testing.T) {
	for _, name := range themeOrder {
		applyTheme(name)
		c := testChat(t)
		c.resize(120, 34)
		c.layout()
		c.refresh()
		for _, line := range strings.Split(c.view(), "\n") {
			if got := lipgloss.Width(line); got > c.w {
				t.Errorf("%s welcome line is %d wide, terminal %d", name, got, c.w)
			}
		}
	}
	applyTheme(themeDefault)
}

func TestThemePickerShowsEveryTheme(t *testing.T) {
	for _, sz := range [][2]int{{120, 34}, {80, 24}, {60, 20}, {44, 20}} {
		c := testChat(t)
		c.resize(sz[0], sz[1])
		c.openThemePicker()
		card := stripANSI(c.themeCard())
		for _, th := range themeEntries() {
			if !strings.Contains(card, th.label) {
				t.Errorf("%dx%d theme picker is missing %q:\n%s", sz[0], sz[1], th.label, card)
			}
		}
	}
}

func TestThemePickerScrollsThroughEveryTheme(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.openThemePicker()
	for i := range themeOrder {
		c.themeSel = i
		c.layout()
		c.refresh()
		for _, line := range strings.Split(c.view(), "\n") {
			if got := lipgloss.Width(line); got > c.w {
				t.Errorf("theme %d picker line is %d wide, terminal %d", i, got, c.w)
			}
		}
		if card := c.themeCard(); card == "" {
			t.Fatalf("theme %d has an empty card", i)
		}
	}
}

func TestPaletteShowsThemeOptions(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.ta.SetValue("/theme")
	c.updatePalette()
	view := c.paletteView()
	for _, want := range []string{"THEMES", "Wombat", "Tokyo Night", "Catppuccin"} {
		if !strings.Contains(view, want) {
			t.Errorf("theme palette is missing %q:\n%s", want, view)
		}
	}
	c.ta.SetValue("/theme ")
	c.updatePalette()
	view = c.paletteView()
	for _, want := range []string{"wombat", "tokyo", "catppuccin"} {
		if !strings.Contains(view, want) {
			t.Errorf("theme options are missing %q:\n%s", want, view)
		}
	}
}

func TestPreviewRailLayout(t *testing.T) {
	c := testChat(t)
	c.resize(140, 40)
	c.panel = true
	c.previews = []preview{
		{Ref: "http://localhost:5173/", Kind: "site", Label: "http://localhost:5173/"},
		{Ref: "report.pdf", Kind: "pdf", Label: "report.pdf"},
	}
	if !c.previewRailVisible() {
		t.Fatal("the preview rail should be visible at 140 columns")
	}
	total := c.previewRailWidth() + 1 + c.mainWidth() + 1 + c.panelWidth()
	if total != c.w {
		t.Errorf("panes take %d columns, want %d", total, c.w)
	}
	c.layout()
	c.refresh()
	view := c.view()
	if !strings.Contains(view, "PREVIEWS") {
		t.Error("the view is missing the preview rail")
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > c.w {
			t.Errorf("line is %d wide, terminal %d", got, c.w)
		}
	}
	c.resize(100, 34)
	if c.previewRailVisible() {
		t.Error("the preview rail should wait for 120 columns")
	}
	if !c.panelVisible() {
		t.Error("the inspector should stay visible at 100 columns")
	}
}

func TestInspectorShowsMascot(t *testing.T) {
	c := testChat(t)
	c.resize(140, 40)
	c.panel = true
	c.layout()
	view := stripANSI(c.panelView())
	if !strings.Contains(view, "█") {
		t.Errorf("the inspector is missing the WOMBAT mark:\n%s", view)
	}
}

func TestQuestionWithoutOptionsFocusesInput(t *testing.T) {
	c := testChat(t)
	c.update(questionMsg{question: tools.Question{
		Header:   "changes",
		Question: "What should change?",
	}})
	if c.question == nil || !c.questionTyping {
		t.Fatalf("a question without options should focus the text input: typing=%v", c.questionTyping)
	}
	c.questionText.SetValue("make it blue")
	if cmd := c.key(keyMsg("enter")); cmd != nil {
		t.Fatal("enter should send the typed answer")
	}
	if c.question != nil {
		t.Error("the question should close after the answer")
	}
}

func TestCopyPicker(t *testing.T) {
	c := testChat(t)
	c.resize(120, 34)
	c.entries = []*chatEntry{
		{kind: entryUser, text: "question"},
		{kind: entryAssistant, text: "first answer"},
		{kind: entryAssistant, text: "second answer"},
	}
	if got := c.copyableMessages(); len(got) != 2 || got[0] != "second answer" {
		t.Fatalf("copyableMessages = %+v", got)
	}
	if cmd := c.openCopyPicker(); cmd != nil {
		t.Fatal("openCopyPicker should not return a command")
	}
	if c.modal != modalCopy || len(c.copyItems) != 2 {
		t.Fatalf("copy picker = %+v", c.copyItems)
	}
	if card := c.copyCard(); !strings.Contains(card, "COPY") || !strings.Contains(card, "second answer") {
		t.Errorf("copy card =\n%s", card)
	}
	c.copyKey(keyMsg("down"))
	if c.copySel != 1 {
		t.Errorf("selection = %d", c.copySel)
	}
	c.copyKey(keyMsg("esc"))
	if c.modal != modalNone {
		t.Error("escape did not close the copy picker")
	}
}

func TestGitBranch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/feature/tui\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(nested); got != "feature/tui" {
		t.Errorf("branch = %q", got)
	}
	if got := gitBranch(t.TempDir()); got != "" {
		t.Errorf("branch outside git = %q", got)
	}
}

func TestToolGlyphs(t *testing.T) {
	for _, name := range []string{"bash", "read", "write", "grep", "browser", "delegate_many", "worker:edit"} {
		glyph, _ := toolGlyph(name)
		if glyph == "" || glyph == "•" {
			t.Errorf("no glyph for %q", name)
		}
	}
	if glyph, _ := toolGlyph("worker:read"); glyph != "◫" {
		t.Errorf("worker prefix not stripped: %q", glyph)
	}
}

func TestMarkdownTablesAndDiffs(t *testing.T) {
	out := strings.Join(renderMarkdown("| a | b |\n|---|---|\n| 1 | 2 |\n", 60), "\n")
	for _, want := range []string{"┌", "a", "b", "1", "2", "└"} {
		if !strings.Contains(out, want) {
			t.Errorf("table is missing %q:\n%s", want, out)
		}
	}
	diff := strings.Join(renderMarkdown("```diff\n-old\n+new\n```\n", 60), "\n")
	if !strings.Contains(diff, "diff") || !strings.Contains(diff, "-old") || !strings.Contains(diff, "+new") {
		t.Errorf("diff fence =\n%s", diff)
	}
}

func TestScreenTabsAndGlobalKeys(t *testing.T) {
	c := testChat(t)
	m := &rootModel{chat: c, screen: scrChat}
	if cmd, ok := m.globalKey(keyMsg("alt+5")); !ok || cmd == nil {
		t.Fatal("ctrl+5 should open the doctor screen")
	}
	if m.screen != scrDoctor {
		t.Errorf("screen = %v, want doctor", m.screen)
	}
	tabs := screenTabs(scrSessions)
	for _, want := range []string{"chat", "sessions", "models", "usage", "doctor"} {
		if !strings.Contains(tabs, want) {
			t.Errorf("tabs are missing %q: %s", want, tabs)
		}
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	if strings.HasPrefix(s, "alt+") {
		return tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune(strings.TrimPrefix(s, "alt+"))}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
