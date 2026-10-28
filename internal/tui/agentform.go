package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/tools"
)

// Wizard sections, in display order.
const (
	wizardBasics = iota
	wizardPrompt
	wizardTools
	wizardMCP
	wizardPerms
	wizardSkills
	wizardOptions
	wizardSave
)

var wizardSections = []string{
	"Basics", "System prompt", "Tools", "MCP servers",
	"Permissions", "Skills", "Options", "Save",
}

type wizardItem struct {
	name string
	desc string
	on   bool
}

type wizardPerm struct {
	kind  string
	label string
	value string // "", "allow", "deny"
}

// agentWizard is the in-TUI agent builder. It edits a draft definition in
// place across eight sections and only writes the file on Save.
type agentWizard struct {
	section int
	editing bool

	name     textinput.Model
	desc     textinput.Model
	provider textinput.Model
	model    textinput.Model
	maxSteps textinput.Model
	field    int

	prompt textarea.Model

	tools    []wizardItem
	toolCur  int
	mcps     []wizardItem
	mcpCur   int
	skills   []wizardItem
	skillCur int
	perms    []wizardPerm
	permCur  int

	readonly bool
}

func wizardInput(placeholder string, limit int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.CharLimit = limit
	input.Width = 48
	return input
}

func newAgentWizard(c *chat) *agentWizard {
	cwd := c.agentCwd()
	w := &agentWizard{
		name:     wizardInput("math-research", config.MaxAgentNameLen),
		desc:     wizardInput("when the planner should use this agent", 200),
		provider: wizardInput("same as session", 40),
		model:    wizardInput("same as session", 80),
		maxSteps: wizardInput("0 = session default", 3),
	}
	w.prompt = textarea.New()
	w.prompt.Prompt = ""
	w.prompt.Placeholder = "You are a specialist. Describe your expertise, method, and quality bar…"
	w.prompt.ShowLineNumbers = false
	w.prompt.CharLimit = 0
	w.prompt.MaxHeight = 14
	w.prompt.SetWidth(60)
	w.prompt.SetHeight(8)

	w.tools = wizardToolItems()
	if c.cfg != nil {
		names := make([]string, 0, len(c.cfg.MCPServers))
		for name := range c.cfg.MCPServers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			server := c.cfg.MCPServers[name]
			desc := "mcp server"
			if server != nil {
				if !server.EffectiveEnabled() {
					desc = "disabled"
				} else if len(server.AllowTools) > 0 {
					desc = "mcp server · filtered"
				}
			}
			w.mcps = append(w.mcps, wizardItem{name: name, desc: desc})
		}
	}
	skills, problems := agents.LoadSkills(config.Home(), cwd)
	c.skillProblems = problems
	for _, skill := range skills {
		w.skills = append(w.skills, wizardItem{name: skill.Name, desc: skill.Description})
	}
	w.perms = []wizardPerm{
		{kind: "write", label: "write files"},
		{kind: "edit", label: "edit files"},
		{kind: "bash", label: "shell commands"},
		{kind: "network", label: "network access"},
		{kind: "mcp", label: "MCP tools"},
		{kind: "dangerous", label: "dangerous commands"},
		{kind: "sensitive-read", label: "credential reads"},
		{kind: "outside", label: "paths outside the workspace"},
	}
	w.focusField()
	return w
}

// agentCwd is the workspace the session belongs to.
func (c *chat) agentCwd() string {
	if c.sess != nil && c.sess.Meta.Cwd != "" {
		return c.sess.Meta.Cwd
	}
	return c.opt.Cwd
}

func wizardToolItems() []wizardItem {
	items := []wizardItem{
		{name: "class:read", desc: "every read-only builtin tool"},
		{name: "class:network", desc: "web search and fetch"},
		{name: "class:bash", desc: "shell commands"},
		{name: "class:write", desc: "write and edit files"},
	}
	seen := map[string]bool{}
	for _, tool := range tools.Registry() {
		if seen[tool.Name()] {
			continue
		}
		seen[tool.Name()] = true
		items = append(items, wizardItem{name: tool.Name(), desc: tool.Class()})
	}
	sort.SliceStable(items[4:], func(i, j int) bool { return items[4+i].name < items[4+j].name })
	return items
}

