package cli

import (
	"github.com/ramsterr/Wombat/internal/mode"
)

// modeStatus describes the current mode.
func modeStatus(m mode.Mode) string { return mode.Status(m) }
