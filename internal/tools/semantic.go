package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ramsterr/Wombat/internal/embeddings"
)

const (
	semanticVersion    = 1
	semanticMaxBytes   = 1 << 20
	semanticMaxChunks  = 5000
	semanticChunkLines = 80
	semanticOverlap    = 10
	semanticMinScore   = 0.15
)

var semanticMu sync.Mutex

// SemanticSearch finds code by meaning: it embeds file chunks with an
// OpenAI-compatible embedding model when one is configured, and with a
// deterministic offline embedder otherwise. The index is cached outside the
// workspace and updated incrementally.
type SemanticSearch struct{}

type semanticArgs struct {
	Query   string `json:"query"`
	Path    string `json:"path,omitempty"`
	TopK    int    `json:"top_k,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
}

func (t *SemanticSearch) Name() string  { return "semantic_search" }
func (t *SemanticSearch) Class() string { return "read" }
func (t *SemanticSearch) Description() string {
	return "Find code by meaning rather than exact text: indexes the workspace and ranks chunks by semantic similarity to the query. Use it when you know what the code does but not what it is called. Uses a configured embedding model, or a deterministic offline embedder when none is configured. The index is cached and updated incrementally."
}
func (t *SemanticSearch) Schema() map[string]any {
	return obj(map[string]any{
		"query":   desc("what you are looking for, in natural language"),
		"path":    desc("directory to search (default: working directory)"),
		"top_k":   map[string]any{"type": "integer", "description": "number of results (default 8, max 25)"},
		"refresh": map[string]any{"type": "boolean", "description": "rebuild the whole index instead of reusing unchanged files"},
	}, "query")
}

type semanticChunk struct {
	Start int       `json:"start"`
	End   int       `json:"end"`
	Text  string    `json:"text"`
	Vec   []float32 `json:"vec"`
}

type semanticFile struct {
	Size    int64           `json:"size"`
	ModTime int64           `json:"mod_time"`
	Chunks  []semanticChunk `json:"chunks"`
}

type semanticIndex struct {
	Version    int                     `json:"version"`
	Root       string                  `json:"root"`
	Vectorizer string                  `json:"vectorizer"`
	Files      map[string]semanticFile `json:"files"`
}

type vectorizer interface {
	Name() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type localVectorizer struct{}

func (localVectorizer) Name() string { return embeddings.LocalModel }
func (localVectorizer) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = embeddings.Local(text)
	}
	return out, nil
}

type remoteVectorizer struct{ client *embeddings.Client }

func (v remoteVectorizer) Name() string { return "remote:" + v.client.Signature() }
func (v remoteVectorizer) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return v.client.Embed(ctx, texts)
}

func (t *SemanticSearch) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[semanticArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	root := env.Dir
	if strings.TrimSpace(a.Path) != "" {
		if err := env.CheckPath("read", a.Path); err != nil {
			return "", err
		}
		root, err = env.ResolvePath(a.Path)
		if err != nil {
			return "", err
		}
		st, err := os.Stat(root)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", fmt.Errorf("semantic_search needs a directory, not a file")
		}
	}
	topK := a.TopK
	if topK <= 0 {
		topK = 8
	}
	if topK > 25 {
		topK = 25
	}

	remote := env.Embeddings != nil && !env.DenyNetwork
	if remote {
		out, err := searchSemantic(ctx, env, root, query, topK, a.Refresh, remoteVectorizer{env.Embeddings})
		if err == nil {
			return out, nil
		}
		local, localErr := searchSemantic(ctx, env, root, query, topK, a.Refresh, localVectorizer{})
		if localErr != nil {
			return "", localErr
		}
		return fmt.Sprintf("embedding provider failed (%v); used the offline embedder instead.\n\n%s", err, local), nil
	}
	return searchSemantic(ctx, env, root, query, topK, a.Refresh, localVectorizer{})
}

func searchSemantic(ctx context.Context, env *Env, root, query string, topK int, refresh bool, vec vectorizer) (string, error) {
	semanticMu.Lock()
	defer semanticMu.Unlock()

	idx, note, err := loadOrBuildIndex(ctx, env, root, refresh, vec)
	if err != nil {
		return "", err
	}
	if len(idx.Files) == 0 {
		return "no indexable files found in " + relativePath(env.Dir, root), nil
	}
	qv, err := vec.Embed(ctx, []string{query})
	if err != nil {
		return "", err
	}
	if len(qv) != 1 {
		return "", fmt.Errorf("embedding provider returned no query vector")
	}

	type hit struct {
		path  string
		chunk semanticChunk
		score float64
	}
	var hits []hit
	for path, file := range idx.Files {
		for _, chunk := range file.Chunks {
			score := cosine32(qv[0], chunk.Vec)
			if score < semanticMinScore {
				continue
			}
			hits = append(hits, hit{path: path, chunk: chunk, score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].path != hits[j].path {
			return hits[i].path < hits[j].path
		}
		return hits[i].chunk.Start < hits[j].chunk.Start
	})
	if len(hits) == 0 {
		return fmt.Sprintf("no matches for %q (%s)", query, note), nil
	}
	if len(hits) > topK {
		hits = hits[:topK]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "semantic search %q — %s\n", query, note)
	for i, h := range hits {
		fmt.Fprintf(&b, "\n%d. %s:%d-%d  score %.2f\n", i+1, h.path, h.chunk.Start, h.chunk.End, h.score)
		b.WriteString(indentSnippet(h.chunk.Text, "   "))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func loadOrBuildIndex(ctx context.Context, env *Env, root string, refresh bool, vec vectorizer) (*semanticIndex, string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	path := semanticIndexPath(env, abs, vec.Name())
	idx := &semanticIndex{Version: semanticVersion, Root: abs, Vectorizer: vec.Name(), Files: map[string]semanticFile{}}
	if !refresh {
		if data, err := os.ReadFile(path); err == nil {
			var old semanticIndex
			if json.Unmarshal(data, &old) == nil && old.Version == semanticVersion && old.Vectorizer == vec.Name() && old.Files != nil {
				idx = &old
			}
		}
	}

	oldCount := len(idx.Files)
	current := map[string]semanticFile{}
	embeddedFiles, embeddedChunks, reusedFiles := 0, 0, 0
	truncated := false
	err = walk(abs, func(path string, d os.DirEntry) error {
		if d.IsDir() {
			if path != abs && semanticSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if truncated || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !indexableFile(path) || isSecretFile(path) {
			return nil
		}
		st, err := d.Info()
		if err != nil || st.Size() == 0 || st.Size() > semanticMaxBytes {
			return nil
		}
		if old, ok := idx.Files[rel]; ok && old.Size == st.Size() && old.ModTime == st.ModTime().UnixNano() {
			current[rel] = old
			reusedFiles++
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 || !indexableText(data) {
			return nil
		}
		chunks := chunkText(string(data))
		if len(chunks) == 0 {
			return nil
		}
		if embeddedChunks+len(chunks) > semanticMaxChunks {
			chunks = chunks[:semanticMaxChunks-embeddedChunks]
			truncated = true
			if len(chunks) == 0 {
				return filepath.SkipAll
			}
		}
		texts := make([]string, len(chunks))
		for i := range chunks {
			texts[i] = chunks[i].Text
		}
		vecs, err := vec.Embed(ctx, texts)
		if err != nil {
			return err
		}
		if len(vecs) != len(chunks) {
			return fmt.Errorf("embedding provider returned %d vectors for %d chunks", len(vecs), len(chunks))
		}
		for i := range chunks {
			chunks[i].Vec = vecs[i]
		}
		current[rel] = semanticFile{Size: st.Size(), ModTime: st.ModTime().UnixNano(), Chunks: chunks}
		embeddedFiles++
		embeddedChunks += len(chunks)
		if truncated {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	oldFiles := idx.Files
	idx.Files = current
	changed := refresh || embeddedFiles > 0 || len(current) != oldCount
	if !changed {
		for rel := range oldFiles {
			if _, ok := current[rel]; !ok {
				changed = true
				break
			}
		}
	}
	if changed {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, "", err
		}
		if err := writeIndex(path, idx); err != nil {
			return nil, "", err
		}
	}
	totalChunks := 0
	for _, f := range idx.Files {
		totalChunks += len(f.Chunks)
	}
	note := fmt.Sprintf("%s, %d file(s), %d chunk(s)", vec.Name(), len(idx.Files), totalChunks)
	if reusedFiles > 0 && embeddedFiles > 0 {
		note += fmt.Sprintf(" (%d reused, %d reindexed)", reusedFiles, embeddedFiles)
	}
	if truncated {
		note += fmt.Sprintf("; index capped at %d chunks, narrow the path for more", semanticMaxChunks)
	}
	return idx, note, nil
}

func semanticIndexPath(env *Env, root, vectorizer string) string {
	dir := env.IndexDir
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil || cache == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".wombat", "index")
		} else {
			dir = filepath.Join(cache, "wombat", "index")
		}
	}
	sum := sha256.Sum256([]byte(root + "\x00" + vectorizer))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".json")
}

func writeIndex(path string, idx *semanticIndex) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var indexExtensions = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".rs": true, ".c": true,
	".h": true, ".cc": true, ".cpp": true, ".hpp": true, ".java": true,
	".rb": true, ".php": true, ".lua": true, ".cs": true, ".swift": true,
	".kt": true, ".kts": true, ".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".md": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".toml": true, ".ini": true, ".cfg": true, ".css": true,
	".scss": true, ".sass": true, ".less": true, ".html": true, ".htm": true,
	".vue": true, ".svelte": true, ".proto": true, ".graphql": true,
	".ex": true, ".exs": true, ".dart": true, ".scala": true, ".clj": true,
	".r": true, ".jl": true, ".bzl": true, ".cmake": true, ".tf": true,
	".dockerfile": true, ".env.example": true,
}

var indexNames = map[string]bool{
	"Makefile": true, "Dockerfile": true, "Containerfile": true,
	"go.mod": true, "Cargo.toml": true, "package.json": true,
	"pyproject.toml": true, "Gemfile": true, "Rakefile": true,
	".env.example": true, ".env.sample": true,
}

var semanticSkipDirs = map[string]bool{
	"dist": true, "build": true, "target": true, "out": true,
	".next": true, ".nuxt": true, "coverage": true, ".cache": true,
	"vendor": true, "__pycache__": true, ".pytest_cache": true,
	".gradle": true, ".idea": true, ".vscode": true, "Pods": true,
	"DerivedData": true,
}

func indexableFile(path string) bool {
	base := filepath.Base(path)
	if indexNames[base] {
		return true
	}
	return indexExtensions[strings.ToLower(filepath.Ext(path))]
}

func indexableText(data []byte) bool {
	if len(data) > semanticMaxBytes || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return false
	}
	// Minified bundles and generated blobs have enormous lines and are not
	// useful to a code search.
	if longestLine(data) > 4000 {
		return false
	}
	return true
}

func longestLine(data []byte) int {
	longest, current := 0, 0
	for _, b := range data {
		if b == '\n' {
			if current > longest {
				longest = current
			}
			current = 0
			continue
		}
		current++
	}
	if current > longest {
		longest = current
	}
	return longest
}

func chunkText(text string) []semanticChunk {
	lines := strings.Split(text, "\n")
	var chunks []semanticChunk
	for start := 0; start < len(lines); start += semanticChunkLines - semanticOverlap {
		end := start + semanticChunkLines
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.Join(lines[start:end], "\n")
		if strings.TrimSpace(body) != "" {
			chunks = append(chunks, semanticChunk{Start: start + 1, End: end, Text: body})
		}
		if end == len(lines) {
			break
		}
	}
	return chunks
}

func cosine32(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func indentSnippet(text, indent string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(indent)
		b.WriteString(clipText(line, 200))
		b.WriteByte('\n')
	}
	return b.String()
}
