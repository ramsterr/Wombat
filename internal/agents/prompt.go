package agents

import (
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
)

// Persona composes the system-prompt section for one custom agent: its
// role statement, its prompt, and the full text of every enabled skill.
func Persona(def *config.AgentDef, skills []Skill) string {
	if def == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Your role: %s\n", def.Name)
	if desc := oneLine(def.Description, 600); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n")
	}
	if prompt := strings.TrimSpace(def.Prompt); prompt != "" {
		b.WriteString("\n")
		b.WriteString(prompt)
		b.WriteString("\n")
	}
	if len(skills) > 0 {
		b.WriteString("\n### Skills\n")
		b.WriteString("These skills are part of your role. Follow them while you work.\n")
		for _, skill := range skills {
			if skill.Description != "" {
				fmt.Fprintf(&b, "\n#### %s — %s\n", skill.Name, oneLine(skill.Description, 300))
			} else {
				fmt.Fprintf(&b, "\n#### %s\n", skill.Name)
			}
			b.WriteString(strings.TrimSpace(skill.Prompt))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
