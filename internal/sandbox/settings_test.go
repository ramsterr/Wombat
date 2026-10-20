package sandbox

import "testing"

func TestApplyCommand(t *testing.T) {
	base := Settings{Mode: WorkspaceWrite, Network: false}

	settings, msg, changed, err := ApplyCommand("/sandbox", base)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a bare /sandbox should not change anything")
	}
	if msg == "" {
		t.Error("a bare /sandbox should report the state")
	}

	settings, _, changed, err = ApplyCommand("/sandbox network on", base)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !settings.Network {
		t.Errorf("network on = %+v changed=%v", settings, changed)
	}

	settings, _, changed, err = ApplyCommand("/sandbox network off", Settings{Network: true})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || settings.Network {
		t.Errorf("network off = %+v changed=%v", settings, changed)
	}

	settings, _, changed, err = ApplyCommand("/sandbox mode off", base)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || settings.Mode != Off {
		t.Errorf("mode off = %+v changed=%v", settings, changed)
	}

	if _, _, _, err := ApplyCommand("/sandbox mode nonsense", base); err == nil {
		t.Error("expected an error for an unknown mode")
	}
	if _, _, _, err := ApplyCommand("/sandbox bogus", base); err == nil {
		t.Error("expected an error for an unknown command")
	}
}

func TestSettingsPolicy(t *testing.T) {
	s := Settings{Mode: WorkspaceWrite, Network: true}
	p := s.Policy("/tmp/work")
	if p.Workspace != "/tmp/work" {
		t.Errorf("workspace = %q", p.Workspace)
	}
	if !p.AllowNetwork {
		t.Error("network should be allowed")
	}
	if p.Mode != Resolve(WorkspaceWrite) {
		t.Errorf("mode = %q", p.Mode)
	}
}

func TestSettingsNote(t *testing.T) {
	note := Settings{Mode: WorkspaceWrite, Network: false}.Note()
	if note == "" {
		t.Fatal("empty note")
	}
	if !contains(note, "network is unavailable") {
		t.Errorf("note = %q", note)
	}
	if !contains(note, "/sandbox network on") {
		t.Errorf("note should tell the model how to ask for the network: %q", note)
	}
	note = Settings{Mode: WorkspaceWrite, Network: true}.Note()
	if contains(note, "network is unavailable") {
		t.Errorf("note = %q", note)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
