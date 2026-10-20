package mode

import "testing"

func TestParse(t *testing.T) {
	for _, s := range []string{"chat", "create", "CHAT", " create "} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q): %v", s, err)
		}
	}
	if _, err := Parse("nope"); err == nil {
		t.Error("expected an error")
	}
}

func TestAllowsWrite(t *testing.T) {
	if Chat.AllowsWrite() {
		t.Error("chat must not allow writes")
	}
	if !Create.AllowsWrite() {
		t.Error("create must allow writes")
	}
}

func TestApply(t *testing.T) {
	m, msg, changed, err := Apply("/mode chat", Create)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || m != Chat {
		t.Errorf("mode = %v changed = %v", m, changed)
	}
	if msg == "" {
		t.Error("expected a message")
	}

	_, msg, changed, err = Apply("/modes", Chat)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("/modes should not change the mode")
	}
	if msg == "" {
		t.Error("expected the mode list")
	}

	if _, _, _, err := Apply("/mode bogus", Chat); err == nil {
		t.Error("expected an error for an unknown mode")
	}
}

func TestNote(t *testing.T) {
	if note := Chat.Note(); note == "" {
		t.Error("chat needs a note")
	}
	if note := Create.Note(); note == "" {
		t.Error("create needs a note")
	}
}
