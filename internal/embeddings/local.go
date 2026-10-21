package embeddings

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// LocalModel is the signature of the built-in, offline embedder.
const LocalModel = "local-hash-v1"

const localDim = 384

// Local embeds text without a network call. It is a hashed bag of code-aware
// tokens: identifiers are split on case and punctuation, common words are
// dropped, and a small synonym table maps related terms (auth/login,
// error/exception, config/settings) to the same bucket. It is less precise
// than a neural embedding model, but it works offline and deterministically.
func Local(text string) []float32 {
	vec := make([]float32, localDim)
	tokens := tokensOf(text)
	for i, tok := range tokens {
		add(vec, tok, 1)
		if i > 0 {
			add(vec, tokens[i-1]+" "+tok, 0.5)
		}
	}
	normalize(vec)
	return vec
}

func add(vec []float32, token string, weight float32) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(token))
	sum := h.Sum64()
	idx := int(sum % uint64(localDim))
	if sum&(1<<63) != 0 {
		weight = -weight
	}
	vec[idx] += weight
}

func normalize(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
}

func tokensOf(text string) []string {
	raw := splitIdentifiers(text)
	tokens := make([]string, 0, len(raw))
	for _, tok := range raw {
		tok = strings.ToLower(tok)
		if len(tok) < 2 || stopWords[tok] {
			continue
		}
		tokens = append(tokens, stem(tok))
		if canon, ok := synonyms[tok]; ok {
			tokens = append(tokens, canon)
		}
	}
	return tokens
}

// splitIdentifiers breaks on non-letters and on camelCase boundaries, so
// getUserName becomes get, user, name.
func splitIdentifiers(text string) []string {
	var out []string
	var b strings.Builder
	var prev rune
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				flush()
			}
			b.WriteRune(r)
		default:
			flush()
		}
		prev = r
	}
	flush()
	return out
}

func stem(tok string) string {
	switch {
	case strings.HasSuffix(tok, "ies") && len(tok) > 4:
		return tok[:len(tok)-3] + "y"
	case strings.HasSuffix(tok, "ing") && len(tok) > 5:
		return tok[:len(tok)-3]
	case strings.HasSuffix(tok, "ed") && len(tok) > 4:
		return tok[:len(tok)-2]
	case strings.HasSuffix(tok, "es") && len(tok) > 4:
		return tok[:len(tok)-2]
	case strings.HasSuffix(tok, "s") && len(tok) > 3:
		return tok[:len(tok)-1]
	}
	return tok
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true,
	"that": true, "from": true, "are": true, "was": true, "were": true,
	"have": true, "has": true, "had": true, "not": true,
	"you": true, "your": true, "our": true, "its": true, "their": true,
	"into": true, "onto": true, "when": true, "where": true, "what": true,
	"which": true, "who": true, "how": true, "why": true, "all": true,
	"any": true, "can": true, "could": true, "should": true, "would": true,
	"does": true, "did": true, "use": true, "used": true, "using": true,
}

var synonyms = map[string]string{
	"auth": "auth", "authentication": "auth", "authenticate": "auth",
	"authenticated": "auth", "login": "auth", "signin": "auth",
	"signup": "auth", "credential": "auth", "credentials": "auth",

	"error": "err", "errors": "err", "err": "err", "exception": "err",
	"failure": "err", "failed": "err", "fail": "err", "panic": "err",

	"config": "config", "configuration": "config", "settings": "config",
	"setting": "config", "option": "config", "options": "config",
	"preference": "config", "preferences": "config",

	"test": "test", "tests": "test", "testing": "test", "spec": "test",
	"assert": "test", "expect": "test", "verify": "test",

	"database": "db", "db": "db", "sql": "db", "query": "db",
	"store": "db", "repository": "db", "persist": "db",

	"http": "http", "request": "http", "response": "http",
	"endpoint": "http", "route": "http", "handler": "http",
	"server": "http", "client": "http",

	"file": "file", "path": "file", "filesystem": "file", "fs": "file",
	"read": "file", "write": "file", "directory": "file", "folder": "file",

	"user": "user", "account": "user", "profile": "user", "member": "user",
	"customer": "user",

	"search": "search", "find": "search", "lookup": "search",
	"grep": "search", "querying": "search",

	"image": "image", "photo": "image", "picture": "image", "screenshot": "image",
	"render": "image", "draw": "image",
}
