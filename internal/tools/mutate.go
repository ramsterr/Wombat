package tools

import (
	"regexp"
	"strings"
)

var gitMutatingCommand = regexp.MustCompile(`(^|[;&|]\s*)git\s+(apply|checkout|switch|reset|clean|merge|rebase|cherry-pick|stash|commit|add|rm|mv|init|clone|fetch|pull|push|tag|branch\s+-[dD])\b`)

var mutatingCommand = regexp.MustCompile(`(^|[;&|]\s*)(rm|mv|cp|mkdir|rmdir|touch|truncate|tee|dd|ln|chmod|chown|patch|make|install|sed\s+-i|perl\s+-i|git\s+(apply|checkout|switch|reset|clean|merge|rebase|cherry-pick|stash|commit|add|rm|mv)|go\s+mod\s+(tidy|vendor)|(npm|pnpm|yarn)\s+(install|add|remove|ci)|pip\s+install|cargo\s+(add|remove|install))\b`)

func IsGitMutatingCommand(command string) bool {
	return gitMutatingCommand.MatchString(strings.ToLower(command))
}

func MayMutate(command string) bool {
	cmd := strings.ToLower(command)
	if writesAFile(cmd) {
		return true
	}
	if strings.Contains(cmd, "sed -i") || strings.Contains(cmd, "perl -i") {
		return true
	}
	return mutatingCommand.MatchString(cmd)
}

// writesAFile reports whether the command redirects output into a real file.
// A redirect to /dev/null, or a copy of one file descriptor to another,
// changes nothing on disk and must not trigger a checkpoint.
func writesAFile(cmd string) bool {
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '>' {
			continue
		}
		if i > 0 && cmd[i-1] == '<' {
			continue
		}
		rest := strings.TrimSpace(cmd[i+1:])
		if strings.HasPrefix(rest, "&") {
			// 2>&1 and friends
			continue
		}
		if strings.HasPrefix(rest, "/dev/null") {
			continue
		}
		return true
	}
	return false
}
