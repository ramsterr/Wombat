package tools

import (
	"fmt"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strings"
)

// replaceText applies an edit. It first tries an exact match; when that
// fails it retries with normalized line endings and whitespace, so a model
// that quotes code from a slightly different formatter still succeeds. The
// returned bool reports whether the match was fuzzy.
func replaceText(content, old, new string, all bool) (string, int, bool, error) {
	return replaceTextIn("", content, old, new, all)
}

// replaceTextIn is replaceText with the file path available so Go files can
// be tokenized with go/scanner. Other files use a small language-agnostic
// lexer. Both fall back to matching a unique sequence of code tokens when
// line-oriented matching cannot find an unambiguous answer.
func replaceTextIn(path, content, old, new string, all bool) (string, int, bool, error) {
	if old == "" {
		return content, 0, false, fmt.Errorf("old_string is required")
	}
	if strings.Contains(content, old) {
		count := strings.Count(content, old)
		if count > 1 && !all {
			return content, count, false, fmt.Errorf("old_string matches %d times; add more context or set replace_all", count)
		}
		if all {
			return strings.ReplaceAll(content, old, new), count, false, nil
		}
		return strings.Replace(content, old, new, 1), 1, false, nil
	}

	// CRLF: the file has Windows line endings and the model quoted Unix
	// ones, or the reverse.
	if strings.Contains(content, "\r\n") && strings.Contains(old, "\n") && !strings.Contains(old, "\r\n") {
		crlf := strings.ReplaceAll(old, "\n", "\r\n")
		if strings.Contains(content, crlf) {
			count := strings.Count(content, crlf)
			if count > 1 && !all {
				return content, count, true, fmt.Errorf("old_string matches %d times after normalizing line endings; add more context or set replace_all", count)
			}
			replacement := strings.ReplaceAll(new, "\n", "\r\n")
			if all {
				return strings.ReplaceAll(content, crlf, replacement), count, true, nil
			}
			return strings.Replace(content, crlf, replacement, 1), 1, true, nil
		}
	}

	updated, count, err := replaceLoose(content, old, new, all)
	if err == nil {
		return updated, count, true, nil
	}
	lineErr := err

	if updated, count, tokenErr := replaceTokenText(path, content, old, new, all); tokenErr == nil {
		return updated, count, true, nil
	} else if strings.Contains(tokenErr.Error(), "matches ") {
		return content, count, false, tokenErr
	}
	return content, 0, false, lineErr
}

// replaceLoose matches whole lines while ignoring leading/trailing
// whitespace and CRLF differences. It preserves the file's own line endings
// around the replacement.
func replaceLoose(content, old, new string, all bool) (string, int, error) {
	oldNorm := strings.ReplaceAll(old, "\r\n", "\n")
	oldNorm = strings.TrimSuffix(oldNorm, "\n")
	want := strings.Split(oldNorm, "\n")
	if len(want) == 0 || (len(want) == 1 && want[0] == "") {
		return content, 0, fmt.Errorf("old_string not found")
	}
	have := strings.Split(content, "\n")

	type span struct{ start, end int } // line indexes, end exclusive
	var spans []span
	for i := 0; i+len(want) <= len(have); i++ {
		ok := true
		for j := range want {
			if !looseLineEqual(have[i+j], want[j]) {
				ok = false
				break
			}
		}
		if ok {
			spans = append(spans, span{start: i, end: i + len(want)})
		}
	}
	if len(spans) == 0 {
		return content, 0, fmt.Errorf("old_string not found (also tried a whitespace and line-ending insensitive match)")
	}
	if len(spans) > 1 && !all {
		return content, len(spans), fmt.Errorf("old_string matches %d times when whitespace is ignored; add more context or set replace_all", len(spans))
	}
	if !all {
		spans = spans[:1]
	}
	// Replace from the end so earlier line numbers stay valid.
	for i := len(spans) - 1; i >= 0; i-- {
		s := spans[i]
		original := strings.Join(have[s.start:s.end], "\n")
		replacement := new
		if strings.Contains(original, "\r") && !strings.Contains(replacement, "\r\n") {
			replacement = strings.ReplaceAll(strings.ReplaceAll(replacement, "\r\n", "\n"), "\n", "\r\n")
		}
		replLines := strings.Split(replacement, "\n")
		have = append(have[:s.start], append(replLines, have[s.end:]...)...)
	}
	return strings.Join(have, "\n"), len(spans), nil
}

