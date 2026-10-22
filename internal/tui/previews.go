package tui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// preview is one artifact the agent produced: a local dev server, an HTML
// file, a PDF, an image, or a video. The newest is shown in the top bar; the
// full de-duplicated list lives in the inspector and /previews.
type preview struct {
	Ref    string
	Kind   string
	Label  string
	Source string
	At     time.Time
}

var fileURLPattern = regexp.MustCompile(`file://[^\s"'<>)]+`)

func (p preview) isURL() bool {
	return strings.HasPrefix(p.Ref, "http://") || strings.HasPrefix(p.Ref, "https://") || strings.HasPrefix(p.Ref, "file://")
}

// key collapses local URLs to their origin so a site does not appear once for
// every path the browser visits, and file paths to their absolute path.
func (p preview) key(cwd string) string {
	if strings.HasPrefix(p.Ref, "http://") || strings.HasPrefix(p.Ref, "https://") {
		if u, err := url.Parse(p.Ref); err == nil && u.Host != "" {
			return strings.ToLower(u.Scheme + "://" + u.Host)
		}
	}
	ref := p.Ref
	if strings.HasPrefix(ref, "file://") {
		if u, err := url.Parse(ref); err == nil {
			ref = u.Path
		}
	}
	if !filepath.IsAbs(ref) {
		ref = filepath.Join(cwd, ref)
	}
	return filepath.Clean(ref)
}

func (c *chat) addPreview(p preview) {
	p.Ref = strings.TrimSpace(sanitizeTerminalText(p.Ref))
	p.Label = sanitizeTerminalText(p.Label)
	if p.Ref == "" {
		return
	}
	if p.Kind == "" {
		if p.isURL() {
			p.Kind = "site"
		} else {
			p.Kind = previewKindForFile(p.Ref)
		}
	}
	if p.Label == "" {
		p.Label = p.Ref
	}
	p.At = time.Now()
	key := p.key(c.opt.Cwd)
	for i, old := range c.previews {
		if old.key(c.opt.Cwd) == key {
			c.previews = append(c.previews[:i], c.previews[i+1:]...)
			break
		}
	}
	c.previews = append([]preview{p}, c.previews...)
	if len(c.previews) > 12 {
		c.previews = c.previews[:12]
	}
	c.previewURL = c.previews[0].Ref
	c.layout()
}

func sitePreview(ref, source string) preview {
	ref = strings.TrimRight(ref, ".,;:!?)]}'\"·–—")
	return preview{Ref: ref, Kind: "site", Label: ref, Source: source}
}

func filePreview(path, source string) (preview, bool) {
	kind := previewKindForFile(path)
	if kind == "" {
		return preview{}, false
	}
	return preview{Ref: path, Kind: kind, Label: path, Source: source}, true
}

func previewKindForFile(path string) string {
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
	return ""
}

func previewGlyph(kind string) (string, lipgloss.Style) {
	switch kind {
	case "site":
		return "◉", styleLink
	case "html":
		return "▤", styleAccent2
	case "pdf":
		return "▤", styleBad
	case "image":
		return "▣", styleSandbox
	case "video":
		return "▶", styleAccent
	default:
		return "•", styleDim
	}
}

func previewLabel(p preview) string {
	if p.Label != "" {
		return p.Label
	}
	return p.Ref
}

