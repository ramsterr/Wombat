package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ImageSearch finds image references with direct download URLs and can save
// them into the workspace.
type ImageSearch struct{}

type imageSearchArgs struct {
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
	Download    bool   `json:"download,omitempty"`
	Path        string `json:"path,omitempty"`
	Orientation string `json:"orientation,omitempty"`
	MinWidth    int    `json:"min_width,omitempty"`
	MinHeight   int    `json:"min_height,omitempty"`
	Attach      bool   `json:"attach,omitempty"`
}

type ImageResult struct {
	Title     string
	PageURL   string
	ImageURL  string
	Thumbnail string
	Width     int
	Height    int
	Source    string
	License   string
}

type imageBatch struct {
	engine  string
	results []ImageResult
}

var (
	serperImagesURL    = "https://google.serper.dev/images"
	braveImagesURL     = "https://api.search.brave.com/res/v1/images/search?q="
	ddgImagesURL       = "https://duckduckgo.com/?q="
	ddgImagesJSONURL   = "https://duckduckgo.com/i.js?l=us-en&o=json&q="
	openverseImagesURL = "https://api.openverse.org/v1/images/?q="
	ddgVQDRe           = regexp.MustCompile(`vqd=["']?([0-9-]+)`)
	imageExtRe         = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|avif|bmp|svg)(?:$|\?)`)
)

func (t *ImageSearch) Name() string  { return "imagesearch" }
func (t *ImageSearch) Class() string { return "network" }
func (t *ImageSearch) Description() string {
	return "Find image references and assets with direct image URLs. Uses Google Images through Serper, Brave Images, DuckDuckGo Images, and Openverse; can download selected images into the workspace. Use it for photos, illustrations, textures, and UI references instead of claiming image search or Google Images is unavailable."
}
func (t *ImageSearch) Schema() map[string]any {
	return obj(map[string]any{
		"query":       desc("what the image should show; include style, subject, and context"),
		"limit":       map[string]any{"type": "integer", "description": "number of results (default 8, max 20)"},
		"download":    map[string]any{"type": "boolean", "description": "save the selected images into the workspace"},
		"path":        desc("directory to save downloaded images in (default .wombat/images)"),
		"orientation": map[string]any{"type": "string", "enum": []string{"all", "landscape", "portrait", "square"}, "description": "preferred image orientation"},
		"min_width":   map[string]any{"type": "integer", "description": "minimum image width in pixels"},
		"min_height":  map[string]any{"type": "integer", "description": "minimum image height in pixels"},
		"attach":      map[string]any{"type": "boolean", "description": "attach the first downloaded image so you can see it"},
	}, "query")
}

func (t *ImageSearch) Run(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
	a, err := decode[imageSearchArgs](raw)
	if err != nil {
		return "", fmt.Errorf("bad arguments: %w", err)
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	if err := env.Check("network", "image search: "+query); err != nil {
		return "", err
	}
	results := searchImages(ctx, query, limit, a.MinWidth, a.MinHeight, a.Orientation)
	if len(results) == 0 {
		return fmt.Sprintf("no safe image results for %q. Try a more specific query, a different orientation, or a direct image URL with webfetch.", query), nil
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   page: %s\n   image: %s\n", i+1, r.Title, r.PageURL, r.ImageURL)
		var meta []string
		if r.Width > 0 && r.Height > 0 {
			meta = append(meta, fmt.Sprintf("%dx%d", r.Width, r.Height))
		}
		if r.Source != "" {
			meta = append(meta, "via "+r.Source)
		}
		if r.License != "" {
			meta = append(meta, r.License)
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "   %s\n", strings.Join(meta, " · "))
		}
	}
	if !a.Download {
		return strings.TrimSpace(b.String()), nil
	}

	dir := strings.TrimSpace(a.Path)
	if dir == "" {
		dir = ".wombat/images"
	}
	if err := env.CheckPath("write", dir); err != nil {
		return "", err
	}
	absDir, err := env.ResolvePath(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", err
	}
	slug := imageSlug(query)
	saved := 0
	for i, r := range results {
		if saved >= limit {
			break
		}
		data, ext, err := downloadImage(ctx, r.ImageURL)
		if err != nil {
			continue
		}
		name := fmt.Sprintf("%s-%02d%s", slug, i+1, ext)
		path := filepath.Join(absDir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			continue
		}
		fmt.Fprintf(&b, "\nsaved: %s\n", filepath.ToSlash(filepath.Join(dir, name)))
		if a.Attach && saved == 0 {
			env.AddMedia(Media{Path: path, MIME: mimeForExt(ext), Name: name})
		}
		saved++
	}
	if saved == 0 {
		return strings.TrimSpace(b.String()) + "\n\nno images could be downloaded; try the page URLs or a direct image URL.", nil
	}
	return strings.TrimSpace(b.String()) + fmt.Sprintf("\n\ndownloaded %d image(s) to %s", saved, filepath.ToSlash(dir)), nil
}

func searchImages(ctx context.Context, query string, limit, minW, minH int, orientation string) []ImageResult {
	engines := []struct {
		name string
		run  func(context.Context, string, int) ([]ImageResult, error)
	}{}
	if key := strings.TrimSpace(os.Getenv("SERPER_API_KEY")); key != "" {
		engines = append(engines, struct {
			name string
			run  func(context.Context, string, int) ([]ImageResult, error)
		}{"serper", func(ctx context.Context, q string, n int) ([]ImageResult, error) {
			return searchSerperImages(ctx, key, q, n)
		}})
	}
	if key := strings.TrimSpace(os.Getenv("BRAVE_API_KEY")); key != "" {
		engines = append(engines, struct {
			name string
			run  func(context.Context, string, int) ([]ImageResult, error)
		}{"brave", func(ctx context.Context, q string, n int) ([]ImageResult, error) {
			return searchBraveImages(ctx, key, q, n)
		}})
	}
	engines = append(engines, struct {
		name string
		run  func(context.Context, string, int) ([]ImageResult, error)
	}{"duckduckgo", searchDuckDuckGoImages})
	engines = append(engines, struct {
		name string
		run  func(context.Context, string, int) ([]ImageResult, error)
	}{"openverse", searchOpenverseImages})

	searchCtx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()
	ch := make(chan imageBatch, len(engines))
	for _, engine := range engines {
		engine := engine
		go func() {
			engineCtx, engineCancel := context.WithTimeout(searchCtx, 3*time.Second)
			defer engineCancel()
			results, _ := engine.run(engineCtx, query, limit)
			ch <- imageBatch{engine: engine.name, results: results}
		}()
	}
	var batches []imageBatch
	remaining := len(engines)
	for remaining > 0 {
		select {
		case batch := <-ch:
			remaining--
			if len(batch.results) > 0 {
				batches = append(batches, batch)
			}
		case <-searchCtx.Done():
			remaining = 0
		}
	}
	cancel()
	return rankImageResults(query, batches, limit, minW, minH, orientation)
}

func searchSerperImages(ctx context.Context, key, query string, limit int) ([]ImageResult, error) {
	payload, _ := json.Marshal(map[string]any{"q": query, "num": limit, "safe": "active"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serperImagesURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", key)
	var body struct {
		Images []struct {
			Title       string `json:"title"`
			ImageURL    string `json:"imageUrl"`
			ImageWidth  int    `json:"imageWidth"`
			ImageHeight int    `json:"imageHeight"`
			Thumbnail   string `json:"thumbnailUrl"`
			Link        string `json:"link"`
			Source      string `json:"source"`
		} `json:"images"`
	}
	if err := webJSON(req, &body); err != nil {
		return nil, err
	}
	out := make([]ImageResult, 0, len(body.Images))
	for _, r := range body.Images {
		out = append(out, ImageResult{
			Title: r.Title, PageURL: r.Link, ImageURL: r.ImageURL,
			Thumbnail: r.Thumbnail, Width: r.ImageWidth, Height: r.ImageHeight,
			Source: "google",
		})
	}
	return out, nil
}

func searchBraveImages(ctx context.Context, key, query string, limit int) ([]ImageResult, error) {
	endpoint := braveImagesURL + url.QueryEscape(query) + "&count=" + fmt.Sprint(limit) + "&safesearch=strict"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	var body struct {
		Results []struct {
			Title     string `json:"title"`
			URL       string `json:"url"`
			Thumbnail struct {
				Src    string `json:"src"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"thumbnail"`
			Properties struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"properties"`
			Source string `json:"source"`
		} `json:"results"`
	}
	if err := webJSON(req, &body); err != nil {
		return nil, err
	}
	out := make([]ImageResult, 0, len(body.Results))
	for _, r := range body.Results {
		imageURL := r.Properties.URL
		if imageURL == "" {
			imageURL = r.Thumbnail.Src
		}
		out = append(out, ImageResult{
			Title: r.Title, PageURL: r.URL, ImageURL: imageURL,
			Thumbnail: r.Thumbnail.Src, Width: r.Properties.Width, Height: r.Properties.Height,
			Source: "brave",
		})
	}
	return out, nil
}

