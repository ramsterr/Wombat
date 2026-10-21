package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type document struct {
	text    string
	version int
}

type Server struct {
	manager    *Manager
	client     *Client
	root       string
	languageID string

	mu   sync.Mutex
	docs map[string]document
}

func (s *Server) Root() string {
	return s.root
}

func (s *Server) Close() {
	if s != nil && s.client != nil {
		s.client.Close()
	}
}

func (s *Server) Definition(ctx context.Context, path string, line, column int) ([]Location, error) {
	uri, text, _, err := s.open(ctx, path)
	if err != nil {
		return nil, err
	}
	pos, err := positionAt(text, line, column)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	params := textDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
	}
	if err := s.client.Request(ctx, "textDocument/definition", params, &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}

func (s *Server) References(ctx context.Context, path string, line, column int) ([]Location, error) {
	uri, text, _, err := s.open(ctx, path)
	if err != nil {
		return nil, err
	}
	pos, err := positionAt(text, line, column)
	if err != nil {
		return nil, err
	}
	var locations []Location
	params := referenceParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
		Context:      referenceContext{IncludeDeclaration: true},
	}
	if err := s.client.Request(ctx, "textDocument/references", params, &locations); err != nil {
		return nil, err
	}
	return locations, nil
}

func (s *Server) Diagnostics(ctx context.Context, path string) ([]Diagnostic, error) {
	uri, _, _, err := s.open(ctx, path)
	if err != nil {
		return nil, err
	}
	if capabilityEnabled(s.client.capabilities().DiagnosticProvider) {
		var report documentDiagnosticReport
		params := documentDiagnosticParams{TextDocument: TextDocumentIdentifier{URI: uri}}
		err := s.client.Request(ctx, "textDocument/diagnostic", params, &report)
		if err == nil {
			if report.Kind == "unchanged" {
				items, _ := s.client.publishedDiagnostics(uri, time.Time{})
				return items, nil
			}
			return report.Items, nil
		}
		if !strings.Contains(err.Error(), "method not found") {
			return nil, err
		}
	}
	since := time.Now()
	if _, _, _, err := s.refresh(ctx, path); err != nil {
		return nil, err
	}
	return s.client.waitDiagnostics(ctx, uri, since)
}

func (s *Server) Rename(ctx context.Context, path string, line, column int, newName string) (map[string][]TextEdit, error) {
	uri, text, _, err := s.open(ctx, path)
	if err != nil {
		return nil, err
	}
	pos, err := positionAt(text, line, column)
	if err != nil {
		return nil, err
	}
	var edit WorkspaceEdit
	params := renameParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
		NewName:      newName,
	}
	if err := s.client.Request(ctx, "textDocument/rename", params, &edit); err != nil {
		return nil, err
	}
	changes := map[string][]TextEdit{}
	for uri, edits := range edit.Changes {
		changes[uri] = append(changes[uri], edits...)
	}
	for _, change := range edit.DocumentChanges {
		if change.Kind != "" || change.URI != "" || change.OldURI != "" || change.NewURI != "" {
			return nil, fmt.Errorf("language server requested an unsupported file operation during rename")
		}
		if change.TextDocument == nil {
			return nil, fmt.Errorf("language server returned a rename edit without a document")
		}
		changes[change.TextDocument.URI] = append(changes[change.TextDocument.URI], change.Edits...)
	}
	if len(changes) == 0 {
		return nil, fmt.Errorf("language server returned no rename edits")
	}
	return changes, nil
}

