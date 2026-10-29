package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/skills"
)

// skillRow is one installed skill plus the agents that reference it.
type skillRow struct {
	name        string
	description string
	source      string
	prompt      string
	usedBy      []string
}

// openSkills shows the installed skills browser.
func (c *chat) openSkills() tea.Cmd {
	rows, problems := c.loadSkillRows()
	c.skillRows = rows
	c.skillProblems = problems
	c.skillCursor = 0
	c.skillExpanded = false
	if len(rows) == 0 {
		c.note("no skills installed — /skill install <path|url|name>", true)
		for _, problem := range problems {
			c.note("skill: "+problem.String(), true)
		}
		return nil
	}
	c.modal = modalSkills
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

func (c *chat) loadSkillRows() ([]skillRow, []agents.Problem) {
	cwd := c.agentCwd()
	skills, problems := agents.LoadSkills(config.Home(), cwd)
	defs := agents.Load(c.cfg, config.Home(), cwd)
	used := map[string][]string{}
	for _, loaded := range defs {
		if loaded.Def == nil || loaded.Err != nil {
			continue
		}
		for _, ref := range loaded.Def.Skills {
			key := config.NormalizeAgentName(ref)
			already := false
			for _, name := range used[key] {
				if name == loaded.Def.Name {
					already = true
				}
			}
			if !already {
				used[key] = append(used[key], loaded.Def.Name)
			}
		}
	}
	rows := make([]skillRow, 0, len(skills))
	for _, skill := range skills {
		rows = append(rows, skillRow{
			name:        skill.Name,
			description: skill.Description,
			source:      skill.Source,
			prompt:      skill.Prompt,
			usedBy:      used[strings.ToLower(skill.Name)],
		})
	}
	return rows, problems
}

// skillsKey navigates the skills browser.
func (c *chat) skillsKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		c.modal = modalNone
		c.layout()
		c.refresh()
		return nil
	case "up", "k", "ctrl+p":
		if c.skillCursor > 0 {
			c.skillCursor--
		}
	case "down", "j", "ctrl+n":
		if c.skillCursor < len(c.skillRows)-1 {
			c.skillCursor++
		}
	case "home", "g":
		c.skillCursor = 0
	case "end", "G":
		c.skillCursor = len(c.skillRows) - 1
	case "enter":
		c.skillExpanded = !c.skillExpanded
	}
	c.layout()
	c.refresh()
	return nil
}

// skillsCard renders the browser overlay.
func (c *chat) skillsCard() string {
	cardW := min(max(56, c.mainWidth()-6), 96)
	innerW := max(40, cardW-6)
	lines := []string{
		styleAccent.Bold(true).Render("◈  SKILLS") + "  " +
			styleFaint.Render(fmt.Sprintf("%d installed", len(c.skillRows))),
		"",
	}
	maxRows := max(4, min(10, c.vp.Height-18))
	start := 0
	if c.skillCursor >= maxRows {
		start = c.skillCursor - maxRows + 1
	}
	end := min(len(c.skillRows), start+maxRows)
	for i := start; i < end; i++ {
		row := c.skillRows[i]
		label := row.name
		if row.description != "" {
			label += " — " + oneLineText(row.description, 60)
		}
		if i == c.skillCursor {
			lines = append(lines, styleSelected.Render(" > "+pad(clip(label, innerW-3), innerW-3)))
		} else {
			lines = append(lines, "   "+styleDim.Render(clip(label, innerW-3)))
		}
	}
	if start > 0 || end < len(c.skillRows) {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("   %d–%d of %d", start+1, end, len(c.skillRows))))
	}
	if len(c.skillRows) > 0 && c.skillCursor >= 0 && c.skillCursor < len(c.skillRows) {
		row := c.skillRows[c.skillCursor]
		lines = append(lines, "", styleSection.Render(row.name))
		if row.description != "" {
			lines = append(lines, " "+styleDim.Render(clip(row.description, innerW-2)))
		}
		lines = append(lines, " "+styleFaint.Render("source: "+shortPath(row.source, innerW-10)))
		if len(row.usedBy) > 0 {
			lines = append(lines, " "+styleFaint.Render("used by: "+clip(strings.Join(row.usedBy, ", "), innerW-11)))
		} else {
			lines = append(lines, " "+styleFaint.Render("used by: no selected or installed agent"))
		}
		lines = append(lines, "")
		preview := 6
		if c.skillExpanded {
			preview = max(8, c.vp.Height-20)
		}
		for _, line := range previewLines(row.prompt, preview) {
			lines = append(lines, " "+styleDim.Render(clip(line, innerW-2)))
		}
	}
	for i, problem := range c.skillProblems {
		if i >= 2 {
			break
		}
		lines = append(lines, " "+styleWarn.Render(clip("! "+problem.String(), innerW-2)))
	}
	hint := "↑↓ browse · enter expand · esc close"
	if c.skillExpanded {
		hint = "↑↓ browse · enter collapse · esc close"
	}
	lines = append(lines, "", " "+styleFaint.Render(hint))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

