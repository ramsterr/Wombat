package agents

import (
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/tools"
)

// WorkerPermit builds the permission callback for a custom agent.
//
// Workers never prompt the user, so the callback decides silently:
//   - an explicit allow or deny in the definition wins;
//   - credentials reads and dangerous commands are refused unless the
//     definition explicitly allows them (matching the safety of the default
//     worker environment);
//   - everything else is allowed, which is what a delegated worker does
//     today.
//
// An empty rules map returns nil so the tool layer keeps its exact default
// worker behavior.
func WorkerPermit(rules map[string]string, label string) func(kind, detail string) tools.Decision {
	if len(rules) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(rules))
	for kind, value := range rules {
		normalized[strings.ToLower(strings.TrimSpace(kind))] = strings.ToLower(strings.TrimSpace(value))
	}
	deny := func(kind, detail string) tools.Decision {
		who := strings.TrimSpace(label)
		if who == "" {
			who = "this agent"
		}
		return tools.Decision{Feedback: fmt.Sprintf("%s is denied for custom agent %s", kind, who)}
	}
	return func(kind, detail string) tools.Decision {
		if value, ok := tools.PermissionValue(normalized, kind); ok {
			if value == "deny" {
				return deny(kind, detail)
			}
			return tools.Decision{Allow: true}
		}
		switch kind {
		case "sensitive-read", "dangerous":
			return deny(kind, detail)
		}
		return tools.Decision{Allow: true}
	}
}