// fields exposes the basic text inputs for focus cycling.
func (w *agentWizard) fields() []*textinput.Model {
	return []*textinput.Model{&w.name, &w.desc, &w.provider, &w.model, &w.maxSteps}
}

func (w *agentWizard) focusField() {
	for i, field := range w.fields() {
		if i == w.field {
			field.Focus()
		} else {
			field.Blur()
		}
	}
}

func (w *agentWizard) blurAll() {
	for _, field := range w.fields() {
		field.Blur()
	}
	w.prompt.Blur()
}

// openAgentWizard starts the create flow, ready to type a name.
func (c *chat) openAgentWizard() tea.Cmd {
	c.wizard = newAgentWizard(c)
	c.wizard.editing = true
	c.wizard.field = 0
	c.wizard.focusField()
	c.modal = modalAgentForm
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

func (c *chat) closeWizard() {
	if c.wizard != nil {
		c.wizard.blurAll()
	}
	c.wizard = nil
	// Return to the picker so the user can keep selecting.
	c.openAgentPicker()
}

// agentFormKey navigates the section rail and the active editor.
func (c *chat) agentFormKey(msg tea.KeyMsg) tea.Cmd {
	w := c.wizard
	if w == nil {
		c.modal = modalNone
		return nil
	}
	if !w.editing {
		switch msg.String() {
		case "esc", "ctrl+c":
			c.closeWizard()
			return nil
		case "up", "k":
			if w.section > 0 {
				w.section--
			}
		case "down", "j":
			if w.section < len(wizardSections)-1 {
				w.section++
			}
		case "enter", "right", "l", " ":
			if w.section == wizardSave {
				return c.saveWizard()
			}
			w.editing = true
			if w.section == wizardBasics {
				w.focusField()
			}
			if w.section == wizardPrompt {
				w.prompt.Focus()
			}
		case "ctrl+s":
			return c.saveWizard()
		}
		c.layout()
		c.refresh()
		return nil
	}
	return c.agentFormEditKey(msg)
}

func (c *chat) agentFormEditKey(msg tea.KeyMsg) tea.Cmd {
	w := c.wizard
	if msg.String() == "ctrl+s" && w.section != wizardPrompt {
		return c.saveWizard()
	}
	switch w.section {
	case wizardBasics:
		fields := w.fields()
		switch msg.String() {
		case "esc":
			w.blurAll()
			w.editing = false
		case "tab", "down":
			w.field = (w.field + 1) % len(fields)
			w.focusField()
		case "shift+tab", "up":
			w.field = (w.field - 1 + len(fields)) % len(fields)
			w.focusField()
		case "enter":
			if w.field < len(fields)-1 {
				w.field++
				w.focusField()
			} else {
				w.blurAll()
				w.editing = false
			}
		default:
			var cmd tea.Cmd
			switch w.field {
			case 0:
				w.name, cmd = w.name.Update(msg)
			case 1:
				w.desc, cmd = w.desc.Update(msg)
			case 2:
				w.provider, cmd = w.provider.Update(msg)
			case 3:
				w.model, cmd = w.model.Update(msg)
			case 4:
				w.maxSteps, cmd = w.maxSteps.Update(msg)
			}
			c.layout()
			c.refresh()
			return cmd
		}
	case wizardPrompt:
		switch msg.String() {
		case "esc", "ctrl+s":
			w.prompt.Blur()
			w.editing = false
		default:
			var cmd tea.Cmd
			w.prompt, cmd = w.prompt.Update(msg)
			c.layout()
			c.refresh()
			return cmd
		}
	case wizardTools:
		cmd := c.wizardListKey(msg, &w.tools, &w.toolCur)
		if cmd != nil {
			return cmd
		}
	case wizardMCP:
		cmd := c.wizardListKey(msg, &w.mcps, &w.mcpCur)
		if cmd != nil {
			return cmd
		}
	case wizardSkills:
		cmd := c.wizardListKey(msg, &w.skills, &w.skillCur)
		if cmd != nil {
			return cmd
		}
	case wizardPerms:
		switch msg.String() {
		case "esc", "enter":
			w.editing = false
		case "up", "k":
			if w.permCur > 0 {
				w.permCur--
			}
		case "down", "j":
			if w.permCur < len(w.perms)-1 {
				w.permCur++
			}
		case " ", "x":
			if w.permCur >= 0 && w.permCur < len(w.perms) {
				switch w.perms[w.permCur].value {
				case "":
					w.perms[w.permCur].value = "allow"
				case "allow":
					w.perms[w.permCur].value = "deny"
				default:
					w.perms[w.permCur].value = ""
				}
			}
		}
	case wizardOptions:
		switch msg.String() {
		case "esc", "enter":
			w.editing = false
		case " ", "x":
			w.readonly = !w.readonly
		}
	}
	c.layout()
	c.refresh()
	return nil
}

// wizardListKey handles the shared multi-select list editor. It returns nil
// after acting; the caller falls through to layout/refresh.
func (c *chat) wizardListKey(msg tea.KeyMsg, items *[]wizardItem, cursor *int) tea.Cmd {
	w := c.wizard
	switch msg.String() {
	case "esc", "enter":
		w.editing = false
	case "up", "k":
		if *cursor > 0 {
			*cursor--
		}
	case "down", "j":
		if *cursor < len(*items)-1 {
			*cursor++
		}
	case "home", "g":
		*cursor = 0
	case "end", "G":
		if len(*items) > 0 {
			*cursor = len(*items) - 1
		}
	case " ":
		if *cursor >= 0 && *cursor < len(*items) {
			(*items)[*cursor].on = !(*items)[*cursor].on
		}
	case "a", "A":
		all := true
		for _, item := range *items {
			if !item.on {
				all = false
				break
			}
		}
		for i := range *items {
			(*items)[i].on = !all
		}
	case "x", "X":
		for i := range *items {
			(*items)[i].on = false
		}
	}
	return nil
}

// agentFormCard renders the wizard.
func (c *chat) agentFormCard() string {
	w := c.wizard
	if w == nil {
		return ""
	}
	dir := agents.DefDir(config.Home(), c.agentCwd())
	cardW := min(max(58, c.mainWidth()-6), 96)
	innerW := max(42, cardW-6)

	lines := []string{
		styleAccent.Bold(true).Render("◈  NEW CUSTOM AGENT") + "  " + styleFaint.Render("saved to "+shortPath(dir, 34)),
		"",
	}
	if w.editing {
		lines = append(lines, styleSection.Render(wizardSections[w.section]))
		lines = append(lines, "")
		lines = append(lines, c.wizardEditorLines(innerW)...)
	} else {
		lines = append(lines, styleDim.Render("select a section, then enter to edit it"), "")
		for i, name := range wizardSections {
			row := pad(name, 15) + " " + w.summary(i)
			if i == w.section {
				lines = append(lines, styleSelected.Render(" > "+pad(clip(row, innerW-3), innerW-3)))
			} else {
				lines = append(lines, "   "+styleDim.Render(clip(row, innerW-3)))
			}
		}
		lines = append(lines, "", " "+styleFaint.Render("↑↓ move · enter edit · ctrl+s save · esc cancel"))
	}
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

// wizardEditorLines renders the editor for the active section.
func (c *chat) wizardEditorLines(innerW int) []string {
	w := c.wizard
	var lines []string
	switch w.section {
	case wizardBasics:
		labels := []string{"name", "description", "provider", "model", "max steps"}
		for i, field := range w.fields() {
			field.Width = max(10, innerW-20)
			marker := "  "
			if i == w.field && w.editing {
				marker = "› "
			}
			lines = append(lines, marker+pad(labels[i], 12)+" "+field.View())
		}
		lines = append(lines,
			"",
			" "+styleFaint.Render("name is required · tab/↑↓ switch fields · enter done · esc done"),
		)
	case wizardPrompt:
		w.prompt.SetWidth(max(24, innerW-4))
		w.prompt.SetHeight(min(12, max(5, c.vp.Height-16)))
		lines = append(lines, strings.Split(w.prompt.View(), "\n")...)
		lines = append(lines, "", " "+styleFaint.Render("ctrl+s or esc done · enter adds a line"))
	case wizardTools:
		lines = append(lines, c.wizardListLines(w.tools, w.toolCur, c.wizardListHeight(), "no builtin tools")...)
	case wizardMCP:
		lines = append(lines, c.wizardListLines(w.mcps, w.mcpCur, c.wizardListHeight(),
			"no MCP servers configured — add one with `wombat mcp add`")...)
	case wizardSkills:
		lines = append(lines, c.wizardListLines(w.skills, w.skillCur, c.wizardListHeight(),
			"no skills installed — add ~/.wombat/skills/<name>.md or .wombat/skills/<name>.md")...)
	case wizardPerms:
		for i, perm := range w.perms {
			value := perm.value
			rendered := styleFaint.Render("inherit")
			switch value {
			case "allow":
				rendered = styleGood.Render("allow")
			case "deny":
				rendered = styleBad.Render("deny")
			}
			row := pad(perm.kind, 16) + " " + rendered + styleDim.Render("  "+perm.label)
			if i == w.permCur {
				lines = append(lines, styleSelected.Render(" > "+pad(clipANSI(row, innerW-3), innerW-3)))
			} else {
				lines = append(lines, "   "+clipANSI(row, innerW-3))
			}
		}
		lines = append(lines, "", " "+styleFaint.Render("space cycles inherit → allow → deny · enter done"))
	case wizardOptions:
		box := "[ ]"
		if w.readonly {
			box = "[x]"
		}
		lines = append(lines,
			" "+box+" read-only"+styleDim.Render("  block writes, edits, and mutating commands"),
			"",
			" "+styleFaint.Render("space toggles · enter done"),
			"",
			" "+styleDim.Render("Provider and model live in Basics; per-tool rules can be refined later in the JSON file."),
		)
	case wizardSave:
		lines = append(lines, c.wizardSummaryLines(innerW)...)
	}
	return lines
}

func (c *chat) wizardListHeight() int {
	return max(6, min(12, c.vp.Height-16))
}

func (c *chat) wizardListLines(items []wizardItem, cursor, maxRows int, empty string) []string {
	if len(items) == 0 {
		return []string{" " + styleFaint.Render(empty), "", " " + styleFaint.Render("enter done · esc done")}
	}
	if maxRows < 4 {
		maxRows = 4
	}
	start := 0
	if cursor >= maxRows {
		start = cursor - maxRows + 1
	}
	end := min(len(items), start+maxRows)
	var lines []string
	for i := start; i < end; i++ {
		item := items[i]
		box := "[ ]"
		if item.on {
			box = "[x]"
		}
		row := box + " " + pad(item.name, 22)
		if item.desc != "" {
			row += " " + styleDim.Render(item.desc)
		}
		if i == cursor {
			lines = append(lines, styleSelected.Render(" > "+pad(clipANSI(row, innerWOf(c)), innerWOf(c))))
		} else {
			lines = append(lines, "   "+clipANSI(row, innerWOf(c)))
		}
	}
	if start > 0 || end < len(items) {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("   %d–%d of %d", start+1, end, len(items))))
	}
	lines = append(lines, "", " "+styleFaint.Render("space toggle · a all/none · enter done · esc done"))
	return lines
}

