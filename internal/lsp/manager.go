package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Manager struct {
	root    string
	mu      sync.Mutex
	servers map[string]*Server
	closed  bool
}

func NewManager(root string) *Manager {
	return &Manager{root: root, servers: map[string]*Server{}}
}

func (m *Manager) Root() string {
	if m == nil {
		return ""
	}
	return m.root
}

func (m *Manager) Server(ctx context.Context, path string) (*Server, error) {
	if m == nil {
		return nil, fmt.Errorf("language server support is unavailable")
	}
	spec, err := specFor(path)
	if err != nil {
		return nil, err
	}
	key := strings.Join(spec.Command, "\x00") + "\x00" + m.root
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("language server manager is closed")
	}
	if s := m.servers[key]; s != nil {
		return s, nil
	}
	client, err := startClient(ctx, m.root, spec.Command)
	if err != nil {
		return nil, err
	}
	s := &Server{
		manager:    m,
		client:     client,
		root:       m.root,
		languageID: spec.LanguageID,
		docs:       map[string]document{},
	}
	m.servers[key] = s
	return s, nil
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	servers := make([]*Server, 0, len(m.servers))
	for _, s := range m.servers {
		servers = append(servers, s)
	}
	m.servers = map[string]*Server{}
	m.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
}

type ServerSpec struct {
	Command    []string
	LanguageID string
}

func specFor(path string) (ServerSpec, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if v := os.Getenv("WOMBAT_LSP_" + strings.ToUpper(strings.TrimPrefix(ext, "."))); v != "" {
		return specFromString(v, languageID(ext))
	}
	if v := os.Getenv("WOMBAT_LSP"); v != "" {
		return specFromString(v, languageID(ext))
	}
	switch ext {
	case ".go":
		return command(ext, "gopls", "go")
	case ".rs":
		return command(ext, "rust-analyzer", "rust")
	case ".c", ".h":
		return command(ext, "clangd", "c")
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return command(ext, "clangd", "cpp")
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return command(ext, "typescript-language-server", "--stdio", languageID(ext))
	case ".py", ".pyi":
		if cmd, ok := findExecutable("pyright-langserver"); ok {
			return ServerSpec{Command: []string{cmd, "--stdio"}, LanguageID: "python"}, nil
		}
		return command(ext, "pylsp", "python")
	case ".java":
		return command(ext, "jdtls", "java")
	case ".rb":
		return command(ext, "solargraph", "stdio", "ruby")
	case ".php":
		return command(ext, "intelephense", "--stdio", "php")
	case ".lua":
		return command(ext, "lua-language-server", "lua")
	case ".cs":
		return command(ext, "csharp-ls", "csharp")
	case ".swift":
		return command(ext, "sourcekit-lsp", "swift")
	case ".kt", ".kts":
		return command(ext, "kotlin-language-server", "kotlin")
	case ".sh", ".bash", ".zsh":
		return command(ext, "bash-language-server", "start", "shellscript")
	}
	return ServerSpec{}, fmt.Errorf("no language server configured for %s files", ext)
}

func specFromString(value, lang string) (ServerSpec, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ServerSpec{}, fmt.Errorf("empty WOMBAT_LSP command")
	}
	if strings.ContainsAny(fields[0], `/\`) {
		if _, err := os.Stat(fields[0]); err != nil {
			return ServerSpec{}, fmt.Errorf("language server %s: %w", fields[0], err)
		}
		return ServerSpec{Command: fields, LanguageID: lang}, nil
	}
	path, ok := findExecutable(fields[0])
	if !ok {
		return ServerSpec{}, fmt.Errorf("language server %q is not installed or not on PATH", fields[0])
	}
	fields[0] = path
	return ServerSpec{Command: fields, LanguageID: lang}, nil
}

func command(ext, name, lang string, args ...string) (ServerSpec, error) {
	path, ok := findExecutable(name)
	if !ok {
		envName := "WOMBAT_LSP_" + strings.ToUpper(strings.TrimPrefix(ext, "."))
		return ServerSpec{}, fmt.Errorf("%s is not installed or not on PATH; install it or set %s", name, envName)
	}
	return ServerSpec{Command: append([]string{path}, args...), LanguageID: lang}, nil
}

func findExecutable(name string) (string, bool) {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		if st, err := os.Stat(name); err == nil && !st.IsDir() {
			return name, true
		}
		return "", false
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}
	var dirs []string
	if v := os.Getenv("GOBIN"); v != "" {
		dirs = append(dirs, v)
	}
	if v := os.Getenv("GOPATH"); v != "" {
		for _, dir := range filepath.SplitList(v) {
			dirs = append(dirs, filepath.Join(dir, "bin"))
		}
	}
	if v := os.Getenv("CARGO_HOME"); v != "" {
		dirs = append(dirs, filepath.Join(v, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path, true
		}
	}
	return "", false
}

func languageID(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return "cpp"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".py", ".pyi":
		return "python"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".lua":
		return "lua"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".sh", ".bash", ".zsh":
		return "shellscript"
	}
	return strings.TrimPrefix(strings.ToLower(ext), ".")
}

func Supports(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".rs", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx",
		".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".pyi", ".java",
		".rb", ".php", ".lua", ".cs", ".swift", ".kt", ".kts", ".sh", ".bash", ".zsh":
		return true
	}
	return false
}