func looseLineEqual(a, b string) bool {
	return strings.TrimSpace(strings.TrimSuffix(a, "\r")) == strings.TrimSpace(strings.TrimSuffix(b, "\r"))
}

type codeToken struct {
	key        string
	start, end int
}

type tokenSpan struct{ start, end int }

func replaceTokenText(path, content, old, new string, all bool) (string, int, error) {
	oldTokens := tokenizeCode(path, old)
	if len(oldTokens) == 0 {
		return content, 0, fmt.Errorf("old_string has no code tokens")
	}
	tokens := tokenizeCode(path, content)
	var spans []tokenSpan
	for i := 0; i+len(oldTokens) <= len(tokens); i++ {
		match := true
		for j := range oldTokens {
			if tokens[i+j].key != oldTokens[j].key {
				match = false
				break
			}
		}
		if match {
			spans = append(spans, tokenSpan{start: tokens[i].start, end: tokens[i+len(oldTokens)-1].end})
		}
	}
	if len(spans) == 0 {
		return content, 0, fmt.Errorf("old_string not found")
	}
	if len(spans) > 1 && !all {
		return content, len(spans), fmt.Errorf("old_string matches %d times when whitespace and formatting are ignored (candidate lines %s); add more context or set replace_all", len(spans), tokenCandidateLines(content, spans))
	}
	selected := spans
	if !all {
		selected = spans[:1]
	} else {
		selected = nonOverlapping(spans)
	}
	replacement := new
	if strings.Contains(content, "\r\n") && !strings.Contains(replacement, "\r\n") {
		replacement = strings.ReplaceAll(strings.ReplaceAll(replacement, "\r\n", "\n"), "\n", "\r\n")
	}
	for i := len(selected) - 1; i >= 0; i-- {
		span := selected[i]
		repl := replacement
		if prefix := lineIndent(content, span.start); prefix != "" && strings.HasPrefix(repl, prefix) {
			repl = repl[len(prefix):]
		}
		content = content[:span.start] + repl + content[span.end:]
	}
	return content, len(selected), nil
}

func nonOverlapping(spans []tokenSpan) []tokenSpan {
	out := make([]tokenSpan, 0, len(spans))
	for _, s := range spans {
		if len(out) == 0 || s.start >= out[len(out)-1].end {
			out = append(out, s)
		}
	}
	return out
}

func tokenCandidateLines(content string, spans []tokenSpan) string {
	seen := map[int]bool{}
	var lines []string
	for _, s := range spans {
		line := strings.Count(content[:s.start], "\n") + 1
		if !seen[line] {
			seen[line] = true
			lines = append(lines, fmt.Sprintf("%d", line))
		}
		if len(lines) == 5 {
			break
		}
	}
	return strings.Join(lines, ", ")
}

func lineIndent(content string, pos int) string {
	start := strings.LastIndexByte(content[:pos], '\n') + 1
	prefix := content[start:pos]
	for i := 0; i < len(prefix); i++ {
		if prefix[i] != ' ' && prefix[i] != '\t' {
			return ""
		}
	}
	return prefix
}

