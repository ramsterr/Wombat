// Package skills discovers, installs, and serves reusable instruction files.
//
// A skill is a markdown file with optional frontmatter:
//
//	---
//	name: hyperframes
//	description: video, animation, and motion graphics
//	---
//	instructions...
//
// Skills are discovered from the project, Wombat's store, and the common
// agent skill directories. The full text is never placed in the system
// prompt: the prompt carries a compact route index and the model calls the
// use_skill tool to fetch the one skill it needs.
package skills

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ramsterr/Wombat/internal/config"
)

// MaxBytes caps a skill body so one file cannot swallow the context window.
const MaxBytes = 64 * 1024

// maxInstallBytes caps a remote skill download.
const maxInstallBytes = 2 << 20

// maxSkillDirBytes caps a skill directory that carries templates or scripts.
const maxSkillDirBytes = 32 << 20

// Skill is one discovered instruction file.
type Skill struct {
	Name        string
	Description string
	// Source is the file the skill was read from.
	Source string
	// Dir is the skill's directory. It is substituted for <SKILL_DIR> so a
	// skill can point at its own scripts and references.
	Dir    string
	Prompt string
}

// Problem is a skill that could not be loaded.
type Problem struct {
	Name   string
	Source string
	Err    error
}

func (p Problem) String() string {
	label := strings.TrimSpace(p.Name)
	if label == "" {
		label = p.Source
	}
	if p.Name != "" && p.Source != "" {
		label += " (" + p.Source + ")"
	}
	return label + ": " + p.Err.Error()
}

// StoreDir is where installed skills live.
func StoreDir(home string) string {
	if strings.TrimSpace(home) == "" {
		home = config.Home()
	}
	return filepath.Join(home, "skills")
}

// Dirs lists skill directories from highest to lowest precedence. The project
// wins over the user, and Wombat's store wins over shared agent directories.
func Dirs(cwd, home string) []string {
	if strings.TrimSpace(home) == "" {
		home = config.Home()
	}
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "." || dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	if cwd != "" {
		add(filepath.Join(cwd, ".wombat", "skills"))
		add(filepath.Join(cwd, ".agents", "skills"))
		add(filepath.Join(cwd, ".claude", "skills"))
	}
	add(StoreDir(home))
	// The shared directories only belong to this user's real home. Tests and
	// isolated homes must stay deterministic.
	if userHome, err := os.UserHomeDir(); err == nil && userHome != "" &&
		filepath.Clean(home) == filepath.Join(userHome, ".wombat") {
		add(filepath.Join(userHome, ".agents", "skills"))
		add(filepath.Join(userHome, ".claude", "skills"))
		add(filepath.Join(userHome, ".config", "opencode", "skills"))
	}
	return dirs
}

// Load discovers skills from the project, the Wombat store, and the shared
// skill directories. The first definition of a name wins.
func Load(cwd, home string) ([]Skill, []Problem) {
	byName := map[string]Skill{}
	var problems []Problem
	for _, dir := range Dirs(cwd, home) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, Problem{Source: dir, Err: err})
			}
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join(dir, name)
			switch {
			case entry.IsDir():
				// <dir>/<skill>/SKILL.md
				if strings.HasPrefix(name, ".") {
					continue
				}
				file := filepath.Join(path, "SKILL.md")
				if _, err := os.Stat(file); err != nil {
					continue
				}
				path = file
			case strings.EqualFold(filepath.Ext(name), ".md"):
				// Skip repository notes that are not skills.
				switch strings.ToLower(name) {
				case "readme.md", "agents.md", "claude.md", "skill.md":
					continue
				}
			default:
				continue
			}
			skill, err := parseFile(path)
			if err != nil {
				problems = append(problems, Problem{Name: filepath.Base(path), Source: path, Err: err})
				continue
			}
			key := strings.ToLower(skill.Name)
			if _, exists := byName[key]; exists {
				continue
			}
			byName[key] = skill
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, skill := range byName {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Source < problems[j].Source })
	return out, problems
}

