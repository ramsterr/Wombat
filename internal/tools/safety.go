package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// isSecretFile reports whether a path looks like it holds credentials.
func isSecretFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case ".env", ".netrc", ".npmrc", ".pypirc", ".pgpass", ".git-credentials",
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

// protectedDirs are the directories that hold credentials for common tools.
// Reading one is never automatic: the user has to approve it, even when the
// read permission itself is allow.
var protectedDirs = []string{
	".ssh", ".aws", ".gnupg", ".kube", ".azure", ".config/gcloud",
	".config/gh", ".docker", ".terraform.d", ".password-store",
}

// isProtectedPath reports whether a read has to be approved by the user
// because the file can contain credentials. It does not block the read, it
// asks; the permission class is sensitive-read.
func isProtectedPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return isSecretFile(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if isSecretFile(abs) {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	home, _ = filepath.Abs(home)
	rel, err := filepath.Rel(home, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".wombat/config.json" || rel == ".config/wombat/config.json" {
		return true
	}
	for _, dir := range protectedDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
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

var redactPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private keys, including the body.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "[redacted private key]"},
	// Common provider tokens.
	{regexp.MustCompile(`\b(sk|rk)-[A-Za-z0-9_-]{20,}\b`), "$1-[redacted]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`), "gh*_[redacted]"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), "github_pat_[redacted]"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), "xox*-[redacted]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "AKIA[redacted]"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`), "AIza[redacted]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), "[redacted jwt]"},
	// Assignments such as API_KEY=..., password: "...", Authorization: Bearer ...
	{regexp.MustCompile(`(?i)\b(api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|password|passwd|client[_-]?secret)\b(\s*[:=]\s*)(["']?)[^\s"'\n]{6,}`), "$1$2$3[redacted]"},
	{regexp.MustCompile(`(?i)\b(authorization\s*:\s*(?:bearer|basic)\s+)[^\s"']+`), "$1[redacted]"},
	// Credentials embedded in a URL.
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), "$1[redacted]@"},
	// Secret-looking command-line flags, such as --token=... or --api-key ...
	{regexp.MustCompile(`(?i)(--?(?:api[-_]?key|token|secret|password|passwd|client[-_]?secret)(?:=|\s+))[^\s"']+`), "$1[redacted]"},
}

// RedactSecrets removes credentials from tool output before the model sees
// it, and before the result is written to the session. It is deliberately
// conservative: it only rewrites strings that look like keys or assignments,
// so ordinary source code is left alone.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	out := s
	for _, p := range redactPatterns {
		out = p.re.ReplaceAllString(out, p.repl)
	}
	return out
}

// checkOldContent gates reading an existing credentials file before an edit:
// a diff would otherwise put the old secret in front of the user and the
// model without the explicit sensitive-read approval.
func (e *Env) checkOldContent(path string) error {
	if !isProtectedPath(path) {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return e.CheckPath("read", path)
}

// shellSecretTarget reports the credentials path a shell command appears to
// name, so `cat .env` cannot bypass the read gate. It is deliberately
// conservative about not matching ordinary filenames.
var shellSecretPattern = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_.-])(\.env(\.[A-Za-z0-9_.-]+)?|id_(rsa|dsa|ecdsa|ed25519)|\.aws/credentials|\.ssh/[A-Za-z0-9_.-]+|\.netrc|\.git-credentials|\.npmrc|\.pypirc|credentials\.json|service[-_]account\.json|\.wombat/config\.json)($|[^A-Za-z0-9_.-])`)

func shellSecretTarget(command string) string {
	m := shellSecretPattern.FindStringSubmatch(command)
	if len(m) < 2 {
		return ""
	}
	target := strings.ToLower(m[2])
	for _, safe := range []string{".env.example", ".env.sample", ".env.template", ".env.dist"} {
		if target == safe {
			return ""
		}
	}
	return m[2]
}
