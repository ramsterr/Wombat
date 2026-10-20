package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Decision struct {
	Allow    bool
	Always   bool
	Feedback string
}

type Env struct {
	Dir           string
	Permit        func(kind, detail string) Decision
	ReadOnly      bool
	AllowOutside  bool
	DenyDangerous bool
	DenyNetwork   bool
}

type Tool interface {
	Name() string
	Class() string
	Description() string
	Schema() map[string]any
	Run(ctx context.Context, args json.RawMessage, env *Env) (string, error)
}

func Registry() []Tool {
	return []Tool{&Bash{}, &Read{}, &Write{}, &Edit{}, &Glob{}, &Grep{}}
}

func (e *Env) Check(kind, detail string) error {
	if err := e.checkPolicy(kind, detail); err != nil {
		return err
	}
	return e.permit(kind, detail)
}

func (e *Env) checkPolicy(kind, detail string) error {
	if kind == "read" {
		return nil
	}
	if e.ReadOnly && (kind == "write" || kind == "edit" || kind == "bash") {
		return fmt.Errorf("read-only mode: %s is disabled", kind)
	}
	if kind == "bash" {
		if e.DenyDangerous && IsDangerousCommand(detail) {
			return fmt.Errorf("blocked dangerous command: %s", detail)
		}
		if e.DenyNetwork && IsNetworkCommand(detail) {
			return fmt.Errorf("network access is disabled: %s", detail)
		}
	}
	return nil
}

func (e *Env) permit(kind, detail string) error {
	if kind == "read" || e.Permit == nil {
		return nil
	}
	d := e.Permit(kind, detail)
	if d.Allow {
		return nil
	}
	if d.Feedback != "" {
		return fmt.Errorf("permission denied by user — instructions: %s", d.Feedback)
	}
	return fmt.Errorf("permission denied by user")
}

func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	err := json.Unmarshal(args, &v)
	return v, err
}

func desc(what string) map[string]any {
	return map[string]any{"type": "string", "description": what}
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func Truncate(s string, maxLines int, headFraction float64, maxBytes int) string {
	if maxLines <= 0 && maxBytes <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	tooManyLines := maxLines > 0 && len(lines) > maxLines
	tooManyBytes := maxBytes > 0 && len(s) > maxBytes
	if !tooManyLines && !tooManyBytes {
		return s
	}
	if !tooManyLines {
		head := maxBytes * 2 / 3
		if head > len(s) {
			head = len(s)
		}
		tail := len(s) - maxBytes/3
		if tail < head {
			tail = head
		}
		return s[:head] + fmt.Sprintf("\n… [%d bytes omitted] …\n", len(s)-head-(len(s)-tail)) + s[tail:]
	}
	head := int(float64(maxLines) * headFraction)
	if head < 1 {
		head = 1
	}
	tail := maxLines - head
	if tail < 0 {
		tail = 0
	}
	omitted := len(lines) - head - tail
	out := append([]string{}, lines[:head]...)
	out = append(out, fmt.Sprintf("… [%d lines omitted] …", omitted))
	out = append(out, lines[len(lines)-tail:]...)
	return strings.Join(out, "\n")
}
