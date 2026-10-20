package tools

import "testing"

func TestMayMutate(t *testing.T) {
	for _, cmd := range []string{"rm -rf build", "echo hi > out.txt", "git checkout main", "go mod tidy", "sed -i s/a/b/ f"} {
		if !MayMutate(cmd) {
			t.Errorf("MayMutate(%q) = false", cmd)
		}
	}
	for _, cmd := range []string{"ls -la", "go test ./...", "git status --short", "cat file"} {
		if MayMutate(cmd) {
			t.Errorf("MayMutate(%q) = true", cmd)
		}
	}
}

func TestIsGitMutatingCommand(t *testing.T) {
	for _, cmd := range []string{"git commit -m x", "git checkout main", "echo x && git reset --hard"} {
		if !IsGitMutatingCommand(cmd) {
			t.Errorf("IsGitMutatingCommand(%q) = false", cmd)
		}
	}
	for _, cmd := range []string{"git status", "git diff", "go test ./..."} {
		if IsGitMutatingCommand(cmd) {
			t.Errorf("IsGitMutatingCommand(%q) = true", cmd)
		}
	}
}
