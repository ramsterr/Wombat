package cli

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
)

func testConfig(provider, model string) *config.Config {
	cfg := config.Defaults()
	cfg.Provider = provider
	cfg.Model = model
	return cfg
}

func itemActions(items []menuItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.action)
	}
	return out
}

func TestHomeItemsActions(t *testing.T) {
	items := homeItems(testConfig("p", "m"), "")
	got := itemActions(items)
	want := []string{"chat", "configure", "models", "sessions", "doctor", "quit"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}

func TestHomeItemsResumeWhenSession(t *testing.T) {
	items := homeItems(testConfig("p", "m"), "20261019-120000-ab12cd · fix tests")
	got := itemActions(items)
	want := []string{"chat", "resume", "configure", "models", "sessions", "doctor", "quit"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	if !strings.Contains(items[1].hint, "fix tests") {
		t.Fatalf("resume hint missing title: %q", items[1].hint)
	}
}

func TestHomeItemsUnconfiguredHint(t *testing.T) {
	items := homeItems(testConfig("", ""), "")
	if items[0].hint != "no provider configured" {
		t.Fatalf("hint = %q", items[0].hint)
	}
	items = homeItems(testConfig("opencode-go", "glm-5.2"), "")
	if items[0].hint != "opencode-go/glm-5.2" {
		t.Fatalf("hint = %q", items[0].hint)
	}
}

func TestMenuKeyLoop(t *testing.T) {
	render := func(int) {}
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"enter selects start", "\r", 0},
		{"arrow down then enter", "\x1b[B\x1b[B\r", 2},
		{"arrow up wraps", "\x1b[A\r", 5},
		{"j walks down", "jjj\r", 3},
		{"k walks up", "k\r", 5},
		{"number jumps", "4\r", 3},
		{"number out of range ignored", "9\r", 0},
		{"q quits", "q", -1},
	}
	for _, tc := range cases {
		sel, err := menuKeyLoop(bufio.NewReader(strings.NewReader(tc.in)), 6, 0, render)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if sel != tc.want {
			t.Fatalf("%s: sel = %d, want %d", tc.name, sel, tc.want)
		}
	}
}

func TestRunMenuText(t *testing.T) {
	cfg := testConfig("opencode-go", "glm-5.2")
	items := homeItems(cfg, "")

	var out bytes.Buffer
	action, err := runMenuText(strings.NewReader("3\n"), &out, items, 0)
	if err != nil || action != "models" {
		t.Fatalf("action = %q, err = %v", action, err)
	}

	action, err = runMenuText(strings.NewReader(""), &out, items, 0)
	if err != nil || action != "quit" {
		t.Fatalf("eof: action = %q, err = %v", action, err)
	}

	action, err = runMenuText(strings.NewReader("\n"), &out, items, 1)
	if err != nil || action != "configure" {
		t.Fatalf("default: action = %q, err = %v", action, err)
	}
}
