package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var dangerousCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|]\s*)sudo(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)doas(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)rm\s+(-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*)\s+(/|~|\$HOME|\.\.)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)mkfs(\.[a-z0-9]+)?(\s|$)`),
	regexp.MustCompile(`(?i)\bdd\s+[^\n]*\bof=/dev/`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(shutdown|reboot|halt|poweroff)(\s|$)`),
	regexp.MustCompile(`(?i)\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f[a-z]*|push\s+[^\n]*--force|push\s+-f)(\s|$)`),
	regexp.MustCompile(`(?i)\bchmod\s+-R\s+777\s+/`),
	regexp.MustCompile(`(?i)\bchown\s+-R\s+\S+\s+/`),
	regexp.MustCompile(`(?i)\bkill\s+-9\s+-1(\s|$)`),
	regexp.MustCompile(`(?i):\(\)\s*\{`),
	// Piping a download straight into a shell, or running one through a
	// process substitution: untrusted code with no review step.
	regexp.MustCompile(`(?i)\b(curl|wget|fetch)\b[^\n|]*\|\s*(sudo\s+)?(sh|bash|zsh|fish|dash)(\s|$)`),
	regexp.MustCompile(`(?i)\b(sh|bash|zsh|fish|dash)\s+<\(\s*(curl|wget|fetch)\b`),
	regexp.MustCompile(`(?i)\beval\s+["']?\$\(\s*(curl|wget|fetch)\b`),
	// Irreversible infrastructure and repository operations.
	regexp.MustCompile(`(?i)\b(docker\s+(system|volume|network)\s+prune|docker\s+rm\s+-f|kubectl\s+delete|terraform\s+destroy|gh\s+repo\s+delete)\b`),
	regexp.MustCompile(`(?i)\b(aws|gcloud|az)\b[^\n]*\b(delete|terminate|destroy)\b`),
	regexp.MustCompile(`(?i)\b(drop\s+(database|schema|table)|truncate\s+table)\b`),
}

var networkCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(curl|wget|nc|netcat|ssh|scp|sftp|ftp|telnet)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)git\s+(clone|fetch|pull)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)go\s+(get|install)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(npm|pnpm|yarn)\s+(install|ci|add)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(pip|pip3|uv\s+pip)\s+install(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)cargo\s+install(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)brew\s+install(\s|$)`),
}

func IsDangerousCommand(command string) bool {
	for _, re := range dangerousCommandPatterns {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

func IsNetworkCommand(command string) bool {
	for _, re := range networkCommandPatterns {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

func (e *Env) ResolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Join(e.Dir, path), nil
}

// CheckPath checks policy and permission for one path. Reads of credentials
// files are approved explicitly; other reads are not gated.
func (e *Env) CheckPath(kind, path string) error {
	return e.CheckPathPreview(kind, path, "")
}

// CheckPathPreview is CheckPath with the content or diff shown to the user
// when the call is a write or an edit.
func (e *Env) CheckPathPreview(kind, path, preview string) error {
	if err := e.checkPolicy(kind, path); err != nil {
		return err
	}
	if kind == "read" {
		if isProtectedPath(path) {
			return e.permitPreview("sensitive-read", path, "")
		}
		return nil
	}
	outside, err := e.pathOutside(path)
	if err != nil {
		return err
	}
	if outside {
		if e.DenyOutside {
			return fmt.Errorf("path outside workspace is disabled: %s", path)
		}
		if err := e.permit("outside", path); err != nil {
			return fmt.Errorf("path outside workspace: %w", err)
		}
		return nil
	}
	return e.permitPreview(kind, path, preview)
}

// checkWriteOwnership verifies that a write or edit targets one of the files
// the caller declared. Paths are compared cleanly, relative to the workspace.
func (e *Env) checkWriteOwnership(path string) error {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.Dir, abs)
	}
	abs = filepath.Clean(abs)
	for _, allowed := range e.WritePaths {
		p := allowed
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.Dir, p)
		}
		if filepath.Clean(p) == abs {
			return nil
		}
	}
	return fmt.Errorf("path is outside this worker's declared files: %s (allowed: %s)", path, strings.Join(e.WritePaths, ", "))
}

// CheckPaths asks for one permission decision covering several paths, so an
// atomic patch is approved once with its complete diff.
func (e *Env) CheckPaths(kind string, paths []string, preview string) error {
	for _, path := range paths {
		if err := e.checkPolicy(kind, path); err != nil {
			return err
		}
	}
	outside := make([]string, 0, len(paths))
	for _, path := range paths {
		ok, err := e.pathOutside(path)
		if err != nil {
			return err
		}
		if ok {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		if e.DenyOutside {
			return fmt.Errorf("path outside workspace is disabled: %s", strings.Join(outside, ", "))
		}
		if err := e.permit("outside", strings.Join(outside, ", ")); err != nil {
			return fmt.Errorf("path outside workspace: %w", err)
		}
		return nil
	}
	return e.permitPreview(kind, strings.Join(paths, ", "), preview)
}

// pathOutside reports whether path is outside the workspace.
func (e *Env) pathOutside(path string) (bool, error) {
	if e == nil || e.Dir == "" || e.AllowOutside {
		return false, nil
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.Dir, abs)
	}
	abs = canonicalPath(abs)
	root := canonicalPath(e.Dir)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false, err
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel), nil
}
