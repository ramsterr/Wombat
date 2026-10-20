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
