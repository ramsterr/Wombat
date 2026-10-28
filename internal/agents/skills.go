package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
)

// Skill is a reusable instruction file. Skills live in
// ~/.wombat/skills/<name>.md and .wombat/skills/<name>.md; the project copy
// wins when both exist. An agent enables skills by name.
type Skill struct {
	Name        string
	Description string
	Source      string
	Prompt      string
}

// LoadSkills discovers skills from the user home and the project. Files that
// cannot be read are returned as problems rather than failing the session.
func LoadSkills(home, cwd string) ([]Skill, []Problem) {
	byName := map[string]Skill{}
	var problems []Problem
	dirs := []string{}
	if strings.TrimSpace(home) != "" {
		dirs = append(dirs, filepath.Join(home, "skills"))
	}
	if strings.TrimSpace(cwd) != "" {
		dirs = append(dirs, filepath.Join(cwd, ".wombat", "skills"))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, Problem{Source: dir, Err: err})
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			skill, err := parseSkillFile(path)
			if err != nil {
				problems = append(problems, Problem{Name: entry.Name(), Source: path, Err: err})
				continue
			}
			byName[strings.ToLower(skill.Name)] = skill
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, skill := range byName {
		out = append(out, skill)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Source < problems[j].Source })
	return out, problems
}

func parseSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, body, err := config.ParseFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}
	name := strings.TrimSpace(meta["name"])
	if name == "" {
		name = Slug(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	name = config.NormalizeAgentName(name)
	if name == "" {
		return Skill{}, fmt.Errorf("skill needs a name")
	}
	description := strings.TrimSpace(meta["description"])
	if description == "" {
		description = firstLine(body)
	}
	prompt := strings.TrimSpace(body)
	if prompt == "" {
		return Skill{}, fmt.Errorf("skill %q has no instructions", name)
	}
	if len(prompt) > maxPromptFileBytes {
		prompt = prompt[:maxPromptFileBytes] + "\n…[truncated]"
	}
	return Skill{Name: name, Description: description, Source: path, Prompt: prompt}, nil
}

// firstLine returns the first meaningful line of a markdown body as a
// description fallback.
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>*-• ")
		if line != "" {
			if len(line) > 160 {
				line = line[:160] + "…"
			}
			return line
		}
	}
	return ""
}

// resolveSkills maps skill references to loaded skills. Missing names are
// reported so an agent with a typo cannot silently lose its instructions.
func resolveSkills(refs []string, skills []Skill) ([]Skill, []Problem) {
	if len(refs) == 0 {
		return nil, nil
	}
	index := make(map[string]Skill, len(skills))
	for _, skill := range skills {
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
