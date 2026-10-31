package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tokens"
)

// PromptStats is an approximate token breakdown for one model request. It is
// emitted as a trace event so a benchmark can separate base context from tool
// results without changing the request itself.
type PromptStats struct {
	SystemTokens             int `json:"system_tokens"`
	BaseSystemTokens         int `json:"base_system_tokens"`
	ModeNoteTokens           int `json:"mode_note_tokens"`
	TmpNoteTokens            int `json:"tmp_note_tokens"`
	ExtraTokens              int `json:"extra_tokens"`
	SkillTokens              int `json:"skill_tokens"`
	ToolSchemaTokens         int `json:"tool_schema_tokens"`
	MCPToolSchemaTokens      int `json:"mcp_tool_schema_tokens"`
	ToolCount                int `json:"tool_count"`
	MCPToolCount             int `json:"mcp_tool_count"`
	HistoryTokens            int `json:"history_tokens"`
	UserTokens               int `json:"user_tokens"`
	TaskTokens               int `json:"task_tokens"`
	AssistantTokens          int `json:"assistant_tokens"`
	ToolResultTokens         int `json:"tool_result_tokens"`
	ReasoningTokens          int `json:"reasoning_tokens"`
	ToolCallTokens           int `json:"tool_call_tokens"`
	ProjectInstructionTokens int `json:"project_instruction_tokens"`
}

func (s PromptStats) Fields(step int) map[string]any {
	return map[string]any{
		"step":                       step,
		"system_tokens":              s.SystemTokens,
		"base_system_tokens":         s.BaseSystemTokens,
		"mode_note_tokens":           s.ModeNoteTokens,
		"tmp_note_tokens":            s.TmpNoteTokens,
		"extra_tokens":               s.ExtraTokens,
		"skill_tokens":               s.SkillTokens,
		"tool_schema_tokens":         s.ToolSchemaTokens,
		"mcp_tool_schema_tokens":     s.MCPToolSchemaTokens,
		"tool_count":                 s.ToolCount,
		"mcp_tool_count":             s.MCPToolCount,
		"history_tokens":             s.HistoryTokens,
		"user_tokens":                s.UserTokens,
		"task_tokens":                s.TaskTokens,
		"assistant_tokens":           s.AssistantTokens,
		"tool_result_tokens":         s.ToolResultTokens,
		"reasoning_tokens":           s.ReasoningTokens,
		"tool_call_tokens":           s.ToolCallTokens,
		"project_instruction_tokens": s.ProjectInstructionTokens,
	}
}

func (a *Agent) promptStats(system string, defs []llm.Tool, baseSystem, modeNote, tmpNote, extra string) PromptStats {
	toolTokens, mcpToolTokens, toolCount, mcpToolCount := toolSchemaStats(defs)
	userTokens, taskTokens, assistantTokens, toolResultTokens, reasoningTokens, toolCallTokens := a.historyBreakdown()
	cwd := ""
	if a.Env != nil {
		cwd = a.Env.Dir
	}
	return PromptStats{
		SystemTokens:             tokens.Estimate(system),
		BaseSystemTokens:         tokens.Estimate(baseSystem),
		ModeNoteTokens:           tokens.Estimate(modeNote),
		TmpNoteTokens:            tokens.Estimate(tmpNote),
		ExtraTokens:              tokens.Estimate(extra),
		SkillTokens:              skillSectionTokens(extra),
		ToolSchemaTokens:         toolTokens,
		MCPToolSchemaTokens:      mcpToolTokens,
		ToolCount:                toolCount,
		MCPToolCount:             mcpToolCount,
		HistoryTokens:            a.historyTokens(),
		UserTokens:               userTokens,
		TaskTokens:               taskTokens,
		AssistantTokens:          assistantTokens,
		ToolResultTokens:         toolResultTokens,
		ReasoningTokens:          reasoningTokens,
		ToolCallTokens:           toolCallTokens,
		ProjectInstructionTokens: projectInstructionTokens(cwd),
	}
}

func toolSchemaStats(defs []llm.Tool) (total, mcp, count, mcpCount int) {
	for _, def := range defs {
		raw, err := json.Marshal(def)
		if err != nil {
			continue
		}
		n := tokens.Estimate(string(raw))
		total += n
		count++
		if strings.HasPrefix(def.Name, "mcp__") || strings.HasPrefix(def.Name, "mcp_") {
			mcp += n
			mcpCount++
		}
	}
	return total, mcp, count, mcpCount
}

func (a *Agent) historyBreakdown() (user, task, assistant, toolResult, reasoning, toolCall int) {
	for _, m := range a.Sess.Messages {
		switch m.Role {
		case "user":
			n := tokens.Estimate(m.Content)
			user += n
			if !strings.HasPrefix(m.Content, "Today:") {
				task += n
			}
		case "assistant":
			assistant += tokens.Estimate(m.Content)
		case "tool":
			toolResult += tokens.Estimate(m.Content)
		}
		reasoning += tokens.Estimate(m.Reasoning)
		for _, tc := range m.ToolCalls {
			toolCall += tokens.Estimate(tc.Name) + tokens.Estimate(tc.Arguments) + 4
		}
	}
	return user, task, assistant, toolResult, reasoning, toolCall
}

func skillSectionTokens(extra string) int {
	const marker = "### Skills"
	i := strings.Index(extra, marker)
	if i < 0 {
		return 0
	}
	section := extra[i:]
	tail := section[len(marker):]
	if j := strings.Index(tail, "\n## "); j >= 0 {
		section = section[:len(marker)+j]
	}
	return tokens.Estimate(section)
}

func projectInstructionTokens(cwd string) int {
	if strings.TrimSpace(cwd) == "" {
		return 0
	}
	total := 0
	for _, name := range []string{"AGENTS.md", "WOMBAT.md"} {
		data, err := os.ReadFile(filepath.Join(cwd, name))
		if err != nil || len(data) == 0 {
			continue
		}
		if len(data) > 16*1024 {
			data = data[:16*1024]
		}
		total += tokens.Estimate(string(data))
	}
	return total
}