func searchDuckDuckGoImages(ctx context.Context, query string, limit int) ([]ImageResult, error) {
	page, err := fetchText(ctx, ddgImagesURL+url.QueryEscape(query)+"&iax=images&ia=images")
	if err != nil {
		return nil, err
	}
	m := ddgVQDRe.FindStringSubmatch(page)
	if len(m) < 2 {
		return nil, fmt.Errorf("duckduckgo: no image token")
	}
	endpoint := ddgImagesJSONURL + url.QueryEscape(query) + "&vqd=" + url.QueryEscape(m[1]) + "&f=,,,&p=1&kp=-2"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", "https://duckduckgo.com/")
	req.Header.Set("Accept", "application/json")
	resp, err := webHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo images: %s", resp.Status)
	}
	var body struct {
		Results []struct {
			Image     string `json:"image"`
			Thumbnail string `json:"thumbnail"`
			Title     string `json:"title"`
			URL       string `json:"url"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Source    string `json:"source"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	out := make([]ImageResult, 0, len(body.Results))
	for _, r := range body.Results {
		out = append(out, ImageResult{
			Title: r.Title, PageURL: r.URL, ImageURL: r.Image,
			Thumbnail: r.Thumbnail, Width: r.Width, Height: r.Height,
			Source: "duckduckgo",
		})
	}
	return out, nil
}

func searchOpenverseImages(ctx context.Context, query string, limit int) ([]ImageResult, error) {
	endpoint := openverseImagesURL + url.QueryEscape(query) + "&page_size=" + fmt.Sprint(limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)
	var body struct {
		Results []struct {
			Title             string `json:"title"`
			URL               string `json:"url"`
			Thumbnail         string `json:"thumbnail"`
			ForeignLandingURL string `json:"foreign_landing_url"`
			License           string `json:"license"`
			Width             int    `json:"width"`
			Height            int    `json:"height"`
		} `json:"results"`
	}
	if err := webJSON(req, &body); err != nil {
		return nil, err
	}
	out := make([]ImageResult, 0, len(body.Results))
	for _, r := range body.Results {
		page := r.ForeignLandingURL
		if page == "" {
			page = r.URL
		}
		out = append(out, ImageResult{
			Title: r.Title, PageURL: page, ImageURL: r.URL,
			Thumbnail: r.Thumbnail, Width: r.Width, Height: r.Height,
			Source: "openverse", License: r.License,
		})
	}
	return out, nil
}

func rankImageResults(query string, batches []imageBatch, limit, minW, minH int, orientation string) []ImageResult {
	tokens := queryTokens(query)
	byURL := map[string]*ImageResult{}
	engines := map[string]int{}
	order := 0
	orderOf := map[string]int{}
	for _, batch := range batches {
		for _, r := range batch.results {
			r.ImageURL = canonicalResultURL(r.ImageURL)
			r.PageURL = canonicalResultURL(r.PageURL)
			r.Title = strings.TrimSpace(stripTags(r.Title))
			if r.ImageURL == "" || !strings.HasPrefix(r.ImageURL, "http") {
				continue
			}
			if r.Width > 0 && r.Width < minW || r.Height > 0 && r.Height < minH {
				continue
			}
			if orientation == "landscape" && r.Width > 0 && r.Width <= r.Height {
				continue
			}
			if orientation == "portrait" && r.Height > 0 && r.Height <= r.Width {
				continue
			}
			if orientation == "square" && r.Width > 0 && r.Height > 0 {
				ratio := float64(r.Width) / float64(r.Height)
				if ratio < 0.8 || ratio > 1.25 {
					continue
				}
			}
			if isAdultResult(SearchResult{Title: r.Title, URL: r.ImageURL, Snippet: r.PageURL}) ||
				isSpamHost(SearchResult{URL: r.ImageURL}) ||
				isSpamHost(SearchResult{URL: r.PageURL}) || isParked(SearchResult{Title: r.Title}) {
				continue
			}
			if old, ok := byURL[r.ImageURL]; ok {
				if len(r.Title) > len(old.Title) {
					old.Title = r.Title
				}
				engines[r.ImageURL]++
				continue
			}
			cp := r
			byURL[r.ImageURL] = &cp
			engines[r.ImageURL] = 1
			orderOf[r.ImageURL] = order
			order++
		}
	}
	cands := make([]*ImageResult, 0, len(byURL))
	for url, r := range byURL {
		_ = url
		cands = append(cands, r)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		si := scoreImage(tokens, *cands[i], engines[cands[i].ImageURL])
		sj := scoreImage(tokens, *cands[j], engines[cands[j].ImageURL])
		if si != sj {
			return si > sj
		}
		return orderOf[cands[i].ImageURL] < orderOf[cands[j].ImageURL]
	})
	out := make([]ImageResult, 0, limit)
	perHost := map[string]int{}
	for _, r := range cands {
		if len(out) >= limit {
			break
		}
		host := hostnameOf(r.PageURL)
		if perHost[host] >= 3 {
			continue
		}
		perHost[host]++
		out = append(out, *r)
	}
	return out
}

