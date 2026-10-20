package cli

import (
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/commands"
	"github.com/ramsterr/Wombat/internal/mode"
	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/trace"
)

// helpText lists every command.
func helpText() string {
	return commands.Help() + "\ntip: write @path/to/file (or @\"path with spaces\") to attach an image,\n" +
		"     video, audio, or document to that message.\n"
}

// toolsetText lists the tools the session can use.
func toolsetText(ag *agent.Agent, current mode.Mode, perms map[string]string) string {
	if ag == nil {
		return "no tools yet"
	}
	var b strings.Builder
	b.WriteString("tools:\n")
	for _, t := range ag.Tools {
		gate := "allowed"
		switch t.Class() {
		case "bash", "write", "edit", "network":
			gate = perms[t.Class()]
			if gate == "" {
				gate = "allow"
			}
		case "read":
			gate = "always"
		}
		if !current.AllowsWrite() && (t.Class() == "write" || t.Class() == "edit") {
			gate = "off in chat mode"
		}
		fmt.Fprintf(&b, "  %-14s %-9s %s\n", t.Name(), t.Class(), gate)
	}
	if !current.AllowsWrite() {
		b.WriteString("\n  chat mode: writing and mutating commands are off — /mode create to enable them")
	}
	return b.String()
}

// sourcesText lists the sites and files the session used.
func sourcesText(sess *session.Session) string {
	if sess == nil {
		return "no session yet"
	}
	path := strings.TrimSuffix(sess.Path(), ".jsonl") + ".events.jsonl"
	urls, files, err := trace.Sources(path)
	if err != nil || (len(urls) == 0 && len(files) == 0) {
		return "no sources recorded yet"
	}
	var b strings.Builder
	if len(urls) > 0 {
		b.WriteString("sites:\n")
		for _, u := range urls {
			fmt.Fprintf(&b, "  %-52s %d\n", clipLine(u.Value, 52), u.Calls)
		}
	}
	if len(files) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("files:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "  %-52s %d\n", clipLine(f.Value, 52), f.Calls)
		}
	}
	return b.String()
}

// historyText lists the messages in the session.
func historyText(sess *session.Session) string {
	if sess == nil || len(sess.Messages) == 0 {
		return "this session is empty"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "history (%d messages):\n", len(sess.Messages))
	shown := 0
	for i, m := range sess.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		shown++
		fmt.Fprintf(&b, "  %3d %-9s %s\n", i+1, m.Role, clipLine(strings.ReplaceAll(content, "\n", " "), 70))
	}
	if shown == 0 {
		return "this session has no messages yet"
	}
	return b.String()
}
