package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// JobManager owns the background processes started through one Env. A dev
// server, a watcher, or a long test run keeps going while the agent works;
// the model polls job_output and can stop it with job_kill.
type JobManager struct {
	mu   sync.Mutex
	next int
	jobs map[int]*Job
}

func NewJobManager() *JobManager {
	return &JobManager{jobs: map[int]*Job{}}
}

// Job is one background process.
type Job struct {
	ID      int
	Command string
	Started time.Time
	PID     int

	cmd    *exec.Cmd
	cancel context.CancelFunc

	mu       sync.Mutex
	output   bytes.Buffer
	done     bool
	err      error
	exitCode int
	timedOut bool
	killed   bool
}

func (j *Job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.output.Write(p)
}

func (j *Job) state() (done bool, exitCode int, timedOut, killed bool, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done, j.exitCode, j.timedOut, j.killed, j.err
}

func (j *Job) snapshot(since int) (string, int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	data := j.output.Bytes()
	if since < 0 || since > len(data) {
		since = 0
	}
	return string(data[since:]), len(data)
}

// Start launches a command in the background. It is not tied to the tool
// call's context: the point is that it survives the turn.
func (m *JobManager) Start(_ context.Context, env *Env, command string, timeout time.Duration) (*Job, error) {
	if m == nil {
		return nil, errors.New("background jobs are unavailable")
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if env != nil {
		cmd.Dir = env.Dir
		if tmp := os.Getenv("WOMBAT_TMP"); tmp != "" {
			cmd.Env = append(os.Environ(), "WOMBAT_TMP="+tmp)
		}
	}
	prepareCommand(cmd)
	m.mu.Lock()
	m.next++
	job := &Job{ID: m.next, Command: command, Started: time.Now(), cmd: cmd, cancel: cancel}
	m.jobs[job.ID] = job
	m.mu.Unlock()

	cmd.Stdout = job
	cmd.Stderr = job
	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.jobs, job.ID)
		m.mu.Unlock()
		return nil, err
	}
	job.mu.Lock()
	job.PID = cmd.Process.Pid
	job.mu.Unlock()
	go func() {
		err := cmd.Wait()
		job.mu.Lock()
		job.done = true
		job.err = err
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				job.exitCode = exitErr.ExitCode()
			}
		}
		if ctx.Err() == context.DeadlineExceeded {
			job.timedOut = true
		}
		job.mu.Unlock()
		cancel()
	}()
	return job, nil
}

// Close stops every running job. It is called when the session ends so a
// dev server or watcher does not outlive the process that started it.
func (m *JobManager) Close() {
	if m == nil {
		return
	}
	for _, j := range m.List() {
		if done, _, _, _, _ := j.state(); done {
			continue
		}
		j.mu.Lock()
		j.killed = true
		j.mu.Unlock()
		terminate(j.cmd)
		j.cancel()
	}
}

// List returns every job, newest last.
func (m *JobManager) List() []*Job {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]*Job, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.jobs[id])
	}
	return out
}

// Get returns a job by id.
func (m *JobManager) Get(id int) (*Job, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Kill stops a background job and its process group.
func (m *JobManager) Kill(id int) error {
	j, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("no background job %d", id)
	}
	done, _, _, _, _ := j.state()
	if done {
		return fmt.Errorf("job %d has already finished", id)
	}
	j.mu.Lock()
	j.killed = true
	j.mu.Unlock()
	terminate(j.cmd)
	j.cancel()
	return nil
}

func jobStatus(j *Job) string {
	done, code, timedOut, killed, err := j.state()
	switch {
	case !done:
		return "running"
	case timedOut:
		return "timed out"
	case killed:
		return "killed"
	case code != 0:
		return fmt.Sprintf("exited %d", code)
	case err != nil:
		return "failed: " + err.Error()
	default:
		return "exited 0"
	}
}

func (j *Job) StartSummary() string {
	done, code, _, _, _ := j.state()
	status := "running"
	if done {
		status = fmt.Sprintf("finished (exit %d)", code)
	}
	out, _ := j.snapshot(0)
	out = strings.TrimRight(RedactSecrets(out), "\n")
	if out == "" {
		return fmt.Sprintf("started background job %d (pid %d, %s). Use job_output %d to read it.", j.ID, j.PID, status, j.ID)
	}
	return fmt.Sprintf("started background job %d (pid %d, %s)\n%s", j.ID, j.PID, status, out)
}

type JobOutput struct{}

type jobOutputArgs struct {
	ID      int  `json:"id"`
	Since   int  `json:"since"`
	WaitMS  int  `json:"wait_ms"`
	Follow  bool `json:"follow"`
	MaxRead int  `json:"max_bytes"`
}

func (t *JobOutput) Name() string  { return "job_output" }
func (t *JobOutput) Class() string { return "read" }
func (t *JobOutput) Description() string {
	return "Read output from a background job. Returns only bytes after since, plus the next offset. wait_ms waits for new output or for the job to finish; follow keeps polling until it finishes."
}
func (t *JobOutput) Schema() map[string]any {
	return obj(map[string]any{
		"id":        map[string]any{"type": "integer", "description": "job id"},
		"since":     map[string]any{"type": "integer", "description": "byte offset returned by the previous call"},
		"wait_ms":   map[string]any{"type": "integer", "description": "how long to wait for new output, default 0"},
		"follow":    map[string]any{"type": "boolean", "description": "keep waiting until the job finishes"},
		"max_bytes": map[string]any{"type": "integer", "description": "optional cap for this read; the next offset points past what was returned"},
	}, "id")
}

func (t *JobOutput) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[jobOutputArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	j, ok := env.JobManager().Get(a.ID)
	if !ok {
		return "", fmt.Errorf("no background job %d", a.ID)
	}
	deadline := time.Duration(0)
	if a.WaitMS > 0 {
		deadline = time.Duration(a.WaitMS) * time.Millisecond
	}
	start := time.Now()
	for {
		out, next := j.snapshot(a.Since)
		done, code, _, _, _ := j.state()
		waiting := a.Follow || a.WaitMS > 0
		expired := deadline > 0 && !a.Follow && time.Since(start) >= deadline
		if done || !waiting || expired {
			header := fmt.Sprintf("job %d: %s", j.ID, jobStatus(j))
			if done {
				header += fmt.Sprintf(" (exit %d)", code)
			}
			body := out
			if a.MaxRead > 0 && len(body) > a.MaxRead {
				body = body[:a.MaxRead]
			}
			next = a.Since + len(body)
			if body == "" {
				body = "(no new output)"
			}
			return fmt.Sprintf("%s\nnext_offset: %d\n%s", header, next, RedactSecrets(body)), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

type JobKill struct{}

type jobKillArgs struct {
	ID int `json:"id"`
}

func (t *JobKill) Name() string  { return "job_kill" }
func (t *JobKill) Class() string { return "bash" }
func (t *JobKill) Description() string {
	return "Stop a background job and its process group."
}
func (t *JobKill) Schema() map[string]any {
	return obj(map[string]any{
		"id": map[string]any{"type": "integer", "description": "job id"},
	}, "id")
}

func (t *JobKill) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[jobKillArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if err := env.JobManager().Kill(a.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("stopping background job %d", a.ID), nil
}
