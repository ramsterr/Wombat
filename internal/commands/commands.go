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
	{Name: "/mode", Args: "[chat|create]", Summary: "switch the working mode", Options: []Option{
		{Value: "chat", Summary: "look and explain, change nothing"},
		{Value: "create", Summary: "read, write, edit, and run any command"},
	}},
	{Name: "/modes", Summary: "list the working modes"},
	{Name: "/thinking", Args: "[on|off]", Summary: "show or hide the model's thinking", Options: []Option{
		{Value: "on", Summary: "show the model's thinking as it arrives"},
		{Value: "off", Summary: "hide it and show a one-line summary instead"},
	}},
	{Name: "/toolset", Summary: "the tools this session can use"},
	{Name: "/sources", Summary: "sites and files used in this session"},
	{Name: "/history", Summary: "the messages in this session"},
	{Name: "/sessions", Summary: "browse past sessions"},
	{Name: "/status", Summary: "show the current settings"},
	{Name: "/cost", Summary: "token usage and cost"},
	{Name: "/model", Args: "[name]", Summary: "switch model"},
	{Name: "/provider", Args: "[name]", Summary: "switch provider"},
	{Name: "/models", Summary: "browse models"},
	{Name: "/attach", Args: "<path>", Summary: "attach an image, video, audio, or file"},
	{Name: "/paste", Summary: "attach an image from the clipboard"},
	{Name: "/detach", Args: "[n|all]", Summary: "remove pending attachments", Options: []Option{
		{Value: "all", Summary: "remove every pending attachment"},
	}},
	{Name: "/mouse", Summary: "toggle mouse: wheel scroll or text selection"},
	{Name: "/new", Summary: "start a fresh session"},
	{Name: "/compact", Summary: "compact the context now"},
	{Name: "/yolo", Summary: "toggle auto-approve"},
	{Name: "/clear", Summary: "clear the transcript view"},
	{Name: "/setup", Summary: "reconfigure providers"},
	{Name: "/help", Summary: "show this list"},
	{Name: "/exit", Summary: "quit"},
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
