package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	shellRedirect = regexp.MustCompile(`(?:^|[^0-9&<>])>>?\s*(?:"([^"]+)"|'([^']+)'|([^\s;&|()<>]+))`)
	shellTee      = regexp.MustCompile(`(?:^|[;&|]\s*)tee\b([^;&|\n]*)`)
	shellCD       = regexp.MustCompile(`(?:^|[;&|]\s*)cd\s+(?:"([^"]+)"|'([^']+)'|([^\s;&|]+))`)
	shellCopy     = regexp.MustCompile(`(?:^|[;&|]\s*)(?:cp|mv|install|ln|rsync)\b([^;&|\n]*)`)
	shellMove     = regexp.MustCompile(`(?:^|[;&|]\s*)mv\b([^;&|\n]*)`)
	shellWrite    = regexp.MustCompile(`(?:^|[;&|]\s*)(?:touch|mkdir|rm|rmdir|truncate|chmod|chown)\b([^;&|\n]*)`)
	shellSed      = regexp.MustCompile(`(?:^|[;&|]\s*)(?:sed|perl)\b([^;&|\n]*)`)
	shellDD       = regexp.MustCompile(`\bdd\b[^\n]*\bof=("[^"]+"|'[^']+'|[^\s;&|]+)`)
)

// checkShellWriteIsolation rejects shell commands that write into a blocked
// root, such as the lead workspace from inside a writer worktree. It is
// deliberately best-effort: the file tools are checked exactly, and bash is
// guarded for the common ways a command names a destination.
func (e *Env) checkShellWriteIsolation(command string) error {
	if e == nil || len(e.BlockedRoots) == 0 {
		return nil
	}
	for _, target := range shellWriteTargets(command) {
		if pathWithinAnyRoot(e, target) {
			return fmt.Errorf("write outside the worktree is disabled: %s", target)
		}
	}
	if e.AllowOutside {
		return nil
	}
	// A relative write after cd'ing into a blocked root is still an escape.
	for _, dir := range shellCDTargets(command) {
		if pathWithinAnyRoot(e, dir) {
			return fmt.Errorf("changing into the blocked workspace is disabled: %s", dir)
		}
	}
	// Interpreter one-liners that open a blocked path for writing are not
	// parsed as shell programs; catch the absolute path itself.
	if hasInterpreter(command) && MayMutate(command) {
		for _, root := range e.BlockedRoots {
			if root != "" && strings.Contains(command, root) {
				return fmt.Errorf("write outside the worktree is disabled: %s", root)
			}
		}
	}
	return nil
}

func shellWriteTargets(command string) []string {
	var targets []string
	for _, m := range shellRedirect.FindAllStringSubmatch(command, -1) {
		targets = append(targets, firstNonEmpty(m[1:]...))
	}
	for _, re := range []*regexp.Regexp{shellTee, shellCopy, shellWrite, shellSed} {
		for _, m := range re.FindAllStringSubmatch(command, -1) {
			args := shellWords(m[1])
			switch re {
			case shellTee, shellCopy:
				if len(args) > 0 {
					targets = append(targets, args[len(args)-1])
				}
			case shellSed:
				if hasFlag(args, "-i") || hasFlag(args, "--in-place") {
					if len(args) > 0 {
						targets = append(targets, args[len(args)-1])
					}
				}
			default:
				for i, arg := range args {
					if strings.HasPrefix(arg, "-") {
						continue
					}
					if re == shellWrite && i == 0 && regexp.MustCompile(`^[0-7]{3,4}$`).MatchString(arg) {
						continue
					}
					targets = append(targets, arg)
				}
			}
		}
	}
	for _, m := range shellMove.FindAllStringSubmatch(command, -1) {
		for _, arg := range shellWords(m[1]) {
			if !strings.HasPrefix(arg, "-") {
				targets = append(targets, arg)
			}
		}
	}
	for _, m := range shellDD.FindAllStringSubmatch(command, -1) {
		targets = append(targets, trimQuotes(m[1]))
	}
	return targets
}

func shellCDTargets(command string) []string {
	var targets []string
	for _, m := range shellCD.FindAllStringSubmatch(command, -1) {
		targets = append(targets, firstNonEmpty(m[1:]...))
	}
	return targets
}

func pathWithinAnyRoot(e *Env, target string) bool {
	if target == "" || strings.HasPrefix(target, "-") {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(e.Dir, target)
	}
	target = canonicalPath(target)
	for _, root := range e.BlockedRoots {
		if root == "" {
			continue
		}
		root = canonicalPath(root)
		rel, err := filepath.Rel(root, target)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	dir, base := filepath.Split(abs)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(resolved, base)
	}
	return abs
}

func shellWords(s string) []string {
	var out []string
	var b strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else {
				b.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case ' ', '\t', '\n', '\r':
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		case '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func firstNonEmpty(items ...string) string {
	for _, item := range items {
		if item != "" {
			return item
		}
	}
	return ""
}

func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func hasInterpreter(command string) bool {
	for _, name := range []string{"python", "python3", "node", "deno", "ruby", "php", "lua"} {
		if hasCommand(command, name) {
			return true
		}
	}
	return false
}
