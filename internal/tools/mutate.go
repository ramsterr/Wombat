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
	if networkWritesFile(cmd) {
		return true
	}
	return mutatingCommand.MatchString(cmd)
}

// networkWritesFile recognizes downloaders that write to disk by default or
// through an output flag. In a read-only worker these must be refused even
// though they are not shell redirections.
func networkWritesFile(cmd string) bool {
	if hasCommand(cmd, "curl") &&
		(regexp.MustCompile(`(^|\s)(-o|--output)(\s|=)`).MatchString(cmd)) {
		return true
	}
	if hasCommand(cmd, "wget") &&
		(regexp.MustCompile(`(^|\s)(-o|--output-document)(\s|=)`).MatchString(cmd)) {
		return true
	}
	if hasCommand(cmd, "yt-dlp") {
		if regexp.MustCompile(`(^|\s)(-o|--output)(\s|=)`).MatchString(cmd) {
			return true
		}
		for _, safe := range []string{"--flat-playlist", "--print", "--dump-json", "--simulate", "--skip-download", "-j"} {
			if strings.Contains(cmd, safe) {
				return false
			}
		}
		return true
	}
	if hasCommand(cmd, "ffmpeg") {
		for _, safe := range []string{"-version", "-buildconf", "-filters", "-formats", "-codecs", "-h", "-help"} {
			if strings.Contains(cmd, safe) {
				return false
			}
		}
		return true
	}
	// Common writes inside interpreters. This is best-effort, not a jail.
	if hasCommand(cmd, "python") || hasCommand(cmd, "python3") {
		for _, marker := range []string{"open(", "write_text(", "write_bytes(", "shutil.copy", "os.remove", "os.unlink", "os.rename"} {
			if strings.Contains(cmd, marker) &&
				(strings.Contains(cmd, "'w'") || strings.Contains(cmd, "\"w\"") || strings.Contains(cmd, "'a'") || strings.Contains(cmd, "\"a\"")) {
				return true
			}
		}
	}
	return false
}

// hasCommand reports whether name is invoked as a command (start of the
// line, or after a shell separator), not merely mentioned as an argument.
func hasCommand(cmd, name string) bool {
	re := regexp.MustCompile(`(^|[;&|]\s*)` + regexp.QuoteMeta(name) + `(\s|$)`)
	return re.MatchString(cmd)
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
