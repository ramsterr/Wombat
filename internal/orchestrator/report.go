package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramsterr/Wombat/internal/agent"
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
	text := strings.TrimSpace(content)
	if text == "" {
		return nil, false
	}
	text = stripFence(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	var r WorkerReport
	if err := json.Unmarshal([]byte(text[start:end+1]), &r); err != nil {
		return nil, false
	}
	if strings.TrimSpace(r.Summary) == "" && r.Status == "" {
		return nil, false
	}
	r.Status = normalizeReportStatus(r.Status)
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		r.Summary = "worker returned a structured report without a summary"
	}
	return trimReport(&r), true
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
	return nil
}
