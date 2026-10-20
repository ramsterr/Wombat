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
	if strings.Contains(cmd, ">") {
		return true
	}
	if strings.Contains(cmd, "sed -i") || strings.Contains(cmd, "perl -i") {
		return true
	}
	return mutatingCommand.MatchString(cmd)
}
