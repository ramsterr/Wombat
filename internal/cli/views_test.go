package cli

import (
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/session"
)

func testAgent(t *testing.T, cfg *config.Config, sess *session.Session) (*runOpts, *Prompter, *renderer) {
	t.Helper()
	p := newPrompter(false, false, cfg)
	r := &renderer{cfg: cfg, sess: sess}
	opts := &runOpts{}
	ag, err := buildAgent(cfg, *opts, sess, p)
	if err != nil {
		t.Fatal(err)
	}
	_ = ag
	return opts, p, r
}

func TestReplModeCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cfg := &config.Config{
		Provider:  "openai",
		Model:     "test-model",
		Mode:      "create",
		Sandbox:   "off",
		Providers: map[string]*config.Provider{"openai": {Kind: "openai", BaseURL: "http://127.0.0.1:1/v1"}},
	}
	sess, err := session.CreateAt(home, t.TempDir(), "openai", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	opts, p, r := testAgent(t, cfg, sess)
	ag, err := buildAgent(cfg, *opts, sess, p)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Env.Mode != "create" {
		t.Fatalf("mode = %q", ag.Env.Mode)
	}
	_, newAg, _, err := handleCommand("/mode chat", cfg, ag, sess, p, r, opts)
	if err != nil {
		t.Fatal(err)
	}
	if newAg == nil {
		t.Fatal("expected a new agent")
	}
	if newAg.Env.Mode != "chat" {
		t.Fatalf("mode = %q, want chat", newAg.Env.Mode)
	}
	if !newAg.Env.DenyMutate {
		t.Error("chat mode should block mutations")
	}
}

func TestReplViewCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	cfg := &config.Config{
		Provider:  "openai",
		Model:     "test-model",
		Mode:      "create",
		Sandbox:   "off",
		Providers: map[string]*config.Provider{"openai": {Kind: "openai", BaseURL: "http://127.0.0.1:1/v1"}},
	}
	sess, err := session.CreateAt(home, t.TempDir(), "openai", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	opts, p, r := testAgent(t, cfg, sess)
	ag, err := buildAgent(cfg, *opts, sess, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"/toolset", "/sources", "/history", "/modes"} {
		if _, _, _, err := handleCommand(cmd, cfg, ag, sess, p, r, opts); err != nil {
			t.Errorf("%s: %v", cmd, err)
		}
	}
}

func TestHelpListsCommands(t *testing.T) {
	help := helpText()
	for _, want := range []string{"/mode", "/sandbox", "/toolset", "/sources", "/history"} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %s", want)
		}
	}
}
