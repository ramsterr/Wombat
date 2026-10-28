package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/session"
)

func agentsTestChat(t *testing.T) *chat {
	t.Helper()
	cfg := config.Defaults()
	cfg.Provider = "ollama"
	cfg.Model = "llama3"
	cfg.Theme = "wombat"
	cfg.Panel = true
	c, err := newChat(Options{Version: "test", Cwd: t.TempDir(), Cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	c.mode = mode.Create
	c.provName = "ollama"
	c.modelName = "llama3"
	c.window = 32_000
	return c
}

func writeAgentFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupAgentFiles(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	writeAgentFile(t, filepath.Join(home, "agents", "math.md"),
		"---\nname: math\ndescription: verifies mathematics\nreadonly: true\n---\nVerify every step.\n")
	writeAgentFile(t, filepath.Join(home, "agents", "design.json"),
		`{"name":"design","description":"designs decks","model":"design-model"}`)
	return home
}

func TestAgentsCommandSelectsAndPersists(t *testing.T) {
	setupAgentFiles(t)
	c := agentsTestChat(t)

	if cmd := c.command("/agents math,design"); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	if got := strings.Join(c.agentSel, ","); got != "math,design" {
		t.Fatalf("selection = %q", got)
	}
	if err := c.ensureSession(); err != nil {
		t.Fatal(err)
	}
	if c.sess == nil {
		t.Fatal("no session created")
	}
	if got := strings.Join(c.sess.Meta.Agents, ","); got != "math,design" {
		t.Fatalf("session meta agents = %q", got)
	}
	reopened, err := session.Open(c.sess.ID(), c.opt.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(reopened.Meta.Agents, ","); got != "math,design" {
		t.Fatalf("reopened meta agents = %q", got)
	}

	if cmd := c.command("/agents clear"); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	if len(c.agentSel) != 0 {
		t.Fatalf("selection = %v", c.agentSel)
	}
	if c.sess.Meta.Agents != nil {
		t.Fatalf("session meta agents = %v", c.sess.Meta.Agents)
	}
}

func TestAgentsCommandRejectsUnknown(t *testing.T) {
	setupAgentFiles(t)
	c := agentsTestChat(t)
	c.command("/agents math")
	before := strings.Join(c.agentSel, ",")
	c.command("/agents does-not-exist")
	if got := strings.Join(c.agentSel, ","); got != before {
		t.Fatalf("selection changed on an unknown agent: %q", got)
	}
	last := c.entries[len(c.entries)-1]
	if !last.isErr || !strings.Contains(last.text, "unknown agent") {
		t.Fatalf("last entry = %+v", last)
	}
}

func TestAgentPickerTogglesAndApplies(t *testing.T) {
	setupAgentFiles(t)
	c := agentsTestChat(t)
	if cmd := c.openAgentPicker(); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	if c.modal != modalAgents {
		t.Fatalf("modal = %v", c.modal)
	}
	if len(c.agentRows) != 3 {
		t.Fatalf("rows = %+v", c.agentRows)
	}
	if !c.agentRows[0].create {
		t.Fatal("the first row should create a new agent")
	}
	// Space on the create row does nothing. Then toggle design and math.
	c.agentsKey(tea.KeyMsg{Type: tea.KeySpace})
	c.agentsKey(tea.KeyMsg{Type: tea.KeyDown})
	c.agentsKey(tea.KeyMsg{Type: tea.KeySpace})
	c.agentsKey(tea.KeyMsg{Type: tea.KeyDown})
	c.agentsKey(tea.KeyMsg{Type: tea.KeySpace})
	c.agentsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if c.modal != modalNone {
		t.Fatalf("modal = %v", c.modal)
	}
	// Definitions are listed alphabetically, so the picker applies them in
	// that order.
	if got := strings.Join(c.agentSel, ","); got != "design,math" {
		t.Fatalf("selection = %q", got)
	}
	// The card renders and shows the selection.
	c.agentDraft = map[string]bool{"math": true}
	if card := c.agentsCard(); !strings.Contains(card, "CUSTOM AGENTS") {
		t.Fatalf("card = %q", card)
	}
}

func TestAgentPickerCreateOpensWizard(t *testing.T) {
	setupAgentFiles(t)
	c := agentsTestChat(t)
	c.openAgentPicker()
	// The create row is selected first; enter opens the builder.
	c.agentsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if c.modal != modalAgentForm {
		t.Fatalf("modal = %v", c.modal)
	}
	if c.wizard == nil || !c.wizard.editing {
		t.Fatalf("wizard = %+v", c.wizard)
	}
	if len(wizardSections) != 8 {
		t.Fatalf("sections = %v", wizardSections)
	}
	if card := c.agentFormCard(); !strings.Contains(card, "NEW CUSTOM AGENT") {
		t.Fatalf("card = %q", card)
	}
	// esc leaves the field editor, a second esc returns to the picker.
	c.agentFormKey(tea.KeyMsg{Type: tea.KeyEsc})
	c.agentFormKey(tea.KeyMsg{Type: tea.KeyEsc})
	if c.wizard != nil || c.modal != modalAgents {
		t.Fatalf("wizard = %+v modal = %v", c.wizard, c.modal)
	}
}

func TestWizardCreatesAgentFile(t *testing.T) {
	home := setupAgentFiles(t)
	writeAgentFile(t, filepath.Join(home, "skills", "proofs.md"),
		"---\nname: proofs\ndescription: careful proofs\n---\nJustify each step.\n")
	c := agentsTestChat(t)
	if cmd := c.openAgentWizard(); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	w := c.wizard
	w.name.SetValue("math-research")
	w.desc.SetValue("Verifies mathematics")
	w.model.SetValue("reasoning-model")
	w.prompt.SetValue("You verify proofs rigorously.")
	w.readonly = true
	// Select the read-only class and the proofs skill.
	for i := range w.tools {
		if w.tools[i].name == "class:read" {
			w.tools[i].on = true
		}
	}
	for i := range w.skills {
		if w.skills[i].name == "proofs" {
			w.skills[i].on = true
		}
	}
	w.perms[0].value = "deny" // write

	if cmd := c.saveWizard(); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	path := filepath.Join(home, "agents", "math-research.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("agent file not written: %v", err)
	}
	def, err := config.ParseAgentJSON(data, "math-research")
	if err != nil {
		t.Fatalf("created file does not parse: %v", err)
	}
	if def.Description != "Verifies mathematics" || !def.ReadOnly {
		t.Fatalf("def = %+v", def)
	}
	if def.Prompt != "You verify proofs rigorously." {
		t.Fatalf("prompt = %q", def.Prompt)
	}
	if strings.Join(def.Tools, ",") != "class:read" || strings.Join(def.Skills, ",") != "proofs" {
		t.Fatalf("tools = %v skills = %v", def.Tools, def.Skills)
	}
	if def.Permissions["write"] != "deny" {
		t.Fatalf("permissions = %#v", def.Permissions)
	}
	if def.Model != "reasoning-model" {
		t.Fatalf("model = %q", def.Model)
	}
	// The picker reopens with the new agent available.
	if c.modal != modalAgents {
		t.Fatalf("modal = %v", c.modal)
	}
	found := false
	for _, row := range c.agentRows {
		if row.name == "math-research" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the new agent is not in the picker: %+v", c.agentRows)
	}
}

func TestWizardRejectsInvalidName(t *testing.T) {
	home := setupAgentFiles(t)
	c := agentsTestChat(t)
	c.openAgentWizard()
	c.wizard.name.SetValue("")
	if cmd := c.saveWizard(); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	if c.wizard == nil {
		t.Fatal("the wizard should stay open after a validation error")
	}
	last := c.entries[len(c.entries)-1]
	if !last.isErr || !strings.Contains(last.text, "name is required") {
		t.Fatalf("last entry = %+v", last)
	}
	if _, err := os.Stat(filepath.Join(home, "agents", ".json")); err == nil {
		t.Fatal("an invalid definition was written")
	}
}

func TestSkillsCommandListsAndShowsDetails(t *testing.T) {
	home := setupAgentFiles(t)
	writeAgentFile(t, filepath.Join(home, "skills", "proofs.md"),
		"---\nname: proofs\ndescription: careful proofs\n---\nJustify each step.\n")
	endpoint := filepath.Join(home, "agents", "verified.json")
	writeAgentFile(t, endpoint, `{"name":"verified","skills":["proofs"]}`)
	c := agentsTestChat(t)

	if cmd := c.command("/skill"); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	if c.modal != modalSkills {
		t.Fatalf("modal = %v", c.modal)
	}
	if len(c.skillRows) != 1 || c.skillRows[0].name != "proofs" {
		t.Fatalf("skill rows = %+v", c.skillRows)
	}
	if len(c.skillRows[0].usedBy) != 1 || c.skillRows[0].usedBy[0] != "verified" {
		t.Fatalf("usedBy = %v", c.skillRows[0].usedBy)
	}
	card := c.skillsCard()
	if !strings.Contains(card, "SKILLS") || !strings.Contains(card, "Justify each step") {
		t.Fatalf("card = %q", card)
	}
	c.skillsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !c.skillExpanded {
		t.Fatal("enter should expand the skill preview")
	}

	c.modal = modalNone
	c.notePre(c.skillDetail("proofs"))
	last := c.entries[len(c.entries)-1]
	if !strings.Contains(last.text, "source:") || !strings.Contains(last.text, "Justify each step") {
		t.Fatalf("detail = %q", last.text)
	}

	c.notePre(c.skillsList())
	last = c.entries[len(c.entries)-1]
	if !strings.Contains(last.text, "installed skills") || !strings.Contains(last.text, "used by") {
		t.Fatalf("list = %q", last.text)
	}
}

func TestAgentPaletteOptions(t *testing.T) {
	setupAgentFiles(t)
	c := agentsTestChat(t)
	opts := c.agentPaletteOptions("ma")
	var values []string
	for _, opt := range opts {
		values = append(values, opt.Value)
	}
	if strings.Join(values, ",") != "math" {
		t.Fatalf("filtered options = %v", values)
	}
	all := c.agentPaletteOptions("")
	values = nil
	for _, opt := range all {
		values = append(values, opt.Value)
	}
	for _, want := range []string{"list", "clear", "math", "design"} {
		found := false
		for _, value := range values {
			if value == want {
				found = true
			}
		}
		if !found {
			t.Errorf("palette is missing %q: %v", want, values)
		}
	}
}

func TestAgentsListShowsProblems(t *testing.T) {
	home := setupAgentFiles(t)
	writeAgentFile(t, filepath.Join(home, "agents", "broken.json"), `{"name":`)
	c := agentsTestChat(t)
	list := c.agentsList()
	if !strings.Contains(list, "!") {
		t.Fatalf("list does not show the broken definition:\n%s", list)
	}
	if !strings.Contains(list, "math") || !strings.Contains(list, "design") {
		t.Fatalf("list is missing agents:\n%s", list)
	}
}

func TestAgentsBadge(t *testing.T) {
	c := agentsTestChat(t)
	if badge := c.agentsBadge(); badge != "" {
		t.Fatalf("badge = %q", badge)
	}
	c.agentSel = []string{"math", "design"}
	if badge := c.agentsBadge(); badge != "team math+design" {
		t.Fatalf("badge = %q", badge)
	}
}
