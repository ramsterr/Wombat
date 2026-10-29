package todo

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSetPersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "s.todos.json")
	store := New(path)
	if err := store.Set([]Item{
		{ID: "a", Content: "  first  ", Status: InProgress},
		{Content: "", Status: Pending},
		{Content: "second", Status: "bogus"},
	}); err != nil {
		t.Fatal(err)
	}
	items := store.Items()
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Content != "first" || items[0].Status != InProgress {
		t.Errorf("first = %+v", items[0])
	}
	if items[1].ID != "t2" || items[1].Status != Pending {
		t.Errorf("second = %+v", items[1])
	}

	reloaded := New(path)
	if got := reloaded.Items(); len(got) != 2 || got[1].Content != "second" {
		t.Fatalf("reloaded = %+v", got)
	}
	if done, total := reloaded.Progress(); done != 0 || total != 2 {
		t.Errorf("progress = %d/%d", done, total)
	}
	if !strings.Contains(reloaded.Brief(), "first") || !strings.Contains(reloaded.Brief(), "0/2 complete") {
		t.Errorf("brief = %q", reloaded.Brief())
	}
}

func TestStoreCapsAndDeduplicates(t *testing.T) {
	store := New("")
	list := make([]Item, 0, MaxItems+5)
	for i := 0; i < MaxItems+5; i++ {
		list = append(list, Item{ID: "same", Content: "x", Status: Pending})
	}
	if err := store.Set(list); err != nil {
		t.Fatal(err)
	}
	if len(store.Items()) != 1 {
		t.Fatalf("duplicate ids should collapse, got %d", len(store.Items()))
	}

	list = list[:0]
	for i := 0; i < MaxItems+5; i++ {
		list = append(list, Item{Content: "item", Status: Completed})
	}
	if err := store.Set(list); err != nil {
		t.Fatal(err)
	}
	if len(store.Items()) != MaxItems {
		t.Fatalf("items = %d, want %d", len(store.Items()), MaxItems)
	}
}

func TestPathForSession(t *testing.T) {
	if got := PathForSession("/tmp/a/session.jsonl"); got != "/tmp/a/session.todos.json" {
		t.Errorf("got %q", got)
	}
	if got := PathForSession(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestFromStringsStartsFirstInProgress(t *testing.T) {
	items := FromStrings([]string{"a", "", "b"})
	if len(items) != 2 || items[0].Status != InProgress || items[1].Status != Pending {
		t.Fatalf("items = %+v", items)
	}
}
