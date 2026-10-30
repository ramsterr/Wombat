package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFuzzyCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	out := run(t, &Edit{}, env, `{"path":"f.txt","old_string":"alpha\nbeta","new_string":"ALPHA\nBETA"}`)
	if !strings.Contains(out, "matched ignoring") {
		t.Fatalf("output = %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "ALPHA\r\nBETA\r\ngamma\r\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestEditFuzzyWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("func main() {\n\tx := 1\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	out := run(t, &Edit{}, env, `{"path":"f.go","old_string":"    x := 1\n    return","new_string":"    x := 2\n    return"}`)
	if !strings.Contains(out, "matched ignoring") {
		t.Fatalf("output = %q", out)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "x := 2") {
		t.Fatalf("file = %q", data)
	}
}

func TestEditFuzzyStillRejectsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("  one\n  one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	err := runErr(t, &Edit{}, env, `{"path":"f.txt","old_string":"one","new_string":"two"}`)
	if !strings.Contains(err.Error(), "matches 2 times") {
		t.Fatalf("error = %v", err)
	}
}

func TestWritePreviewPassedToPermit(t *testing.T) {
	dir := t.TempDir()
	var preview string
	env := &Env{Dir: dir, PermitPreview: func(kind, detail, p string) Decision {
		preview = p
		return Decision{Allow: true}
	}}
	run(t, &Write{}, env, `{"path":"a.txt","content":"hello\n"}`)
	if !strings.Contains(preview, "+hello") || !strings.Contains(preview, "+++ b/a.txt") {
		t.Fatalf("preview = %q", preview)
	}
}

func TestApplyPatchMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	patch := `*** Begin Patch
*** Update File: a.txt
@@
 one
-two
+TWO
*** Add File: sub/b.txt
+hello
+world
*** Delete File: a.txt
*** End Patch`
	// a file cannot be both updated and deleted in one patch, so use two patches
	patch = `*** Begin Patch
*** Update File: a.txt
@@
 one
-two
+TWO
*** Add File: sub/b.txt
+hello
+world
*** End Patch`
	raw, _ := json.Marshal(applyPatchArgs{Patch: patch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	b, _ := os.ReadFile(filepath.Join(dir, "sub", "b.txt"))
	if string(a) != "one\nTWO\n" || string(b) != "hello\nworld\n" {
		t.Fatalf("a=%q b=%q", a, b)
	}

	deletePatch := `*** Begin Patch
*** Delete File: a.txt
*** End Patch`
	raw, _ = json.Marshal(applyPatchArgs{Patch: deletePatch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err != nil {
		t.Fatalf("delete patch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt should be deleted")
	}
}

func TestApplyPatchRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	patch := `*** Begin Patch
*** Update File: a.txt
@@
-one
+two
*** Update File: missing.txt
@@
-x
+y
*** End Patch`
	raw, _ := json.Marshal(applyPatchArgs{Patch: patch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("patch should fail")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "one\n" {
		t.Fatalf("a.txt changed despite failed patch: %q", data)
	}
}

func TestApplyPatchRejectsDuplicateFile(t *testing.T) {
	env := testEnv(t.TempDir())
	patch := `*** Begin Patch
*** Add File: a.txt
+a
*** Add File: a.txt
+b
*** End Patch`
	raw, _ := json.Marshal(applyPatchArgs{Patch: patch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("duplicate file should fail")
	}
}

func TestLooseEditPreservesTrailingBlankLines(t *testing.T) {
	content := "first\n  target  \n\n\n"
	updated, _, fuzzy, err := replaceText(content, "first\ntarget", "first\nreplaced", false)
	if err != nil {
		t.Fatal(err)
	}
	if !fuzzy {
		t.Fatal("expected a fuzzy match")
	}
	if updated != "first\nreplaced\n\n\n" {
		t.Fatalf("got %q", updated)
	}
}

func TestApplyPatchRollbackRestoresDeletedFileAndMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bye\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	patch := `*** Begin Patch
*** Update File: a.txt
@@
-one
+two
*** Delete File: b.txt
*** Add File: sub/x.txt
+x
*** End Patch`
	raw, _ := json.Marshal(applyPatchArgs{Patch: patch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err == nil {
		t.Fatal("patch should fail when the parent is a file")
	}
	for _, tc := range []struct {
		name string
		want string
		mode os.FileMode
	}{{"a.txt", "one\n", 0o600}, {"b.txt", "bye\n", 0o640}} {
		data, err := os.ReadFile(filepath.Join(dir, tc.name))
		if err != nil {
			t.Fatalf("%s was not restored: %v", tc.name, err)
		}
		if string(data) != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, data, tc.want)
		}
		st, _ := os.Stat(filepath.Join(dir, tc.name))
		if st.Mode().Perm() != tc.mode {
			t.Fatalf("%s mode = %o, want %o", tc.name, st.Mode().Perm(), tc.mode)
		}
	}
}

func TestReplaceTextTokenSequenceIgnoresFormatting(t *testing.T) {
	content := "package p\n\nfunc main() {\n\tif x {\n\t\ty()\n\t}\n}\n"
	old := "if  x  {\n    y( )\n}"
	new := "\tif x {\n\t\ty2()\n\t}"
	updated, count, fuzzy, err := replaceTextIn("main.go", content, old, new, false)
	if err != nil {
		t.Fatal(err)
	}
	if !fuzzy || count != 1 {
		t.Fatalf("fuzzy=%v count=%d", fuzzy, count)
	}
	want := "package p\n\nfunc main() {\n\tif x {\n\t\ty2()\n\t}\n}\n"
	if updated != want {
		t.Fatalf("got %q, want %q", updated, want)
	}
}

func TestReplaceTextTokenSequenceKeepsStringWhitespaceSignificant(t *testing.T) {
	content := "package p\n\nvar msg = \"a  b\"\n"
	_, _, _, err := replaceTextIn("main.go", content, "msg = \"a b\"", "msg = \"a   c\"", false)
	if err == nil {
		t.Fatal("string literals with different whitespace should not match")
	}
}

func TestReplaceTextTokenSequenceNormalizesComments(t *testing.T) {
	content := "package p\n\n// hello   world\nfunc f() { x:=1 }\n"
	old := "// hello world\nfunc f() { x := 1 }"
	new := "// done\nfunc f() { x:=2 }"
	updated, _, _, err := replaceTextIn("main.go", content, old, new, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "package p\n\n// done\nfunc f() { x:=2 }\n"
	if updated != want {
		t.Fatalf("got %q, want %q", updated, want)
	}
}

func TestReplaceTextTokenSequenceRejectsAmbiguity(t *testing.T) {
	content := "if  x { a() }\nif x{a()}\n"
	old := "if x { a() }"
	_, count, _, err := replaceTextIn("main.go", content, old, "if y { b() }", false)
	if err == nil {
		t.Fatal("expected an ambiguous token match")
	}
	if count != 2 || !strings.Contains(err.Error(), "matches 2 times") {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if !strings.Contains(err.Error(), "candidate lines 1, 2") {
		t.Fatalf("missing candidate lines: %v", err)
	}
}

func TestReplaceTextTokenSequencePreservesCRLF(t *testing.T) {
	content := "package p\r\n\r\nfunc f() {\r\n\tif  x {\r\n\t\ty()\r\n\t}\r\n}\r\n"
	old := "if x {\n    y( )\n}"
	new := "\tif x {\n\t\ty2()\n\t}"
	updated, _, fuzzy, err := replaceTextIn("main.go", content, old, new, false)
	if err != nil {
		t.Fatal(err)
	}
	if !fuzzy {
		t.Fatal("expected a fuzzy token match")
	}
	want := "package p\r\n\r\nfunc f() {\r\n\tif x {\r\n\t\ty2()\r\n\t}\r\n}\r\n"
	if updated != want {
		t.Fatalf("got %q, want %q", updated, want)
	}
}

func TestApplyPatchMergesDuplicateUpdateSections(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	patch := `*** Begin Patch
*** Update File: a.txt
@@
-one
+ONE
*** Update File: a.txt
@@
-three
+THREE
*** End Patch`
	raw, _ := json.Marshal(applyPatchArgs{Patch: patch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ONE\ntwo\nTHREE\n" {
		t.Fatalf("a.txt = %q", data)
	}
}

func TestApplyPatchAcceptsMissingEndMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(dir)
	patch := `*** Begin Patch
*** Update File: a.txt
@@
-old
+new`
	raw, _ := json.Marshal(applyPatchArgs{Patch: patch})
	if _, err := (&ApplyPatch{}).Run(context.Background(), raw, env); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "new\n" {
		t.Fatalf("a.txt = %q", data)
	}
}
