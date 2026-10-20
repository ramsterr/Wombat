package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatModeBlocksWrites(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Dir: dir, DenyMutate: true}
	_, err := (&Write{}).Run(context.Background(), json.RawMessage(`{"path":"a.txt","content":"x"}`), env)
	if err == nil || !strings.Contains(err.Error(), "chat mode") {
		t.Fatalf("write error = %v", err)
	}
	_, err = (&Edit{}).Run(context.Background(), json.RawMessage(`{"path":"a.txt","old_string":"a","new_string":"b"}`), env)
	if err == nil || !strings.Contains(err.Error(), "chat mode") {
		t.Fatalf("edit error = %v", err)
	}
}

func TestChatModeBlocksMutatingCommands(t *testing.T) {
	env := &Env{Dir: t.TempDir(), DenyMutate: true}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi > out.txt"}`), env)
	if err == nil || !strings.Contains(err.Error(), "chat mode") {
		t.Fatalf("bash error = %v", err)
	}
	// read-only commands still run
	out, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`), env)
	if err != nil {
		t.Fatalf("read-only command: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Errorf("out = %q", out)
	}
}
