package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBashCancelStopsPromptly(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := (&Bash{}).Run(ctx, json.RawMessage(`{"command":"sleep 30 | cat"}`), env)
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bash did not stop after cancel")
	}
}

func TestBashDeniedWithFeedback(t *testing.T) {
	env := &Env{Dir: t.TempDir(), Permit: func(string, string) Decision {
		return Decision{Feedback: "use printf instead"}
	}}
	_, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"echo hi"}`), env)
	if err == nil || !strings.Contains(err.Error(), "use printf instead") {
		t.Fatalf("got %v, want feedback in error", err)
	}
}
