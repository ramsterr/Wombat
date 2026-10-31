package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tools"
)

// visualArtifact is something a person has to look at before another pass.
type visualArtifact struct {
	Kind string
	Ref  string
}

var (
	visualURLPattern  = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?::[0-9]+)?(?:[/?#][^\s"'<>)]*)?`)
	visualFilePattern = regexp.MustCompile(`(?i)[\w./~-]+\.(?:html?|xhtml|pdf|svg|png|jpe?g|gif|webp|avif|bmp|mp4|webm|mov|m4v)\b`)
)

func previewableFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm", ".xhtml", ".pdf", ".svg", ".png", ".jpg", ".jpeg",
		".gif", ".webp", ".avif", ".bmp", ".mp4", ".webm", ".mov", ".m4v":
		return true
	}
	return false
}

func visualKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm", ".xhtml":
		return "html"
	case ".pdf":
		return "pdf"
	case ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp":
		return "image"
	case ".mp4", ".webm", ".mov", ".m4v":
		return "video"
	}
	return "file"
}

func serverBanner(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"local:", "ready in", "serving http", "listening on",
		"uvicorn running", "development server", "running at",
		"server running", "available at", "preview at", "compiled successfully",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func firstURL(text string) string {
	m := visualURLPattern.FindString(text)
	return strings.TrimRight(m, ".,;:!?)]}'\"")
}

func firstFilePath(text string) string {
	for _, m := range visualFilePattern.FindAllString(text, -1) {
		if strings.Contains(m, "://") {
			continue
		}
		if previewableFile(m) {
			return strings.TrimRight(m, ".,;:!?)]}'\"")
		}
	}
	return ""
}

// detectVisualArtifact decides whether a tool call produced something that
// needs the user's eyes. Reads and ordinary source edits do not count.
func detectVisualArtifact(tc llm.ToolCall, result string) (visualArtifact, bool) {
	if strings.HasPrefix(strings.TrimSpace(result), "error:") {
		return visualArtifact{}, false
	}
	switch tc.Name {
	case "write", "edit":
		path := toolArg(tc, "path")
		if path == "" {
			path = toolArg(tc, "file_path")
		}
		if previewableFile(path) {
			return visualArtifact{Kind: visualKind(path), Ref: path}, true
		}
	case "apply_patch":
		if path := firstFilePath(tc.Arguments); path != "" {
			return visualArtifact{Kind: visualKind(path), Ref: path}, true
		}
	case "browser":
		if u := firstURL(result); u != "" {
			return visualArtifact{Kind: "site", Ref: u}, true
		}
		if path := firstFilePath(result); path != "" {
			return visualArtifact{Kind: visualKind(path), Ref: path}, true
		}
	case "bash":
		if !serverBanner(result) {
			return visualArtifact{}, false
		}
		if u := firstURL(result); u != "" {
			return visualArtifact{Kind: "site", Ref: u}, true
		}
	}
	return visualArtifact{}, false
}

// rememberVisualTurn records previews from one tool batch. It deliberately
// does not ask the user: a three-page site should finish all three pages before
// the review prompt appears.
func (a *Agent) rememberVisualTurn(calls []llm.ToolCall, results []toolResult) {
	if a.Env == nil {
		return
	}
	for _, tc := range calls {
		if tc.Name == "question" {
			return
		}
	}
	seen := map[string]bool{}
	var found []visualArtifact
	for i, tc := range calls {
		if i >= len(results) {
			break
		}
		if art, ok := detectVisualArtifact(tc, results[i].content); ok && !seen[art.Ref] {
			seen[art.Ref] = true
			found = append(found, art)
		}
	}
	for _, art := range found {
		duplicate := false
		for _, pending := range a.visualPending {
			if pending.Ref == art.Ref {
				duplicate = true
				break
			}
		}
		if !duplicate {
			a.visualPending = append(a.visualPending, art)
		}
	}
}

// finishVisualReview is the end-of-run hand-off. A preview is ready only when
// the model has stopped producing tool calls.
func (a *Agent) finishVisualReview(ctx context.Context) (stop, retry bool, err error) {
	if a.Env == nil || len(a.visualPending) == 0 || a.Env.AskUser == nil {
		return false, false, nil
	}
	pending := append([]visualArtifact(nil), a.visualPending...)
	what := pending[0].Ref
	if len(pending) > 1 {
		what = fmt.Sprintf("%d previews (newest: %s)", len(pending), pending[0].Ref)
	}
	answer, err := a.Env.AskUser(tools.Question{
		ID:       "visual-review",
		Header:   "preview ready",
		Question: "The preview is ready: " + what + ". Does it look right?",
		Options: []tools.QuestionOption{
			{Label: "Looks good — ship it", Description: "Stop here and keep this result."},
			{Label: "Needs changes — I'll describe", Description: "I will type the changes next."},
		},
	})
	if err != nil {
		return false, false, err
	}
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)

	switch {
	case answer == "", strings.HasPrefix(lower, "looks good"), strings.Contains(lower, "ship it"):
		a.visualPending = nil
		_ = a.Sess.Append(llm.Message{Role: "user", Content: "The user approved the preview. Stop here; do not iterate further."})
		return true, false, nil
	case strings.HasPrefix(lower, "needs changes"):
		follow, err := a.Env.AskUser(tools.Question{
			ID:       "visual-changes",
			Header:   "changes",
			Question: "What should change? Type the changes you want.",
		})
		if err != nil {
			return false, false, err
		}
		follow = strings.TrimSpace(follow)
		if follow == "" {
			a.visualPending = nil
			_ = a.Sess.Append(llm.Message{Role: "user", Content: "The user did not specify changes. Stop here."})
			return true, false, nil
		}
		_ = a.Sess.Append(llm.Message{Role: "user", Content: "Preview feedback: " + follow})
		return false, true, nil
	default:
		_ = a.Sess.Append(llm.Message{Role: "user", Content: "Preview feedback: " + answer})
		return false, true, nil
	}
}