// Find resolves one skill by name from the current project and home.
func Find(name, cwd, home string) (Skill, error) {
	key := Normalize(name)
	if key == "" {
		return Skill{}, errors.New("skill name is empty")
	}
	list, _ := Load(cwd, home)
	for _, skill := range list {
		if strings.EqualFold(skill.Name, key) {
			return skill, nil
		}
	}
	var near []string
	for _, skill := range list {
		if strings.Contains(strings.ToLower(skill.Name), key) || strings.Contains(key, strings.ToLower(skill.Name)) {
			near = append(near, skill.Name)
		}
	}
	if len(near) > 0 {
		return Skill{}, fmt.Errorf("unknown skill %q; did you mean %s?", name, strings.Join(near, ", "))
	}
	available := make([]string, 0, len(list))
	for _, skill := range list {
		available = append(available, skill.Name)
	}
	hint := "none installed"
	if len(available) > 0 {
		hint = strings.Join(available, ", ")
	}
	return Skill{}, fmt.Errorf("unknown skill %q (available: %s)", name, hint)
}

// PromptFor returns the text the model should read, with <SKILL_DIR> resolved.
func (s Skill) PromptFor() string {
	prompt := strings.TrimSpace(s.Prompt)
	if s.Dir != "" {
		prompt = strings.ReplaceAll(prompt, "<SKILL_DIR>", s.Dir)
	}
	return prompt
}

// Normalize turns a display name into a stable skill id.
func Normalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return strings.Trim(name, "-")
}

// slug is Normalize plus a filename-safe fallback.
func slug(value string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	if len(out) > config.MaxAgentNameLen {
		out = strings.Trim(out[:config.MaxAgentNameLen], "-_")
	}
	return out
}

func parseFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, body, err := parseFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}
	name := Normalize(meta["name"])
	if name == "" {
		base := filepath.Base(filepath.Dir(path))
		if !strings.EqualFold(filepath.Base(path), "SKILL.md") {
			base = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		name = slug(base)
	}
	if name == "" {
		return Skill{}, errors.New("skill needs a name")
	}
	description := strings.TrimSpace(meta["description"])
	if description == "" {
		description = firstLine(body)
	}
	prompt := strings.TrimSpace(body)
	if prompt == "" {
		return Skill{}, fmt.Errorf("skill %q has no instructions", name)
	}
	if len(prompt) > MaxBytes {
		prompt = prompt[:MaxBytes] + "\n…[truncated]"
	}
	return Skill{
		Name:        name,
		Description: description,
		Source:      path,
		Dir:         filepath.Dir(path),
		Prompt:      prompt,
	}, nil
}

// parseFrontmatter reads the small YAML subset skill files use: flat keys,
// folded (>) and literal (|) multi-line values, and quoted strings.
func parseFrontmatter(data []byte) (map[string]string, string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	meta := map[string]string{}
	trimmed := strings.TrimLeft(text, " \t\n")
	if !strings.HasPrefix(trimmed, "---") {
		return meta, text, nil
	}
	lines := strings.Split(trimmed, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, "", errors.New("frontmatter is not closed with ---")
	}
	var key string
	var block []string
	flush := func() {
		if key == "" {
			return
		}
		meta[key] = strings.TrimSpace(strings.Join(block, " "))
		key, block = "", nil
	}
	for _, line := range lines[1:end] {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if key != "" {
				block = append(block, strings.TrimSpace(line))
			}
			continue
		}
		flush()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		k = strings.ReplaceAll(k, "-", "_")
		if k == "" {
			continue
		}
		v = strings.TrimSpace(v)
		if v == ">" || v == "|" || v == ">-" || v == "|-" || strings.HasPrefix(v, ">") || strings.HasPrefix(v, "|") {
			key = k
			continue
		}
		meta[k] = strings.Trim(v, `"'`)
	}
	flush()
	return meta, strings.Join(lines[end+1:], "\n"), nil
}

