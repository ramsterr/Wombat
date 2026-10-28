package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/commands"
	"github.com/ramsterr/Wombat/internal/config"
)

// agentRow is one selectable row in the picker: either an installed custom
// agent or the create action.
type agentRow struct {
	name        string
	description string
	tags        string
	create      bool
}

// refreshAgentDefs reloads the definitions from config, home, and project.
func (c *chat) refreshAgentDefs() {
	c.agentDefs = agents.Load(c.cfg, config.Home(), c.agentCwd())
}

// agentTags summarises a definition for the picker.
func agentTags(def *config.AgentDef) string {
	if def == nil {
		return ""
	}
	var tags []string
	if def.Model != "" {
		tags = append(tags, def.Model)
	}
	if def.Provider != "" {
		tags = append(tags, "provider "+def.Provider)
	}
	if def.ReadOnly {
		tags = append(tags, "read-only")
	}
	if len(def.Skills) > 0 {
		tags = append(tags, "skills "+strings.Join(def.Skills, ", "))
	}
	if len(def.MCPServers) > 0 {
		tags = append(tags, "mcp "+strings.Join(def.MCPServers, ", "))
	}
	if len(def.Tools) > 0 {
		tags = append(tags, fmt.Sprintf("%d tool filters", len(def.Tools)))
	}
	return strings.Join(tags, " · ")
}