// innerWOf is a convenience for list rows rendered outside the card helper.
func innerWOf(c *chat) int {
	return max(42, min(max(58, c.mainWidth()-6), 96)-6)
}

// summary is the one-line value preview for a section.
func (w *agentWizard) summary(section int) string {
	switch section {
	case wizardBasics:
		name := strings.TrimSpace(w.name.Value())
		if name == "" {
			name = styleWarn.Render("(name required)")
		}
		var extras []string
		if v := strings.TrimSpace(w.provider.Value()); v != "" {
			extras = append(extras, "provider "+v)
		}
		if v := strings.TrimSpace(w.model.Value()); v != "" {
			extras = append(extras, "model "+v)
		}
		if v := strings.TrimSpace(w.maxSteps.Value()); v != "" && v != "0" {
			extras = append(extras, v+" steps")
		}
		if len(extras) > 0 {
			name += styleFaint.Render(" · " + strings.Join(extras, " · "))
		}
		return name
	case wizardPrompt:
		chars := len(strings.TrimSpace(w.prompt.Value()))
		if chars == 0 {
			return styleFaint.Render("empty — base identity only")
		}
		return fmt.Sprintf("%d characters", chars)
	case wizardTools:
		names := selectedWizardNames(w.tools)
		if len(names) == 0 {
			return styleFaint.Render("all worker tools")
		}
		return clip(strings.Join(names, ", "), 60)
	case wizardMCP:
		names := selectedWizardNames(w.mcps)
		if len(names) == 0 {
			return styleFaint.Render("all connected servers")
		}
		return clip(strings.Join(names, ", "), 60)
	case wizardPerms:
		n := 0
		for _, perm := range w.perms {
			if perm.value != "" {
				n++
			}
		}
		if n == 0 {
			return styleFaint.Render("worker defaults")
		}
		return fmt.Sprintf("%d explicit decisions", n)
	case wizardSkills:
		names := selectedWizardNames(w.skills)
		if len(names) == 0 {
			return styleFaint.Render("none")
		}
		return clip(strings.Join(names, ", "), 60)
	case wizardOptions:
		if w.readonly {
			return styleGood.Render("read-only")
		}
		return styleFaint.Render("writable")
	case wizardSave:
		if strings.TrimSpace(w.name.Value()) == "" {
			return styleWarn.Render("add a name first")
		}
		return styleGood.Render("enter creates the agent")
	}
	return ""
}

