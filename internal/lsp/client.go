package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxMessageBytes = 64 << 20
	diagnosticWait  = 5 * time.Second
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type diagnosticState struct {
	items []Diagnostic
	at    time.Time
}

type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr *tailBuffer

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResult
	err     error
	done    chan struct{}

	doneOnce  sync.Once
	closeOnce sync.Once

	diagMu sync.Mutex
	diag   map[string]diagnosticState

	applyMu sync.Mutex
	applied []WorkspaceEdit

	caps serverCapabilities
	root string
}

func startClient(ctx context.Context, root string, command []string) (*Client, error) {
	if len(command) == 0 {
		return nil, errors.New("empty language server command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = root
	configureProcess(cmd)
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start %s: %w", command[0], err)
	}
	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReader(stdout),
		stderr:  stderr,
		pending: map[int]chan rpcResult{},
		done:    make(chan struct{}),
		diag:    map[string]diagnosticState{},
		root:    root,
	}
	go c.readLoop()
	go func() {
		err := cmd.Wait()
		c.markDone(fmt.Errorf("language server exited: %w", err))
	}()

	params := initializeParams{
		ProcessID: os.Getpid(),
		RootURI:   URI(root),
		Capabilities: clientCapabilities{
			Workspace: workspaceCapabilities{
				WorkspaceFolders:      true,
				Symbol:                map[string]any{},
				Configuration:         true,
				ApplyEdit:             true,
				DidChangeWatchedFiles: map[string]any{},
			},
			TextDocument: textDocumentCapabilities{
				Synchronization:    synchronizationCapabilities{DidSave: true},
				Definition:         map[string]any{"linkSupport": true},
				References:         map[string]any{},
				Rename:             map[string]any{"prepareSupport": true},
				Diagnostic:         map[string]any{},
				PublishDiagnostics: map[string]any{"relatedInformation": true},
				DocumentSymbol:     map[string]any{"hierarchicalDocumentSymbolSupport": true},
				Completion: map[string]any{
					"completionItem": map[string]any{
						"snippetSupport":      true,
						"documentationFormat": []string{"markdown", "plaintext"},
					},
				},
				CodeAction: map[string]any{
					"codeActionLiteralSupport": map[string]any{
						"codeActionKind": map[string]any{
							"valueSet": []string{
								"", "quickfix", "refactor", "refactor.extract",
								"refactor.inline", "refactor.rewrite", "source",
								"source.organizeImports", "source.fixAll",
							},
						},
					},
					"isPreferredSupport": true,
					"disabledSupport":    true,
				},
			},
			Window: windowCapabilities{WorkDoneProgress: true},
		},
		WorkspaceFolders: []workspaceFolder{{URI: URI(root), Name: filepathBase(root)}},
	}
	var init initializeResult
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.Request(initCtx, "initialize", params, &init); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize %s: %w%s", command[0], err, stderr.suffix())
	}
	c.caps = init.Capabilities
	if err := c.Notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) readLoop() {
	for {
		payload, err := readMessage(c.reader)
		if err != nil {
			c.markDone(err)
			return
		}
		var msg wireMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			c.markDone(fmt.Errorf("bad language server message: %w", err))
			return
		}
		if msg.Method != "" {
			if len(msg.ID) > 0 && string(msg.ID) != "null" {
				c.answerRequest(msg.ID, msg.Method, msg.Params)
				continue
			}
			c.handleNotification(msg.Method, msg.Params)
			continue
		}
		if len(msg.ID) == 0 || string(msg.ID) == "null" {
			continue
		}
		var id int
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if msg.Error != nil {
			ch <- rpcResult{err: fmt.Errorf("language server: %s (%d)", msg.Error.Message, msg.Error.Code)}
		} else {
			ch <- rpcResult{result: msg.Result}
		}
	}
}

