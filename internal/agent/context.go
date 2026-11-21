package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tokens"
)

const (
	// protectToolTokens caps how many recent tool-output tokens stay
	// verbatim; older ones become masking candidates.
	protectToolTokens = 200000
	// minProtectToolTokens keeps a floor for small windows.
	minProtectToolTokens = 4000
	minReclaim           = 20000
	keepVerbatim         = 60000
	// minKeepVerbatim keeps a floor for small windows.
	minKeepVerbatim = 8000
	compactReserve  = 20000
	compactSafety   = 13000
)

func (a *Agent) historyTokens() int {
	total := 0
	// Reasoning counts against the context window. When trim_reasoning is on,
	// only the newest assistant turn keeps it in the request; otherwise every
	// retained thinking turn counts.
	last := -1
	for i := len(a.Sess.Messages) - 1; i >= 0; i-- {
		if a.Sess.Messages[i].Role == "assistant" {
			last = i
			break
		}
	}
	for i, m := range a.Sess.Messages {
		reasoning := m.Reasoning
		if a.TrimReasoning && i != last {
			reasoning = ""
		}
		total += tokens.Estimate(m.Content) + tokens.Estimate(reasoning) + 6
		for _, at := range m.Attachments {
			total += attachmentTokens(at)
		}
		for _, tc := range m.ToolCalls {
			total += tokens.Estimate(tc.Name) + tokens.Estimate(tc.Arguments) + 4
		}
	}
	return int(float64(total)*a.tokenEstimateScale() + 0.5)
}

// protectToolTokensFor is the verbatim tool-output budget. It scales with
// the context window - a fifth of it - clamped to the legacy bounds, so
// masking becomes the primary compaction strategy on long runs instead of
// the LLM summarizer.
func (a *Agent) protectToolTokensFor() int {
	if a.ContextWindow <= 0 {
		return protectToolTokens
	}
	p := a.ContextWindow / 5
	if p < minProtectToolTokens {
		p = minProtectToolTokens
	}
	if p > protectToolTokens {
		p = protectToolTokens
	}
	return p
}

// keepVerbatimTokens is the recent-history budget the summarizer keeps
// word-for-word. It scales with the context window so small windows still
// summarize while large ones keep a useful recency window.
func (a *Agent) keepVerbatimTokens() int {
	if a.ContextWindow <= 0 {
		return keepVerbatim
	}
	v := a.ContextWindow / 3
	if v < minKeepVerbatim {
		v = minKeepVerbatim
	}
	if v > keepVerbatim {
		v = keepVerbatim
	}
	return v
}

func attachmentTokens(a llm.Attachment) int {
	switch a.Kind {
	case "image":
		return 1200
	case "video":
		return 4000
	case "audio":
		return 1000
	default:
		return 1500
	}
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
	// The arguments of every call are kept so a masked output can say how
	// to get the full text back.
	argsByID := map[string]string{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			argsByID[tc.ID] = tc.Arguments
		}
	}
	type candidate struct {
		idx   int
		tok   int
		rerun string
	}
	var cands []candidate
	toolTok := 0
	reclaim := 0
	protect := a.protectToolTokensFor()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "tool" {
			continue
		}
		t := tokens.Estimate(m.Content)
		if i >= protectFrom || toolTok < protect {
			toolTok += t
			continue
		}
		// Failure output is the feedback the model needs to adjust; masking
		// it would erase the reason the last attempt went wrong.
		if strings.HasPrefix(m.Content, "error:") {
			continue
		}
		if len(m.Content) > 200 {
			rerun := m.Name
			if args := strings.TrimSpace(argsByID[m.ToolCallID]); args != "" {
				rerun += " " + clip(args, 160)
			}
			cands = append(cands, candidate{idx: i, tok: t, rerun: rerun})
			reclaim += t - 48
		}
	}
	if reclaim < minReclaim {
		return 0
	}
	for _, c := range cands {
		msgs[c.idx].Content = fmt.Sprintf(
			"[elided tool output: ~%d tokens removed — re-run %s to see it again]", c.tok, c.rerun)
	}
	_ = a.Sess.Flush()
	return reclaim
}

func (a *Agent) summarize(ctx context.Context) error {
	msgs := a.Sess.Messages
	split := len(msgs)
	acc := 0
	keep := a.keepVerbatimTokens()
	for i := len(msgs) - 1; i >= 0; i-- {
		acc += tokens.Estimate(msgs[i].Content) + 8
		if acc >= keep {
			split = i
			break
		}
	}
	// If the whole history fits in the verbatim budget there is nothing
	// older to summarize. This also guards the index below: split can be
	// len(msgs) after the loop above.
	if split >= len(msgs) {
		return nil
	}
	for split > 0 && msgs[split].Role == "tool" {
		split--
	}
	for split < len(msgs) && msgs[split].Role != "user" {
		split++
	}
	if split <= 0 || split >= len(msgs) {
		return nil
	}
	transcript := renderTranscript(msgs[:split])
	prompt := "Summarize this coding session so a fresh context can continue seamlessly. " +
		"Be dense and factual. Use exactly these headings, skipping empty ones:\n" +
		"## Goal\n## User instructions and preferences\n## Codebase discoveries (paths, symbols)\n" +
		"## Decisions and why\n## Work completed (files touched)\n" +
		"## Failures (quote the exact failing command and error)\n## Remaining todos\n" +
		"Output only the summary.\n\n--- transcript ---\n" + transcript
	model := a.Model
	if a.CompactModel != "" {
		model = a.CompactModel
	}
	res, err := a.Provider.Chat(ctx, llm.Request{
		Model:     model,
		MaxTokens: 2048,
		Messages:  []llm.Message{{Role: "user", Content: prompt}},
		SessionID: a.Sess.ID(),
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
			b.WriteString("user: " + clip(m.Content, 1200))
			for _, at := range m.Attachments {
				b.WriteString(" [attached " + at.Kind + ": " + at.Name + "]")
			}
			b.WriteString("\n")
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
