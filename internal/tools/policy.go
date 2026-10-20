package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var dangerousCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|]\s*)sudo(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)rm\s+(-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*)\s+(/|~|\$HOME|\.\.)(\s|$)`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)mkfs(\.[a-z0-9]+)?(\s|$)`),
	regexp.MustCompile(`(?i)\bdd\s+[^\n]*\bof=/dev/`),
	regexp.MustCompile(`(?i)(^|[;&|]\s*)(shutdown|reboot|halt|poweroff)(\s|$)`),
	regexp.MustCompile(`(?i)\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f[a-z]*|push\s+[^\n]*--force|push\s+-f)(\s|$)`),
	regexp.MustCompile(`(?i)\bchmod\s+-R\s+777\s+/`),
	regexp.MustCompile(`(?i)\bchown\s+-R\s+\S+\s+/`),
	regexp.MustCompile(`(?i)\bkill\s+-9\s+-1(\s|$)`),
	regexp.MustCompile(`(?i):\(\)\s*\{`),
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

func (e *Env) CheckPath(kind, path string) error {
	if kind != "read" && e.Dir != "" && !e.AllowOutside {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(e.Dir, abs)
		}
		abs, err := filepath.Abs(abs)
		if err != nil {
			return err
		}
		root, err := filepath.Abs(e.Dir)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return err
		}
		outside := rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
		if err := e.checkPolicy(kind, path); err != nil {
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
		return e.permit(kind, path)
	}
	return e.Check(kind, path)
}