func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>*-• ")
		if line != "" {
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			return line
		}
	}
	return ""
}

// Install copies a skill into the Wombat store. The source may be a path to
// a markdown file, a skill directory, a remote URL, or the name of a skill
// that is already installed somewhere discoverable. It returns every skill
// written.
func Install(source, home string) ([]Skill, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, errors.New("usage: /skill install <path|url|name>")
	}
	store := StoreDir(home)
	if err := os.MkdirAll(store, 0o755); err != nil {
		return nil, err
	}
	if u, err := url.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return installURL(source, store)
	}
	if info, err := os.Stat(source); err == nil {
		if info.IsDir() {
			return installDir(source, store)
		}
		if strings.EqualFold(filepath.Base(source), "SKILL.md") {
			return installDir(filepath.Dir(source), store)
		}
		return installFile(source, store)
	}
	// Not a path: treat it as the name of a discovered skill.
	skill, err := Find(source, "", home)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Base(skill.Source), "SKILL.md") {
		return installDir(filepath.Dir(skill.Source), store)
	}
	return installFile(skill.Source, store)
}

func installDir(dir, store string) ([]Skill, error) {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		return installSkillDir(dir, store)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") {
				continue
			}
			nested := filepath.Join(dir, name, "SKILL.md")
			if _, err := os.Stat(nested); err == nil {
				installed, err := installSkillDir(filepath.Dir(nested), store)
				if err != nil {
					return out, err
				}
				out = append(out, installed...)
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".md") && !strings.EqualFold(name, "SKILL.md") {
			installed, err := installFile(filepath.Join(dir, name), store)
			if err != nil {
				return out, err
			}
			out = append(out, installed...)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no skill markdown found in %s", dir)
	}
	return out, nil
}

