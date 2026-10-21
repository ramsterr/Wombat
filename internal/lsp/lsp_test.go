package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPositionEncoding(t *testing.T) {
	text := "a😀b\nsecond"
	pos, err := positionAt(text, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if pos.Line != 0 || pos.Character != 3 {
		t.Fatalf("position = %+v, want line 0 character 3", pos)
	}
	if got := offsetAt(text, Position{Line: 0, Character: 3}); got != len("a😀") {
		t.Fatalf("offset = %d, want %d", got, len("a😀"))
	}
}

func TestURIFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a b.go")
	uri := URI(path)
	got, err := Path(uri)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("uri = %q", uri)
	}
}

func TestGoplsSemantics(t *testing.T) {
	if _, ok := findExecutable("gopls"); !ok {
		t.Skip("gopls is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package demo

func Add(a, b int) int { return a + b }

func Use() int {
	return Add(1, 2)
}
`
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m := NewManager(dir)
	defer m.Close()
	server, err := m.Server(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	locs, err := server.Definition(ctx, path, 6, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Fatalf("definition = %+v", locs)
	}
	refs, err := server.References(ctx, path, 6, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) < 2 {
		t.Fatalf("references = %+v", refs)
	}
	symbols, err := server.WorkspaceSymbols(ctx, "Add")
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) == 0 {
		t.Fatal("workspace symbols found nothing")
	}
	items, err := server.Completion(ctx, path, 6, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("completion returned nothing")
	}
	outline, err := server.DocumentSymbols(ctx, path)
	if err != nil || len(outline) == 0 {
		t.Fatalf("outline = %+v, err = %v", outline, err)
	}
	changes, err := server.Rename(ctx, path, 6, 9, "Sum")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("rename returned no changes")
	}
	if _, err := server.Diagnostics(ctx, path); err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
}
