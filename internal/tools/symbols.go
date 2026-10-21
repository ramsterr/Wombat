package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Symbols answers the questions a plain text search cannot: where is this
// defined, where is it used, and what does this file contain? Go files are
// parsed with the standard library; other languages use conservative
// definition patterns. References are exact identifier matches, not
// substrings.
type Symbols struct{}

type symbolArgs struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	Path   string `json:"path"`
}

type symbolHit struct {
	Path string
	Line int
	Col  int
	Kind string
	Name string
	Sig  string
}

func (t *Symbols) Name() string  { return "symbols" }
func (t *Symbols) Class() string { return "read" }
func (t *Symbols) Description() string {
	return "Find definitions, references, or a file outline without guessing from text. Go is parsed with go/ast; JavaScript, TypeScript, Python, Rust, Java, C/C++, Ruby, PHP, Swift, Kotlin, and shell have definition patterns. Use defs to locate a symbol, refs to find every use, outline to list a file."
}
func (t *Symbols) Schema() map[string]any {
	return obj(map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"defs", "refs", "outline"}, "description": "defs (default), refs, or outline"},
		"name":   desc("symbol name to find; required for refs, optional filter for defs and outline"),
		"path":   desc("file or directory to search (default: working directory)"),
	}, "action")
}

func (t *Symbols) Run(_ context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[symbolArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	if action == "" {
		action = "defs"
	}
	if action != "defs" && action != "refs" && action != "outline" {
		return "", fmt.Errorf("action must be defs, refs, or outline")
	}
	if action == "refs" && strings.TrimSpace(a.Name) == "" {
		return "", fmt.Errorf("refs needs a name")
	}
	root := env.Dir
	if a.Path != "" {
		if err := env.CheckPath("read", a.Path); err != nil {
			return "", err
		}
		root, err = env.ResolvePath(a.Path)
		if err != nil {
			return "", err
		}
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	query := strings.TrimSpace(a.Name)
	var hits []symbolHit
	scan := func(path string) error {
		rel, err := filepath.Rel(env.Dir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(strings.ToLower(path), ".go") {
			defs, refs := goSymbols(path, rel, query, action)
			hits = append(hits, defs...)
			hits = append(hits, refs...)
			return nil
		}
		defs, refs := genericSymbols(path, rel, query, action)
		hits = append(hits, defs...)
		hits = append(hits, refs...)
		return nil
	}
	if !info.IsDir() {
		if err := scan(root); err != nil {
			return "", err
		}
	} else {
		_ = walk(root, func(path string, d fs.DirEntry) error {
			if d.IsDir() {
				switch d.Name() {
				case "vendor", "dist", "build", "target", ".next", "__pycache__", ".wombat":
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if !isSymbolFile(ext) {
				return nil
			}
			_ = scan(path)
			return nil
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}
		return hits[i].Col < hits[j].Col
	})
	if len(hits) == 0 {
		if query != "" {
			return fmt.Sprintf("no %s found for %q", action, query), nil
		}
		return "no symbols found", nil
	}
	var b strings.Builder
	label := action
	if action == "defs" {
		label = "definitions"
	} else if action == "refs" {
		label = "references"
	}
	fmt.Fprintf(&b, "%d %s%s:\n", len(hits), label, nameSuffix(query))
	for _, h := range hits {
		sig := ""
		if h.Sig != "" {
			sig = "  " + h.Sig
		}
		fmt.Fprintf(&b, "%s:%d:%d  %-8s %s%s\n", h.Path, h.Line, h.Col, h.Kind, h.Name, sig)
	}
	spilled, _ := env.Spill("symbols.txt", strings.TrimRight(b.String(), "\n"))
	return spilled, nil
}

func nameSuffix(name string) string {
	if name == "" {
		return ""
	}
	return " for " + name
}

func isSymbolFile(ext string) bool {
	switch ext {
	case ".go", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".py", ".rs",
		".java", ".kt", ".kts", ".cs", ".c", ".h", ".cc", ".cpp", ".hpp",
		".rb", ".php", ".swift", ".sh", ".bash", ".zsh", ".lua", ".scala",
		".ex", ".exs":
		return true
	}
	return false
}

func goSymbols(path, rel, query, action string) (defs, refs []symbolHit) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil
	}
	defPos := map[token.Pos]bool{}
	queryLower := strings.ToLower(query)
	addDef := func(pos token.Pos, kind, name, sig string) {
		defPos[pos] = true
		if query != "" && !strings.Contains(strings.ToLower(name), queryLower) {
			return
		}
		p := fset.Position(pos)
		defs = append(defs, symbolHit{Path: rel, Line: p.Line, Col: p.Column, Kind: kind, Name: name, Sig: sig})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := "func"
			name := d.Name.Name
			sig := goFuncSignature(d)
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = "method"
				name = goReceiver(d.Recv.List[0].Type) + "." + d.Name.Name
			}
			addDef(d.Name.Pos(), kind, name, sig)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					addDef(s.Name.Pos(), "type", s.Name.Name, "")
				case *ast.ValueSpec:
					kind := strings.ToLower(d.Tok.String())
					for _, n := range s.Names {
						addDef(n.Pos(), kind, n.Name, "")
					}
				}
			}
		}
	}
	if action != "refs" {
		return defs, nil
	}
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name != query || defPos[id.Pos()] {
			return true
		}
		p := fset.Position(id.Pos())
		refs = append(refs, symbolHit{Path: rel, Line: p.Line, Col: p.Column, Kind: "ref", Name: id.Name})
		return true
	})
	return defs, refs
}

func goReceiver(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "(*" + goReceiver(t.X) + ")"
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return goReceiver(t.X)
	}
	return "?"
}

