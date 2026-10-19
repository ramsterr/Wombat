package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ramsterr/Wombat/internal/llm"
)

func TestSessionRoundtrip(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	cwd := t.TempDir()

	s, err := Create(cwd, "openai", "gpt-5", "first task")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() == "" {
		t.Fatal("empty session id")
	}
	if err := s.Append(llm.Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{
		Role:      "assistant",
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{Role: "tool", ToolCallID: "c1", Name: "bash", Content: "a.txt"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(s.ID(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Messages) != 3 {
		t.Fatalf("messages = %d", len(reopened.Messages))
	}
	if reopened.Messages[0].Content != "hello" {
		t.Errorf("first message = %+v", reopened.Messages[0])
	}
	if len(reopened.Messages[1].ToolCalls) != 1 || reopened.Messages[1].ToolCalls[0].Arguments != `{"command":"ls"}` {
		t.Errorf("tool call not persisted: %+v", reopened.Messages[1])
	}
	if reopened.Messages[2].ToolCallID != "c1" {
		t.Errorf("tool result not persisted: %+v", reopened.Messages[2])
	}
	if reopened.Meta.Title != "first task" || reopened.Meta.Model != "gpt-5" {
		t.Errorf("meta = %+v", reopened.Meta)
	}

	metas, err := List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != s.ID() {
		t.Fatalf("list = %+v", metas)
	}
	latest, err := Latest(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != s.ID() {
		t.Errorf("latest = %s", latest.ID)
	}
}

func TestIsolatedByCwd(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	cwd1 := t.TempDir()
	cwd2 := t.TempDir()

	if _, err := Create(cwd1, "", "", "one"); err != nil {
		t.Fatal(err)
	}
	metas, err := List(cwd2)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 0 {
		t.Fatalf("sessions leaked across cwds: %+v", metas)
	}
}

func TestReplaceMessages(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	cwd := t.TempDir()
	s, _ := Create(cwd, "", "", "")
	_ = s.Append(llm.Message{Role: "user", Content: "a"})
	_ = s.Append(llm.Message{Role: "user", Content: "b"})

	if err := s.ReplaceMessages([]llm.Message{{Role: "user", Content: "summary"}}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.ID(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Messages) != 1 || reopened.Messages[0].Content != "summary" {
		t.Fatalf("messages = %+v", reopened.Messages)
	}
}

func TestStoreAttachment(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	dir := t.TempDir()
	s, err := Create(dir, "p", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "cat.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := s.StoreAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "image" || a.Name != "cat.png" || a.Path == src {
		t.Fatalf("attachment = %+v", a)
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatal(err)
	}
	b, err := s.StoreAttachment(src)
	if err != nil {
		t.Fatal(err)
	}
	if b.Path != a.Path {
		t.Fatalf("not deduplicated: %s vs %s", a.Path, b.Path)
	}
}
