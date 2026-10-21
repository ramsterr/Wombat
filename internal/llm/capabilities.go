package llm

import "github.com/ramsterr/Wombat/internal/modelinfo"

// catalogueLookup is indirect so tests can stand in for the models.dev
// catalogue without touching the network.
var catalogueLookup = modelinfo.Lookup

// catalogueReasoning reports whether the provider catalogue knows the model
// and whether it accepts reasoning parameters. known is false when there is
// no catalogue entry, in which case callers keep their built-in behavior.
func catalogueReasoning(provider, model string) (allowed, known bool) {
	info, ok := catalogueLookup(provider, model)
	if !ok {
		return false, false
	}
	return info.Reasoning, true
}

// reasoningEnabled resolves a per-protocol fallback against the provider
// catalogue. The catalogue wins when it has an entry for the model; a model
// the catalogue says is not a reasoning model is never sent reasoning
// parameters.
func reasoningEnabled(provider, model string, fallback func(string) bool) bool {
	if allowed, known := catalogueReasoning(provider, model); known {
		return allowed
	}
	return fallback(model)
}
