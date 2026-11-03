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
)

const uiInstallCommand = "curl -fsSL https://raw.githubusercontent.com/ramsterr/Wombat/main/update.sh | sh"

func uiSetupError(reason string) error {
	return fmt.Errorf("%s\n\ninstall or repair the Wombat UI with:\n  %s\nOn Windows, run this inside WSL2 (Ubuntu). Then run `hash -r` and start wombat again", reason, uiInstallCommand)
}

// cmdHome launches the opentui frontend. There is no silent fallback: an old
// or missing frontend is reported instead of opening a different interface.
func cmdHome(sessionID string) error {
	node, err := openTUINode()
	if err != nil {
		return err
	}
	app, err := openTUIApp()
	if err != nil {
		return err
	}
	lib, err := openTUILibrary(app)
	if err != nil {
		return err
	}
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
func openTUINode() (string, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return "", uiSetupError("Node.js was not found; the Wombat UI needs Node 26.4 or newer")
	}
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		return "", uiSetupError("could not run `node --version`")
	}
	version := strings.TrimSpace(string(out))
	if !nodeSupportsOpenTUI(version) {
		return "", uiSetupError(fmt.Sprintf("Node %s is too old; the Wombat UI needs Node 26.4 or newer", version))
	}
	return node, nil
}

func nodeSupportsOpenTUI(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 26 || (major == 26 && minor >= 4)
}

// openTUIApp locates the built Solid app. It works from a checkout and from
// a binary installed next to a tui/ directory.
func openTUIApp() (string, error) {
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
			return candidate, nil
		}
	}
	return "", uiSetupError("the Wombat UI bundle is missing (expected " + filepath.Join(config.Home(), "ui", "app.mjs") + ")")
}

// openTUILibrary finds the native renderer shipped beside the app.
func openTUILibrary(app string) (string, error) {
	name := "libopentui.so"
	switch runtime.GOOS {
	case "darwin":
		name = "libopentui.dylib"
	case "windows":
		name = "opentui.dll"
	}
	if value := strings.TrimSpace(os.Getenv("WOMBAT_OTUI_LIB")); value != "" {
		if abs, err := filepath.Abs(value); err == nil {
			value = abs
		}
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			return value, nil
		}
		return "", uiSetupError("WOMBAT_OTUI_LIB points to a missing renderer: " + value)
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
			return candidate, nil
		}
	}
	return "", uiSetupError("the native opentui renderer is missing (expected " + filepath.Join(config.Home(), "native", name) + ")")
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
