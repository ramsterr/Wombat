package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/ramsterr/Wombat/internal/agent"
	"github.com/ramsterr/Wombat/internal/tools"
)

type Finding struct {
	Claim    string `json:"claim"`
	Evidence string `json:"evidence,omitempty"`
	Path     string `json:"path,omitempty"`
	Lines    string `json:"lines,omitempty"`
}

type Change struct {
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
}

type WorkerReport struct {
	Status    string      `json:"status"`
	Summary   string      `json:"summary"`
	Findings  []Finding   `json:"findings,omitempty"`
	Changes   []Change    `json:"changes,omitempty"`
	Tests     []string    `json:"tests,omitempty"`
	Risks     []string    `json:"risks,omitempty"`
	NextSteps []string    `json:"next_steps,omitempty"`
	Questions []string    `json:"questions,omitempty"`
	Usage     ReportUsage `json:"usage,omitempty"`
}

type ReportUsage struct {
	Calls  int `json:"calls"`
	In     int `json:"in"`
	Cached int `json:"cached"`
	Out    int `json:"out"`
}

func usageFrom(stats agent.UsageStats) ReportUsage {
	return ReportUsage{Calls: stats.Calls, In: stats.In, Cached: stats.Cached, Out: stats.Out}
}

func parseReport(content string) (*WorkerReport, bool) {
	for _, candidate := range jsonCandidates(content) {
		var r WorkerReport
		if err := json.Unmarshal([]byte(candidate), &r); err != nil {
			continue
		}
		if strings.TrimSpace(r.Summary) == "" && r.Status == "" {
			continue
		}
		r.Status = normalizeReportStatus(r.Status)
		r.Summary = strings.TrimSpace(r.Summary)
		if r.Summary == "" {
			r.Summary = "worker returned a structured report without a summary"
		}
		return trimReport(&r), true
	}
	return nil, false
}

// jsonCandidates returns the whole text (without a markdown fence) and every
// balanced JSON object in it, so a report still parses when the model wraps
// it in prose.
func jsonCandidates(content string) []string {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil
	}
	text = stripFence(text)
	out := []string{text}
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		if obj, ok := balancedJSON(text[i:]); ok {
			out = append(out, obj)
		}
	}
	return out
}

// balancedJSON returns the first complete JSON object at the start of s,
// respecting strings and escapes.
func balancedJSON(s string) (string, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i+1], true
			}
		}
	}
	return "", false
}

func fallbackReport(content string) *WorkerReport {
	summary := strings.TrimSpace(content)
	if summary == "" {
		summary = "worker returned no final report"
	}
	return trimReport(&WorkerReport{
		Status:  "unstructured",
		Summary: summary,
	})
}

func normalizeReportStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "completed", "complete", "done", "success":
		return "completed"
	case "partial", "in_progress", "incomplete":
		return "partial"
	case "blocked":
		return "blocked"
	case "needs_input", "needs-input", "input_needed", "question", "questions":
		return "needs_input"
	case "failed", "error":
		return "failed"
	case "unstructured":
		return "unstructured"
	default:
		return "partial"
	}
}

func reportStatusToStep(status string) StepStatus {
	switch status {
	case "completed":
		return StepCompleted
	case "blocked":
		return StepBlocked
	case "failed":
		return StepFailed
	default:
		return StepPending
	}
}