// skillsList is the textual listing for "/skill list".
func (c *chat) skillsList() string {
	rows, problems := c.loadSkillRows()
	var b strings.Builder
	b.WriteString("installed skills\n")
	if len(rows) == 0 {
		b.WriteString("  (none)\n")
		b.WriteString("  install one with /skill install <path|url|name>\n")
		b.WriteString("  or add a markdown file: .wombat/skills/<name>.md or ~/.wombat/skills/<name>.md\n")
		b.WriteString("  enable it on an agent with \"skills\": [\"<name>\"] or the /agents builder\n")
	}
	for _, row := range rows {
		line := "  " + row.name
		if row.description != "" {
			line += " — " + oneLineText(row.description, 80)
		}
		b.WriteString(line + "\n")
		meta := "      " + shortPath(row.source, 70)
		if len(row.usedBy) > 0 {
			meta += " · used by " + strings.Join(row.usedBy, ", ")
		}
		b.WriteString(styleFaint.Render(meta) + "\n")
	}
	for _, problem := range problems {
		b.WriteString("  ! " + oneLineText(problem.String(), 140) + "\n")
	}
	return strings.TrimSpace(b.String())
}

// skillDetail returns the full text of one skill for "/skill <name>".
func (c *chat) skillDetail(name string) string {
	rows, _ := c.loadSkillRows()
	key := config.NormalizeAgentName(name)
	for _, row := range rows {
		if strings.EqualFold(row.name, key) {
			return skillDetailText(row)
		}
	}
	available := make([]string, 0, len(rows))
	for _, row := range rows {
		available = append(available, row.name)
	}
	hint := "none installed"
	if len(available) > 0 {
		hint = strings.Join(available, ", ")
	}
	return "unknown skill " + name + " (installed: " + hint + ")"
}

func skillDetailText(row skillRow) string {
	var b strings.Builder
	b.WriteString("skill: " + row.name + "\n")
	if row.description != "" {
		b.WriteString(row.description + "\n")
	}
	b.WriteString("source: " + row.source + "\n")
	if len(row.usedBy) > 0 {
		b.WriteString("used by: " + strings.Join(row.usedBy, ", ") + "\n")
	}
	b.WriteString("\n" + strings.TrimSpace(row.prompt))
	return b.String()
}

func previewLines(text string, n int) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return lines
}

// installSkill adds a skill from a file, directory, URL, or registry name.
func (c *chat) installSkill(source string) {
	installed, err := skills.Install(source, config.Home())
	if err != nil {
		c.note("skill install: "+err.Error(), true)
		return
	}
	for _, skill := range installed {
		c.note("installed "+skill.Name+" → "+skill.Source, false)
	}
	c.refreshAgentDefs()
}

// removeSkill deletes an installed skill by name.
func (c *chat) removeSkill(name string) {
	path, err := skills.Remove(name, config.Home())
	if err != nil {
		c.note("skill remove: "+err.Error(), true)
		return
	}
	c.note("removed "+name+" ("+path+")", false)
	c.refreshAgentDefs()
}
