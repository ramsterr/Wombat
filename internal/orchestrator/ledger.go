package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxStepsInPlan   = 12
	maxFacts         = 20
	maxReportBytes   = 24 * 1024
	maxChecksPerStep = 12
	maxCheckOutput   = 4000
)

type StepStatus string

const (
	StepPending    StepStatus = "pending"
	StepInProgress StepStatus = "in_progress"
	StepCompleted  StepStatus = "completed"
	StepBlocked    StepStatus = "blocked"
	StepFailed     StepStatus = "failed"
)

func (s StepStatus) Valid() bool {
	switch s {
	case StepPending, StepInProgress, StepCompleted, StepBlocked, StepFailed:
		return true
	}
	return false
}

type CheckStatus string

const (
	CheckPending CheckStatus = "pending"
	CheckPassed  CheckStatus = "passed"
	CheckFailed  CheckStatus = "failed"
)

func (s CheckStatus) Valid() bool {
	return s == CheckPending || s == CheckPassed || s == CheckFailed
}

type Check struct {
	ID             string      `json:"id"`
	Command        string      `json:"command"`
	TimeoutSeconds int         `json:"timeout_seconds,omitempty"`
	Status         CheckStatus `json:"status"`
	Output         string      `json:"output,omitempty"`
	ExitCode       int         `json:"exit_code,omitempty"`
	RanAt          time.Time   `json:"ran_at,omitempty"`
}

const (
	StepScout = "scout"
	StepWrite = "write"
)

func normalizeStepKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", StepScout:
		return StepScout
	case StepWrite, "writer", "implement", "implementer":
		return StepWrite
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

type Step struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Objective   string        `json:"objective,omitempty"`
	Role        string        `json:"role,omitempty"`
	Kind        string        `json:"kind,omitempty"`
	Files       []string      `json:"files,omitempty"`
	Deliverable string        `json:"deliverable,omitempty"`
	Status      StepStatus    `json:"status"`
	DependsOn   []string      `json:"depends_on,omitempty"`
	Checks      []Check       `json:"checks,omitempty"`
	Report      *WorkerReport `json:"report,omitempty"`
	Review      *Review       `json:"review,omitempty"`
	Updated     time.Time     `json:"updated,omitempty"`
}

