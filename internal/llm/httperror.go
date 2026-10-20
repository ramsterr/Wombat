package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HTTPError struct {
	Provider   string
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: http %d: %s", e.Provider, e.StatusCode, e.Body)
}

func (e *HTTPError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return true
	}
	return e.StatusCode >= 500 && e.StatusCode <= 599
}

// InvalidRequest reports whether an error is the provider rejecting the
// request itself - a bad message, an image a text-only model cannot take, a
// missing reasoning block. Retrying the same request will not help; the
// history has to change.
func InvalidRequest(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusBadRequest {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"invalid_request",
		"invalid_value",
		"must be passed back",
		"must be followed by tool messages",
		"supports text input only",
		"contains an image",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
