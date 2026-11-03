package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNodeSupportsOpenTUI(t *testing.T) {
	yes := []string{"v26.4.0", "v26.4", "v27.0.0", "26.10.1"}
	for _, v := range yes {
		if !nodeSupportsOpenTUI(v) {
			t.Errorf("%s should be supported", v)
		}
	}
	no := []string{"v26.3.9", "v25.9.0", "v24.0.0", "", "nope"}
	for _, v := range no {
		if nodeSupportsOpenTUI(v) {
			t.Errorf("%s should be rejected", v)
		}
	}
}

func TestOpenTUIAppFindsInstalledBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_TUI_APP", "")
	t.Chdir(t.TempDir())

	path := filepath.Join(home, "ui", "app.mjs")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("app"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := openTUIApp()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("app = %q, want %q", got, path)
	}
}

func TestOpenTUIAppExplainsHowToRepair(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	t.Setenv("WOMBAT_TUI_APP", "")
	t.Chdir(t.TempDir())

	_, err := openTUIApp()
	if err == nil {
		t.Fatal("expected a missing-UI error")
	}
	for _, want := range []string{"update.sh", "WSL2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q: %v", want, err)
		}
	}
}

func TestOpenTUILibraryFindsInstalledRenderer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	t.Setenv("WOMBAT_OTUI_LIB", "")
	app := filepath.Join(home, "ui", "app.mjs")
	name := "libopentui.so"
	switch runtime.GOOS {
	case "darwin":
		name = "libopentui.dylib"
	case "windows":
		name = "opentui.dll"
	}
	lib := filepath.Join(home, "native", name)
	if err := os.MkdirAll(filepath.Dir(lib), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lib, []byte("lib"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := openTUILibrary(app)
	if err != nil {
		t.Fatal(err)
	}
	if got != lib {
		t.Fatalf("renderer = %q, want %q", got, lib)
	}
}

func TestOpenTUILibraryRejectsMissingOverride(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "missing.so")
	t.Setenv("WOMBAT_OTUI_LIB", missing)

	_, err := openTUILibrary(filepath.Join(t.TempDir(), "app.mjs"))
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("expected an error naming %s, got %v", missing, err)
	}
}
