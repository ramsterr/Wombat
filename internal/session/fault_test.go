package session

import (
	"os"
	"strings"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
)

func newSession(t *testing.T) (*Session, string) {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	s, err := CreateAt(home, cwd, "fault", "fault-model", "fault test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: "user", Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: "assistant", Content: "second"}); err != nil {
		t.Fatal(err)
	}
	return s, cwd
}

// A session file truncated inside its header cannot be interpreted; Open must
// say so instead of returning a half-built session.
func TestFaultOpenCorruptHeader(t *testing.T) {
	s, cwd := newSession(t)
	if err := os.WriteFile(s.Path(), []byte("{not json\n{\"type\":\"msg\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Open(s.ID(), cwd)
	if err == nil {
		t.Fatalf("Open must reject a corrupt header, got session %+v", got)
	}
	if !strings.Contains(err.Error(), "corrupt session file") {
		t.Fatalf("error should name the corruption, got %q", err)
	}
}

// An empty file is a failed write, not an empty conversation.
func TestFaultOpenEmptyFile(t *testing.T) {
	s, cwd := newSession(t)
	if err := os.WriteFile(s.Path(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.ID(), cwd); err == nil {
		t.Fatal("Open must reject an empty session file")
	}
}

// A process killed mid-append leaves a partial trailing line. That is
// recoverable: everything before the tear must survive.
func TestFaultOpenTornTail(t *testing.T) {
	s, cwd := newSession(t)
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	torn := append(append([]byte{}, raw...), []byte(`{"type":"msg","msg":{"role":"user","content":"tor`)...)
	if err := os.WriteFile(s.Path(), torn, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Open(s.ID(), cwd)
	if err != nil {
		t.Fatalf("a torn tail must not fail the open: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("recovered %d messages, want the 2 complete ones", len(got.Messages))
	}
	if got.Messages[0].Content != "first" || got.Messages[1].Content != "second" {
		t.Fatalf("recovered messages are wrong: %+v", got.Messages)
	}
}

// The scanner is capped at 16 MiB per line. An oversized line is a corrupt
// file, and must surface as an error rather than a panic or a hang.
func TestFaultOpenOversizedLine(t *testing.T) {
	s, cwd := newSession(t)
	huge := append([]byte(`{"id":"`), make([]byte, 17<<20)...)
	for i := 0; i < len(huge); i++ {
		if huge[i] == 0 {
			huge[i] = 'x'
		}
	}
	if err := os.WriteFile(s.Path(), huge, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.ID(), cwd); err == nil {
		t.Fatal("Open must reject an oversized session line")
	}
}

// Opening a session that was never written reports not-exist so callers can
// distinguish it from corruption.
func TestFaultOpenMissingFile(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("WOMBAT_HOME", home)
	if _, err := Open("nope", cwd); err == nil || !os.IsNotExist(err) {
		t.Fatalf("want not-exist error, got %v", err)
	}
}