type Ledger struct {
	Goal    string    `json:"goal,omitempty"`
	Facts   []string  `json:"facts,omitempty"`
	Steps   []Step    `json:"steps,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
}

func normalizeChecks(checks []Check) []Check {
	if len(checks) > maxChecksPerStep {
		checks = checks[:maxChecksPerStep]
	}
	for i := range checks {
		checks[i].ID = strings.TrimSpace(checks[i].ID)
		checks[i].Command = strings.TrimSpace(checks[i].Command)
		if !checks[i].Status.Valid() {
			checks[i].Status = CheckPending
		}
		if len(checks[i].Output) > maxCheckOutput {
			checks[i].Output = checks[i].Output[:maxCheckOutput] + "\n…[truncated]"
		}
	}
	return checks
}

func checkSummary(checks []Check) string {
	parts := make([]string, 0, len(checks))
	for _, c := range checks {
		parts = append(parts, c.ID+"="+string(c.Status))
	}
	return strings.Join(parts, ", ")
}

func allChecksPassed(steps []Step) bool {
	for _, s := range steps {
		for _, c := range s.Checks {
			if c.Status != CheckPassed {
				return false
			}
		}
	}
	return true
}

func LedgerPath(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".ledger.json"
}

func LoadLedger(path string) (*Ledger, string, error) {
	empty := &Ledger{}
	if path == "" {
		return empty, "", nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, "", nil
	}
	if err != nil {
		return empty, "", err
	}
	var l Ledger
	if err := json.Unmarshal(raw, &l); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		_ = os.Rename(path, backup)
		return empty, fmt.Sprintf("ledger was corrupt and has been moved to %s", backup), err
	}
	l.normalize()
	return &l, "", nil
}

func (l *Ledger) normalize() {
	if l.Facts == nil {
		l.Facts = nil
	}
	seen := make(map[string]bool, len(l.Steps))
	out := l.Steps[:0]
	for _, s := range l.Steps {
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		if !s.Status.Valid() {
			s.Status = StepPending
		}
		s.Report = trimReport(s.Report)
		s.Checks = normalizeChecks(s.Checks)
		s.Kind = normalizeStepKind(s.Kind)
		out = append(out, s)
	}
	l.Steps = out
	if len(l.Facts) > maxFacts {
		l.Facts = l.Facts[:maxFacts]
	}
}

func (l *Ledger) Save(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ledger-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (l *Ledger) Validate() error {
	if len(l.Steps) > maxStepsInPlan {
		return fmt.Errorf("too many steps: %d (max %d)", len(l.Steps), maxStepsInPlan)
	}
	ids := make(map[string]int, len(l.Steps))
	for i, s := range l.Steps {
		if strings.TrimSpace(s.ID) == "" {
			return fmt.Errorf("step %d: id is required", i+1)
		}
		if strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("step %q: title is required", s.ID)
		}
		if _, ok := ids[s.ID]; ok {
			return fmt.Errorf("duplicate step id %q", s.ID)
		}
		ids[s.ID] = i
		if !s.Status.Valid() {
			return fmt.Errorf("step %q: invalid status %q", s.ID, s.Status)
		}
		if kind := normalizeStepKind(s.Kind); kind != StepScout && kind != StepWrite {
			return fmt.Errorf("step %q: kind must be scout or write", s.ID)
		}
		if len(s.Files) > 40 {
			return fmt.Errorf("step %q: too many owned files: %d (max 40)", s.ID, len(s.Files))
		}
		if len(s.Deliverable) > 4000 {
			return fmt.Errorf("step %q: deliverable is too long (max 4000 chars)", s.ID)
		}
		for _, f := range s.Files {
			if _, err := cleanOwnedPath(f); err != nil {
				return fmt.Errorf("step %q: %w", s.ID, err)
			}
		}
		if len(s.Checks) > maxChecksPerStep {
			return fmt.Errorf("step %q: too many checks: %d (max %d)", s.ID, len(s.Checks), maxChecksPerStep)
		}
		checkIDs := make(map[string]bool, len(s.Checks))
		for j, c := range s.Checks {
			if strings.TrimSpace(c.ID) == "" {
				return fmt.Errorf("step %q: check %d needs an id", s.ID, j+1)
			}
			if checkIDs[c.ID] {
				return fmt.Errorf("step %q: duplicate check id %q", s.ID, c.ID)
			}
			checkIDs[c.ID] = true
			if strings.TrimSpace(c.Command) == "" {
				return fmt.Errorf("step %q: check %q needs a command", s.ID, c.ID)
			}
			if !c.Status.Valid() {
				return fmt.Errorf("step %q: check %q has invalid status %q", s.ID, c.ID, c.Status)
			}
		}
	}
	for _, s := range l.Steps {
		for _, dep := range s.DependsOn {
			if dep == s.ID {
				return fmt.Errorf("step %q cannot depend on itself", s.ID)
			}
			if _, ok := ids[dep]; !ok {
				return fmt.Errorf("step %q depends on unknown step %q", s.ID, dep)
			}
		}
	}
	return validateAcyclic(l.Steps, ids)
}

func validateAcyclic(steps []Step, ids map[string]int) error {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make([]int, len(steps))
	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case visiting:
			return fmt.Errorf("dependency cycle involving step %q", steps[i].ID)
		case done:
			return nil
		}
		state[i] = visiting
		for _, dep := range steps[i].DependsOn {
			if err := visit(ids[dep]); err != nil {
				return err
			}
		}
		state[i] = done
		return nil
	}
	for i := range steps {
		if err := visit(i); err != nil {
			return err
		}
	}
	return nil
}

func (l *Ledger) Find(id string) *Step {
	for i := range l.Steps {
		if l.Steps[i].ID == id {
			return &l.Steps[i]
		}
	}
	return nil
}

func (l *Ledger) PromptView() string {
	var b strings.Builder
	b.WriteString("Goal: ")
	if l.Goal == "" {
		b.WriteString("(not set)")
	} else {
		b.WriteString(oneLine(l.Goal, 500))
	}
	b.WriteByte('\n')
	if len(l.Facts) > 0 {
		b.WriteString("Facts:\n")
		for _, f := range l.Facts {
			fmt.Fprintf(&b, "- %s\n", oneLine(f, 400))
		}
	}
	if len(l.Steps) == 0 {
		b.WriteString("Steps: (none)\n")
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Steps:\n")
	for _, s := range l.Steps {
		kind := normalizeStepKind(s.Kind)
		fmt.Fprintf(&b, "- [%s] %s (%s) — %s", s.Status, s.ID, kind, oneLine(s.Title, 160))
		if s.Objective != "" && s.Objective != s.Title {
			fmt.Fprintf(&b, ": %s", oneLine(s.Objective, 300))
		}
		if len(s.DependsOn) > 0 {
			fmt.Fprintf(&b, " (depends on: %s)", strings.Join(s.DependsOn, ", "))
		}
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, " (files: %s)", oneLine(strings.Join(s.Files, ", "), 300))
		}
		if len(s.Checks) > 0 {
			fmt.Fprintf(&b, " (checks: %s)", checkSummary(s.Checks))
		}
		if s.Report != nil && s.Report.Summary != "" {
			fmt.Fprintf(&b, "\n  result: %s", oneLine(s.Report.Summary, 400))
		}
		if s.Review != nil && s.Review.Verdict != "" {
			fmt.Fprintf(&b, "\n  review: %s — %s", s.Review.Verdict, oneLine(s.Review.Summary, 300))
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func (l *Ledger) View() LedgerView {
	v := LedgerView{Goal: l.Goal, Facts: append([]string(nil), l.Facts...)}
	for _, s := range l.Steps {
		item := StepView{
			ID:        s.ID,
			Title:     s.Title,
			Kind:      normalizeStepKind(s.Kind),
			Files:     append([]string(nil), s.Files...),
			Status:    s.Status,
			DependsOn: append([]string(nil), s.DependsOn...),
		}
		for _, c := range s.Checks {
			item.Checks = append(item.Checks, CheckView{ID: c.ID, Command: c.Command, Status: c.Status, ExitCode: c.ExitCode})
		}
		if s.Report != nil {
			item.Result = oneLine(s.Report.Summary, 500)
			item.ReportStatus = s.Report.Status
		}
		if s.Review != nil {
			item.ReviewVerdict = s.Review.Verdict
		}
		v.Steps = append(v.Steps, item)
	}
	return v
}

type LedgerView struct {
	Goal  string     `json:"goal,omitempty"`
	Facts []string   `json:"facts,omitempty"`
	Steps []StepView `json:"steps,omitempty"`
}

type StepView struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Kind          string      `json:"kind,omitempty"`
	Files         []string    `json:"files,omitempty"`
	Status        StepStatus  `json:"status"`
	DependsOn     []string    `json:"depends_on,omitempty"`
	Checks        []CheckView `json:"checks,omitempty"`
	ReportStatus  string      `json:"report_status,omitempty"`
	ReviewVerdict string      `json:"review_verdict,omitempty"`
	Result        string      `json:"result,omitempty"`
}

type CheckView struct {
	ID       string      `json:"id"`
	Command  string      `json:"command"`
	Status   CheckStatus `json:"status"`
	ExitCode int         `json:"exit_code,omitempty"`
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func trimReport(r *WorkerReport) *WorkerReport {
	if r == nil {
		return nil
	}
	r.Status = strings.TrimSpace(r.Status)
	r.Summary = oneLine(r.Summary, 4000)
	if len(r.Findings) > 20 {
		r.Findings = r.Findings[:20]
	}
	for i := range r.Findings {
		r.Findings[i].Claim = oneLine(r.Findings[i].Claim, 700)
		r.Findings[i].Evidence = oneLine(r.Findings[i].Evidence, 1000)
		r.Findings[i].Path = oneLine(r.Findings[i].Path, 300)
		r.Findings[i].Lines = oneLine(r.Findings[i].Lines, 80)
	}
	if len(r.Changes) > 20 {
		r.Changes = r.Changes[:20]
	}
	if len(r.Tests) > 20 {
		r.Tests = r.Tests[:20]
	}
	if len(r.Risks) > 20 {
		r.Risks = r.Risks[:20]
	}
	if len(r.NextSteps) > 20 {
		r.NextSteps = r.NextSteps[:20]
	}
	if raw, err := json.Marshal(r); err == nil && len(raw) > maxReportBytes {
		r.Findings = r.Findings[:min(len(r.Findings), 5)]
		r.Changes = nil
		r.Tests = nil
		r.Risks = r.Risks[:min(len(r.Risks), 5)]
		r.NextSteps = r.NextSteps[:min(len(r.NextSteps), 5)]
	}
	return r
}

func sortedStatusCounts(l *Ledger) string {
	counts := map[StepStatus]int{}
	for _, s := range l.Steps {
		counts[s.Status]++
	}
	keys := make([]string, 0, len(counts))
	for status, n := range counts {
		keys = append(keys, fmt.Sprintf("%s=%d", status, n))
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func mergeChecks(old, next []Check) []Check {
	byID := make(map[string]Check, len(old))
	for _, c := range old {
		byID[c.ID] = c
	}
	out := make([]Check, 0, len(next))
	for _, c := range next {
		c.ID = strings.TrimSpace(c.ID)
		c.Command = strings.TrimSpace(c.Command)
		if c.ID == "" {
			c.ID = c.Command
		}
		if prev, ok := byID[c.ID]; ok && strings.TrimSpace(prev.Command) == c.Command {
			c.Status = prev.Status
			c.Output = prev.Output
			c.ExitCode = prev.ExitCode
			c.RanAt = prev.RanAt
		}
		if !c.Status.Valid() {
			c.Status = CheckPending
		}
		out = append(out, c)
	}
	return out
}

func completionGate(l *Ledger) error {
	for _, s := range l.Steps {
		if s.Status != StepCompleted {
			continue
		}
		for _, c := range s.Checks {
			if c.Status != CheckPassed {
				return fmt.Errorf("step %q cannot be completed: check %q is %s; run run_checks after fixing it", s.ID, c.ID, c.Status)
			}
		}
	}
	return nil
}
