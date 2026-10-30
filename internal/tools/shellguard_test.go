package tools

import (
	"os"
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

func TestBroadScanGuard(t *testing.T) {
	home, _ := os.UserHomeDir()
	blocked := []string{
		"find / -name go.mod",
		"grep -r wombat ~",
		"ls -R /",
		"du -sh $HOME",
		"find " + home + " -name '*.go'",
	}
	for _, command := range blocked {
		if err := guardBroadScan(command); err == nil {
			t.Errorf("broad scan allowed: %q", command)
		}
	}
	allowed := []string{
		"find . -name go.mod",
		"grep -r wombat internal",
		"ls -la /Users/ramster/Wombatt",
		"ls /",
		"find /tmp -maxdepth 1",
		"cd /tmp && find . -name go.mod",
	}
	for _, command := range allowed {
		if err := guardBroadScan(command); err != nil {
			t.Errorf("focused scan rejected: %q: %v", command, err)
		}
	}
}
