package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ramsterr/Wombat/internal/lsp"
)

// LSP answers semantic questions from a real language server. It is the
// difference between finding an identifier with grep and knowing what it
// means, where it is defined, and what uses it.
type LSP struct{}

type lspArgs struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Query  string `json:"query"`
}

func (t *LSP) Name() string  { return "lsp" }
func (t *LSP) Class() string { return "read" }
func (t *LSP) Description() string {
	return "Ask a language server for semantic answers: definition, references, diagnostics, completion, workspace symbols, or a file outline. Go uses gopls; TypeScript, Python, Rust, C/C++, Java, Ruby, PHP, Lua, C#, Swift, Kotlin, and shell use their standard servers when installed. Prefer this over grep when you need the exact symbol."
}
func (t *LSP) Schema() map[string]any {
	return obj(map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"definition", "references", "diagnostics", "completion", "symbols", "outline"}},
		"path":   desc("file to inspect; for symbols, a file or directory to identify the language"),
		"line":   map[string]any{"type": "integer", "description": "1-based line for definition/references/completion"},
		"column": map[string]any{"type": "integer", "description": "1-based column for definition/references/completion"},
		"query":  desc("symbol name or fragment for workspace symbols"),
	}, "action")
}

func (t *LSP) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[lspArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}
	switch action {
	case "definition", "references", "diagnostics", "outline", "completion":
		if strings.TrimSpace(a.Path) == "" {
			return "", fmt.Errorf("%s needs a path", action)
		}
		path, err := lspPath(env, a.Path)
		if err != nil {
			return "", err
		}
		server, err := env.LSPManager().Server(ctx, path)
		if err != nil {
			return "", err
		}
		switch action {
		case "definition":
			if err := requirePosition(a.Line, a.Column); err != nil {
				return "", err
			}
			locs, err := server.Definition(ctx, path, a.Line, a.Column)
			if err != nil {
				return "", err
			}
			if len(locs) == 0 {
				return "no definition found", nil
			}
			return finishLSP(env, "lsp-definition.txt", formatLocations(env, locs)), nil
		case "references":
			if err := requirePosition(a.Line, a.Column); err != nil {
				return "", err
			}
			locs, err := server.References(ctx, path, a.Line, a.Column)
			if err != nil {
				return "", err
			}
			if len(locs) == 0 {
				return "no references found", nil
			}
			return finishLSP(env, "lsp-references.txt", formatLocations(env, locs)), nil
		case "diagnostics":
			items, err := server.Diagnostics(ctx, path)
			if err != nil {
				return "", err
			}
			return finishLSP(env, "lsp-diagnostics.txt", formatDiagnostics(env, path, items)), nil
		case "completion":
			if err := requirePosition(a.Line, a.Column); err != nil {
				return "", err
			}
			items, err := server.Completion(ctx, path, a.Line, a.Column)
			if err != nil {
				return "", err
			}
			if len(items) == 0 {
				return "no completions found", nil
			}
			return finishLSP(env, "lsp-completions.txt", formatCompletions(items)), nil
		case "outline":
			symbols, err := server.DocumentSymbols(ctx, path)
			if err != nil {
				return "", err
			}
			if len(symbols) == 0 {
				return "no symbols found", nil
			}
			return finishLSP(env, "lsp-outline.txt", formatDocumentSymbols(env, path, symbols)), nil
		}
	case "symbols":
		path, err := lspSymbolPath(env, a.Path)
		if err != nil {
			return "", err
		}
		server, err := env.LSPManager().Server(ctx, path)
		if err != nil {
			return "", err
		}
		symbols, err := server.WorkspaceSymbols(ctx, a.Query)
		if err != nil {
			return "", err
		}
		if len(symbols) == 0 {
			return "no symbols found", nil
		}
		return finishLSP(env, "lsp-symbols.txt", formatSymbols(env, symbols)), nil
	}
	return "", fmt.Errorf("action must be definition, references, diagnostics, completion, symbols, or outline")
}

