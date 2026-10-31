package tools

import (
	"strings"
	"testing"
)

func TestCheckSearchScope(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	env := &Env{Dir: dir}

	blocked := []string{
		"find / -name x",
		"find / -maxdepth 1",
		"grep -R foo /",
		"rg foo /",
		"ls /",
		"du -sh /",
		"tree /",
		"find ~ -name x",
		"find $HOME -name x",
		"find " + outside + " -name x",
		"grep -R foo " + outside,
		"find .. -name x",
		"mdfind foo",
		"mdfind -onlyin / foo",
		"locate foo",
	}
	for _, command := range blocked {
		if err := checkSearchScope(env, command); err == nil {
			t.Errorf("checkSearchScope(%q) = nil, want an error", command)
		}
	}

	allowed := []string{
		"find . -name x",
		"find " + dir + " -name x",
		"grep -R foo .",
		"grep -R foo " + dir,
		"rg foo",
		"rg foo .",
		"ls -la",
		"du -sh .",
		"tree .",
		"mdfind -onlyin " + dir + " foo",
		"echo find / -name x",
	}
	for _, command := range allowed {
		if err := checkSearchScope(env, command); err != nil {
			t.Errorf("checkSearchScope(%q) = %v, want nil", command, err)
		}
	}
}

func TestSearchScopeErrorIsActionable(t *testing.T) {
	err := checkSearchScope(&Env{Dir: t.TempDir()}, "find / -name x")
	if err == nil || !strings.Contains(err.Error(), "Search only inside the workspace") {
		t.Fatalf("error = %v, want a focused-path hint", err)
	}
}
