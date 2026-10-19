package llm

import (
	"context"
	"fmt"

	"github.com/ramsterr/Wombat/internal/config"
)

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	Reasoning   string       `json:"reasoning,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolCallID  string       `json:"tool_call_id,omitempty"`
	Name        string       `json:"name,omitempty"`
}

type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
}

type Usage struct {
	In     int
	Cached int
	Out    int
}

func (u Usage) Total() int { return u.In + u.Out }

type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []Tool
	MaxTokens   int
	Temperature float64
	NumCtx      int
	SessionID   string
	OnReasoning func(string)
}

type Result struct {
	Content    string
	Reasoning  string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string
}

type Provider interface {
	Name() string
	Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error)
	ListModels(ctx context.Context) ([]string, error)
}

var UserAgent = "wombat/dev"

func New(name string, pc *config.Provider, apiKey string) (Provider, error) {
	switch pc.Kind {
	case "openai":
		return newOpenAI(name, pc.BaseURL, apiKey, pc.NumCtx), nil
	case "anthropic":
		return newAnthropic(name, pc.BaseURL, apiKey), nil
	case "ollama":
		return newOllama(name, pc.BaseURL, pc.NumCtx), nil
	case "opencode":
		return newOpenCode(name, pc.BaseURL, apiKey), nil
	default:
		return nil, fmt.Errorf("provider %q: unknown kind %q", name, pc.Kind)
	}
}
