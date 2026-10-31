package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackgroundJobLifecycle(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	out, err := (&Bash{}).Run(context.Background(), json.RawMessage(`{"command":"sleep 0.2; echo finished","background":true}`), env)
	if err != nil {
		t.Fatalf("start background job: %v", err)
	}
	if !strings.Contains(out, "started background job 1") {
		t.Fatalf("start output = %q", out)
	}
	raw, _ := json.Marshal(jobOutputArgs{ID: 1, WaitMS: 5000})
	polled, err := (&JobOutput{}).Run(context.Background(), raw, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(polled, "finished") || !strings.Contains(polled, "exited 0") {
		t.Fatalf("output = %q", polled)
	}
}

func TestJobOutputOffset(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	raw, _ := json.Marshal(bashArgs{Command: "printf 'alpha'; sleep 0.1; printf 'beta'", Background: true})
	if _, err := (&Bash{}).Run(context.Background(), raw, env); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(jobOutputArgs{ID: 1, WaitMS: 3000})
	out, err := (&JobOutput{}).Run(context.Background(), args, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alphabeta") {
		t.Fatalf("first read = %q", out)
	}
	offset := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "next_offset: ") {
			_, _ = fmt.Sscanf(line, "next_offset: %d", &offset)
		}
	}
	if offset != len("alphabeta") {
		t.Fatalf("offset = %d", offset)
	}
	args, _ = json.Marshal(jobOutputArgs{ID: 1, Since: offset})
	out, err = (&JobOutput{}).Run(context.Background(), args, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(no new output)") {
		t.Fatalf("second read = %q", out)
	}
}

func TestJobKill(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	raw, _ := json.Marshal(bashArgs{Command: "sleep 30", Background: true})
	if _, err := (&Bash{}).Run(context.Background(), raw, env); err != nil {
		t.Fatal(err)
	}
	kill, _ := json.Marshal(jobKillArgs{ID: 1})
	if _, err := (&JobKill{}).Run(context.Background(), kill, env); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := env.JobManager().Get(1)
		if done, _, _, _, _ := j.state(); done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background job did not stop")
}

func TestIsolateSeparatesJobs(t *testing.T) {
	lead := &Env{Dir: t.TempDir()}
	worker := *lead
	worker.Isolate()
	if worker.JobManager() == lead.JobManager() {
		t.Fatal("worker should not share the lead's job manager")
	}
}

func TestJobOutputWaitDoesNotHangWithoutOutput(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	job, err := env.JobManager().Start(context.Background(), env, "sleep 5", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer env.JobManager().Close()
	start := time.Now()
	out, err := (&JobOutput{}).Run(context.Background(), json.RawMessage(`{"id":`+itoaJob(job.ID)+`,"wait_ms":40}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("job_output waited too long: %s", time.Since(start))
	}
	if !strings.Contains(out, "running") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestJobManagerCloseStopsJobs(t *testing.T) {
	env := &Env{Dir: t.TempDir()}
	job, err := env.JobManager().Start(context.Background(), env, "sleep 30", 0)
	if err != nil {
		t.Fatal(err)
	}
	env.JobManager().Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, _, _, killed, _ := job.state(); done && killed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job was not stopped")
}

func itoaJob(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
