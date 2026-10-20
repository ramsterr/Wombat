package cli

import (
	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/sandbox"
)

// sandboxSettings turns the config into the sandbox settings for a session.
func sandboxSettings(cfg *config.Config) (sandbox.Settings, error) {
	mode, err := sandbox.Parse(cfg.Sandbox)
	if err != nil {
		return sandbox.Settings{}, err
	}
	return sandbox.Settings{Mode: mode, Network: cfg.SandboxNetwork}, nil
}
