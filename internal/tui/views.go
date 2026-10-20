package tui

import (
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/trace"
)

// toolsetView lists the tools this session can use and how each one is
// gated.
func (c *chat) toolsetView() string {
	if c.ag == nil {
		return "no tools yet"
	}
	var b strings.Builder
	b.WriteString("tools:\n")
	for _, t := range c.ag.Tools {
		gate := "allowed"
		switch t.Class() {
		case "bash", "write", "edit":
			gate = "asks"
		case "read":
			gate = "always"
		}
		if c.mode == "chat" && (t.Class() == "write" || t.Class() == "edit") {
			gate = "off in chat mode"
		}
		fmt.Fprintf(&b, "  %-14s %-9s %s\n", t.Name(), t.Class(), gate)
	}
	if c.mode == "chat" {
		b.WriteString("\n  chat mode: writing and mutating commands are off — /mode create to enable them")
	}
	return b.String()
}

// sourcesView lists the sites and files the session used.
func (c *chat) sourcesView() string {
	if c.sess == nil {
		return "no session yet"
	}
	path := strings.TrimSuffix(c.sess.Path(), ".jsonl") + ".events.jsonl"
	urls, files, err := trace.Sources(path)
	if err != nil {
		return "no sources recorded yet"
	}
	if len(urls) == 0 && len(files) == 0 {
		return "no sources recorded yet"
	}
	var b strings.Builder
	if len(urls) > 0 {
		b.WriteString("sites:\n")
		for _, u := range urls {
			fmt.Fprintf(&b, "  %-52s %d\n", clip(u.Value, 52), u.Calls)
		}
	}
	if len(files) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("files:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "  %-52s %d\n", clip(f.Value, 52), f.Calls)
		}
	}
	return b.String()
}

// historyView lists the messages in this session.
func (c *chat) historyView() string {
	if c.sess == nil || len(c.sess.Messages) == 0 {
		return "this session is empty"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "history (%d messages):\n", len(c.sess.Messages))
	shown := 0
	for i, m := range c.sess.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		shown++
		fmt.Fprintf(&b, "  %3d %-9s %s\n", i+1, m.Role, clip(strings.ReplaceAll(content, "\n", " "), 70))
	}
	if shown == 0 {
		return "this session has no messages yet"
	}
	return b.String()
}
