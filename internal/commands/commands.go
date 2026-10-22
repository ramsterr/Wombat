// Package commands lists the slash commands a session understands, so the
// TUI palette, the REPL help, and the documentation stay in one place.
package commands

import (
	"fmt"
	"strings"
)

// Option is one choice a command offers.
type Option struct {
	// Value is what gets appended to the command.
	Value string
	// Summary explains the choice.
	Summary string
}

// Command is one slash command.
type Command struct {
	// Name is the command, including the leading slash.
	Name string
	// Args describes the optional argument.
	Args string
	// Summary is the one-line description.
	Summary string
	// Group is the section the command is shown under in the palette.
	Group string
	// Options are the choices the command offers when it is run.
	Options []Option
}

// Usage renders the command with its argument.
func (c Command) Usage() string {
	if c.Args == "" {
		return c.Name
	}
	return c.Name + " " + c.Args
}

// List is every command, in the order they are shown.
var List = []Command{
	{Name: "/mode", Group: "Session", Args: "[chat|create]", Summary: "switch the working mode", Options: []Option{
		{Value: "chat", Summary: "look and explain, change nothing"},
		{Value: "create", Summary: "read, write, edit, and run any command"},
	}},
	{Name: "/modes", Group: "Session", Summary: "list the working modes"},
	{Name: "/thinking", Group: "Session", Args: "[on|off]", Summary: "show or hide the model's thinking", Options: []Option{
		{Value: "on", Summary: "show the model's thinking as it arrives"},
		{Value: "off", Summary: "hide it and show a one-line summary instead"},
	}},
	{Name: "/toolset", Group: "Tools", Summary: "the tools this session can use"},
	{Name: "/sources", Group: "Context", Summary: "browse the sites and files used"},
	{Name: "/reference", Group: "Context", Args: "[add|rm|clear] <path|url>", Summary: "keep project references in the inspector"},
	{Name: "/usage", Group: "Context", Summary: "activity charts across sessions"},
	{Name: "/history", Group: "Context", Summary: "the messages in this session"},
	{Name: "/sessions", Group: "Session", Summary: "browse past sessions"},
	{Name: "/status", Group: "Context", Summary: "show the current settings"},
	{Name: "/preview", Group: "Tools", Summary: "open the local preview the agent reported"},
	{Name: "/cost", Group: "Context", Summary: "token usage and cost"},
	{Name: "/model", Group: "Models", Args: "[name]", Summary: "switch model"},
	{Name: "/provider", Group: "Models", Args: "[name]", Summary: "switch provider"},
	{Name: "/models", Group: "Models", Summary: "browse models"},
	{Name: "/attach", Group: "Files", Args: "<path>", Summary: "attach an image, video, audio, or file"},
	{Name: "/paste", Group: "Files", Summary: "attach an image from the clipboard"},
	{Name: "/detach", Group: "Files", Args: "[n|all]", Summary: "remove pending attachments", Options: []Option{
		{Value: "all", Summary: "remove every pending attachment"},
	}},
	{Name: "/mouse", Group: "Appearance", Summary: "toggle mouse: wheel scroll or text selection"},
	{Name: "/panel", Summary: "toggle the inspector panel", Group: "Appearance"},
	{Name: "/keys", Summary: "keyboard shortcuts and commands", Group: "Help"},
	{Name: "/theme", Group: "Appearance", Args: "[name]", Summary: "switch the color theme", Options: []Option{
		{Value: "wombat", Summary: "warm amber on charcoal"},
		{Value: "tokyo", Summary: "Tokyo Night blue"},
		{Value: "catppuccin", Summary: "soft pastel mauve"},
		{Value: "gruvbox", Summary: "retro warm earth tones"},
		{Value: "nord", Summary: "cool arctic blue"},
		{Value: "rosepine", Summary: "muted rose and iris"},
		{Value: "dracula", Summary: "purple night, vivid accents"},
	}},
	{Name: "/new", Group: "Session", Summary: "start a fresh session"},
	{Name: "/compact", Group: "Context", Summary: "compact the context now"},
	{Name: "/yolo", Group: "Safety", Summary: "toggle auto-approve"},
	{Name: "/clear", Group: "Session", Summary: "clear the transcript view"},
	{Name: "/doctor", Group: "System", Summary: "diagnose config and connectivity"},
	{Name: "/setup", Group: "System", Summary: "reconfigure providers"},
	{Name: "/help", Group: "Help", Summary: "show this list"},
	{Name: "/exit", Group: "System", Summary: "quit"},
}

// Help renders the command list for the help output, grouped by area.
func Help() string {
	order := []string{"Session", "Models", "Context", "Tools", "Files", "Appearance", "Safety", "Help", "System"}
	groups := map[string][]Command{}
	for _, c := range List {
		g := c.Group
		if g == "" {
			g = "System"
		}
		groups[g] = append(groups[g], c)
	}
	var b strings.Builder
	b.WriteString("commands:\n")
	for _, g := range order {
		list := groups[g]
		if len(list) == 0 {
			continue
		}
		b.WriteString("\n  " + g + "\n")
		for _, c := range list {
			b.WriteString(fmt.Sprintf("    %-32s %s\n", c.Usage(), c.Summary))
		}
	}
	return b.String()
}

// Match returns the commands that start with the given prefix, keeping the
// order of the list.
func Match(prefix string) []Command {
	if prefix == "" {
		return List
	}
	var out []Command
	for _, c := range List {
		if len(c.Name) >= len(prefix) && c.Name[:len(prefix)] == prefix {
			out = append(out, c)
		}
	}
	return out
}

// Find returns the command with the given name.
func Find(name string) (Command, bool) {
	for _, c := range List {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}
