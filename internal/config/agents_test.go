package config

import (
	"strings"
	"testing"
)

func TestAgentDefValidate(t *testing.T) {
	tests := []struct {
		name    string
		def     AgentDef
		wantErr string
	}{
		{name: "minimal", def: AgentDef{Name: "math-research"}},
		{name: "normalizes spaces", def: AgentDef{Name: "Math  Research"}, wantErr: ""},
		{name: "missing name", def: AgentDef{}, wantErr: "name is required"},
		{name: "reserved", def: AgentDef{Name: "default"}, wantErr: "reserved"},
		{name: "bad chars", def: AgentDef{Name: "Math.Research"}, wantErr: "lowercase"},
		{name: "too long", def: AgentDef{Name: "a" + strings.Repeat("b", MaxAgentNameLen)}, wantErr: "too long"},
		{
			name:    "bad permission value",
			def:     AgentDef{Name: "x", Permissions: map[string]string{"bash": "ask"}},
			wantErr: "allow or deny",
		},
		{
			name:    "bad permission kind",
			def:     AgentDef{Name: "x", Permissions: map[string]string{"telepathy": "allow"}},
			wantErr: "unknown permission",
		},
		{
			name: "mcp permission",
			def:  AgentDef{Name: "x", Permissions: map[string]string{"mcp:github": "allow"}},
		},
		{
			name:    "max steps cap",
			def:     AgentDef{Name: "x", MaxSteps: MaxAgentSteps + 1},
			wantErr: "max_steps",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.def.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseAgentMarkdown(t *testing.T) {
	data := []byte(`---
name: math-research
description: Verifies mathematical claims
model: reasoning-model
tools: read, grep, websearch
exclude_tools: write
mcp_servers: wolfram, arxiv
permissions: write=deny, bash=allow, mcp:wolfram=allow
skills: careful-proofs, citations
max_steps: 20
readonly: true
---
You are a rigorous mathematician.
Cite every claim.
`)
	def, err := ParseAgentMarkdown(data, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "math-research" {
		t.Errorf("name = %q", def.Name)
	}
	if def.Description != "Verifies mathematical claims" {
		t.Errorf("description = %q", def.Description)
	}
	if def.Model != "reasoning-model" {
		t.Errorf("model = %q", def.Model)
	}
	if got := strings.Join(def.Tools, ","); got != "read,grep,websearch" {
		t.Errorf("tools = %q", got)
	}
	if got := strings.Join(def.ExcludeTools, ","); got != "write" {
		t.Errorf("exclude = %q", got)
	}
	if got := strings.Join(def.MCPServers, ","); got != "wolfram,arxiv" {
		t.Errorf("mcp = %q", got)
	}
	if def.Permissions["write"] != "deny" || def.Permissions["bash"] != "allow" {
		t.Errorf("permissions = %#v", def.Permissions)
	}
	if def.Permissions["mcp:wolfram"] != "allow" {
		t.Errorf("permission key normalization failed: %#v", def.Permissions)
	}
	if def.MaxSteps != 20 || !def.ReadOnly {
		t.Errorf("max_steps=%d readonly=%v", def.MaxSteps, def.ReadOnly)
	}
	if !strings.Contains(def.Prompt, "rigorous mathematician") || !strings.Contains(def.Prompt, "Cite every claim") {
		t.Errorf("prompt = %q", def.Prompt)
	}
}

func TestParseAgentMarkdownNameFromFilename(t *testing.T) {
	def, err := ParseAgentMarkdown([]byte("You are a designer.\n"), "deck-design")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "deck-design" {
		t.Fatalf("name = %q", def.Name)
	}
	if def.Prompt != "You are a designer." {
		t.Fatalf("prompt = %q", def.Prompt)
	}
}

func TestParseAgentMarkdownUnclosedFrontmatter(t *testing.T) {
	if _, err := ParseAgentMarkdown([]byte("---\nname: x\nbody"), "x"); err == nil {
		t.Fatal("expected an error for unclosed frontmatter")
	}
}

func TestParseAgentJSON(t *testing.T) {
	def, err := ParseAgentJSON([]byte(`{"description":"d","tools":["read"],"permissions":{"write":"deny"}}`), "code-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "code-reviewer" {
		t.Fatalf("name = %q", def.Name)
	}
}

func TestParseFrontmatterPlainMarkdown(t *testing.T) {
	meta, body, err := ParseFrontmatter([]byte("# Title\nhello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 0 {
		t.Fatalf("meta = %#v", meta)
	}
	if !strings.Contains(body, "hello") {
		t.Fatalf("body = %q", body)
	}
}
