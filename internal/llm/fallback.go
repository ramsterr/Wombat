package llm

import (
	"context"
	"fmt"
)

// fallbackProvider retries a request on another model when the first one is
// unavailable, rate-limited, or returns a server error. It never changes the
// protocol or the provider, and it never replays a request after output has
// already been streamed to the user.
type fallbackProvider struct {
	primary Provider
	models  []string
	onFall  func(from, to string, err error)
}

// WithFallback wraps a provider with an ordered model fallback list. The
// primary model in the request is always tried first.
func WithFallback(p Provider, models []string, onFallback func(from, to string, err error)) Provider {
	if p == nil || len(models) == 0 {
		return p
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(models))
	for _, m := range models {
		m = trimModel(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		clean = append(clean, m)
	}
	if len(clean) == 0 {
		return p
	}
	return &fallbackProvider{primary: p, models: clean, onFall: onFallback}
}

func trimModel(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

func (f *fallbackProvider) Name() string { return f.primary.Name() }

func (f *fallbackProvider) ListModels(ctx context.Context) ([]string, error) {
	return f.primary.ListModels(ctx)
}

func (f *fallbackProvider) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	emitted := false
	wrapped := func(s string) {
		emitted = true
		if onDelta != nil {
			onDelta(s)
		}
	}
	reasoning := req.OnReasoning
	req.OnReasoning = func(s string) {
		emitted = true
		if reasoning != nil {
			reasoning(s)
		}
	}
	res, err := f.primary.Chat(ctx, req, wrapped)
	if err == nil || emitted || ctx.Err() != nil || !retryable(err) {
		return res, err
	}
	from := req.Model
	last := err
	for _, model := range f.models {
		if model == from {
			continue
		}
		if f.onFall != nil {
			f.onFall(from, model, last)
		}
		next := req
		next.Model = model
		res, err = f.primary.Chat(ctx, next, wrapped)
		if err == nil {
			return res, nil
		}
		last = err
		if emitted || ctx.Err() != nil || !retryable(err) {
			return nil, err
		}
		from = model
	}
	return nil, fmt.Errorf("all models failed, last error: %w", last)
}