// installSkillDir preserves the whole skill directory, including templates,
// references, and scripts. A skill that points at <SKILL_DIR>/templates would
// break if only SKILL.md were copied.
func installSkillDir(src, store string) ([]Skill, error) {
	skill, err := parseFile(filepath.Join(src, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	target := filepath.Join(store, skill.Name)
	if filepath.Clean(src) != filepath.Clean(target) {
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
		if err := copySkillTree(src, target, maxSkillDirBytes); err != nil {
			return nil, err
		}
	}
	installed, err := parseFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	return []Skill{installed}, nil
}

func copySkillTree(src, dst string, limit int64) error {
	var total int64
	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > limit {
			return fmt.Errorf("skill directory is larger than %d bytes", limit)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		return os.WriteFile(filepath.Join(dst, rel), data, mode)
	})
	if err != nil {
		_ = os.RemoveAll(dst)
	}
	return err
}

func installFile(path, store string) ([]Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	meta, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	name := Normalize(meta["name"])
	if name == "" {
		base := filepath.Base(filepath.Dir(path))
		if !strings.EqualFold(filepath.Base(path), "SKILL.md") {
			base = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		name = slug(base)
	}
	if name == "" {
		return nil, fmt.Errorf("%s: skill needs a name", path)
	}
	target := filepath.Join(store, name+".md")
	// If the source is already the store target, do not rewrite it.
	if filepath.Clean(path) != filepath.Clean(target) {
		content := data
		if _, _, err := parseFrontmatter(content); err == nil && !strings.HasPrefix(strings.TrimLeft(string(content), " \t\n"), "---") {
			desc := firstLine(body)
			header := fmt.Sprintf("---\nname: %s\n", name)
			if desc != "" {
				header += "description: " + desc + "\n"
			}
			header += "---\n\n"
			content = append([]byte(header), content...)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return nil, err
		}
	}
	skill, err := parseFile(target)
	if err != nil {
		return nil, err
	}
	return []Skill{skill}, nil
}

func installURL(raw, store string) ([]Skill, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d", raw, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInstallBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInstallBytes {
		return nil, fmt.Errorf("%s: skill is larger than %d bytes", raw, maxInstallBytes)
	}
	meta, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, err
	}
	name := Normalize(meta["name"])
	if name == "" {
		base := filepath.Base(strings.TrimSuffix(raw, "/"))
		name = slug(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if name == "" {
		return nil, fmt.Errorf("%s: could not infer a skill name", raw)
	}
	if !strings.HasPrefix(strings.TrimLeft(string(data), " \t\n"), "---") {
		header := fmt.Sprintf("---\nname: %s\n", name)
		if desc := firstLine(body); desc != "" {
			header += "description: " + desc + "\n"
		}
		header += "---\n\n"
		data = append([]byte(header), data...)
	}
	target := filepath.Join(store, name+".md")
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return nil, err
	}
	skill, err := parseFile(target)
	if err != nil {
		return nil, err
	}
	return []Skill{skill}, nil
}

// Remove deletes a skill from the Wombat store. A skill that only exists in
// a shared directory is left alone.
func Remove(name, home string) (string, error) {
	key := Normalize(name)
	if key == "" {
		return "", errors.New("skill name is empty")
	}
	store := StoreDir(home)
	target := filepath.Join(store, key+".md")
	if err := os.Remove(target); err == nil {
		return target, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	target = filepath.Join(store, key)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if err := os.RemoveAll(target); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("skill %q is not installed in %s", name, store)
}

// RouterPrompt is the compact skill section appended to the system prompt.
// It lists task routes and installed names, never full skill bodies.
func RouterPrompt(cwd, home string) string {
	list, _ := Load(cwd, home)
	installed := make([]string, 0, len(list))
	present := make(map[string]bool, len(list))
	for _, skill := range list {
		installed = append(installed, skill.Name)
		present[strings.ToLower(skill.Name)] = true
	}
	var b strings.Builder
	b.WriteString("## Skills\n")
	b.WriteString("A skill is a procedure you must load, not recall. When the task matches a route, call use_skill(name) before your first substantive step; then follow that skill's routing to any sub-skill.\n")
	b.WriteString("Routes:\n")
	for _, route := range routes {
		b.WriteString("- ")
		b.WriteString(route.task)
		b.WriteString(" → ")
		var names []string
		for _, name := range route.skills {
			if present[name] {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			names = route.skills
		}
		b.WriteString(strings.Join(names, " then "))
		if len(names) > 0 && !present[names[0]] {
			b.WriteString(" (not installed — say so and suggest /skill install " + names[0] + ")")
		}
		b.WriteString("\n")
	}
	if len(installed) > 0 {
		b.WriteString("Installed: ")
		b.WriteString(strings.Join(installed, ", "))
		b.WriteString("\n")
	}
	b.WriteString("For anything else, call use_skill with no name to list installed skills. Load only the skill you need; never paste a whole skill into the reply. If a skill has its own references or scripts, call use_skill again for the named sub-skill.")
	return strings.TrimSpace(b.String())
}

type route struct {
	task   string
	skills []string
}

// routes is the built-in task-to-skill map. It is intentionally small and
// points at skills the user can install; no skill body lives here.
var routes = []route{
	{"video, animation, motion graphics, promo, reel, captions, slideshow, video edit", []string{"hyperframes"}},
	{"music, SFX, voiceover, image, icon, logo, color grade, LUT, media treatment", []string{"media-use"}},
	{"faceless explainer video, topic explainer, narrated breakdown", []string{"faceless-explainer"}},
	{"video animation details, keyframes, scene blueprints, runtime adapters", []string{"hyperframes-animation"}},
	{"job search, job fit, hiring, recruiter, resume matching", []string{"job-search"}},
	{"live browser, Playwright, authenticated handoff, visual verification", []string{"webcmd-browser"}},
	{"opencode config, agents, skills, plugins, MCP in opencode", []string{"customize-opencode"}},
}
