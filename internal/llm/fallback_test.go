package llm

import (
	"context"
	"testing"
)

type scriptedProvider struct {
	models []string
	errs   []error
}

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) ListModels(context.Context) ([]string, error) {
	return s.models, nil
}
func (s *scriptedProvider) Chat(_ context.Context, req Request, _ func(string)) (*Result, error) {
	s.models = append(s.models, req.Model)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	return &Result{Content: "ok from " + req.Model}, nil
}

func TestFallbackUsesNextModelOnRetryableError(t *testing.T) {
	base := &scriptedProvider{errs: []error{&HTTPError{StatusCode: 503}}}
	var from, to string
	p := WithFallback(base, []string{"backup", "backup", " "}, func(f, n string, _ error) { from, to = f, n })
	res, err := p.Chat(context.Background(), Request{Model: "primary"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "ok from backup" || from != "primary" || to != "backup" {
		t.Fatalf("res=%+v from=%q to=%q", res, from, to)
	}
}

func TestFallbackDoesNotRetryInvalidRequest(t *testing.T) {
	base := &scriptedProvider{errs: []error{&HTTPError{StatusCode: 400}}}
	p := WithFallback(base, []string{"backup"}, nil)
	if _, err := p.Chat(context.Background(), Request{Model: "primary"}, nil); err == nil {
		t.Fatal("expected the 400 to be returned")
	}
	if len(base.models) != 1 || base.models[0] != "primary" {
		t.Fatalf("models = %v", base.models)
	}
}

func TestFallbackResetsAfterStreaming(t *testing.T) {
	base := &streamThenOKProvider{}
	p := WithFallback(base, []string{"backup"}, nil)
	var got string
	resets := 0
	req := Request{Model: "primary", OnReset: func() { got = ""; resets++ }}
	res, err := p.Chat(context.Background(), req, func(s string) { got += s })
	if err != nil || res.Content != "ok" || base.calls != 2 || got != "final" || resets != 1 {
		t.Fatalf("err=%v res=%+v calls=%d got=%q resets=%d", err, res, base.calls, got, resets)
	}
}

type streamThenOKProvider struct{ calls int }

func (s *streamThenOKProvider) Name() string                                 { return "stream" }
func (s *streamThenOKProvider) ListModels(context.Context) ([]string, error) { return nil, nil }
func (s *streamThenOKProvider) Chat(_ context.Context, _ Request, on func(string)) (*Result, error) {
	s.calls++
	if s.calls == 1 {
		if on != nil {
			on("partial")
		}
		return nil, &HTTPError{StatusCode: 503}
	}
	if on != nil {
		on("final")
	}
	return &Result{Content: "ok"}, nil
}