func scoreImage(tokens []string, r ImageResult, engines int) float64 {
	score := 0.0
	host := hostnameOf(r.PageURL)
	if trustedHost(host) {
		score += 3
	}
	lowerTitle := strings.ToLower(r.Title)
	for _, token := range tokens {
		if strings.Contains(lowerTitle, token) {
			score += 1.2
		}
		if strings.Contains(strings.ToLower(r.PageURL+" "+r.ImageURL), token) {
			score += 0.5
		}
		if strings.Contains(host, token) {
			score += 1.5
		}
	}
	if engines > 1 {
		score += float64(engines-1) * 0.8
	}
	if r.Width > 0 && r.Height > 0 {
		score += float64(min(r.Width, 1200)) / 2000.0
	}
	if r.License != "" {
		score += 0.5
	}
	if strings.Contains(strings.ToLower(r.ImageURL), ".svg") {
		score += 0.8
	}
	return score
}

func downloadImage(ctx context.Context, target string) ([]byte, string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(runCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/*,*/*;q=0.8")
	resp, err := webHTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("empty image")
	}
	kind := http.DetectContentType(data)
	if !strings.HasPrefix(kind, "image/") {
		head := strings.ToLower(string(data[:min(512, len(data))]))
		if strings.Contains(head, "<svg") {
			return data, ".svg", nil
		}
		return nil, "", fmt.Errorf("not an image: %s", kind)
	}
	return data, extForImageKind(kind), nil
}

func extForImageKind(kind string) string {
	switch strings.ToLower(kind) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ".png"
	}
}

func imageSlug(query string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(query) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "image"
	}
	return s
}

func mimeForExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "image/png"
	}
}

func fetchText(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := webHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
