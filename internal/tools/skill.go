package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/skills"
)

// UseSkill loads one installed skill into the conversation. The system prompt
// carries only a compact route index; the full instructions arrive here, when
// the task actually needs them.
type UseSkill struct{}

func (t *UseSkill) Name() string  { return "use_skill" }
func (t *UseSkill) Class() string { return "read" }
func (t *UseSkill) Description() string {
	return "Load an installed skill and follow its instructions. Call this before any task that matches a skill route (video, media, job search, browser QA, opencode config, or a named skill). " +
		"With no name it lists installed skills and their descriptions. Call it again for each referenced sub-skill instead of guessing its contents; a skill's <SKILL_DIR> is resolved so its scripts and references work."
}

func (t *UseSkill) Schema() map[string]any {
	return obj(map[string]any{
		"name": desc("skill id to load, e.g. hyperframes or media-use; omit to list installed skills"),
	})
}

func (t *UseSkill) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	cwd := ""
	if env != nil {
		cwd = env.Dir
	}
	name := strings.TrimSpace(args.Name)
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return listSkills(cwd)
	}
	skill, err := skills.Find(name, cwd, config.Home())
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "skill: %s\n", skill.Name)
	if skill.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", oneLine(skill.Description, 500))
	}
	fmt.Fprintf(&b, "source: %s\n", skill.Source)
	if skill.Dir != "" {
		fmt.Fprintf(&b, "skill_dir: %s\n", skill.Dir)
	}
	b.WriteString("\n---\n\n")
	b.WriteString(skill.PromptFor())
	b.WriteString("\n\n---\nFollow this skill. If it names another skill, call use_skill for that skill before continuing. Do not summarize the skill instead of doing the work.")
	return b.String(), nil
}

func listSkills(cwd string) (string, error) {
	list, problems := skills.Load(cwd, config.Home())
	var b strings.Builder
	if len(list) == 0 {
		b.WriteString("no skills installed. Install one with /skill install <path|url|name>, or add ~/.wombat/skills/<name>.md.")
	} else {
		fmt.Fprintf(&b, "%d skills installed\n", len(list))
		for _, skill := range list {
			line := "  " + skill.Name
			if skill.Description != "" {
				line += " — " + oneLine(skill.Description, 180)
			}
			b.WriteString(line + "\n")
		}
	}
	for _, problem := range problems {
		fmt.Fprintf(&b, "  ! %s\n", problem.String())
	}
	return strings.TrimSpace(b.String()), nil
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
