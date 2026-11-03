package commands

import "testing"

func TestMatch(t *testing.T) {
	all := Match("/")
	if len(all) != len(List) {
		t.Errorf("match all = %d, want %d", len(all), len(List))
	}
	modes := Match("/mode")
	if len(modes) < 2 {
		t.Fatalf("expected /mode and /modes, got %d", len(modes))
	}
	if modes[0].Name != "/mode" || modes[1].Name != "/modes" {
		t.Errorf("order = %s, %s", modes[0].Name, modes[1].Name)
	}
	if len(Match("/zzz")) != 0 {
		t.Error("expected no matches")
	}
}

func TestFind(t *testing.T) {
	if _, ok := Find("/mode"); !ok {
		t.Error("/mode missing")
	}
	if _, ok := Find("/nope"); ok {
		t.Error("unexpected command")
	}
}

func TestHelp(t *testing.T) {
	help := Help()
	if help == "" {
		t.Fatal("empty help")
	}
	if len(Match("/")) == 0 {
		t.Error("no commands")
	}
}

func TestThinkingCommand(t *testing.T) {
	state, _, changed, err := ThinkingCommand("/thinking on", false)
	if err != nil || !changed || !state {
		t.Fatalf("on: state=%v changed=%v err=%v", state, changed, err)
	}
	state, _, changed, err = ThinkingCommand("/thinking off", true)
	if err != nil || !changed || state {
		t.Fatalf("off: state=%v changed=%v err=%v", state, changed, err)
	}
	if _, _, _, err := ThinkingCommand("/thinking", true); err != nil {
		t.Fatalf("status: %v", err)
	}
	if _, _, _, err := ThinkingCommand("on", false); err != nil {
		t.Fatalf("bare arg: %v", err)
	}
}
