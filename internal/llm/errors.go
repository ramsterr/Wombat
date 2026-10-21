package llm

import (
	"errors"
	"strings"
)

// IsInvalidRequest reports whether the provider rejected the request shape
// or content (a 400/422). A fresh context can recover from these; network,
// auth, and rate-limit errors need a different response.
func IsInvalidRequest(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 400 || httpErr.StatusCode == 422
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"invalid_request_error",
		"invalid_value",
		"context_length_exceeded",
		"maximum context length",
		"too many tokens",
		"prompt is too long",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// RequiresReasoning reports whether a provider rejected a request because
// older reasoning_content was not sent back. This is a compatibility
// requirement, not a quality signal: retry the same request with the full
// reasoning.
func RequiresReasoning(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{"reasoning_content", "reasoning item", "reasoning_content is required", "thinking mode"} {
		if !strings.Contains(s, marker) {
			continue
		}
		for _, need := range []string{"passed back", "required", "missing", "must be"} {
			if strings.Contains(s, need) {
				return true
			}
		}
	}
	return false
}