func lspPath(env *Env, path string) (string, error) {
	if err := env.CheckPath("read", path); err != nil {
		return "", err
	}
	return env.ResolvePath(path)
}

func lspSymbolPath(env *Env, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return representativeFile(env.Dir)
	}
	abs, err := lspPath(env, path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		if !lsp.Supports(abs) {
			return "", fmt.Errorf("no language server configured for %s", abs)
		}
		return abs, nil
	}
	return representativeFile(abs)
}

func representativeFile(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "dist", "build", "target", ".next", "__pycache__", ".wombat":
				return filepath.SkipDir
			}
			return nil
		}
		if lsp.Supports(path) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("no supported source file found in %s", root)
	}
	return found, nil
}

func requirePosition(line, column int) error {
	if line < 1 || column < 1 {
		return fmt.Errorf("line and column are 1-based and required")
	}
	return nil
}

func formatLocations(env *Env, locations []lsp.Location) string {
	var b strings.Builder
	for _, loc := range locations {
		path, err := lsp.Path(loc.URI)
		if err != nil {
			fmt.Fprintf(&b, "%s:%d:%d\n", loc.URI, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
			continue
		}
		rel := relativePath(env.Dir, path)
		fmt.Fprintf(&b, "%s:%d:%d", rel, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
		if withinRoot(env.Dir, path) {
			if text := sourceLine(path, loc.Range.Start.Line); text != "" {
				fmt.Fprintf(&b, "  %s", text)
			}
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatCompletions(items []lsp.CompletionItem) string {
	limit := len(items)
	if limit > 100 {
		limit = 100
	}
	var b strings.Builder
	for _, item := range items[:limit] {
		fmt.Fprintf(&b, "%-40s %-9s %s", clipText(item.Label, 40), completionKind(item.Kind), item.Detail)
		if doc := completionDoc(item.Documentation); doc != "" {
			fmt.Fprintf(&b, "  — %s", doc)
		}
		b.WriteByte('\n')
	}
	header := fmt.Sprintf("%d completion(s)", len(items))
	if limit < len(items) {
		header += fmt.Sprintf(" (showing %d)", limit)
	}
	return header + ":\n" + strings.TrimRight(b.String(), "\n")
}

func completionDoc(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return firstLine(text)
	}
	var markup struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &markup); err == nil {
		return firstLine(markup.Value)
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clipText(strings.TrimSpace(s), 100)
}

func completionKind(kind int) string {
	names := []string{"", "text", "method", "function", "constructor", "field", "variable", "class", "interface", "module", "property", "unit", "value", "enum", "keyword", "snippet", "color", "file", "reference", "folder", "enum-member", "constant", "struct", "event", "operator", "type-parameter"}
	if kind > 0 && kind < len(names) {
		return names[kind]
	}
	return "item"
}

func formatDiagnostics(env *Env, path string, items []lsp.Diagnostic) string {
	rel := relativePath(env.Dir, path)
	if len(items) == 0 {
		return "no diagnostics for " + rel
	}
	var b strings.Builder
	for _, d := range items {
		severity := diagnosticSeverity(d.Severity)
		fmt.Fprintf(&b, "%s:%d:%d  %s: %s", rel, d.Range.Start.Line+1, d.Range.Start.Character+1, severity, d.Message)
		if d.Source != "" {
			fmt.Fprintf(&b, " (%s)", d.Source)
		}
		b.WriteByte('\n')
	}
	return fmt.Sprintf("%d diagnostic(s) for %s:\n%s", len(items), rel, strings.TrimRight(b.String(), "\n"))
}

func formatSymbols(env *Env, symbols []lsp.Symbol) string {
	var b strings.Builder
	for _, s := range symbols {
		pos := ""
		if s.Location != nil {
			if path, err := lsp.Path(s.Location.URI); err == nil {
				pos = relativePath(env.Dir, path)
				if s.Location.Range.Start.Line > 0 || s.Location.Range.Start.Character > 0 {
					pos += fmt.Sprintf(":%d:%d", s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
				}
			} else {
				pos = s.Location.URI
			}
		}
		if pos != "" {
			pos += "  "
		}
		fmt.Fprintf(&b, "%s%-10s %s", pos, symbolKind(s.Kind), s.Name)
		if s.ContainerName != "" {
			fmt.Fprintf(&b, "  (%s)", s.ContainerName)
		}
		b.WriteByte('\n')
	}
	return fmt.Sprintf("%d symbol(s):\n%s", len(symbols), strings.TrimRight(b.String(), "\n"))
}

func formatDocumentSymbols(env *Env, path string, symbols []lsp.DocumentSymbol) string {
	rel := relativePath(env.Dir, path)
	var b strings.Builder
	var walk func(items []lsp.DocumentSymbol, depth int)
	walk = func(items []lsp.DocumentSymbol, depth int) {
		for _, s := range items {
			indent := strings.Repeat("  ", depth)
			fmt.Fprintf(&b, "%s%s:%d  %-10s %s\n", indent, rel, s.SelectionRange.Start.Line+1, symbolKind(s.Kind), s.Name)
			walk(s.Children, depth+1)
		}
	}
	walk(symbols, 0)
	return strings.TrimRight(b.String(), "\n")
}

func sourceLine(path string, zeroLine int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	start := lsp.OffsetAt(string(data), lsp.Position{Line: zeroLine})
	if start < 0 || start > len(data) {
		return ""
	}
	end := strings.IndexByte(string(data[start:]), '\n')
	if end < 0 {
		end = len(data) - start
	}
	return clipText(strings.TrimSpace(string(data[start:start+end])), 120)
}

func clipText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func diagnosticSeverity(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	}
	return "diagnostic"
}

func symbolKind(kind int) string {
	names := []string{"", "file", "module", "namespace", "package", "class", "method", "property", "field", "constructor", "enum", "interface", "function", "variable", "constant", "string", "number", "boolean", "array", "object", "key", "null", "enum-member", "struct", "event", "operator", "type-parameter"}
	if kind > 0 && kind < len(names) {
		return names[kind]
	}
	return "symbol"
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func finishLSP(env *Env, name, text string) string {
	text = strings.TrimRight(text, "\n")
	if len(text) > 24<<10 {
		spilled, _ := env.Spill(name, text)
		return spilled
	}
	return text
}

// Rename is a language-server rename. It computes every edit first, shows
// one combined diff, and applies the files atomically.
type Rename struct{}

type renameToolArgs struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	NewName string `json:"new_name"`
	Apply   *bool  `json:"apply"`
}

type renameFile struct {
	path  string
	rel   string
	old   string
	new   string
	edits int
	mode  os.FileMode
}

func (t *Rename) Name() string  { return "rename" }
func (t *Rename) Class() string { return "edit" }
func (t *Rename) Description() string {
	return "Rename a symbol semantically through a language server. Computes all edits first, shows a combined diff, and applies them atomically. Set apply=false to preview only."
}
func (t *Rename) Schema() map[string]any {
	return obj(map[string]any{
		"path":     desc("file containing the symbol"),
		"line":     map[string]any{"type": "integer", "description": "1-based line of the symbol"},
		"column":   map[string]any{"type": "integer", "description": "1-based column of the symbol"},
		"new_name": desc("new identifier"),
		"apply":    map[string]any{"type": "boolean", "description": "apply the rename (default true); false returns the diff"},
	}, "path", "line", "column", "new_name")
}

func (t *Rename) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[renameToolArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if err := requirePosition(a.Line, a.Column); err != nil {
		return "", err
	}
	if !validIdentifier(a.NewName) || token.Lookup(a.NewName).IsKeyword() {
		return "", fmt.Errorf("%q is not a valid identifier", a.NewName)
	}
	path, err := lspPath(env, a.Path)
	if err != nil {
		return "", err
	}
	server, err := env.LSPManager().Server(ctx, path)
	if err != nil {
		return "", err
	}
	changes, err := server.Rename(ctx, path, a.Line, a.Column, a.NewName)
	if err != nil {
		return "", err
	}
	files, err := buildRenameFiles(env, changes)
	if err != nil {
		return "", err
	}
	apply := true
	if a.Apply != nil {
		apply = *a.Apply
	}
	if !apply {
		return renamePreview(files, a.NewName), nil
	}
	if err := applyEditFiles(env, files, "before rename"); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("renamed to %s (%d edit(s) in %d file(s))", a.NewName, countRenameEdits(files), len(files))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.path)
	}
	if err := server.Sync(ctx, paths...); err != nil {
		return msg + fmt.Sprintf("; language server refresh failed: %v", err), nil
	}
	return msg, nil
}

// applyEditFiles checks, snapshots, and writes computed edits as one unit,
// rolling back every file if a write fails.
func applyEditFiles(env *Env, files []renameFile, reason string) error {
	if len(files) == 0 {
		return fmt.Errorf("the language server returned no edits")
	}
	paths := make([]string, 0, len(files))
	var preview strings.Builder
	for _, f := range files {
		paths = append(paths, f.rel)
		preview.WriteString(DiffPreview(f.rel, f.old, f.new))
		preview.WriteString("\n")
	}
	if err := env.CheckPaths("edit", paths, strings.TrimSpace(preview.String())); err != nil {
		return err
	}
	for _, f := range files {
		if err := env.checkOldContent(f.path); err != nil {
			return err
		}
	}
	if err := env.Snapshot(reason, paths...); err != nil {
		return err
	}
	written := make([]renameFile, 0, len(files))
	rollback := func() {
		for i := len(written) - 1; i >= 0; i-- {
			f := written[i]
			_ = os.WriteFile(f.path, []byte(f.old), f.mode)
		}
	}
	for _, f := range files {
		st, err := os.Stat(f.path)
		if err != nil {
			rollback()
			return err
		}
		f.mode = st.Mode().Perm()
		if err := os.WriteFile(f.path, []byte(f.new), f.mode); err != nil {
			rollback()
			return err
		}
		written = append(written, f)
	}
	return nil
}

func buildRenameFiles(env *Env, changes map[string][]lsp.TextEdit) ([]renameFile, error) {
	files := make([]renameFile, 0, len(changes))
	for uri, edits := range changes {
		path, err := lsp.Path(uri)
		if err != nil {
			return nil, fmt.Errorf("language server returned an edit for %s: %w", uri, err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(env.Dir, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("rename would edit %s outside the workspace", path)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		old := string(data)
		updated, count, err := applyTextEdits(old, edits)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		files = append(files, renameFile{path: abs, rel: filepath.ToSlash(rel), old: old, new: updated, edits: count})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

func applyTextEdits(text string, edits []lsp.TextEdit) (string, int, error) {
	type span struct{ start, end int }
	spans := make([]span, 0, len(edits))
	repls := make([]string, 0, len(edits))
	for _, edit := range edits {
		start := lsp.OffsetAt(text, edit.Range.Start)
		end := lsp.OffsetAt(text, edit.Range.End)
		if start < 0 || end < start || end > len(text) {
			return "", 0, fmt.Errorf("language server returned an invalid edit range")
		}
		spans = append(spans, span{start: start, end: end})
		repls = append(repls, edit.NewText)
	}
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		if spans[order[i]].start != spans[order[j]].start {
			return spans[order[i]].start < spans[order[j]].start
		}
		return spans[order[i]].end < spans[order[j]].end
	})
	for i := 1; i < len(order); i++ {
		if spans[order[i]].start < spans[order[i-1]].end {
			return "", 0, fmt.Errorf("language server returned overlapping edits")
		}
	}
	updated := text
	for i := len(order) - 1; i >= 0; i-- {
		idx := order[i]
		updated = updated[:spans[idx].start] + repls[idx] + updated[spans[idx].end:]
	}
	return updated, len(spans), nil
}

func renamePreview(files []renameFile, newName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "rename to %s (%d edit(s) in %d file(s)):\n", newName, countRenameEdits(files), len(files))
	for _, f := range files {
		b.WriteString(DiffPreview(f.rel, f.old, f.new))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func countRenameEdits(files []renameFile) int {
	total := 0
	for _, f := range files {
		total += f.edits
	}
	return total
}

func validIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' && r != '$' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$' {
			return false
		}
	}
	return true
}

// Refactor exposes the language server's structural code actions. The server
// parses and type-checks the code, so extract/inline/organize-import edits
// are semantic rather than textual. Rename stays a separate tool because it
// is the most common operation and has its own preview flow.
type Refactor struct{}

type refactorArgs struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Title     string `json:"title,omitempty"`
	Index     int    `json:"index,omitempty"`
	Apply     *bool  `json:"apply,omitempty"`
}

func (t *Refactor) Name() string  { return "refactor" }
func (t *Refactor) Class() string { return "edit" }
func (t *Refactor) Description() string {
	return "Apply an AST-aware refactoring from the language server: extract or inline code, organize imports, rewrite expressions, or fix a diagnostic at a position or selection. Without title/index it lists the available actions. The server parses and type-checks the code, so edits are structural, not string replacement. Use rename for symbol renames."
}
func (t *Refactor) Schema() map[string]any {
	return obj(map[string]any{
		"path":       desc("file to refactor"),
		"line":       map[string]any{"type": "integer", "description": "1-based line of the cursor or selection start"},
		"column":     map[string]any{"type": "integer", "description": "1-based column of the cursor or selection start"},
		"end_line":   map[string]any{"type": "integer", "description": "optional 1-based selection end line"},
		"end_column": map[string]any{"type": "integer", "description": "optional 1-based selection end column"},
		"kind":       desc("optional code action kind filter, e.g. refactor.extract, refactor.inline, source.organizeImports"),
		"title":      desc("action title to apply (case-insensitive); omit to list actions"),
		"index":      map[string]any{"type": "integer", "description": "1-based action index from the list, used instead of title"},
		"apply":      map[string]any{"type": "boolean", "description": "apply the action (default true); false returns the diff or the command it would run"},
	}, "path", "line", "column")
}

func (t *Refactor) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[refactorArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	if err := requirePosition(a.Line, a.Column); err != nil {
		return "", err
	}
	if a.EndLine > 0 && a.EndColumn < 1 {
		return "", fmt.Errorf("end_column is required with end_line")
	}
	if a.EndColumn > 0 && a.EndLine < 1 {
		return "", fmt.Errorf("end_line is required with end_column")
	}
	path, err := lspPath(env, a.Path)
	if err != nil {
		return "", err
	}
	server, err := env.LSPManager().Server(ctx, path)
	if err != nil {
		return "", err
	}
	var only []string
	if kind := strings.TrimSpace(a.Kind); kind != "" {
		only = []string{kind}
	}
	actions, err := server.CodeActions(ctx, path, a.Line, a.Column, a.EndLine, a.EndColumn, only)
	if err != nil {
		return "", err
	}
	if len(actions) == 0 {
		return "no code actions at that position", nil
	}
	if strings.TrimSpace(a.Title) == "" && a.Index == 0 {
		return formatCodeActions(actions), nil
	}
	action, err := chooseAction(actions, a.Title, a.Index)
	if err != nil {
		return "", err
	}
	apply := true
	if a.Apply != nil {
		apply = *a.Apply
	}
	if action.Edit == nil && action.Command == nil {
		return "", fmt.Errorf("action %q has no edit or command", action.Title)
	}

	var files []renameFile
	if action.Edit != nil {
		changes, err := lsp.WorkspaceChanges(*action.Edit)
		if err != nil {
			return "", err
		}
		files, err = buildRenameFiles(env, changes)
		if err != nil {
			return "", err
		}
	}
	if !apply {
		return refactorPreview(action, files), nil
	}
	msg := fmt.Sprintf("applied %s", action.Title)
	if len(files) > 0 {
		if err := applyEditFiles(env, files, "before refactor"); err != nil {
			return "", err
		}
		msg += fmt.Sprintf(" (%d edit(s) in %d file(s))", countRenameEdits(files), len(files))
		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f.path)
		}
		if err := server.Sync(ctx, paths...); err != nil {
			msg += fmt.Sprintf("; language server refresh failed: %v", err)
		}
	}
	if action.Command != nil {
		if err := server.ExecuteCommand(ctx, *action.Command); err != nil {
			return "", err
		}
		msg += fmt.Sprintf("; executed %s", action.Command.Command)
		if len(files) == 0 {
			// Servers may answer a command by asking the client to apply an
			// edit. Give the request a moment to arrive, then apply it through
			// the same permission and rollback path as a direct edit.
			applied := server.TakeAppliedEdits()
			for i := 0; i < 20 && len(applied) == 0; i++ {
				time.Sleep(25 * time.Millisecond)
				applied = server.TakeAppliedEdits()
			}
			for _, edit := range applied {
				changes, err := lsp.WorkspaceChanges(edit)
				if err != nil {
					return "", err
				}
				commandFiles, err := buildRenameFiles(env, changes)
				if err != nil {
					return "", err
				}
				if len(commandFiles) == 0 {
					continue
				}
				if err := applyEditFiles(env, commandFiles, "before refactor"); err != nil {
					return "", err
				}
				msg += fmt.Sprintf(" (%d edit(s) in %d file(s))", countRenameEdits(commandFiles), len(commandFiles))
				paths := make([]string, 0, len(commandFiles))
				for _, f := range commandFiles {
					paths = append(paths, f.path)
				}
				if err := server.Sync(ctx, paths...); err != nil {
					msg += fmt.Sprintf("; language server refresh failed: %v", err)
				}
			}
		}
	}
	return msg, nil
}

func formatCodeActions(actions []lsp.CodeAction) string {
	var b strings.Builder
	for i, action := range actions {
		kind := action.Kind
		if kind == "" {
			kind = "action"
		}
		markers := make([]string, 0, 2)
		if action.Edit != nil {
			markers = append(markers, "edit")
		}
		if action.Command != nil {
			markers = append(markers, "command")
		}
		if action.IsPreferred {
			markers = append(markers, "preferred")
		}
		marker := ""
		if len(markers) > 0 {
			marker = " [" + strings.Join(markers, ", ") + "]"
		}
		fmt.Fprintf(&b, "%d. %s  (%s)%s\n", i+1, action.Title, kind, marker)
	}
	return fmt.Sprintf("%d code action(s):\n%s", len(actions), strings.TrimRight(b.String(), "\n"))
}

func chooseAction(actions []lsp.CodeAction, title string, index int) (lsp.CodeAction, error) {
	if index != 0 {
		if index < 1 || index > len(actions) {
			return lsp.CodeAction{}, fmt.Errorf("index %d is outside the list (1-%d)", index, len(actions))
		}
		return actions[index-1], nil
	}
	want := strings.ToLower(strings.TrimSpace(title))
	for _, action := range actions {
		if strings.ToLower(action.Title) == want {
			return action, nil
		}
	}
	var matches []lsp.CodeAction
	for _, action := range actions {
		if strings.Contains(strings.ToLower(action.Title), want) {
			matches = append(matches, action)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return lsp.CodeAction{}, fmt.Errorf("no action matches %q; available:\n%s", title, formatCodeActions(actions))
	}
	names := make([]string, 0, len(matches))
	for _, action := range matches {
		names = append(names, action.Title)
	}
	return lsp.CodeAction{}, fmt.Errorf("%q matches several actions (%s); use the exact title or index", title, strings.Join(names, "; "))
}

func refactorPreview(action lsp.CodeAction, files []renameFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apply %s", action.Title)
	if len(files) > 0 {
		fmt.Fprintf(&b, " (%d edit(s) in %d file(s))", countRenameEdits(files), len(files))
	}
	if action.Command != nil {
		fmt.Fprintf(&b, "; then run %s", action.Command.Command)
	}
	b.WriteString(":\n")
	for _, f := range files {
		b.WriteString(DiffPreview(f.rel, f.old, f.new))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
