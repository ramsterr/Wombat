package commands

import (
	"fmt"
	"strings"
)

// ThinkingCommand handles "/thinking [on|off]" and returns the new state, a
// message for the user, and whether it changed.
func ThinkingCommand(line string, current bool) (bool, string, bool, error) {
	args := strings.Fields(strings.TrimSpace(line))
	if len(args) == 0 {
		state := "off"
		if current {
			state = "on"
		}
		return current, "thinking: " + state + " (use /thinking on|off)", false, nil
	}
	switch strings.ToLower(args[0]) {
	case "on", "true", "yes", "1", "show":
		if current {
			return current, "thinking: on", false, nil
		}
		return true, "thinking: on", true, nil
	case "off", "false", "no", "0", "hide":
		if !current {
			return current, "thinking: off", false, nil
		}
		return false, "thinking: off", true, nil
	}
	return current, "", false, fmt.Errorf("usage: /thinking on|off")
}
