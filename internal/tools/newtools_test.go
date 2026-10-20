package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchRunsReadOnlyCalls(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("gamma\n"), 0o644)
	env := &Env{Dir: dir, Permit: func(k, d string) Decision { return Decision{Allow: true} }}

	out, err := (&Batch{}).Run(context.Background(), json.RawMessage(`{"calls":[
		{"tool":"read","path":"a.txt"},
		{"tool":"read","path":"b.txt"},
		{"tool":"grep","pattern":"gamma"}
	]}`), env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"alpha", "gamma", "--- 1. read"} {
		if !strings.Contains(out, want) {
			t.Errorf("the batch output is missing %q:\n%s", want, out)
		}
	}
}

func TestBatchRejectsOtherTools(t *testing.T) {
	env := &Env{Dir: t.TempDir(), Permit: func(k, d string) Decision { return Decision{Allow: true} }}
	out, err := (&Batch{}).Run(context.Background(), json.RawMessage(`{"calls":[{"tool":"write","path":"x"}]}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("the batch should report the bad call: %s", out)
	}
}

func TestMultiEditAppliesEveryEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644)
	env := &Env{Dir: dir, Permit: func(k, d string) Decision { return Decision{Allow: true} }}

	out, err := (&MultiEdit{}).Run(context.Background(), json.RawMessage(`{"path":"f.txt","edits":[
		{"old_string":"one","new_string":"1"},
		{"old_string":"three","new_string":"3"}
	]}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 edits") {
		t.Errorf("out = %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "1\ntwo\n3\n" {
		t.Errorf("file = %q", data)
	}
}

func TestMultiEditIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("one\ntwo\n"), 0o644)
	env := &Env{Dir: dir, Permit: func(k, d string) Decision { return Decision{Allow: true} }}

	_, err := (&MultiEdit{}).Run(context.Background(), json.RawMessage(`{"path":"f.txt","edits":[
		{"old_string":"one","new_string":"1"},
		{"old_string":"missing","new_string":"x"}
	]}`), env)
	if err == nil {
		t.Fatal("expected the edit to fail")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\ntwo\n" {
		t.Errorf("the file was changed even though an edit failed: %q", data)
	}
}

func TestMultiEditRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("dup\ndup\n"), 0o644)
	env := &Env{Dir: dir, Permit: func(k, d string) Decision { return Decision{Allow: true} }}
	_, err := (&MultiEdit{}).Run(context.Background(), json.RawMessage(`{"path":"f.txt","edits":[
		{"old_string":"dup","new_string":"x"}
	]}`), env)
	if err == nil || !strings.Contains(err.Error(), "matches 2 times") {
		t.Fatalf("error = %v", err)
	}
}

func TestSpillWritesBigOutputToAFile(t *testing.T) {
	dir := t.TempDir()
	env := &Env{Dir: dir}
	env.SetSpillDir(filepath.Join(dir, "spill"))
	big := strings.Repeat("line of output\n", 20000)
	out, ok := env.Spill("grep-output.txt", big)
	if !ok {
		t.Fatal("the big output should have been spilled")
	}
	if !strings.Contains(out, "read the parts you need") {
		t.Errorf("out = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "spill", "grep-output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(big) {
		t.Errorf("the spilled file has %d bytes, want %d", len(data), len(big))
	}
	small, ok := env.Spill("x.txt", "tiny")
	if ok || small != "tiny" {
		t.Errorf("a small output should not be spilled: %q", small)
	}
}

func TestHTMLToText(t *testing.T) {
	page := `<html><head><style>p{color:red}</style><script>var x=1</script></head>
	<body><h1>Title</h1><p>Some <b>bold</b> text &amp; more.</p></body></html>`
	text := htmlToText(page)
	if strings.Contains(text, "var x") || strings.Contains(text, "color:red") {
		t.Errorf("scripts and styles should be removed: %q", text)
	}
	if !strings.Contains(text, "Title") || !strings.Contains(text, "bold") {
		t.Errorf("text = %q", text)
	}
	if !strings.Contains(text, "&") || strings.Contains(text, "&amp;") {
		t.Errorf("entities should be decoded: %q", text)
	}
}
