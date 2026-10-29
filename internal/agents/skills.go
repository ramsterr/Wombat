package agents

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/skills"
)

// Skill is a reusable instruction file. The discovery and installation logic
// lives in the skills package so the use_skill tool, the CLI, and custom
// agents share one catalogue.
type Skill = skills.Skill

// LoadSkills discovers skills from the project, the user's Wombat store, and
// the shared agent skill directories. A project copy wins over a user copy.
// Files that cannot be read are returned as problems rather than failing the
// session.
func LoadSkills(home, cwd string) ([]Skill, []Problem) {
	loaded, skillProblems := skills.Load(cwd, home)
	problems := make([]Problem, 0, len(skillProblems))
	for _, problem := range skillProblems {
		problems = append(problems, Problem{Name: problem.Name, Source: problem.Source, Err: problem.Err})
	}
	return loaded, problems
}

// resolveSkills maps skill references to loaded skills. Missing names are
// reported so an agent with a typo cannot silently lose its instructions.
func resolveSkills(refs []string, list []Skill) ([]Skill, []Problem) {
	if len(refs) == 0 {
		return nil, nil
	}
	index := make(map[string]Skill, len(list))
	for _, skill := range list {
		index[strings.ToLower(skill.Name)] = skill
	}
	var out []Skill
	var problems []Problem
	seen := map[string]bool{}
	for _, ref := range refs {
		key := config.NormalizeAgentName(ref)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		skill, ok := index[key]
		if !ok {
			available := make([]string, 0, len(index))
			for name := range index {
				available = append(available, name)
			}
			sort.Strings(available)
			hint := "none found"
			if len(available) > 0 {
				hint = strings.Join(available, ", ")
			}
			problems = append(problems, Problem{Name: ref,
				Err: fmt.Errorf("unknown skill %q (available: %s)", ref, hint)})
			continue
		}
		out = append(out, skill)
	}
	return out, problems
}
