package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tokens"
)

const (
	protectToolTokens = 40000
	minReclaim        = 15000
	keepVerbatim      = 15000
	compactReserve    = 20000
	compactSafety     = 13000
)

func (a *Agent) historyTokens() int {
	total := 0
	for _, m := range a.Sess.Messages {
		total += tokens.Estimate(m.Content) + 6
		for _, tc := range m.ToolCalls {
			total += tokens.Estimate(tc.Name) + tokens.Estimate(tc.Arguments) + 4
		}
	}
	return total
}

func (a *Agent) maybeCompact(ctx context.Context) {
	trigger := a.ContextWindow - compactReserve - compactSafety
	if trigger < 1000 {
		trigger = a.ContextWindow / 2
	}
	before := a.historyTokens()
	if before <= trigger {
		return
	}
	reclaimed := a.pruneToolOutputs()
	if reclaimed > 0 && a.Hooks.OnCompact != nil {
		a.Hooks.OnCompact("prune", before, a.historyTokens())
	}
	if a.historyTokens() <= trigger {
		return
	}
	mid := a.historyTokens()
	if err := a.summarize(ctx); err != nil {
		return
	}
	if a.Hooks.OnCompact != nil {
		a.Hooks.OnCompact("summarize", mid, a.historyTokens())
	}
}

func (a *Agent) CompactNow(ctx context.Context) error {
	before := a.historyTokens()
	a.pruneToolOutputs()
	if a.historyTokens() < before && a.Hooks.OnCompact != nil {
		a.Hooks.OnCompact("prune", before, a.historyTokens())
	}
	mid := a.historyTokens()
	if err := a.summarize(ctx); err != nil {
		if mid < before {
			return nil
		}
		return err
	}
	if a.Hooks.OnCompact != nil {
		a.Hooks.OnCompact("summarize", mid, a.historyTokens())
	}
	return nil
}

func (a *Agent) pruneToolOutputs() int {
	msgs := a.Sess.Messages
	protectFrom := 0
	userSeen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			userSeen++
			if userSeen >= 2 {
				protectFrom = i
				break
			}
		}
	}
	type candidate struct {
		idx int
		tok int
	}
	var cands []candidate
	toolTok := 0
	reclaim := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "tool" {
			continue
		}
		t := tokens.Estimate(m.Content)
		if i >= protectFrom || toolTok < protectToolTokens {
			toolTok += t
			continue
		}
		if len(m.Content) > 200 {
			cands = append(cands, candidate{idx: i, tok: t})
			reclaim += t - 8
		}
	}
	if reclaim < minReclaim {
		return 0
	}
	for _, c := range cands {
		msgs[c.idx].Content = fmt.Sprintf("[elided tool output: ~%d tokens removed]", c.tok)
	}
	_ = a.Sess.Flush()
	return reclaim
}

func (a *Agent) summarize(ctx context.Context) error {
	msgs := a.Sess.Messages
	split := len(msgs)
	acc := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		acc += tokens.Estimate(msgs[i].Content) + 8
		if acc >= keepVerbatim {
			split = i
			break
		}
	}
	for split > 0 && msgs[split].Role == "tool" {
		split--
	}
	for split < len(msgs) && msgs[split].Role != "user" {
		split++
	}
	if split <= 0 || split >= len(msgs) {
		return fmt.Errorf("nothing to summarize")
	}
	transcript := renderTranscript(msgs[:split])
	prompt := "Summarize this coding session so a fresh context can continue seamlessly. Include: the goal; explicit instructions and preferences from the user; key discoveries about the codebase; decisions made and why; work completed; files touched; remaining todos. Be dense and factual. Output only the summary.\n\n--- transcript ---\n" + transcript
	res, err := a.Provider.Chat(ctx, llm.Request{
		Model:     a.Model,
		MaxTokens: 2048,
		Messages:  []llm.Message{{Role: "user", Content: prompt}},
	}, nil)
	if err != nil {
		return err
	}
	summary := strings.TrimSpace(res.Content)
	if summary == "" {
		return fmt.Errorf("empty summary")
	}
	summaryMsg := llm.Message{
		Role:    "user",
		Content: "[Conversation summary]\n" + summary + "\n[End of summary — continue from here.]",
	}
	newMsgs := append([]llm.Message{summaryMsg}, msgs[split:]...)
	return a.Sess.ReplaceMessages(newMsgs)
}

func renderTranscript(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			b.WriteString("user: " + clip(m.Content, 1200) + "\n")
		case "assistant":
			if m.Content != "" {
				b.WriteString("assistant: " + clip(m.Content, 1200) + "\n")
			}
			for _, tc := range m.ToolCalls {
				b.WriteString("assistant called " + tc.Name + " with " + clip(tc.Arguments, 300) + "\n")
			}
		case "tool":
			b.WriteString("result(" + m.Name + "): " + clip(m.Content, 400) + "\n")
		}
	}
	return b.String()
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
