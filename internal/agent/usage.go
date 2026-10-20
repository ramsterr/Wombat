package agent

import (
	"time"

	"github.com/ramsterr/Wombat/internal/llm"
)

type UsageStats struct {
	Calls    int           `json:"calls"`
	In       int           `json:"in"`
	Cached   int           `json:"cached"`
	Out      int           `json:"out"`
	Started  time.Time     `json:"started,omitempty"`
	Duration time.Duration `json:"duration_ns,omitempty"`
}

func (u UsageStats) Total() int { return u.In + u.Out }

type Pricing struct {
	InputPerMTok  float64 `json:"input_per_mtok"`
	CachedPerMTok float64 `json:"cached_per_mtok"`
	OutputPerMTok float64 `json:"output_per_mtok"`
}

func (p Pricing) Cost(u UsageStats) float64 {
	cached := u.Cached
	if cached > u.In {
		cached = u.In
	}
	uncached := u.In - cached
	cachedRate := p.CachedPerMTok
	if cachedRate == 0 {
		cachedRate = p.InputPerMTok
	}
	const million = 1_000_000
	return float64(uncached)/million*p.InputPerMTok +
		float64(cached)/million*cachedRate +
		float64(u.Out)/million*p.OutputPerMTok
}

func (a *Agent) markStarted() {
	a.mu.Lock()
	if a.usage.Started.IsZero() {
		a.usage.Started = time.Now()
	}
	a.mu.Unlock()
}

func (a *Agent) recordUsage(u llm.Usage) {
	a.mu.Lock()
	a.usage.Calls++
	a.usage.In += u.In
	a.usage.Cached += u.Cached
	a.usage.Out += u.Out
	a.mu.Unlock()
}

func (a *Agent) Usage() UsageStats {
	a.mu.Lock()
	u := a.usage
	a.mu.Unlock()
	if !u.Started.IsZero() {
		u.Duration = time.Since(u.Started)
	}
	return u
}

func (a *Agent) ResetUsage() {
	a.mu.Lock()
	a.usage = UsageStats{Started: time.Now()}
	a.mu.Unlock()
}
