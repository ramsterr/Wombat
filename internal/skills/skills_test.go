package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPrefersProjectAndParsesFrontmatter(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	writeFile(t, filepath.Join(cwd, ".wombat", "skills", "review.md"), "---\nname: review\ndescription: project review\n---\nUse <SKILL_DIR>.")
	writeFile(t, filepath.Join(home, "skills", "review.md"), "---\nname: review\ndescription: global review\n---\nglobal")
	writeFile(t, filepath.Join(cwd, ".agents", "skills", "video", "SKILL.md"), "---\nname: video\ndescription: >\n  motion and\n  video work\n---\nMake video.")

	list, problems := Load(cwd, home)
	if len(problems) > 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(list) != 2 {
		t.Fatalf("skills = %+v", list)
	}
	review, err := Find("review", cwd, home)
	if err != nil {
		t.Fatal(err)
	}
	if review.Description != "project review" {
		t.Errorf("project skill should win, got %q", review.Description)
	}
	if !strings.Contains(review.PromptFor(), filepath.Join(cwd, ".wombat", "skills")) {
		t.Errorf("<SKILL_DIR> not resolved: %q", review.PromptFor())
	}
	video, err := Find("video", cwd, home)
	if err != nil {
		t.Fatal(err)
	}
	if video.Description != "motion and video work" {
		t.Errorf("folded frontmatter = %q", video.Description)
	}
	if !strings.Contains(video.Prompt, "Make video.") {
		t.Errorf("body = %q", video.Prompt)
	}
}

func TestInstallAndRemove(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "project")
	source := filepath.Join(root, "incoming", "deep-research.md")
	writeFile(t, source, "# Research\n\nSearch widely.\n")

	installed, err := Install(source, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Name != "deep-research" {
		t.Fatalf("installed = %+v", installed)
	}
	if installed[0].Description != "Research" {
		t.Errorf("description = %q", installed[0].Description)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "deep-research.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Find("deep-research", cwd, home); err != nil {
		t.Fatal(err)
	}
	path, err := Remove("deep-research", home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("remove left %s", path)
	}
}

func TestInstallDirectoryAndDuplicateName(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dir := filepath.Join(root, "bundle")
	writeFile(t, filepath.Join(dir, "one.md"), "---\nname: one\ndescription: first\n---\none")
	writeFile(t, filepath.Join(dir, "sub", "SKILL.md"), "---\nname: two\ndescription: second\n---\ntwo")

	installed, err := Install(dir, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed = %+v", installed)
	}
}

func TestRouterPromptIsCompactAndRoutesVideo(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "project")
	writeFile(t, filepath.Join(home, "skills", "hyperframes.md"),
		"---\nname: hyperframes\ndescription: video\n---\nSECRET FULL BODY")

	prompt := RouterPrompt(cwd, home)
	for _, want := range []string{"## Skills", "use_skill", "hyperframes", "video"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("router prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "SECRET FULL BODY") {
		t.Error("router prompt must not embed skill bodies")
	}
	if len(prompt) > 3000 {
		t.Errorf("router prompt is %d characters", len(prompt))
	}
}