func (c *Client) answerRequest(id json.RawMessage, method string, params json.RawMessage) {
	var result any
	switch method {
	case "workspace/configuration":
		var p workspaceConfigurationParams
		_ = json.Unmarshal(params, &p)
		result = make([]any, len(p.Items))
	case "workspace/applyEdit":
		var p struct {
			Edit WorkspaceEdit `json:"edit"`
		}
		if json.Unmarshal(params, &p) == nil && (len(p.Edit.Changes) > 0 || len(p.Edit.DocumentChanges) > 0) {
			c.applyMu.Lock()
			c.applied = append(c.applied, p.Edit)
			c.applyMu.Unlock()
			result = map[string]any{"applied": true}
		} else {
			result = map[string]any{"applied": false}
		}
	case "window/showMessageRequest":
		result = nil
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "workspace/workspaceFolders":
		result = nil
	default:
		result = nil
	}
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	_ = c.send(msg)
}

func (c *Client) handleNotification(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p publishDiagnosticsParams
	if json.Unmarshal(params, &p) != nil {
		return
	}
	items := append([]Diagnostic(nil), p.Diagnostics...)
	c.diagMu.Lock()
	c.diag[p.URI] = diagnosticState{items: items, at: time.Now()}
	c.diagMu.Unlock()
}

// TakeAppliedEdits returns and clears workspace edits a server asked the
// client to apply while executing a command.
func (c *Client) TakeAppliedEdits() []WorkspaceEdit {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	edits := c.applied
	c.applied = nil
	return edits
}

func (c *Client) Request(ctx context.Context, method string, params, result any) error {
	id := c.requestID()
	ch := make(chan rpcResult, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.send(msg); err != nil {
		c.removePending(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.done:
		c.removePending(id)
		return c.terminalError()
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result != nil && len(r.result) > 0 && string(r.result) != "null" {
			if err := json.Unmarshal(r.result, result); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	}
}

func (c *Client) Notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

func (c *Client) requestID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

func (c *Client) removePending(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > maxMessageBytes {
		return fmt.Errorf("language server message too large (%d bytes)", len(data))
	}
	select {
	case <-c.done:
		return c.terminalError()
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *Client) markDone(err error) {
	c.doneOnce.Do(func() {
		c.mu.Lock()
		if c.err == nil {
			c.err = err
		}
		for id, ch := range c.pending {
			delete(c.pending, id)
			ch <- rpcResult{err: c.err}
		}
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *Client) terminalError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("language server closed")
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.Request(ctx, "shutdown", nil, nil)
		cancel()
		_ = c.Notify("exit", nil)
		terminateProcess(c.cmd)
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			terminateProcess(c.cmd)
		}
		_ = c.stdin.Close()
		c.markDone(errors.New("language server closed"))
	})
}

func (c *Client) stderrSuffix() string {
	return c.stderr.suffix()
}

func (c *Client) capabilities() serverCapabilities {
	return c.caps
}

// syncKind returns the server's textDocumentSync change mode: 0 none, 1 full,
// or 2 incremental. Servers that omit it are treated as full-text.
func (c *Client) syncKind() int {
	raw := c.capabilities().TextDocumentSync
	if len(raw) == 0 || string(raw) == "null" {
		return 1
	}
	var kind int
	if err := json.Unmarshal(raw, &kind); err == nil {
		return kind
	}
	var object struct {
		Change int `json:"change"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return object.Change
	}
	return 1
}

func (c *Client) publishedDiagnostics(uri string, since time.Time) ([]Diagnostic, bool) {
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	state, ok := c.diag[uri]
	if !ok || state.at.Before(since) {
		return nil, false
	}
	return append([]Diagnostic(nil), state.items...), true
}

func (c *Client) waitDiagnostics(ctx context.Context, uri string, since time.Time) ([]Diagnostic, error) {
	deadline := time.NewTimer(diagnosticWait)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if items, ok := c.publishedDiagnostics(uri, since); ok {
			return items, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, c.terminalError()
		case <-deadline.C:
			return nil, fmt.Errorf("language server did not report diagnostics within %s", diagnosticWait)
		case <-ticker.C:
		}
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("bad Content-Length: %q", value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("message has no Content-Length")
	}
	if length > maxMessageBytes {
		return nil, fmt.Errorf("message too large (%d bytes)", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return data, nil
}

type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if b.buf.Len() > 32<<10 {
		b.buf.Next(b.buf.Len() - 16<<10)
	}
	return n, err
}

func (b *tailBuffer) suffix() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() == 0 {
		return ""
	}
	return ": " + strings.TrimSpace(b.buf.String())
}

func filepathBase(path string) string {
	path = strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}
