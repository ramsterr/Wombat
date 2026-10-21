// Package project detects how a repository is built, tested, and checked.
// The agent uses it so "run the project's tests" means the project's own
// commands, not a command the model guessed.
package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Commands are the detected project commands. An empty field means the
// project does not appear to have that check.
type Commands struct {
	Test      string
	Build     string
	Lint      string
	Typecheck string
	Format    string
}

// Project is one detected repository.
type Project struct {
	Kind     string
	Root     string
	Manager  string
	Commands Commands
}

// KindNames are the check kinds Verify understands.
var KindNames = []string{"test", "build", "typecheck", "lint", "format"}

// Command returns the detected command for one kind.
func (p Project) Command(kind string) (string, bool) {
	var cmd string
	switch kind {
	case "test":
		cmd = p.Commands.Test
	case "build":
		cmd = p.Commands.Build
	case "typecheck":
		cmd = p.Commands.Typecheck
	case "lint":
		cmd = p.Commands.Lint
	case "format":
		cmd = p.Commands.Format
	}
	return cmd, cmd != ""
}

// Detect identifies the project at dir. Detection is deliberately based on
// files that already exist, so it never guesses a command for a stack that
// is not there.
func Detect(dir string) Project {
	p := Project{Root: dir}
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case has("go.mod") || has("go.work"):
		p.Kind = "go"
		p.Commands = goCommands(dir)
	case has("Cargo.toml"):
		p.Kind = "rust"
		p.Commands = rustCommands(dir)
	case has("package.json"):
		p.Kind = "node"
		p.Manager = nodeManager(dir)
		p.Commands = nodeCommands(dir, p.Manager)
	case has("pyproject.toml") || has("setup.py") || has("requirements.txt"):
		p.Kind = "python"
		p.Commands = pythonCommands(dir)
	case has("pom.xml"):
		p.Kind = "maven"
		p.Commands = Commands{Test: "mvn test", Build: "mvn package -DskipTests", Typecheck: "mvn compile"}
	case has("build.gradle") || has("build.gradle.kts"):
		p.Kind = "gradle"
		p.Commands = Commands{Test: "./gradlew test", Build: "./gradlew build", Typecheck: "./gradlew compileJava"}
	case has("Gemfile"):
		p.Kind = "ruby"
		p.Commands = Commands{Test: "bundle exec rspec", Lint: "bundle exec rubocop"}
		p.Commands = fillMakeTargets(dir, p.Commands)
	case has("composer.json"):
		p.Kind = "php"
		p.Commands = Commands{Test: "composer test", Lint: "composer lint"}
	case has("CMakeLists.txt"):
		p.Kind = "cmake"
		p.Commands = Commands{Build: "cmake --build build", Test: "ctest --test-dir build --output-on-failure"}
	case has("Makefile") || has("makefile"):
		p.Kind = "make"
		p.Commands = fillMakeTargets(dir, Commands{})
	default:
		p.Kind = "unknown"
	}
	if p.Kind != "make" {
		p.Commands = fillMakeTargets(dir, p.Commands)
	}
	return p
}

func goCommands(dir string) Commands {
	c := Commands{
		Test:      "go test ./...",
		Build:     "go build ./...",
		Typecheck: "go vet ./...",
		Format:    `test -z "$(gofmt -l .)"`,
	}
	if fileExists(dir, ".golangci.yml") || fileExists(dir, ".golangci.yaml") {
		c.Lint = "golangci-lint run"
	}
	return c
}

func rustCommands(dir string) Commands {
	c := Commands{
		Test:      "cargo test",
		Build:     "cargo build",
		Typecheck: "cargo check",
		Lint:      "cargo clippy -- -D warnings",
		Format:    "cargo fmt --check",
	}
	return c
}

func nodeManager(dir string) string {
	switch {
	case fileExists(dir, "pnpm-lock.yaml"):
		return "pnpm"
	case fileExists(dir, "yarn.lock"):
		return "yarn"
	case fileExists(dir, "bun.lockb") || fileExists(dir, "bun.lock"):
		return "bun"
	}
	return "npm"
}