func (c *chat) wizardSummaryLines(innerW int) []string {
	w := c.wizard
	name := config.NormalizeAgentName(w.name.Value())
	lines := []string{
		" name        " + name,
		" description " + oneLineText(w.desc.Value(), innerW-14),
		" prompt      " + fmt.Sprintf("%d characters", len(strings.TrimSpace(w.prompt.Value()))),
		" tools       " + clip(strings.Join(selectedWizardNames(w.tools), ", "), innerW-14),
		" mcp servers " + clip(strings.Join(selectedWizardNames(w.mcps), ", "), innerW-14),
		" skills      " + clip(strings.Join(selectedWizardNames(w.skills), ", "), innerW-14),
		" options     " + map[bool]string{true: "read-only", false: "writable"}[w.readonly],
		"",
		" " + styleFaint.Render("enter saves · esc cancels"),
	}
	return lines
}

func selectedWizardNames(items []wizardItem) []string {
	var names []string
	for _, item := range items {
		if item.on {
			names = append(names, item.name)
		}
	}
	return names
}

func wizardPermMap(perms []wizardPerm) map[string]string {
	out := map[string]string{}
	for _, perm := range perms {
		if perm.value != "" {
			out[perm.kind] = perm.value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// saveWizard validates the draft and writes the definition file.
func (c *chat) saveWizard() tea.Cmd {
	w := c.wizard
	if w == nil {
		return nil
	}
	def := &config.AgentDef{
		Name:        config.NormalizeAgentName(w.name.Value()),
		Description: strings.TrimSpace(w.desc.Value()),
		Prompt:      strings.TrimSpace(w.prompt.Value()),
		Provider:    strings.TrimSpace(w.provider.Value()),
		Model:       strings.TrimSpace(w.model.Value()),
		Tools:       selectedWizardNames(w.tools),
		MCPServers:  selectedWizardNames(w.mcps),
		Skills:      selectedWizardNames(w.skills),
		Permissions: wizardPermMap(w.perms),
		ReadOnly:    w.readonly,
	}
	if raw := strings.TrimSpace(w.maxSteps.Value()); raw != "" && raw != "0" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			c.note("max steps must be a number", true)
			return nil
		}
		def.MaxSteps = n
	}
	if def.Provider != "" && c.cfg != nil {
		if _, ok := c.cfg.Providers[def.Provider]; !ok {
			c.note("unknown provider "+def.Provider+" — /providers", true)
			return nil
		}
	}
	if err := def.Validate(); err != nil {
		c.note("agent: "+err.Error(), true)
		return nil
	}
	dir := agents.DefDir(config.Home(), c.agentCwd())
	path := filepath.Join(dir, def.Name+".json")
	if _, err := os.Stat(path); err == nil {
		c.note("an agent named "+def.Name+" already exists at "+path, true)
		return nil
	}
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		c.note("could not encode agent: "+err.Error(), true)
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		c.note("could not create "+dir+": "+err.Error(), true)
		return nil
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		c.note("could not write "+path+": "+err.Error(), true)
		return nil
	}

	c.wizard = nil
	c.modal = modalNone
	c.agentDefs = nil
	c.openAgentPicker()
	for i, row := range c.agentRows {
		if row.name == def.Name {
			c.agentCursor = i
		}
	}
	c.note("created custom agent "+def.Name+" at "+path, false)
	c.note("space selects it · enter applies · /agents list shows every agent", false)
	c.layout()
	c.refresh()
	return nil
}
