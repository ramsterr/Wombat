package tools

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// isSecretFile reports whether a path looks like it holds credentials.
func isSecretFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case ".env", ".netrc", ".npmrc", ".pypirc", ".pgpass",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"credentials", "credentials.json", "credentials.yml", "credentials.yaml":
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		switch base {
		case ".env.example", ".env.sample", ".env.template", ".env.dist":
			return false
		}
		return true
	}
	for _, prefix := range []string{"secrets.", "secret.", "credentials."} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".keystore", ".jks"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// secretWarning returns an advisory added to a tool result when a secrets file
// is written outside .gitignore. It never blocks the write.
func secretWarning(env *Env, path string) string {
	if env == nil || env.Dir == "" || !isSecretFile(path) {
		return ""
	}
	cmd := exec.Command("git", "-C", env.Dir, "check-ignore", "-q", "--", path)
	err := cmd.Run()
	if err == nil {
		return "" // already ignored
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return fmt.Sprintf("warning: %s looks like a secrets file and is not ignored by git. Add it to .gitignore (for example .env and .env.*, keeping .env.example) before committing.", filepath.Base(path))
	}
	return ""
}