// previewBar is the one-line live banner above the transcript.
func (c *chat) previewBar() string {
	if c.previewURL == "" {
		return ""
	}
	status := "ready"
	if c.question != nil {
		status = "waiting for your feedback"
	} else if c.streaming {
		status = "still iterating · esc to stop"
	}
	icon := "●"
	if c.streaming {
		icon = c.sp.View()
	}
	label := c.previewURL
	if len(c.previews) > 0 {
		label = previewLabel(c.previews[0])
	}
	left := stylePreviewText.Render(" "+icon+" preview ready ") + " " +
		styleLink.Render(clip(label, max(12, c.w-48))) + "  " + styleFaint.Render(status)
	if len(c.previews) > 1 {
		left += "  " + styleAccent2.Render(fmt.Sprintf("%d previews", len(c.previews)))
	}
	right := ""
	if c.w >= 84 && c.modal == modalNone {
		if len(c.previews) > 1 {
			right = keyHint("/previews", "list") + "  " + keyHint("ctrl+o", "open") + " "
		} else {
			right = keyHint("ctrl+o", "open") + " "
		}
	}
	if c.w <= 0 {
		return left
	}
	gap := c.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		return lipgloss.NewStyle().Background(cPanel).Width(c.w).Render(clipANSI(" "+left+" ", c.w))
	}
	line := " " + left + strings.Repeat(" ", gap) + right + " "
	return lipgloss.NewStyle().Background(cPanel).Width(c.w).Render(line)
}

// detectPreviewFromDetail looks at a write/edit path. It deliberately ignores
// reads and ordinary source files: only things a person can preview count.
func (c *chat) detectPreviewFromDetail(name, detail string) {
	base := toolBaseName(name)
	switch base {
	case "write", "create", "edit", "multi_edit", "replace", "refactor", "rename":
	default:
		return
	}
	detail = strings.TrimSpace(strings.Trim(detail, `"'`))
	if detail == "" || strings.ContainsAny(detail, "\n\r\t{}") {
		return
	}
	if p, ok := filePreview(detail, base); ok {
		c.addPreview(p)
	}
}

// detectToolPreview inspects tool output. Only a browser opening a local/file
// target, or a shell reporting a dev-server banner, counts. Reading an old log
// or file in a fresh chat must not resurrect a previous preview.
func (c *chat) detectToolPreview(name, text string) {
	base := toolBaseName(name)
	switch base {
	case "browser", "screenshot":
		if p, ok := c.findPreviewTarget(text); ok {
			p.Source = base
			c.addPreview(p)
		}
	case "bash", "verify_site":
		if !serverBanner(text) {
			return
		}
		if ref := findPreviewURL(text); ref != "" {
			c.addPreview(sitePreview(ref, base))
		}
	case "imagegen", "image_edit", "image":
		if p, ok := c.findPreviewFile(text); ok {
			p.Source = base
			c.addPreview(p)
		}
	}
}

// findPreviewFile finds an existing previewable file in free-form tool output.
func (c *chat) findPreviewFile(text string) (preview, bool) {
	clean := sanitizeTerminalText(text)
	for _, field := range strings.Fields(clean) {
		field = strings.Trim(field, `"'.,;:!?)]}`)
		if previewKindForFile(field) == "" || strings.Contains(field, "://") {
			continue
		}
		path := field
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.opt.Cwd, path)
		}
		if _, err := os.Stat(path); err == nil {
			if p, ok := filePreview(field, "image"); ok {
				return p, true
			}
		}
	}
	return preview{}, false
}

func toolBaseName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// findPreviewTarget extracts the first genuine local/file preview from browser
// output, preferring a local server URL over a file path.
func (c *chat) findPreviewTarget(text string) (preview, bool) {
	clean := sanitizeTerminalText(text)
	if ref := findPreviewURL(clean); ref != "" {
		return sitePreview(ref, "browser"), true
	}
	if m := fileURLPattern.FindString(clean); m != "" {
		m = strings.TrimRight(m, ".,;:!?)]}'\"")
		if u, err := url.Parse(m); err == nil && u.Path != "" {
			if p, ok := filePreview(u.Path, "browser"); ok {
				return p, true
			}
		}
	}
	if p, ok := c.findPreviewFile(clean); ok {
		return p, true
	}
	return preview{}, false
}

// openPreview opens a site or a file with the platform's default handler.
func (c *chat) openPreview(p preview) error {
	if strings.HasPrefix(p.Ref, "http://") || strings.HasPrefix(p.Ref, "https://") {
		return openLocalURL(p.Ref)
	}
	path := p.Ref
	if strings.HasPrefix(path, "file://") {
		if u, err := url.Parse(path); err == nil {
			path = u.Path
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.opt.Cwd, path)
	}
	return openLocalPath(path)
}

