package sandbox

import "sync"

// State is the mutable sandbox configuration of a running session. The user
// can widen it mid-session (with /sandbox, or by approving an escalation),
// and every tool that runs afterwards sees the new settings.
type State struct {
	mu       sync.RWMutex
	settings Settings
}

// NewState returns a state holding the given settings.
func NewState(s Settings) *State {
	return &State{settings: s}
}

// Settings returns a copy of the current settings.
func (s *State) Settings() Settings {
	if s == nil {
		return Settings{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Update changes the settings under the lock.
func (s *State) Update(fn func(*Settings)) {
	if s == nil || fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.settings)
}

// Policy returns the sandbox policy for a workspace.
func (s *State) Policy(workspace string) Policy {
	return s.Settings().Policy(workspace)
}