func goFuncSignature(d *ast.FuncDecl) string {
	if d.Type == nil {
		return ""
	}
	var params []string
	if d.Type.Params != nil {
		for _, f := range d.Type.Params.List {
			typ := exprString(f.Type)
			if len(f.Names) == 0 {
				params = append(params, typ)
				continue
			}
			for range f.Names {
				params = append(params, typ)
			}
		}
	}
	sig := "(" + strings.Join(params, ", ") + ")"
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		var results []string
		for _, f := range d.Type.Results.List {
			results = append(results, exprString(f.Type))
		}
		if len(results) == 1 {
			sig += " " + results[0]
		} else {
			sig += " (" + strings.Join(results, ", ") + ")"
		}
	}
	return sig
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + exprString(t.Elt)
	case *ast.ChanType:
		return "chan " + exprString(t.Value)
	case *ast.IndexExpr:
		return exprString(t.X) + "[" + exprString(t.Index) + "]"
	}
	return "?"
}

type defPattern struct {
	kind string
	re   *regexp.Regexp
}

var genericPatterns = map[string][]defPattern{}

func init() {
	add := func(exts []string, kind, pattern string) {
		re := regexp.MustCompile(pattern)
		for _, ext := range exts {
			genericPatterns[ext] = append(genericPatterns[ext], defPattern{kind: kind, re: re})
		}
	}
	js := []string{".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx"}
	add(js, "func", `(?m)^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)
	add(js, "class", `(?m)^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	add(js, "interface", `(?m)^\s*(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)`)
	add(js, "type", `(?m)^\s*(?:export\s+)?type\s+([A-Za-z_$][\w$]*)\s*=`)
	add(js, "const", `(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`)
	add([]string{".py"}, "func", `(?m)^\s*(?:async\s+)?def\s+(\w+)`)
	add([]string{".py"}, "class", `(?m)^\s*class\s+(\w+)`)
	add([]string{".rs"}, "func", `(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+(\w+)`)
	add([]string{".rs"}, "type", `(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|type|union)\s+(\w+)`)
	add([]string{".java", ".kt", ".kts", ".cs", ".scala"}, "type", `(?m)^\s*(?:public|private|protected|internal|static|final|abstract|sealed|open|data|partial|inline|export)?\s*(?:class|interface|enum|record|object)\s+(\w+)`)
	add([]string{".java", ".kt", ".kts", ".cs", ".scala"}, "func", `(?m)^\s*(?:public|private|protected|internal|static|final|abstract|override|open|suspend|inline|virtual|async|synchronized|native|fun|func)?\s*(?:fun|func)\s+(\w+)`)
	add([]string{".c", ".h", ".cc", ".cpp", ".hpp"}, "type", `(?m)^\s*(?:typedef\s+)?(?:struct|class|enum|union)\s+(\w+)`)
	add([]string{".rb"}, "func", `(?m)^\s*def\s+([\w.?!]+)`)
	add([]string{".php"}, "func", `(?m)^\s*(?:public|private|protected|static|final|abstract|function\s+)*function\s+(\w+)`)
	add([]string{".php"}, "class", `(?m)^\s*(?:abstract\s+|final\s+)?class\s+(\w+)`)
	add([]string{".swift"}, "func", `(?m)^\s*(?:public|private|internal|open|static|final|override|mutating|class|func)?\s*func\s+(\w+)`)
	add([]string{".swift"}, "type", `(?m)^\s*(?:public|private|internal|open|final)?\s*(?:class|struct|enum|protocol|extension)\s+(\w+)`)
	add([]string{".sh", ".bash", ".zsh"}, "func", `(?m)^\s*(?:function\s+)?([A-Za-z_][\w-]*)\s*\(\)\s*\{`)
	add([]string{".lua"}, "func", `(?m)^\s*(?:local\s+)?function\s+([\w.:]+)`)
	add([]string{".ex", ".exs"}, "func", `(?m)^\s*defp?\s+(\w+)`)
	add([]string{".ex", ".exs"}, "type", `(?m)^\s*defmodule\s+([\w.]+)`)
}

func genericSymbols(path, rel, query, action string) (defs, refs []symbolHit) {
	raw, err := os.ReadFile(path)
	if err != nil || isBinaryData(raw) || len(raw) > 4<<20 {
		return nil, nil
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	ext := strings.ToLower(filepath.Ext(path))
	queryLower := strings.ToLower(query)
	defLine := map[int]bool{}
	for _, dp := range genericPatterns[ext] {
		for i, line := range lines {
			if m := dp.re.FindStringSubmatch(line); len(m) > 1 {
				name := ""
				for _, g := range m[1:] {
					if g != "" {
						name = g
						break
					}
				}
				if name == "" {
					continue
				}
				defLine[i+1] = true
				if query != "" && !strings.Contains(strings.ToLower(name), queryLower) {
					continue
				}
				sig := strings.TrimSpace(line)
				sig = strings.Join(strings.Fields(sig), " ")
				if len(sig) > 140 {
					sig = sig[:140] + "…"
				}
				defs = append(defs, symbolHit{Path: rel, Line: i + 1, Col: 1, Kind: dp.kind, Name: name, Sig: sig})
			}
		}
	}
	if action != "refs" || query == "" {
		return defs, nil
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(query) + `\b`)
	for i, line := range lines {
		if defLine[i+1] || !re.MatchString(line) {
			continue
		}
		col := re.FindStringIndex(line)
		if col == nil {
			continue
		}
		refs = append(refs, symbolHit{Path: rel, Line: i + 1, Col: col[0] + 1, Kind: "ref", Name: query})
	}
	return defs, refs
}
