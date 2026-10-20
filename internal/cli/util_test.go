package cli

import (
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/tools"
)

func TestHeadlessDefaultsAllowWorkspaceTools(t *testing.T) {
	p := newPrompter(false, false, config.Defaults())
	for _, kind := range []string{"bash", "write", "edit", "network", "outside"} {
		if d := p.Permit(kind, "x"); !d.Allow {
			t.Errorf("%s should be allowed by default in a headless run", kind)
		}
	}
}

func TestAskUserFailsWithoutTTY(t *testing.T) {
	p := newPrompter(false, false, config.Defaults())
	_, err := p.AskUser(tools.Question{Question: "which one?"})
	if err == nil || !strings.Contains(err.Error(), "no interactive user") {
		t.Fatalf("error = %v", err)
	}
}
