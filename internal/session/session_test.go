package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestEphemeralAttachmentsAreNotReloaded(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	cwd := t.TempDir()
	s, err := Create(cwd, "openai", "gpt-5", "shot")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(llm.Message{
		Role:    "user",
		Content: "I read screenshot.png.",
		Attachments: []llm.Attachment{{
			Kind: "image", MIME: "image/png", Name: "screenshot.png",
			Size: 3, Data: []byte("png"),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.ID(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Messages) != 1 || len(reopened.Messages[0].Attachments) != 0 {
		t.Fatalf("attachments reloaded: %+v", reopened.Messages)
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

func TestConcurrentAppend(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	cwd := t.TempDir()
	s, err := Create(cwd, "p", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Append(llm.Message{Role: "user", Content: fmt.Sprintf("message-%d", i)})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("append failed: %v", err)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.ID(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Messages) != n {
		t.Fatalf("messages = %d, want %d", len(reopened.Messages), n)
	}
	if reopened.Meta.Turns != n {
		t.Fatalf("turns = %d, want %d", reopened.Meta.Turns, n)
	}
}

func TestCreateAtUsesProvidedHome(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	s, err := CreateAt(home, cwd, "p", "m", "isolated")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := projectDirAt(home, cwd)
	if filepath.Dir(s.path) != wantDir {
		t.Fatalf("session path = %s, want under %s", s.path, wantDir)
	}
	if s.Path() != s.path {
		t.Fatalf("Path() = %q, want %q", s.Path(), s.path)
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("session file missing: %v", err)
	}
}

func TestSessionReasoningRoundtrip(t *testing.T) {
	t.Setenv("WOMBAT_HOME", t.TempDir())
	s, err := Create(t.TempDir(), "p", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"reasoning","encrypted_content":"enc"}`)
	if err := s.Append(llm.Message{
		Role:           "assistant",
		Content:        "answer",
		Reasoning:      "thought",
		ReasoningItems: []json.RawMessage{raw},
		ToolCalls:      []llm.ToolCall{{ID: "c1", Name: "bash", Arguments: "{}", Signature: "sig"}},
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Flush()
	got, err := Open(s.ID(), s.Meta.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	m := got.Messages[0]
	if m.Reasoning != "thought" || len(m.ReasoningItems) != 1 {
		t.Fatalf("reasoning not persisted: %+v", m)
	}
	if m.ToolCalls[0].Signature != "sig" {
		t.Fatalf("tool signature not persisted: %+v", m.ToolCalls[0])
	}
}
