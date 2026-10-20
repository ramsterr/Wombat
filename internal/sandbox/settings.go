package sandbox

import (
	"fmt"
	"strings"
)

// Settings is the sandbox configuration for one session. The user can change
// it while a session runs with the /sandbox command.
type Settings struct {
	// Mode is the requested sandbox mode: off, read-only, workspace-write,
	// or danger-full-access. An empty mode means off.
	Mode Mode
	// Network allows commands to reach the network.
	Network bool
}

// Policy turns the settings into a sandbox policy for a workspace. Modes the
// host cannot enforce fall back to off so commands still run.
func (s Settings) Policy(workspace string) Policy {
	mode := s.Mode
	if mode == "" {
		mode = Off
	}
	return Policy{
		Mode:         Resolve(mode),
		Workspace:    workspace,
		AllowNetwork: s.Network,
	}
}

// Enforced reports whether the requested mode can actually be enforced here.
func (s Settings) Enforced() bool {
	mode := s.Mode
	if mode == "" {
		mode = Off
	}
	return Supported(mode)
}

// Summary describes the settings in one line.
func (s Settings) Summary() string {
	mode := string(s.Mode)
	if mode == "" {
		mode = string(Off)
	}
	net := "network off"
	if s.Network {
		net = "network on"
	}
	return fmt.Sprintf("%s · %s", mode, net)
}

// Note tells the model what the sandbox allows, so it asks the user instead
// of failing with an opaque network error.
func (s Settings) Note() string {
	var b strings.Builder
	b.WriteString("Sandbox: " + s.Summary() + ".")
	if !s.Enforced() && s.Mode != "" && s.Mode != Off {
		b.WriteString(" This host cannot enforce that mode, so commands run without a sandbox.")
	}
	if !s.Network {
		b.WriteString(" The network is unavailable: commands that need it will fail.")
		b.WriteString(" If a task genuinely needs the network, say so and ask the user to enable it")
		b.WriteString(" (they can run `/sandbox network on`); do not work around it.")
	}
	return b.String()
}

// NetworkHint explains how to turn the network on.
func NetworkHint() string {
	return "the user can enable it with `/sandbox network on` (or `wombat config set sandbox_network true`)"
}

// Status describes the settings for the user interface.
func (s Settings) Status() string {
	net := "off"
	if s.Network {
		net = "on"
	}
	mode := string(s.Mode)
	if mode == "" {
		mode = string(Off)
	}
	status := fmt.Sprintf("sandbox: %s · network %s", mode, net)
	if !s.Enforced() && s.Mode != "" && s.Mode != Off {
		status += " (not enforced on this host)"
	}
	return status
}

// Help lists the sandbox commands.
const Help = `sandbox:
  /sandbox                      show the current sandbox and network state
  /sandbox network on|off       allow or block network access
  /sandbox mode <mode>          off | read-only | workspace-write | danger-full-access
`

// ApplyCommand handles a "/sandbox ..." command and returns the new settings,
// a message for the user, and whether anything changed.
func ApplyCommand(line string, current Settings) (Settings, string, bool, error) {
	args := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "/sandbox"))
	if len(args) == 0 {
		return current, current.Status(), false, nil
	}
	switch strings.ToLower(args[0]) {
	case "network", "net":
		if len(args) < 2 {
			return current, "", false, fmt.Errorf("usage: /sandbox network on|off")
		}
		on := false
		switch strings.ToLower(args[1]) {
		case "on", "true", "yes", "1", "enable", "enabled":
			on = true
		case "off", "false", "no", "0", "disable", "disabled":
			on = false
		default:
			return current, "", false, fmt.Errorf("usage: /sandbox network on|off")
		}
		changed := current.Network != on
		current.Network = on
		return current, current.Status(), changed, nil
	case "mode", "set":
		if len(args) < 2 {
			return current, "", false, fmt.Errorf("usage: /sandbox mode off|read-only|workspace-write|danger-full-access")
		}
		mode, err := Parse(args[1])
		if err != nil {
			return current, "", false, err
		}
		changed := current.Mode != mode
		current.Mode = mode
		return current, current.Status(), changed, nil
	case "help", "?":
		return current, Help, false, nil
	default:
		return current, "", false, fmt.Errorf("unknown sandbox command %q — /sandbox help", args[0])
	}
}
