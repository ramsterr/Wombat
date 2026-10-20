package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var contextStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true,
	"that": true, "into": true, "your": true, "you": true, "use": true, "add": true,
	"fix": true, "make": true, "when": true, "where": true, "what": true, "should": true,
	"must": true, "can": true, "will": true, "code": true, "file": true, "files": true,
}

var contextToken = regexp.MustCompile(`[A-Za-z0-9_./-]{3,}`)

func buildContextPack(repo string, files []string, query string) string {
	if strings.TrimSpace(repo) == "" {
		return ""
	}
	paths, err := gitFileList(repo)
	if err != nil || len(paths) == 0 {
		return ""
	}
	wanted := make(map[string]bool)
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			wanted[f] = true
		}
	}
	tokens := contextWords(query)
	scored := make([]string, 0, len(paths))
	for _, path := range paths {
		if wanted[path] {
			scored = append(scored, path)
			continue
		}
		lower := strings.ToLower(path)
		for _, tok := range tokens {
			if strings.Contains(lower, tok) {
				scored = append(scored, path)
				break
			}
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return wanted[scored[i]] && !wanted[scored[j]]
	})
	if len(scored) > 40 {
		scored = scored[:40]
	}
	var b strings.Builder
	b.WriteString("Relevant repository files (excerpts):\n")
	total := 0
	for _, path := range scored {
		if total > 48*1024 {
			break
		}
		data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
		if err != nil || len(data) == 0 || isBinary(data) {
			continue
		}
		if len(data) > 12*1024 {
			data = data[:12*1024]
		}
		chunk := fmt.Sprintf("\n### %s\n```\n%s\n```\n", path, strings.TrimRight(string(data), "\n"))
		if total+len(chunk) > 48*1024 {
			continue
		}
		b.WriteString(chunk)
		total += len(chunk)
	}
	if total == 0 {
		return ""
	}
	return strings.TrimSpace(b.String())
}

func gitFileList(repo string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = repo
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, item := range strings.Split(string(raw), "\x00") {
		if item != "" {
			out = append(out, filepath.ToSlash(item))
		}
	}
	return out, nil
}

func contextWords(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, word := range contextToken.FindAllString(strings.ToLower(query), -1) {
		word = strings.Trim(word, "./-")
		if len(word) < 3 || contextStopwords[word] || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}

func isBinary(data []byte) bool {
	limit := len(data)
	if limit > 1024 {
		limit = 1024
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}
