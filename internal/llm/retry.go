package llm

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"syscall"
	"time"
)

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxElapsed  time.Duration
	OnRetry     func(attempt int, err error, delay time.Duration)
}

var DefaultRetryPolicy = RetryPolicy{
	// One initial attempt plus four retries.
	MaxAttempts: 5,
	BaseDelay:   500 * time.Millisecond,
	MaxDelay:    8 * time.Second,
	MaxElapsed:  30 * time.Second,
}

type retryProvider struct {
	Provider
	policy RetryPolicy
}

func WithRetry(p Provider, policy RetryPolicy) Provider {
	if p == nil {
		return nil
	}
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = 100 * time.Millisecond
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = 5 * time.Second
	}
	return &retryProvider{Provider: p, policy: policy}
}

func (p *retryProvider) Chat(ctx context.Context, req Request, onDelta func(string)) (*Result, error) {
	start := time.Now()
	reasoning := req.OnReasoning
	emitted := false
	req.OnReasoning = func(s string) {
		emitted = true
		if reasoning != nil {
			reasoning(s)
		}
	}
	wrappedDelta := func(s string) {
		emitted = true
		if onDelta != nil {
			onDelta(s)
		}
	}
	var last error
	for attempt := 1; attempt <= p.policy.MaxAttempts; attempt++ {
		res, err := p.Provider.Chat(ctx, req, wrappedDelta)
		if err == nil {
			return res, nil
		}
		last = err
		if !retryable(err) || ctx.Err() != nil || attempt == p.policy.MaxAttempts {
			return nil, err
		}
		delay := p.delay(attempt, err)
		if p.policy.MaxElapsed > 0 && time.Since(start)+delay > p.policy.MaxElapsed {
			return nil, err
		}
		if emitted {
			if req.OnReset != nil {
				req.OnReset()
			}
			emitted = false
		}
		if req.OnRetry != nil {
			req.OnRetry(attempt, err, delay)
		} else if p.policy.OnRetry != nil {
			p.policy.OnRetry(attempt, err, delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, last
}

func (p *retryProvider) delay(attempt int, err error) time.Duration {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		return min(httpErr.RetryAfter, p.policy.MaxDelay)
	}
	delay := p.policy.BaseDelay << (attempt - 1)
	if delay > p.policy.MaxDelay {
		delay = p.policy.MaxDelay
	}
	jitter := time.Duration(rand.Int64N(int64(delay/2) + 1))
	return delay/2 + jitter
}

// Retryable reports whether an error is worth another attempt.
func Retryable(err error) bool { return retryable(err) }

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable()
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Connection resets, refused connections, DNS failures and timeouts
		// are all worth another attempt; a later one may use a healthy
		// address or connection.
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection reset by peer", "connection refused", "broken pipe",
		"unexpected eof", "i/o timeout", "temporary failure in name resolution",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
