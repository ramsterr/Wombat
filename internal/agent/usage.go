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

// calibrateTokens folds the provider's measured input tokens back into the
// heuristic estimate. The ratio between measured and estimated request
// tokens becomes a running scale applied by historyTokens, so compaction
// triggers track reality instead of a chars-per-token guess.
func (a *Agent) calibrateTokens(measured, estimate int) {
	if measured <= 0 || estimate < 500 {
		return
	}
	ratio := float64(measured) / float64(estimate)
	if ratio < 0.25 {
		ratio = 0.25
	} else if ratio > 4 {
		ratio = 4
	}
	a.mu.Lock()
	if a.tokenScale == 0 {
		a.tokenScale = ratio
	} else {
		a.tokenScale = 0.7*a.tokenScale + 0.3*ratio
	}
	a.mu.Unlock()
}

// tokenEstimateScale is the current calibration of the heuristic token
// estimate; 1 when no provider measurement has been seen yet.
func (a *Agent) tokenEstimateScale() float64 {
	a.mu.Lock()
	s := a.tokenScale
	a.mu.Unlock()
	if s <= 0 {
		return 1
	}
	return s
}

func (a *Agent) AddUsageStats(u UsageStats) {
	a.mu.Lock()
	a.usage.Calls += u.Calls
	a.usage.In += u.In
	a.usage.Cached += u.Cached
	a.usage.Out += u.Out
	if a.usage.Started.IsZero() {
		a.usage.Started = time.Now()
	}
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
