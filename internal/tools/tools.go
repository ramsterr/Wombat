package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ramsterr/Wombat/internal/browser"
	"github.com/ramsterr/Wombat/internal/embeddings"
	"github.com/ramsterr/Wombat/internal/images"
	"github.com/ramsterr/Wombat/internal/lsp"
)

type Checkpointer interface {
	Snapshot(reason string, paths []string) (string, error)
}

type Decision struct {
	Allow    bool
	Always   bool
	Feedback string
}

// toolsDeniedNote is what the model sees when a call is refused and there is
// no explanation: it must not claim the work happened.
const toolsDeniedNote = "the user declined this call. Do not claim it was done. " +
	"Continue without it, or tell the user what you need in order to proceed."

type Env struct {
	Dir           string
	Permit        func(kind, detail string) Decision
	PermitPreview func(kind, detail, preview string) Decision
	ReadOnly      bool
	AllowOutside  bool
	DenyDangerous bool
	DenyNetwork   bool
	DenyGit       bool
	DenyOutside   bool
	DenyMutate    bool
	Mode          string
	Checkpoint    Checkpointer

	// media collects files a tool wants the model to look at, such as an
	// image the agent downloaded. The agent owns the list.
	media *mediaSink

	// spillDir is where a large tool result is written so the model can
	// read the parts it needs instead of carrying the whole thing in its
	// context. The agent sets it.
	spillDir string

	// jobs owns background processes started through this environment.
	jobs *JobManager

	// language servers are shared by every semantic tool call in this Env.
	lsp *lsp.Manager

	// Images is the image generation/editing client, when configured.
	Images *images.Client

	// Embeddings is the semantic-search embedder, when configured. When it
	// is nil, semantic search uses the offline local embedder.
	Embeddings *embeddings.Client

	// IndexDir is where semantic-search indexes are cached. The CLI points
	// it at ~/.wombat/index so indexes survive sessions.
	IndexDir string

	// BlockedRoots are directories a shell command must not write into.
	// Isolated writers set this to the lead workspace so an absolute path in
	// a command cannot escape the worktree.
	BlockedRoots []string

	// WritePaths, when non-empty, restricts write and edit tools to these
	// repo-relative (or absolute) files. Writers set it from their declared
	// ownership list. Bash commands are not covered.
	WritePaths []string

	// browser owns the local Chromium session used for visual verification.
	browser *browser.Manager

	// AskUser asks the user a question and waits for the answer. It is nil
	// where nobody can answer, such as inside a worker.
	AskUser func(q Question) (string, error)
}

// mediaSink is a pointer so copying an Env (the orchestrator does that for
// workers) does not copy the mutex or accidentally share the collected list
// between agents.
type mediaSink struct {
	mu   sync.Mutex
	list *[]Media
}

func (s *mediaSink) add(m Media) {
	if s == nil || s.list == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.list = append(*s.list, m)
}

// SetSpillDir sets the directory large tool results are written to.
func (e *Env) SetSpillDir(dir string) {
	if e == nil {
		return
	}
	e.spillDir = dir
}

// SpillDir is the directory large tool results and prepared images go to.
func (e *Env) SpillDir() string {
	if e == nil {
		return ""
	}
	return e.spillDir
}

// Media is an image a tool produced for the model to look at. It may live in
// memory (browser screenshots) or on disk (images read or generated).
type Media struct {
	// Path is the file on disk, when there is one.
	Path string
	// Data is the image bytes for media that is never written to disk.
	Data []byte
	// Size is the byte length when known.
	Size int64
	// MIME is the content type.
	MIME string
	// Name is a friendly name for the model.
	Name string
	// Note describes a transformation, such as a downscale.
	Note string
}

// SetMediaSink gives the environment the list that collects files for the
// model. The agent owns it and drains it after every tool call.
func (e *Env) SetMediaSink(m *[]Media) {
	if e == nil {
		return
	}
	if m == nil {
		e.media = nil
		return
	}
	e.media = &mediaSink{list: m}
}

// AddMedia hands an image to the model as an attachment. It is safe to call
// from parallel read-only tools.
func (e *Env) AddMedia(m Media) {
	if e == nil || e.media == nil {
		return
	}
	e.media.add(m)
}

