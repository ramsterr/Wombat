package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var searchCommandPattern = regexp.MustCompile(`(?i)(?:^|[;&|]\s*)(find|grep|egrep|fgrep|rg|fd|fdfind|ls|du|tree|mdfind|locate)\b([^\n;&|]*)`)

// checkSearchScope rejects search commands that scan outside the workspace.
// It is deliberately conservative: relative paths and the workspace root are
// allowed, while /, ~, $HOME, parent directories, and other absolute roots are
// rejected with a focused-path hint.
func checkSearchScope(env *Env, command string) error {
	if env == nil || strings.TrimSpace(command) == "" {
		return nil
	}
	for _, m := range searchCommandPattern.FindAllStringSubmatch(command, -1) {
		name := strings.ToLower(m[1])
		args := shellWords(m[2])
		switch name {
		case "mdfind":
			onlyIn := ""
			for i := 0; i < len(args); i++ {
				if args[i] == "-onlyin" && i+1 < len(args) {
					onlyIn = args[i+1]
				}
			}
			if onlyIn == "" {
				return fmt.Errorf("mdfind searches the whole index; use glob/grep inside the workspace")
			}
			if outsideWorkspace(env, onlyIn) {
				return fmt.Errorf("search outside the workspace is disabled: %s. Search only inside the workspace with glob/grep and a focused path", onlyIn)
			}
			continue
		case "locate":
			return fmt.Errorf("locate searches the whole index; use glob/grep inside the workspace")
		}
		for _, arg := range args {
			if !searchPathArg(arg) {
				continue
			}
			if outsideWorkspace(env, arg) {
				return fmt.Errorf("search outside the workspace is disabled: %s. Search only inside the workspace with glob/grep and a focused path", arg)
			}
		}
	}
	return nil
}

func searchPathArg(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") {
		return false
	}
	return filepath.IsAbs(arg) ||
		arg == ".." || strings.HasPrefix(arg, "../") ||
		arg == "~" || strings.HasPrefix(arg, "~/") ||
		arg == "$HOME" || strings.HasPrefix(arg, "$HOME/") ||
		arg == "${HOME}" || strings.HasPrefix(arg, "${HOME}/")
}

func outsideWorkspace(env *Env, arg string) bool {
	if env == nil || env.Dir == "" {
		return false
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return false
	}
	if arg == "~" || strings.HasPrefix(arg, "~/") ||
		arg == "$HOME" || strings.HasPrefix(arg, "$HOME/") ||
		arg == "${HOME}" || strings.HasPrefix(arg, "${HOME}/") {
		return true
	}
	if !filepath.IsAbs(arg) {
		return arg == ".." || strings.HasPrefix(arg, "../")
	}
	dir, err := filepath.Abs(env.Dir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(arg)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