// normalizeAgentSelection canonicalises and deduplicates names.
func normalizeAgentSelection(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, entry := range names {
		for _, part := range strings.Split(entry, ",") {
			name := config.NormalizeAgentName(part)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// splitAgentArgs splits "/agents a, b" into names.
func splitAgentArgs(arg string) []string {
	fields := strings.Fields(strings.NewReplacer(",", " ", ";", " ").Replace(arg))
	return normalizeAgentSelection(fields)
}

// agentSelLabel is the short selection summary used by /status.
func (c *chat) agentSelLabel() string {
	if len(c.agentSel) == 0 {
		return "none (default agent only)"
	}
	return strings.Join(c.agentSel, ", ")
}

// agentSelectionNote describes the current selection.
func (c *chat) agentSelectionNote() string {
	if len(c.agentSel) == 0 {
		return "custom agents: none (the default agent plans and works alone)"
	}
	return "custom agents: " + strings.Join(c.agentSel, ", ") + " — the planner will coordinate them with the default agent"
}

// persistAgents records the selection on the session so resume restores it.
func (c *chat) persistAgents() {
	if c.sess == nil {
		return
	}
	if len(c.agentSel) == 0 {
		c.sess.Meta.Agents = nil
	} else {
		c.sess.Meta.Agents = append([]string(nil), c.agentSel...)
	}
	_ = c.sess.Flush()
}

// setAgents applies a selection, rebuilds the session agent, and reports
// anything that could not be resolved.
func (c *chat) setAgents(names []string) tea.Cmd {
	c.agentSel = normalizeAgentSelection(names)
	c.persistAgents()
	if c.sess != nil {
		if err := c.rebuild(false); err != nil {
			c.note("could not load custom agents: "+err.Error(), true)
			return nil
		}
		for _, problem := range c.agentProblems {
			c.note("agent: "+problem.String(), true)
		}
	} else {
		c.refreshAgentDefs()
	}
	if c.cfg != nil && !c.cfg.Orchestrator {
		c.note("note: the orchestrator is off, so custom agents cannot run — enable it in config (orchestrator: true)", true)
	}
	c.note(c.agentSelectionNote(), false)
	return nil
}

// agentPaletteOptions powers inline completion for "/agents <name>".
func (c *chat) agentPaletteOptions(filter string) []commands.Option {
	if len(c.agentDefs) == 0 {
		c.refreshAgentDefs()
	}
	opts := []commands.Option{
		{Value: "list", Summary: "show every custom agent and where it lives"},
		{Value: "clear", Summary: "remove every custom agent from this session"},
	}
	for _, loaded := range c.agentDefs {
		if loaded.Def == nil || loaded.Err != nil {
			continue
		}
		summary := loaded.Def.Description
		if tags := agentTags(loaded.Def); tags != "" {
			if summary == "" {
				summary = tags
			} else {
				summary += " · " + tags
			}
		}
		opts = append(opts, commands.Option{Value: loaded.Def.Name, Summary: summary})
	}
	if filter == "" {
		return opts
	}
	lower := strings.ToLower(filter)
	var out []commands.Option
	for _, opt := range opts {
		if strings.HasPrefix(strings.ToLower(opt.Value), lower) {
			out = append(out, opt)
		}
	}
	return out
}

// openAgentPicker opens the multi-select custom agent picker. The first row
// creates a new agent; the rest toggle the installed definitions.
func (c *chat) openAgentPicker() tea.Cmd {
	c.refreshAgentDefs()
	rows := []agentRow{{
		create:      true,
		name:        "＋  Create a new agent…",
		description: "system prompt, tools, MCP servers, permissions, skills",
	}}
	for _, loaded := range c.agentDefs {
		if loaded.Def == nil || loaded.Err != nil {
			continue
		}
		rows = append(rows, agentRow{
			name:        loaded.Def.Name,
			description: loaded.Def.Description,
			tags:        agentTags(loaded.Def),
		})
	}
	c.agentRows = rows
	c.agentDraft = map[string]bool{}
	for _, name := range c.agentSel {
		c.agentDraft[name] = true
	}
	c.agentCursor = 0
	c.modal = modalAgents
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

// applyAgentDraft commits the picker selection.
func (c *chat) applyAgentDraft() tea.Cmd {
	var names []string
	for _, row := range c.agentRows {
		if row.create || row.name == "" {
			continue
		}
		if c.agentDraft[row.name] {
			names = append(names, row.name)
		}
	}
	c.modal = modalNone
	c.layout()
	return c.setAgents(names)
}

// agentsKey drives the picker.
func (c *chat) agentsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		c.modal = modalNone
		c.layout()
		c.refresh()
		return nil
	case "up", "k", "ctrl+p":
		if c.agentCursor > 0 {
			c.agentCursor--
		}
	case "down", "j", "ctrl+n":
		if c.agentCursor < len(c.agentRows)-1 {
			c.agentCursor++
		}
	case "home", "g":
		c.agentCursor = 0
	case "end", "G":
		c.agentCursor = len(c.agentRows) - 1
	case "c", "C":
		return c.openAgentWizard()
	case " ":
		if c.agentCursor >= 0 && c.agentCursor < len(c.agentRows) {
			row := c.agentRows[c.agentCursor]
			if !row.create && row.name != "" {
				c.agentDraft[row.name] = !c.agentDraft[row.name]
			}
		}
	case "a", "A":
		all := true
		for _, row := range c.agentRows {
			if !c.agentDraft[row.name] {
				all = false
				break
			}
		}
		for _, row := range c.agentRows {
			c.agentDraft[row.name] = !all
		}
	case "x", "X":
		c.agentDraft = map[string]bool{}
	case "enter":
		if c.agentCursor >= 0 && c.agentCursor < len(c.agentRows) && c.agentRows[c.agentCursor].create {
			return c.openAgentWizard()
		}
		return c.applyAgentDraft()
	}
	c.layout()
	c.refresh()
	return nil
}

// agentsCard renders the picker overlay.
func (c *chat) agentsCard() string {
	cardW := min(max(50, c.mainWidth()-6), 92)
	innerW := max(34, cardW-6)
	lines := []string{
		styleAccent.Bold(true).Render("◈  CUSTOM AGENTS") + "  " + styleFaint.Render("default agent always joins"),
		styleDim.Render("selected specialists work in parallel under the planner"),
		"",
	}
	if len(c.agentRows) <= 1 {
		lines = append(lines, styleDim.Render("no agents installed yet — create the first one"))
		lines = append(lines, "")
	}

	maxRows := max(4, c.vp.Height-14)
	start := 0
	if c.agentCursor >= maxRows {
		start = c.agentCursor - maxRows + 1
	}
	end := min(len(c.agentRows), start+maxRows)
	for i := start; i < end; i++ {
		row := c.agentRows[i]
		if row.create {
			text := clip(row.name, innerW)
			if i == c.agentCursor {
				lines = append(lines, styleSelected.Render(" "+pad(text, innerW-1)))
				if row.description != "" && c.vp.Height >= 18 {
					lines = append(lines, styleSelected.Render(" "+pad(clip("      "+row.description, innerW), innerW-1)))
				}
			} else {
				lines = append(lines, " "+styleAccent.Render(text))
				if row.description != "" && c.vp.Height >= 18 {
					lines = append(lines, " "+styleFaint.Render(clip("      "+row.description, innerW)))
				}
			}
			continue
		}
		box := "[ ]"
		if c.agentDraft[row.name] {
			box = "[x]"
		}
		text := box + " " + row.name
		if row.description != "" {
			text += " — " + row.description
		}
		text = clip(text, innerW)
		if i == c.agentCursor {
			lines = append(lines, styleSelected.Render(" "+pad(text, innerW-1)))
		} else {
			lines = append(lines, " "+styleDim.Render(text))
		}
		if row.tags != "" && c.vp.Height >= 18 {
			tagLine := clip("      "+row.tags, innerW)
			if i == c.agentCursor {
				lines = append(lines, styleSelected.Render(" "+pad(tagLine, innerW-1)))
			} else {
				lines = append(lines, " "+styleFaint.Render(tagLine))
			}
		}
	}
	if start > 0 || end < len(c.agentRows) {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("  %d–%d of %d", start+1, end, len(c.agentRows))))
	}

	var picked []string
	for _, row := range c.agentRows {
		if c.agentDraft[row.name] {
			picked = append(picked, row.name)
		}
	}
	lines = append(lines, "")
	if len(picked) == 0 {
		lines = append(lines, " "+styleDim.Render("team: default only"))
	} else {
		lines = append(lines, " "+styleAccent.Render("team: ")+styleDim.Render(clip(strings.Join(picked, ", "), innerW-8)))
	}
	var problems []string
	for _, loaded := range c.agentDefs {
		if loaded.Err != nil {
			problems = append(problems, loaded.Name()+": "+loaded.Err.Error())
		}
	}
	for i, problem := range problems {
		if i >= 2 {
			lines = append(lines, " "+styleWarn.Render("…more problems in /agents list"))
			break
		}
		lines = append(lines, " "+styleWarn.Render(clip("! "+problem, innerW)))
	}
	lines = append(lines, " "+styleFaint.Render("space toggle · enter apply · c create · esc cancel"))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

// agentsBadge is the compact team indicator in the header.
func (c *chat) agentsBadge() string {
	if len(c.agentSel) == 0 {
		return ""
	}
	label := "team " + strings.Join(c.agentSel, "+")
	return clip(label, 32)
}

// agentsList renders the textual /agents list.
func (c *chat) agentsList() string {
	c.refreshAgentDefs()
	var b strings.Builder
	b.WriteString("custom agents\n")
	usable := agents.Usable(c.agentDefs)
	if len(usable) == 0 {
		b.WriteString("  (none)\n")
		b.WriteString("  create one inside /agents (choose Create a new agent), or run wombat agent new <name>\n")
		b.WriteString("  or add a file: .wombat/agents/<name>.md (project) or ~/.wombat/agents/<name>.md (user)\n")
	}
	selected := map[string]bool{}
	for _, name := range c.agentSel {
		selected[name] = true
	}
	for _, def := range usable {
		mark := "[ ]"
		if selected[def.Name] {
			mark = "[x]"
		}
		line := fmt.Sprintf("  %s %s", mark, def.Name)
		if def.Description != "" {
			line += " — " + oneLineText(def.Description, 90)
		}
		if tags := agentTags(def); tags != "" {
			line += " (" + tags + ")"
		}
		b.WriteString(line + "\n")
	}
	for _, loaded := range c.agentDefs {
		if loaded.Def != nil && loaded.Err == nil {
			continue
		}
		problem := loaded.Name() + ": " + loaded.Err.Error()
		b.WriteString("  ! " + oneLineText(problem, 140) + "\n")
	}
	for _, problem := range c.agentProblems {
		b.WriteString("  ! " + oneLineText(problem.String(), 140) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(c.agentSelectionNote() + "\n")
	if c.cfg != nil && !c.cfg.Orchestrator {
		b.WriteString("note: the orchestrator is off, so custom agents cannot run\n")
	}
	b.WriteString("select with /agents (picker, includes create) or /agents name1,name2; clear with /agents clear\n")
	b.WriteString("skills: /skill shows installed skills; enable them per agent in the builder")
	return strings.TrimSpace(b.String())
}

func oneLineText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
