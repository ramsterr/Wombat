package todo

import (
	"strings"
	"testing"
)

func TestNormalizeDefaultsAndValidation(t *testing.T) {
	items, err := Normalize([]Item{
		{Content: "  inspect the page  ", Status: InProgress},
		{Content: "fix the layout"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Content != "inspect the page" || items[0].Priority != Medium {
		t.Fatalf("normalized first item = %+v", items[0])
	}
	if items[1].Status != Pending {
		t.Fatalf("missing status should be pending: %+v", items[1])
	}
	if !Incomplete(items) {
		t.Fatal("pending items should be incomplete")
	}

	if _, err := Normalize(nil); err == nil {
		t.Fatal("an empty list should be rejected")
	}
	if _, err := Normalize([]Item{
		{Content: "one", Status: InProgress},
		{Content: "two", Status: InProgress},
	}); err == nil {
		t.Fatal("two in-progress items should be rejected")
	}
	if _, err := Normalize([]Item{{Content: "one", Status: "blocked"}}); err == nil {
		t.Fatal("an unknown status should be rejected")
	}
}

func TestDoneAndFormat(t *testing.T) {
	items := []Item{
		{Content: "inspect", Status: Completed},
		{Content: "fix", Status: InProgress},
		{Content: "old", Status: Cancelled},
	}
	if !Incomplete(items) {
		t.Fatal("an in-progress item should be incomplete")
	}
	if done, total := Counts(items); done != 1 || total != 3 {
		t.Fatalf("counts = %d/%d", done, total)
	}
	out := Format(items)
	for _, want := range []string{"Todos 1/3 complete", "[x] 1. inspect", "[>] 2. fix", "[-] 3. old"} {
		if !strings.Contains(out, want) {
			t.Errorf("format missing %q:\n%s", want, out)
		}
	}
	items[1].Status = Completed
	if Incomplete(items) {
		t.Fatal("completed and cancelled items should not be incomplete")
	}
}
