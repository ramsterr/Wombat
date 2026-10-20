package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Mode string

const (
	Off              Mode = "off"
	ReadOnly         Mode = "read-only"
	WorkspaceWrite   Mode = "workspace-write"
	DangerFullAccess Mode = "danger-full-access"
)

var ErrUnsupported = errors.New("sandbox is not supported on this platform")

func Parse(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case "", Off:
		return Off, nil
	case ReadOnly:
		return ReadOnly, nil
	case WorkspaceWrite:
		return WorkspaceWrite, nil
	case DangerFullAccess:
		return DangerFullAccess, nil
	default:
		return "", fmt.Errorf("unknown sandbox mode %q (off, read-only, workspace-write, danger-full-access)", s)
	}
}

func (m Mode) Valid() bool {
	switch m {
	case Off, ReadOnly, WorkspaceWrite, DangerFullAccess:
		return true
	}
	return false
}

type Policy struct {
	Mode         Mode
	Workspace    string
	AllowNetwork bool
	TempDir      string
	CacheDir     string
}

func (p Policy) Enabled() bool {
	return p.Mode == ReadOnly || p.Mode == WorkspaceWrite
}

func Supported(mode Mode) bool {
	switch mode {
	case "", Off, DangerFullAccess:
		return true
	case ReadOnly, WorkspaceWrite:
		switch runtime.GOOS {
		case "darwin":
			_, err := exec.LookPath("sandbox-exec")
			return err == nil
		case "linux":
			_, err := exec.LookPath("bwrap")
			return err == nil
		}
	}
	return false
}

// Resolve returns a mode the host can actually enforce. Sandbox modes that
// need OS support that is missing fall back to Off instead of failing every
// command, so the agent still works on hosts without sandbox-exec or bwrap.
func Resolve(mode Mode) Mode {
	if Supported(mode) {
		return mode
	}
	return Off
}

// Command returns the executable and arguments used to run command under the
// configured sandbox. It fails closed when a sandbox mode needs OS support that
// is not available, except for off and danger-full-access.
func (p Policy) Command(command string) (string, []string, error) {
	switch p.Mode {
	case "", Off, DangerFullAccess:
		return "sh", []string{"-c", command}, nil
	case ReadOnly:
		return p.wrapReadOnly(command)
	case WorkspaceWrite:
		return p.wrapWorkspaceWrite(command)
	default:
		return "", nil, fmt.Errorf("unknown sandbox mode %q", p.Mode)
	}
}

func (p Policy) wrapReadOnly(command string) (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		path, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return "", nil, ErrUnsupported
		}
		profile := `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write-data (literal "/dev/null"))
`
		return path, []string{"-p", profile, "/bin/sh", "-c", command}, nil
	case "linux":
		return wrapBubblewrap(p, command, nil)
	default:
		return "", nil, ErrUnsupported
	}
}

func (p Policy) wrapWorkspaceWrite(command string) (string, []string, error) {
	switch runtime.GOOS {
	case "darwin":
		path, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return "", nil, ErrUnsupported
		}
		profile := p.darwinProfile()
		return path, []string{"-p", profile, "/bin/sh", "-c", command}, nil
	case "linux":
		return wrapBubblewrap(p, command, p.writablePaths())
	default:
		return "", nil, ErrUnsupported
	}
}

func (p Policy) writablePaths() []string {
	paths := []string{p.Workspace, p.TempDir, p.CacheDir, os.TempDir()}
	if h, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(h, ".cache"), filepath.Join(h, "Library", "Caches"))
	}
	paths = append(paths, "/tmp", "/private/tmp", "/dev")
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

func (p Policy) darwinProfile() string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")
	if !p.AllowNetwork {
		b.WriteString("(deny network*)\n")
	}
	b.WriteString("(deny file-write*)\n")
	for _, path := range p.writablePaths() {
		fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", sbQuote(path))
	}
	b.WriteString("(allow file-write-data (literal \"/dev/null\"))\n")
	return b.String()
}

func sbQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func wrapBubblewrap(p Policy, command string, writable []string) (string, []string, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return "", nil, ErrUnsupported
	}
	args := []string{"--die-with-parent", "--ro-bind", "/", "/"}
	seen := map[string]bool{}
	for _, path := range writable {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		args = append(args, "--bind", path, path)
	}
	if !p.AllowNetwork {
		args = append(args, "--unshare-net")
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--chdir", p.Workspace, "--", "/bin/sh", "-c", command)
	return bwrap, args, nil
}