// Isolate gives a copied Env its own process and media state. Workers use
// this so they cannot see, kill, or attach the lead's jobs and files.
func (e *Env) Isolate() {
	if e == nil {
		return
	}
	e.media = nil
	e.jobs = nil
	e.lsp = nil
	e.Images = nil
	e.browser = nil
	e.PermitPreview = nil
}

// jobManagerInit guards the one-time construction of an Env's manager:
// background tools may be called concurrently.
var jobManagerInit sync.Mutex

// CloseJobs stops any background jobs this Env started. It is safe to call
// when no job manager exists.
func (e *Env) CloseJobs() {
	if e == nil || e.jobs == nil {
		return
	}
	e.jobs.Close()
}

// JobManager returns the background process manager, creating it on first
// use.
func (e *Env) JobManager() *JobManager {
	if e == nil {
		return nil
	}
	jobManagerInit.Lock()
	defer jobManagerInit.Unlock()
	if e.jobs == nil {
		e.jobs = NewJobManager()
	}
	return e.jobs
}

// lspManagerInit guards the one-time construction of the language server
// manager, just like jobManagerInit guards background jobs.
var lspManagerInit sync.Mutex

// LSPManager returns the language server manager for this workspace.
func (e *Env) LSPManager() *lsp.Manager {
	if e == nil {
		return nil
	}
	lspManagerInit.Lock()
	defer lspManagerInit.Unlock()
	if e.lsp == nil {
		e.lsp = lsp.NewManager(e.Dir)
	}
	return e.lsp
}

// CloseLSP shuts down every language server this Env started.
func (e *Env) CloseLSP() {
	if e == nil {
		return
	}
	lspManagerInit.Lock()
	m := e.lsp
	e.lsp = nil
	lspManagerInit.Unlock()
	if m != nil {
		m.Close()
	}
}

// browserManagerInit guards the one-time construction of the browser manager.
var browserManagerInit sync.Mutex

// BrowserManager returns the browser manager for this workspace.
func (e *Env) BrowserManager() *browser.Manager {
	if e == nil {
		return nil
	}
	browserManagerInit.Lock()
	defer browserManagerInit.Unlock()
	if e.browser == nil {
		e.browser = browser.NewManager(e.Dir)
	}
	return e.browser
}

// CloseBrowser shuts down the browser this Env started.
func (e *Env) CloseBrowser() {
	if e == nil {
		return
	}
	browserManagerInit.Lock()
	m := e.browser
	e.browser = nil
	browserManagerInit.Unlock()
	if m != nil {
		m.Close()
	}
}

// guardDangerous asks before a dangerous command runs. It never silently
// allows one when nobody can approve it.
func (e *Env) guardDangerous(command string) error {
	if e == nil || !IsDangerousCommand(command) {
		return nil
	}
	if e.DenyDangerous {
		return fmt.Errorf("dangerous command refused: %s", command)
	}
	if e.Permit == nil {
		return fmt.Errorf("dangerous command refused because no one is available to approve it: %s", command)
	}
	return e.permit("dangerous", command)
}

func (e *Env) Snapshot(reason string, paths ...string) error {
	if e == nil || e.Checkpoint == nil || e.ReadOnly {
		return nil
	}
	if _, err := e.Checkpoint.Snapshot(reason, paths); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}
	return nil
}

type Tool interface {
	Name() string
	Class() string
	Description() string
	Schema() map[string]any
	Run(ctx context.Context, args json.RawMessage, env *Env) (string, error)
}

func Registry() []Tool {
	return []Tool{
		&Bash{}, &Read{}, &Write{}, &Edit{}, &MultiEdit{}, &ApplyPatch{}, &Rename{}, &Refactor{},
		&Glob{}, &Grep{}, &GitStatus{}, &GitDiff{}, &GitLog{}, &GitShow{}, &GitBlame{}, &Batch{}, &Symbols{}, &LSP{}, &SemanticSearch{}, &Browser{}, &ImageGen{}, &ImageSearch{}, &Verify{},
		&VerifySite{}, &Replace{},
		&WebFetch{}, &WebSearch{},
		&Jobs{}, &JobOutput{}, &JobKill{},
	}
}

// RegistryWithQuestion is the full tool set, including the question tool the
// lead uses to hand decisions back to the user.
func RegistryWithQuestion() []Tool {
	return append(Registry(), &QuestionTool{})
}

func (e *Env) Check(kind, detail string) error {
	if err := e.checkPolicy(kind, detail); err != nil {
		return err
	}
	return e.permit(kind, detail)
}

