// Package mode defines the working modes of a wombat session: chat, where
// the agent looks and explains, and create, where it also changes things.
package mode

import (
	"fmt"
	"strings"
)

// Mode is a session working mode.
type Mode string

const (
	// Chat is read-only: the agent explores, explains, and runs inspection
	// commands, but never changes anything.
	Chat Mode = "chat"
	// Create is the full agent: it can write files and run mutating commands.
	Create Mode = "create"
)

// All lists the modes in the order they are shown.
var All = []Mode{Chat, Create}

// Parse turns a string into a mode.
func Parse(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case Chat:
		return Chat, nil
	case Create:
		return Create, nil
	}
	return "", fmt.Errorf("unknown mode %q (chat, create)", s)
}

// Label is the short name.
func (m Mode) Label() string { return string(m) }

// Description explains the mode in one line.
func (m Mode) Description() string {
	switch m {
	case Chat:
		return "look, explain, and run inspection commands; never change files"
	case Create:
		return "read, write, edit, and run any command"
	}
	return string(m)
}

// AllowsWrite reports whether the mode may change files.
func (m Mode) AllowsWrite() bool { return m == Create }

// Note tells the model which mode it is in, so it does not try to change
// things it is not allowed to change.
func (m Mode) Note() string {
	switch m {
	case Chat:
		return "Mode: chat. You are exploring and explaining: do not create, edit, or delete files, " +
			"and do not run commands that change anything. If the task needs changes, say so and ask " +
			"the user to switch to create mode."
	case Create:
		return "Mode: create. You may change files and run any command the user approves."
	}
	return ""
}

// Status describes the current mode in one line.
func Status(m Mode) string {
	return fmt.Sprintf("mode: %s — %s", m.Label(), m.Description())
}

// List renders every mode and marks the current one.
func List(current Mode) string {
	var b strings.Builder
	b.WriteString("modes:\n")
	for _, m := range All {
		mark := " "
		if m == current {
			mark = "*"
		}
		fmt.Fprintf(&b, "  %s %-8s %s\n", mark, m.Label(), m.Description())
	}
	b.WriteString("\n  /mode chat     explore and explain, change nothing\n")
	b.WriteString("  /mode create   read, write, edit, and run any command\n")
	return b.String()
}

// Apply handles a "/mode ..." or "/modes" command and returns the new mode, a
// message for the user, and whether the mode changed.
func Apply(line string, current Mode) (Mode, string, bool, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return current, Status(current), false, nil
	}
	switch strings.ToLower(fields[0]) {
	case "/modes":
		return current, List(current), false, nil
	case "/mode":
		if len(fields) < 2 {
			return current, List(current), false, nil
		}
		m, err := Parse(fields[1])
		if err != nil {
			return current, "", false, err
		}
		if m == current {
			return current, Status(current), false, nil
		}
		return m, Status(m), true, nil
	}
	return current, "", false, fmt.Errorf("unknown command %q — /modes", fields[0])
}
