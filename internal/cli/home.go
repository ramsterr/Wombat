package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tui"
)

// cmdHome launches the opentui frontend when it is available and falls back
// to the original Bubble Tea interface otherwise.
func cmdHome(sessionID string) error {
	if os.Getenv("WOMBAT_CLASSIC") == "1" || os.Getenv("WOMBAT_HEADLESS") == "1" {
		return cmdClassicHome(sessionID)
	}
	node, ok := openTUINode()
	app, okApp := openTUIApp()
	if !ok || !okApp {
		return cmdClassicHome(sessionID)
	}
	lib := openTUILibrary(app)
	for {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if sessionID == "" && (cfg.Provider == "" || cfg.ActiveModel() == "") {
			if err := cmdSetup(nil); err != nil {
				return err
			}
			continue
		}
		err = runOpenTUI(node, app, lib, sessionID)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "signal:") || strings.Contains(err.Error(), "broken pipe") {
			return nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
			if err := cmdSetup(nil); err != nil {
				return err
			}
			continue
		}
		return err
	}
}

func cmdClassicHome(sessionID string) error {
	for {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if sessionID == "" && (cfg.Provider == "" || cfg.ActiveModel() == "") {
			if err := cmdSetup(nil); err != nil {
				return err
			}
			continue
		}
		cwd, _ := os.Getwd()
		action, err := tui.Run(tui.Options{
			Version:   VersionString(),
			Cwd:       cwd,
			Cfg:       cfg,
			SessionID: sessionID,
			Mouse:     cfg.Mouse,
		})
		if err != nil {
			return err
		}
		switch action.Kind {
		case "configure":
			if err := cmdSetup(nil); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func cmdResumeTUI(args []string) error {
	cwd, _ := os.Getwd()
	id := ""
	if len(args) > 0 {
		id = args[0]
	}
	if id == "" || id == "last" {
		meta, err := session.Latest(cwd)
		if err != nil {
			return fmt.Errorf("no sessions to resume in %s", cwd)
		}
		id = meta.ID
	}
	return cmdHome(id)
}

// openTUINode finds a Node runtime new enough for @opentui/core.
func openTUINode() (string, bool) {
	node, err := exec.LookPath("node")
	if err != nil {
		return "", false
	}
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		return "", false
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return "", false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return "", false
	}
	if major < 26 || (major == 26 && minor < 4) {
		return "", false
	}
	return node, true
}

// openTUIApp locates the built Solid app. It works from a checkout and from
// a binary installed next to a tui/ directory.
func openTUIApp() (string, bool) {
	candidates := []string{}
	if value := strings.TrimSpace(os.Getenv("WOMBAT_TUI_APP")); value != "" {
		candidates = append(candidates, value)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "tui", "dist", "app.mjs"),
			filepath.Join(dir, "..", "share", "wombat", "tui", "app.mjs"),
			filepath.Join(dir, "..", "lib", "wombat", "tui", "app.mjs"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "tui", "dist", "app.mjs"))
	}
	candidates = append(candidates, filepath.Join(config.Home(), "ui", "app.mjs"))
	for _, candidate := range candidates {
		if abs, err := filepath.Abs(candidate); err == nil {
			candidate = abs
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// openTUILibrary finds the native renderer shipped beside the app. When it is
// absent, opentui resolves its platform package from node_modules.
func openTUILibrary(app string) string {
	if value := strings.TrimSpace(os.Getenv("WOMBAT_OTUI_LIB")); value != "" {
		return value
	}
	name := "libopentui.so"
	switch runtime.GOOS {
	case "darwin":
		name = "libopentui.dylib"
	case "windows":
		name = "opentui.dll"
	}
	appDir := filepath.Dir(app)
	candidates := []string{
		filepath.Join(appDir, "..", "native", name),
		filepath.Join(appDir, "native", name),
		filepath.Join(appDir, "..", name),
	}
	for _, candidate := range candidates {
		if abs, err := filepath.Abs(candidate); err == nil {
			candidate = abs
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func runOpenTUI(node, app, lib, sessionID string) error {
	// The opentui client owns ctrl+c. The parent must not disappear before
	// the child can restore the terminal.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	args := []string{"--experimental-ffi", "--no-warnings", app}
	cmd := exec.Command(node, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	env := os.Environ()
	env = append(env, "WOMBAT_BRIDGE_EXE="+exe)
	if sessionID != "" {
		env = append(env, "WOMBAT_SESSION_ID="+sessionID)
	}
	if lib != "" {
		env = append(env, "WOMBAT_OTUI_LIB="+lib)
	}
	cmd.Env = env
	return cmd.Run()
}