func (c *chat) openNewestPreview() error {
	if len(c.previews) > 0 {
		return c.openPreview(c.previews[0])
	}
	if c.previewURL == "" {
		return fmt.Errorf("no preview detected")
	}
	return c.openPreview(preview{Ref: c.previewURL, Kind: "site"})
}

func openLocalPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// openPreviews shows the full list for choosing.
func (c *chat) openPreviews() tea.Cmd {
	if len(c.previews) == 0 {
		c.note("no previews detected yet", true)
		return nil
	}
	c.modal = modalPreviews
	c.previewSel = 0
	c.clearPalette()
	c.closeFilePalette()
	c.layout()
	c.refresh()
	return nil
}

func (c *chat) previewKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		c.modal = modalNone
		c.layout()
		c.refresh()
	case "up", "k", "ctrl+p":
		if c.previewSel > 0 {
			c.previewSel--
		}
		c.layout()
		c.refresh()
	case "down", "j", "ctrl+n":
		if c.previewSel < len(c.previews)-1 {
			c.previewSel++
		}
		c.layout()
		c.refresh()
	case "enter":
		if c.previewSel < 0 || c.previewSel >= len(c.previews) {
			return nil
		}
		p := c.previews[c.previewSel]
		if err := c.openPreview(p); err != nil {
			c.note("could not open preview: "+err.Error(), true)
		} else {
			c.note("opened "+p.Ref, false)
		}
		c.modal = modalNone
		c.layout()
		c.refresh()
	}
	return nil
}

// previewsCard renders the preview picker overlay.
func (c *chat) previewsCard() string {
	mainW := c.mainWidth()
	cardW := min(max(44, mainW-8), 92)
	innerW := max(30, cardW-6)
	lines := []string{
		styleAccent2.Bold(true).Render("◈  PREVIEWS") + "  " + styleFaint.Render(itoa(len(c.previews))+" detected"),
		styleDim.Render("sites, pages, PDFs, images, and video the agent produced"),
		"",
	}
	limit := min(len(c.previews), max(3, c.vp.Height-8))
	start := 0
	if c.previewSel >= limit {
		start = c.previewSel - limit + 1
	}
	end := min(len(c.previews), start+limit)
	for i := start; i < end; i++ {
		p := c.previews[i]
		glyph, gs := previewGlyph(p.Kind)
		text := glyph + " " + clip(previewLabel(p), innerW-18) + "  " + p.Kind
		if i == c.previewSel {
			lines = append(lines, styleSelected.Render(" > "+pad(text, innerW-3)))
		} else {
			lines = append(lines, "   "+gs.Render(glyph)+" "+styleDim.Render(clip(previewLabel(p), innerW-18))+"  "+styleFaint.Render(p.Kind))
		}
	}
	if end < len(c.previews) {
		lines = append(lines, styleFaint.Render(fmt.Sprintf("  … %d more below", len(c.previews)-end)))
	}
	lines = append(lines, "",
		" "+styleFaint.Render("↑↓ move · enter open · esc close"))
	for i := range lines {
		lines[i] = clipANSI(lines[i], innerW)
	}
	return styleModal.Width(innerW + 4).Render(strings.Join(lines, "\n"))
}

// previewsSection renders the inspector block.
func (c *chat) previewsSection(contentW int) []string {
	out := []string{sectionLabel("previews")}
	if len(c.previews) == 0 {
		out = append(out, styleFaint.Render("none · ctrl+o when ready"))
		return out
	}
	for i, p := range c.previews {
		if i >= 6 {
			out = append(out, styleFaint.Render(fmt.Sprintf("+%d more · /previews", len(c.previews)-i)))
			break
		}
		glyph, gs := previewGlyph(p.Kind)
		out = append(out, styleFaint.Render(fmt.Sprintf("%d ", i+1))+
			gs.Render(glyph)+" "+styleDim.Render(clip(previewLabel(p), contentW-5)))
	}
	out = append(out, styleFaint.Render("ctrl+o open · /previews list"))
	return out
}