func tokenizeCode(path, src string) []codeToken {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".go" {
		return tokenizeGo(src)
	}
	hashComments := false
	slashComments := true
	switch ext {
	case ".py", ".pyw", ".rb", ".sh", ".bash", ".zsh", ".fish", ".pl", ".pm", ".r", ".ex", ".exs", ".php", ".yaml", ".yml", ".toml", ".conf", ".ini", ".mk":
		hashComments = true
		slashComments = false
	case ".md", ".markdown", ".txt", ".html", ".htm", ".xml", ".css", ".scss", ".sql":
		slashComments = false
	}
	return tokenizeGeneric(src, hashComments, slashComments)
}

func tokenizeGo(src string) []codeToken {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), nil, scanner.ScanComments)
	var out []codeToken
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		start := file.Offset(pos)
		var end int
		var key string
		switch tok {
		case token.COMMENT:
			end = start + len(lit)
			key = "c:" + commentKey(lit)
		case token.STRING, token.CHAR:
			end = start + len(lit)
			key = tok.String() + ":" + lit
		case token.IDENT:
			end = start + len(lit)
			key = "i:" + lit
		default:
			if lit != "" {
				end = start + len(lit)
				key = lit
			} else {
				text := tok.String()
				end = start + len(text)
				key = text
			}
		}
		out = append(out, codeToken{key: key, start: start, end: end})
	}
	return out
}

func tokenizeGeneric(src string, hashComments, slashComments bool) []codeToken {
	var out []codeToken
	for i := 0; i < len(src); {
		c := src[i]
		if isSpace(c) {
			i++
			continue
		}
		start := i
		if slashComments && c == '/' && i+1 < len(src) && src[i+1] == '/' {
			i += 2
			for i < len(src) && src[i] != '\n' {
				i++
			}
			out = append(out, codeToken{key: "c:" + commentKey(src[start:i]), start: start, end: i})
			continue
		}
		if slashComments && c == '/' && i+1 < len(src) && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			if i+1 < len(src) {
				i += 2
			}
			out = append(out, codeToken{key: "c:" + commentKey(src[start:i]), start: start, end: i})
			continue
		}
		if hashComments && c == '#' {
			i++
			for i < len(src) && src[i] != '\n' {
				i++
			}
			out = append(out, codeToken{key: "c:" + commentKey(src[start:i]), start: start, end: i})
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			i = scanString(src, i)
			out = append(out, codeToken{key: "s:" + src[start:i], start: start, end: i})
			continue
		}
		if isIdentStart(c) {
			i++
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			out = append(out, codeToken{key: "i:" + src[start:i], start: start, end: i})
			continue
		}
		if c >= '0' && c <= '9' {
			i++
			for i < len(src) {
				if isIdentPart(src[i]) || (src[i] == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9') {
					i++
					continue
				}
				if (src[i] == '+' || src[i] == '-') && i > start && (src[i-1] == 'e' || src[i-1] == 'E') {
					i++
					continue
				}
				break
			}
			out = append(out, codeToken{key: "n:" + src[start:i], start: start, end: i})
			continue
		}
		if isOpChar(c) {
			i++
			for i < len(src) && isOpChar(src[i]) {
				i++
			}
			out = append(out, codeToken{key: "o:" + src[start:i], start: start, end: i})
			continue
		}
		i++
		out = append(out, codeToken{key: "x:" + src[start:i], start: start, end: i})
	}
	return out
}

func scanString(src string, i int) int {
	quote := src[i]
	i++
	if i+1 < len(src) && src[i] == quote && src[i+1] == quote {
		i += 2
		for i < len(src) {
			if i+2 < len(src) && src[i] == quote && src[i+1] == quote && src[i+2] == quote {
				return i + 3
			}
			if src[i] == '\\' {
				i += 2
				continue
			}
			i++
		}
		return len(src)
	}
	for i < len(src) {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if src[i] == quote {
			return i + 1
		}
		i++
	}
	return len(src)
}

func commentKey(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c == '@' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isOpChar(c byte) bool {
	return strings.ContainsRune("+-*/%=!<>&|^~?:;,.()[]{}#", rune(c))
}
