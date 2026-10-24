package tui

import (
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/tools"
	"github.com/ramsterr/Wombat/internal/trace"
)

// toolsetView lists the tools this session can use and how each one is
// gated. It is rendered with the transcript's monospace spacing intact.
func (c *chat) toolsetView() string {
	if c.ag == nil {
		return "no tools yet"
	}
	var b strings.Builder
	b.WriteString(styleSection.Render("TOOLS") + styleFaint.Render(fmt.Sprintf("  %d available", len(c.ag.Tools))) + "\n")
	for _, t := range c.ag.Tools {
		gate := "allowed"
		gateStyle := styleGood
		switch t.Class() {
		case "bash", "write", "edit", "network", "mcp":
			gate = c.cfg.Permissions[t.Class()]
			if gate == "" {
				if t.Class() == "mcp" {
					gate = "ask"
				} else {
					gate = "allow"
				}
			}
		case "read":
			gate = "always"
		}
		switch gate {
		case "deny":
			gateStyle = styleBad
		case "ask", "allow for the session":
			gateStyle = styleWarn
		}
		if c.mode == "chat" && (t.Class() == "write" || t.Class() == "edit") {
			gate = "off in chat"
			gateStyle = styleFaint
		}
		glyph, gs := toolGlyph(t.Name())
		fmt.Fprintf(&b, "  %s %-16s %-9s %s\n",
			gs.Render(glyph), styleToolName.Render(t.Name()), styleDim.Render(t.Class()), gateStyle.Render(gate))
	}
	if c.mode == "chat" {
		b.WriteString("\n" + styleWarn.Render("chat mode") + styleDim.Render(": writing and mutating commands are off — /mode create to enable them"))
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
	if err != nil || (len(urls) == 0 && len(files) == 0) {
		return "no sources recorded yet"
	}
	var b strings.Builder
	if len(urls) > 0 {
		b.WriteString(styleSection.Render("SITES") + styleFaint.Render(fmt.Sprintf("  %d", len(urls))) + "\n")
		for _, u := range urls {
			fmt.Fprintf(&b, "  %s %s %s\n",
				styleLink.Render("⌁"), clip(u.Value, 58), styleFaint.Render(fmt.Sprintf("%d×", u.Calls)))
		}
	}
	if len(files) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(styleSection.Render("FILES") + styleFaint.Render(fmt.Sprintf("  %d", len(files))) + "\n")
		for _, f := range files {
			fmt.Fprintf(&b, "  %s %s %s\n",
				styleAccent2.Render("◫"), clip(f.Value, 58), styleFaint.Render(fmt.Sprintf("%d×", f.Calls)))
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
	b.WriteString(styleSection.Render("HISTORY") + styleFaint.Render(fmt.Sprintf("  %d messages", len(c.sess.Messages))) + "\n")
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
		role := styleUserRail.Render("▌ you")
		if m.Role == "assistant" {
			role = styleBrand.Render("◆ wombat")
		}
		fmt.Fprintf(&b, " %s %s %s\n", styleFaint.Render(fmt.Sprintf("%3d", i+1)), role,
			clip(oneline(content), 76))
	}
	if shown == 0 {
		return "this session has no messages yet"
	}
	return b.String()
}

// mcpView reports the MCP servers attached to this session and their tools.
func (c *chat) mcpView() string {
	if c.ag == nil || c.ag.Env == nil {
		return "no agent yet"
	}
	manager := c.ag.Env.MCPManager()
	if manager == nil || len(manager.Servers()) == 0 {
		return "no MCP servers configured\n" + styleDim.Render("add one with: wombat mcp add <name> -- <command>")
	}
	var b strings.Builder
	b.WriteString(styleSection.Render("MCP") + "\n")
	for _, server := range manager.Servers() {
		if err := server.Err(); err != nil {
			fmt.Fprintf(&b, "  %s %-16s %s\n", styleBad.Render("✗"), styleToolName.Render(server.Name()), styleBad.Render(tools.RedactSecrets(err.Error())))
			continue
		}
		usage := fmt.Sprintf("%d tools", len(server.Tools()))
		if calls := server.Calls(); calls > 0 {
			usage += fmt.Sprintf(" · %d calls", calls)
			if failures := server.Failures(); failures > 0 {
				usage += fmt.Sprintf(", %d failed", failures)
			}
		}
		fmt.Fprintf(&b, "  %s %-16s %s\n", styleGood.Render("✓"), styleToolName.Render(server.Name()), styleDim.Render(usage))
		for _, tool := range server.Tools() {
			fmt.Fprintf(&b, "      %s\n", styleDim.Render(tool.Name))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
