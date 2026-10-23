package llm

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

type retryFake struct {
	calls int
	fn    func(call int, onDelta func(string)) (*Result, error)
}

func (f *retryFake) Name() string { return "retry-fake" }
func (f *retryFake) ListModels(context.Context) ([]string, error) {
	return []string{"m"}, nil
}
func (f *retryFake) Chat(_ context.Context, _ Request, onDelta func(string)) (*Result, error) {
	f.calls++
	return f.fn(f.calls, onDelta)
}

func TestRetryTransientHTTPError(t *testing.T) {
	f := &retryFake{fn: func(call int, _ func(string)) (*Result, error) {
		if call < 3 {
			return nil, &HTTPError{Provider: "x", StatusCode: 429, Body: "busy"}
		}
		return &Result{Content: "ok"}, nil
	}}
	p := WithRetry(f, RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	res, err := p.Chat(context.Background(), Request{}, nil)
	if err != nil || res.Content != "ok" || f.calls != 3 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, f.calls)
	}
}

func TestRetryResetsAfterStreaming(t *testing.T) {
	f := &retryFake{fn: func(call int, onDelta func(string)) (*Result, error) {
		if call == 1 {
			if onDelta != nil {
				onDelta("partial")
			}
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
		}
		if onDelta != nil {
			onDelta("final")
		}
		return &Result{Content: "final"}, nil
	}}
	p := WithRetry(f, RetryPolicy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	var got string
	resets, retries := 0, 0
	req := Request{
		OnReset: func() { got = ""; resets++ },
		OnRetry: func(int, error, time.Duration) { retries++ },
	}
	res, err := p.Chat(context.Background(), req, func(s string) { got += s })
	if err != nil || res.Content != "final" || f.calls != 2 || got != "final" || resets != 1 || retries != 1 {
		t.Fatalf("err=%v res=%+v calls=%d got=%q resets=%d retries=%d", err, res, f.calls, got, resets, retries)
	}
}

func TestRetryDoesNotRetryPermanentError(t *testing.T) {
	f := &retryFake{fn: func(int, func(string)) (*Result, error) {
		return nil, &HTTPError{Provider: "x", StatusCode: 400, Body: "bad request"}
	}}
	p := WithRetry(f, RetryPolicy{MaxAttempts: 4, BaseDelay: time.Millisecond})
	if _, err := p.Chat(context.Background(), Request{}, nil); err == nil || f.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, f.calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("2"); got != 2*time.Second {
		t.Fatalf("seconds = %v", got)
	}
	if got := parseRetryAfter("bad"); got != 0 {
		t.Fatalf("bad = %v", got)
	}
}

func TestHTTPErrorRetryable(t *testing.T) {
	e := &HTTPError{StatusCode: 429}
	if !e.Retryable() || !errors.As(error(e), new(*HTTPError)) {
		t.Fatal("429 should be retryable")
	}
}

func TestRetryConnectionResetAndExhaustion(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	f := &retryFake{fn: func(call int, _ func(string)) (*Result, error) {
		if call < 3 {
			return nil, reset
		}
		return &Result{Content: "ok"}, nil
	}}
	p := WithRetry(f, RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	res, err := p.Chat(context.Background(), Request{}, nil)
	if err != nil || res.Content != "ok" || f.calls != 3 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, f.calls)
	}

	always := &retryFake{fn: func(int, func(string)) (*Result, error) { return nil, reset }}
	limited := WithRetry(always, RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	if _, err := limited.Chat(context.Background(), Request{}, nil); err == nil || always.calls != 5 {
		t.Fatalf("err=%v calls=%d", err, always.calls)
	}
}
