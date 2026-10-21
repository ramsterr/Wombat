package tools

import (
	"path/filepath"
	"testing"
)

func TestShellWriteIsolation(t *testing.T) {
	main := t.TempDir()
	work := t.TempDir()
	env := &Env{Dir: work, BlockedRoots: []string{main}}

	allowed := []string{
		"echo hi > NOTES.txt",
		"go test ./...",
		"cat " + filepath.Join(main, "demo.go"),
		"echo hi > /dev/null",
		"cp " + filepath.Join(main, "demo.go") + " .",
		"printf '%s\\n' ok | tee notes.txt",
	}
	for _, command := range allowed {
		if err := env.checkShellWriteIsolation(command); err != nil {
			t.Errorf("allowed command rejected: %q: %v", command, err)
		}
	}

	blocked := []string{
		"printf x > " + filepath.Join(main, "NOTES.txt"),
		"echo x >> " + filepath.Join(main, "NOTES.txt"),
		"tee " + filepath.Join(main, "NOTES.txt"),
		"cp a.txt " + filepath.Join(main, "NOTES.txt"),
		"touch " + filepath.Join(main, "NOTES.txt"),
		"chmod 644 " + filepath.Join(main, "NOTES.txt"),
		"sed -i 's/a/b/' " + filepath.Join(main, "NOTES.txt"),
		"dd if=a of=" + filepath.Join(main, "NOTES.txt"),
		"cd " + main + " && echo x > NOTES.txt",
		"mv " + filepath.Join(main, "demo.go") + " .",
		`python3 -c "open('` + filepath.Join(main, "NOTES.txt") + `','w')"`,
	}
	for _, command := range blocked {
		if err := env.checkShellWriteIsolation(command); err == nil {
			t.Errorf("escaping command allowed: %q", command)
		}
	}
}