func (e *Env) checkPolicy(kind, detail string) error {
	if e.ReadOnly {
		if kind == "write" || kind == "edit" {
			return fmt.Errorf("read-only mode: %s is disabled", kind)
		}
		if kind == "bash" {
			if MayMutate(detail) {
				return fmt.Errorf("read-only mode: commands that change files are disabled: %s", detail)
			}
			if IsDangerousCommand(detail) {
				return fmt.Errorf("read-only mode: dangerous commands are disabled: %s", detail)
			}
		}
	}
	if e.DenyMutate && (kind == "write" || kind == "edit") {
		return fmt.Errorf("chat mode: %s is disabled — switch to create mode to change files", kind)
	}
	if (kind == "write" || kind == "edit") && len(e.WritePaths) > 0 {
		if err := e.checkWriteOwnership(detail); err != nil {
			return err
		}
	}
	if kind == "bash" {
		if e.DenyGit && IsGitMutatingCommand(detail) {
			return fmt.Errorf("git mutations are disabled for this worker: %s", detail)
		}
	}
	return nil
}

func (e *Env) permit(kind, detail string) error {
	return e.permitPreview(kind, detail, "")
}

func (e *Env) permitPreview(kind, detail, preview string) error {
	if kind == "read" {
		return nil
	}
	if kind == "sensitive-read" && e.Permit == nil && e.PermitPreview == nil {
		return fmt.Errorf("refusing to read a credentials file without approval: %s", detail)
	}
	if e.Permit == nil && e.PermitPreview == nil {
		return nil
	}
	var d Decision
	switch {
	case preview != "" && e.PermitPreview != nil:
		d = e.PermitPreview(kind, detail, preview)
	case e.Permit != nil:
		d = e.Permit(kind, detail)
	case e.PermitPreview != nil:
		d = e.PermitPreview(kind, detail, "")
	}
	if d.Allow {
		return nil
	}
	if d.Feedback != "" {
		return fmt.Errorf("permission denied by user: %s", d.Feedback)
	}
	return fmt.Errorf("permission denied by user: %s", toolsDeniedNote)
}

func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if len(args) == 0 {
		return v, nil
	}
	err := json.Unmarshal(args, &v)
	return v, err
}

func desc(what string) map[string]any {
	return map[string]any{"type": "string", "description": what}
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// spillThreshold is how large a tool result may get before it is written to
// a file instead of being carried in the model's context.
const spillThreshold = 200 * 1024

// Spill writes a large result to a file and returns a short pointer to it.
// Claude Code does the same with tool output: the model gets the path and
// reads only the parts it needs, instead of carrying the whole thing.
func (e *Env) Spill(name, content string) (string, bool) {
	if e == nil || e.spillDir == "" || len(content) <= spillThreshold {
		return content, false
	}
	if err := os.MkdirAll(e.spillDir, 0o755); err != nil {
		return content, false
	}
	path := filepath.Join(e.spillDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return content, false
	}
	lines := strings.Count(content, "\n") + 1
	head := content
	if len(head) > spillThreshold {
		head = head[:spillThreshold]
	}
	return fmt.Sprintf("%s\n[the full output is %d lines in %s - read the parts you need]", head, lines, path), true
}

func Truncate(s string, maxLines int, headFraction float64, maxBytes int) string {
	if maxLines <= 0 && maxBytes <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	tooManyLines := maxLines > 0 && len(lines) > maxLines
	tooManyBytes := maxBytes > 0 && len(s) > maxBytes
	if !tooManyLines && !tooManyBytes {
		return s
	}
	if !tooManyLines {
		head := maxBytes * 2 / 3
		if head > len(s) {
			head = len(s)
		}
		tail := len(s) - maxBytes/3
		if tail < head {
			tail = head
		}
		return s[:head] + fmt.Sprintf("\n… [%d bytes omitted] …\n", len(s)-head-(len(s)-tail)) + s[tail:]
	}
	head := int(float64(maxLines) * headFraction)
	if head < 1 {
		head = 1
	}
	tail := maxLines - head
	if tail < 0 {
		tail = 0
	}
	omitted := len(lines) - head - tail
	out := append([]string{}, lines[:head]...)
	out = append(out, fmt.Sprintf("… [%d lines omitted] …", omitted))
	out = append(out, lines[len(lines)-tail:]...)
	return strings.Join(out, "\n")
}