func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	} else {
		return s
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func validateDelegationArgs(a delegateArgs) error {
	if strings.TrimSpace(a.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len(a.Title) > 200 {
		return fmt.Errorf("title is too long (max 200 chars)")
	}
	if strings.TrimSpace(a.Objective) == "" {
		return fmt.Errorf("objective is required")
	}
	if len(a.Objective) > 8000 {
		return fmt.Errorf("objective is too long (max 8000 chars)")
	}
	if len(a.Context) > 16000 {
		return fmt.Errorf("context is too long (max 16000 chars)")
	}
	if len(a.Role) > 200 {
		return fmt.Errorf("role is too long (max 200 chars)")
	}
	if len(a.Deliverable) > 4000 {
		return fmt.Errorf("deliverable is too long (max 4000 chars)")
	}
	if a.MaxSteps < 0 || a.MaxSteps > 100 {
		return fmt.Errorf("max_steps must be between 0 and 100")
	}
	if len(a.Files) > 40 {
		return fmt.Errorf("too many files: %d (max 40)", len(a.Files))
	}
	for _, f := range a.Files {
		if _, err := cleanOwnedPath(f); err != nil {
			return err
		}
	}
	return nil
}

// Review is an independent reviewer's verdict on a writer's patch.
type Review struct {
	Verdict  string          `json:"verdict"`
	Summary  string          `json:"summary"`
	Findings []ReviewFinding `json:"findings,omitempty"`
	Usage    ReportUsage     `json:"usage,omitempty"`
}

type ReviewFinding struct {
	Severity   string `json:"severity,omitempty"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Issue      string `json:"issue"`
	Suggestion string `json:"suggestion,omitempty"`
}

func normalizeVerdict(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approved", "approve", "lgtm", "accept", "accepted", "pass", "passed":
		return "approved"
	case "changes_requested", "request_changes", "changes", "needs_changes", "revise":
		return "changes_requested"
	case "rejected", "reject", "fail", "failed", "blocked":
		return "rejected"
	default:
		return "unstructured"
	}
}

func parseReview(content string) (*Review, bool) {
	for _, candidate := range jsonCandidates(content) {
		var r Review
		if err := json.Unmarshal([]byte(candidate), &r); err != nil {
			continue
		}
		if strings.TrimSpace(r.Verdict) == "" && strings.TrimSpace(r.Summary) == "" {
			continue
		}
		r.Verdict = normalizeVerdict(r.Verdict)
		r.Summary = strings.TrimSpace(r.Summary)
		if r.Summary == "" {
			r.Summary = "reviewer returned no summary"
		}
		if len(r.Findings) > 20 {
			r.Findings = r.Findings[:20]
		}
		for i := range r.Findings {
			r.Findings[i].Severity = oneLine(r.Findings[i].Severity, 30)
			r.Findings[i].Path = oneLine(r.Findings[i].Path, 300)
			r.Findings[i].Issue = oneLine(r.Findings[i].Issue, 1000)
			r.Findings[i].Suggestion = oneLine(r.Findings[i].Suggestion, 1000)
		}
		return &r, true
	}
	return nil, false
}

type reportSink struct {
	mu     sync.Mutex
	report *WorkerReport
	review *Review
}

func (s *reportSink) setReport(r *WorkerReport) {
	s.mu.Lock()
	s.report = r
	s.mu.Unlock()
}

func (s *reportSink) getReport() *WorkerReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report
}

func (s *reportSink) setReview(r *Review) {
	s.mu.Lock()
	s.review = r
	s.mu.Unlock()
}

func (s *reportSink) getReview() *Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.review
}

// submitReportTool lets a worker hand back a structured report as a tool
// call instead of hoping its final prose happens to be valid JSON.
type submitReportTool struct{ sink *reportSink }

func (t *submitReportTool) Name() string  { return "submit_report" }
func (t *submitReportTool) Class() string { return "report" }
func (t *submitReportTool) Description() string {
	return "Record the final structured report for this delegated task. Call this once when the work is finished, or with status=needs_input and your questions when you are blocked on a decision only the lead can make. It is how the lead receives your status, summary, findings, changes, tests, risks, next steps, and questions."
}
func (t *submitReportTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":     map[string]any{"type": "string", "enum": []string{"completed", "partial", "blocked", "needs_input", "failed"}},
			"summary":    map[string]any{"type": "string", "description": "what you established or did, in at most 6 sentences"},
			"findings":   map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"claim": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "lines": map[string]any{"type": "string"}}, "required": []string{"claim"}}, "description": "claims backed by evidence"},
			"changes":    map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}}, "required": []string{"path"}}, "description": "files changed (writers only)"},
			"tests":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "commands run and their outcomes"},
			"risks":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"next_steps": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"questions":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "when status is needs_input, the specific questions the lead must answer"},
		},
		"required": []string{"status", "summary"},
	}
}
func (t *submitReportTool) Run(_ context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var r WorkerReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("bad report: %w", err)
	}
	if strings.TrimSpace(r.Summary) == "" {
		return "", fmt.Errorf("summary is required")
	}
	r.Status = normalizeReportStatus(r.Status)
	t.sink.setReport(trimReport(&r))
	return "report recorded", nil
}

// submitReviewTool is the reviewer's structured verdict.
type submitReviewTool struct{ sink *reportSink }

func (t *submitReviewTool) Name() string  { return "submit_review" }
func (t *submitReviewTool) Class() string { return "report" }
func (t *submitReviewTool) Description() string {
	return "Record the independent review verdict for a writer's patch. Use verdict=approved only when the change is correct, scoped, and covered by evidence."
}
func (t *submitReviewTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdict": map[string]any{"type": "string", "enum": []string{"approved", "changes_requested", "rejected"}},
			"summary": map[string]any{"type": "string", "description": "the decisive reason for the verdict"},
			"findings": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
				"severity":   map[string]any{"type": "string", "enum": []string{"blocker", "major", "minor", "nit"}},
				"path":       map[string]any{"type": "string"},
				"line":       map[string]any{"type": "integer"},
				"issue":      map[string]any{"type": "string"},
				"suggestion": map[string]any{"type": "string"},
			}, "required": []string{"issue"}}},
		},
		"required": []string{"verdict", "summary"},
	}
}
func (t *submitReviewTool) Run(_ context.Context, raw json.RawMessage, _ *tools.Env) (string, error) {
	var r Review
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("bad review: %w", err)
	}
	if strings.TrimSpace(r.Summary) == "" {
		return "", fmt.Errorf("summary is required")
	}
	r.Verdict = normalizeVerdict(r.Verdict)
	t.sink.setReview(&r)
	return "review recorded", nil
}

func truncateText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n…[truncated]"
}
