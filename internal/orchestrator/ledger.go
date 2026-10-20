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
	maxStepsInPlan = 12
	maxFacts       = 20
	maxReportBytes = 24 * 1024
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

type Step struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Objective string        `json:"objective,omitempty"`
	Role      string        `json:"role,omitempty"`
	Status    StepStatus    `json:"status"`
	DependsOn []string      `json:"depends_on,omitempty"`
	Report    *WorkerReport `json:"report,omitempty"`
	Updated   time.Time     `json:"updated,omitempty"`
}

type Ledger struct {
	Goal    string    `json:"goal,omitempty"`
	Facts   []string  `json:"facts,omitempty"`
	Steps   []Step    `json:"steps,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
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
	inProgress := 0
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
		if s.Status == StepInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("at most one step may be in_progress (found %d)", inProgress)
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
		fmt.Fprintf(&b, "- [%s] %s — %s", s.Status, s.ID, oneLine(s.Title, 160))
		if s.Objective != "" && s.Objective != s.Title {
			fmt.Fprintf(&b, ": %s", oneLine(s.Objective, 300))
		}
		if len(s.DependsOn) > 0 {
			fmt.Fprintf(&b, " (depends on: %s)", strings.Join(s.DependsOn, ", "))
		}
		if s.Report != nil && s.Report.Summary != "" {
			fmt.Fprintf(&b, "\n  result: %s", oneLine(s.Report.Summary, 400))
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
			Status:    s.Status,
			DependsOn: append([]string(nil), s.DependsOn...),
		}
		if s.Report != nil {
			item.Result = oneLine(s.Report.Summary, 500)
			item.ReportStatus = s.Report.Status
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
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Status       StepStatus `json:"status"`
	DependsOn    []string   `json:"depends_on,omitempty"`
	ReportStatus string     `json:"report_status,omitempty"`
	Result       string     `json:"result,omitempty"`
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
