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
	{Name: "/mode", Args: "[chat|create]", Summary: "choose chat (explain only) or create (read, write, run)", Options: []Option{
		{Value: "chat", Summary: "look and explain, change nothing"},
		{Value: "create", Summary: "read, write, edit, and run any command"},
	}},
	{Name: "/modes", Summary: "list the working modes"},
	{Name: "/undo", Args: "[1|2|3|5|10]", Summary: "undo the last N file changes (conversation stays)", Options: []Option{
		{Value: "1", Summary: "undo the last change"},
		{Value: "2", Summary: "undo the last 2 changes"},
		{Value: "3", Summary: "undo the last 3 changes"},
		{Value: "5", Summary: "undo the last 5 changes"},
		{Value: "10", Summary: "undo the last 10 changes"},
	}},
	{Name: "/prune", Args: "keep N", Summary: "delete old checkpoints, keep the newest N", Options: []Option{
		{Value: "keep 1", Summary: "keep one checkpoint, delete older undo points"},
		{Value: "keep 5", Summary: "keep the newest 5 checkpoints"},
		{Value: "keep 10", Summary: "keep the newest 10 checkpoints"},
		{Value: "keep 20", Summary: "keep the newest 20 checkpoints"},
		{Value: "keep 0", Summary: "delete all checkpoints (code and history stay)"},
	}},
	{Name: "/transcript", Summary: "open the full chat transcript in a pager"},
	{Name: "/thinking", Args: "[on|off]", Summary: "show or hide the model's thinking", Options: []Option{
		{Value: "on", Summary: "show the model's thinking as it arrives"},
		{Value: "off", Summary: "hide it and show a one-line summary instead"},
	}},
	{Name: "/toolset", Summary: "list tools and how each one is gated"},
	{Name: "/sources", Summary: "list sites and files used this session"},
	{Name: "/history", Summary: "show this session's messages"},
	{Name: "/sessions", Summary: "browse and resume past sessions"},
	{Name: "/status", Summary: "show provider, model, mode, and settings"},
	{Name: "/cost", Summary: "show token usage, context, and cost"},
	{Name: "/model", Args: "[name]", Summary: "switch the model"},
	{Name: "/provider", Args: "[name]", Summary: "switch the provider"},
	{Name: "/models", Summary: "browse available models"},
	{Name: "/attach", Args: "<path>", Summary: "attach an image, video, audio, or file"},
	{Name: "/paste", Summary: "attach an image from the clipboard"},
	{Name: "/detach", Args: "[n|all]", Summary: "remove pending attachments", Options: []Option{
		{Value: "all", Summary: "remove every pending attachment"},
	}},
	{Name: "/mouse", Summary: "toggle wheel scrolling vs native text selection"},
	{Name: "/new", Summary: "start a fresh session"},
	{Name: "/compact", Summary: "summarize the context now"},
	{Name: "/yolo", Summary: "toggle auto-approve for tool permissions"},
	{Name: "/clear", Summary: "clear the transcript view"},
	{Name: "/setup", Summary: "reconfigure providers and API keys"},
	{Name: "/help", Summary: "show all commands"},
	{Name: "/exit", Summary: "quit wombat"},
}

// Help renders the command list for the help output.
func Help() string {
	var b strings.Builder
	b.WriteString("commands:\n")
	for _, c := range List {
		b.WriteString(fmt.Sprintf("  %-34s %s\n", c.Usage(), c.Summary))
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