func (s *Server) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	var raw json.RawMessage
	params := workspaceSymbolParams{Query: query}
	if err := s.client.Request(ctx, "workspace/symbol", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var symbols []Symbol
	if err := json.Unmarshal(raw, &symbols); err == nil {
		return symbols, nil
	}
	var info []struct {
		Name          string    `json:"name"`
		Kind          int       `json:"kind"`
		ContainerName string    `json:"containerName,omitempty"`
		Location      *Location `json:"location,omitempty"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode workspace symbols: %w", err)
	}
	for _, s := range info {
		symbols = append(symbols, Symbol{Name: s.Name, Kind: s.Kind, ContainerName: s.ContainerName, Location: s.Location})
	}
	return symbols, nil
}

func (s *Server) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
	uri, _, _, err := s.open(ctx, path)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	params := documentSymbolParams{TextDocument: TextDocumentIdentifier{URI: uri}}
	if err := s.client.Request(ctx, "textDocument/documentSymbol", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var symbols []DocumentSymbol
	if err := json.Unmarshal(raw, &symbols); err == nil {
		return symbols, nil
	}
	var flat []Symbol
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("decode document symbols: %w", err)
	}
	for _, item := range flat {
		rng := Range{}
		if item.Location != nil {
			rng = item.Location.Range
		}
		symbols = append(symbols, DocumentSymbol{
			Name:           item.Name,
			Kind:           item.Kind,
			Range:          rng,
			SelectionRange: rng,
		})
	}
	return symbols, nil
}

func (s *Server) open(ctx context.Context, path string) (uri, text string, changed bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", false, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", false, err
	}
	text = string(data)
	if strings.IndexByte(text, 0) >= 0 {
		return "", "", false, fmt.Errorf("%s looks like a binary file", path)
	}
	uri = URI(abs)
	lang := languageID(filepath.Ext(abs))
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[uri]
	if !ok {
		s.docs[uri] = document{text: text, version: 1}
		params := didOpenParams{TextDocument: textDocumentItem{
			URI: uri, LanguageID: lang, Version: 1, Text: text,
		}}
		return uri, text, true, s.client.Notify("textDocument/didOpen", params)
	}
	if doc.text == text {
		return uri, text, false, nil
	}
	doc.text = text
	doc.version++
	s.docs[uri] = doc
	params := didChangeParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: uri, Version: doc.version},
		ContentChanges: s.changes(text),
	}
	return uri, text, true, s.client.Notify("textDocument/didChange", params)
}

func (s *Server) refresh(ctx context.Context, path string) (uri, text string, changed bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", false, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", false, err
	}
	text = string(data)
	uri = URI(abs)
	lang := languageID(filepath.Ext(abs))
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[uri]
	if !ok {
		s.docs[uri] = document{text: text, version: 1}
		params := didOpenParams{TextDocument: textDocumentItem{
			URI: uri, LanguageID: lang, Version: 1, Text: text,
		}}
		return uri, text, true, s.client.Notify("textDocument/didOpen", params)
	}
	doc.text = text
	doc.version++
	s.docs[uri] = doc
	params := didChangeParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: uri, Version: doc.version},
		ContentChanges: s.changes(text),
	}
	return uri, text, true, s.client.Notify("textDocument/didChange", params)
}

// Sync pushes the current on-disk contents of paths to the server. Rename
// writes files directly, so the server has to be told what changed.
func (s *Server) Sync(ctx context.Context, paths ...string) error {
	for _, path := range paths {
		if _, _, _, err := s.open(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) changes(text string) []contentChange {
	if s.client.syncKind() != 2 {
		return []contentChange{{Text: text}}
	}
	end := endPosition(text)
	return []contentChange{{Range: &Range{Start: Position{}, End: end}, Text: text}}
}

func endPosition(text string) Position {
	line := strings.Count(text, "\n")
	last := text
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		last = text[i+1:]
	}
	return Position{Line: line, Character: utf16Length(last)}
}

func decodeLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		locations := make([]Location, 0, len(items))
		for _, item := range items {
			if loc, ok := decodeLocation(item); ok {
				locations = append(locations, loc)
			}
		}
		return locations, nil
	}
	if loc, ok := decodeLocation(raw); ok {
		return []Location{loc}, nil
	}
	return nil, nil
}

func decodeLocation(raw json.RawMessage) (Location, bool) {
	var loc Location
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return loc, true
	}
	var link LocationLink
	if err := json.Unmarshal(raw, &link); err == nil && link.TargetURI != "" {
		rng := link.TargetSelectionRange
		if rng.Start.Line == 0 && rng.Start.Character == 0 && rng.End.Line == 0 && rng.End.Character == 0 {
			rng = link.TargetRange
		}
		return Location{URI: link.TargetURI, Range: rng}, true
	}
	return Location{}, false
}

func positionAt(text string, line, column int) (Position, error) {
	if line < 1 {
		return Position{}, fmt.Errorf("line is 1-based")
	}
	if column < 1 {
		return Position{}, fmt.Errorf("column is 1-based")
	}
	start := 0
	for i := 1; i < line; i++ {
		n := strings.IndexByte(text[start:], '\n')
		if n < 0 {
			return Position{}, fmt.Errorf("line %d is outside the file", line)
		}
		start += n + 1
	}
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text) - start
	}
	lineText := text[start : start+end]
	lineText = strings.TrimSuffix(lineText, "\r")
	byteCol := runeOffset(lineText, column-1)
	return Position{Line: line - 1, Character: utf16Length(lineText[:byteCol])}, nil
}

func offsetAt(text string, pos Position) int {
	if pos.Line < 0 {
		return 0
	}
	start := 0
	for i := 0; i < pos.Line; i++ {
		n := strings.IndexByte(text[start:], '\n')
		if n < 0 {
			return len(text)
		}
		start += n + 1
	}
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text) - start
	}
	line := text[start : start+end]
	offset := utf16Offset(line, pos.Character)
	return start + offset
}

func lineAt(text string, line int) string {
	if line < 1 {
		return ""
	}
	start := 0
	for i := 1; i < line; i++ {
		n := strings.IndexByte(text[start:], '\n')
		if n < 0 {
			return ""
		}
		start += n + 1
	}
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text) - start
	}
	return strings.TrimSuffix(text[start:start+end], "\r")
}

func runeOffset(s string, n int) int {
	if n <= 0 {
		return 0
	}
	i := 0
	for pos := range s {
		if i == n {
			return pos
		}
		i++
	}
	return len(s)
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func utf16Offset(s string, units int) int {
	if units <= 0 {
		return 0
	}
	used := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if used+w > units {
			return i
		}
		used += w
		if used == units {
			_, size := utf8.DecodeRuneInString(s[i:])
			return i + size
		}
	}
	return len(s)
}

func capabilityEnabled(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "false"
}

// OffsetAt converts an LSP position to a byte offset in text. It is exported
// for tools that need to apply a WorkspaceEdit.
func OffsetAt(text string, pos Position) int {
	return offsetAt(text, pos)
}