func nodeCommands(dir, manager string) Commands {
	run := func(script string) string {
		switch manager {
		case "pnpm", "bun":
			return manager + " run " + script
		default:
			return manager + " run " + script
		}
	}
	var scripts map[string]string
	if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(raw, &pkg) == nil {
			scripts = pkg.Scripts
		}
	}
	c := Commands{}
	if _, ok := scripts["test"]; ok {
		c.Test = run("test")
	}
	if _, ok := scripts["build"]; ok {
		c.Build = run("build")
	}
	if _, ok := scripts["lint"]; ok {
		c.Lint = run("lint")
	}
	if _, ok := scripts["typecheck"]; ok {
		c.Typecheck = run("typecheck")
	} else if _, ok := scripts["type-check"]; ok {
		c.Typecheck = run("type-check")
	} else if fileExists(dir, "tsconfig.json") {
		c.Typecheck = execTool(manager, "tsc", "--noEmit")
	}
	if _, ok := scripts["format:check"]; ok {
		c.Format = run("format:check")
	} else if _, ok := scripts["format"]; ok {
		c.Format = run("format")
	} else if hasAny(dir, ".prettierrc", ".prettierrc.json", ".prettierrc.yml", ".prettierrc.yaml", "prettier.config.js", "prettier.config.mjs") {
		c.Format = execTool(manager, "prettier", "--check", ".")
	}
	if c.Lint == "" && hasAny(dir, ".eslintrc", ".eslintrc.json", ".eslintrc.js", ".eslintrc.cjs", "eslint.config.js", "eslint.config.mjs", "eslint.config.ts") {
		c.Lint = execTool(manager, "eslint", ".")
	}
	return c
}

func execTool(manager, tool string, args ...string) string {
	switch manager {
	case "pnpm":
		return "pnpm exec " + tool + " " + strings.Join(args, " ")
	case "yarn":
		return "yarn " + tool + " " + strings.Join(args, " ")
	case "bun":
		return "bunx " + tool + " " + strings.Join(args, " ")
	default:
		return "npx " + tool + " " + strings.Join(args, " ")
	}
}

func pythonCommands(dir string) Commands {
	c := Commands{}
	if hasAny(dir, "pytest.ini", "conftest.py", "tests", "test") || fileExists(dir, "pyproject.toml") {
		c.Test = "pytest -q"
	}
	if fileExists(dir, "pyproject.toml") && fileContains(dir, "pyproject.toml", "[tool.ruff") {
		c.Lint = "ruff check ."
		c.Format = "ruff format --check ."
	}
	if fileExists(dir, "pyproject.toml") && fileContains(dir, "pyproject.toml", "[tool.mypy") {
		c.Typecheck = "mypy ."
	} else if hasAny(dir, "mypy.ini", ".mypy.ini") {
		c.Typecheck = "mypy ."
	}
	if fileExists(dir, "pyproject.toml") && fileContains(dir, "pyproject.toml", "build-system") {
		c.Build = "python -m build"
	}
	return c
}

var makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+)\s*:`)

func fillMakeTargets(dir string, c Commands) Commands {
	name := ""
	for _, candidate := range []string{"Makefile", "makefile"} {
		if fileExists(dir, candidate) {
			name = candidate
			break
		}
	}
	if name == "" {
		return c
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return c
	}
	targets := map[string]bool{}
	for _, m := range makeTarget.FindAllStringSubmatch(string(raw), -1) {
		targets[m[1]] = true
	}
	if c.Test == "" {
		for _, t := range []string{"test", "tests", "check"} {
			if targets[t] {
				c.Test = "make " + t
				break
			}
		}
	}
	if c.Build == "" && targets["build"] {
		c.Build = "make build"
	}
	if c.Lint == "" {
		for _, t := range []string{"lint", "vet"} {
			if targets[t] {
				c.Lint = "make " + t
				break
			}
		}
	}
	if c.Format == "" {
		for _, t := range []string{"fmt", "format"} {
			if targets[t] {
				c.Format = "make " + t
				break
			}
		}
	}
	return c
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func hasAny(dir string, names ...string) bool {
	for _, name := range names {
		if fileExists(dir, name) {
			return true
		}
	}
	return false
}

func fileContains(dir, name, needle string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	return err == nil && strings.Contains(string(raw), needle)
}
