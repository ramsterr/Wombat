package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
