package tools

import (
	"net/url"
	"sort"
	"strings"
)

// searchBatch is the result set from one backend.
type searchBatch struct {
	engine  string
	results []SearchResult
	err     error
}

type candidate struct {
	result  SearchResult
	engines int
	score   float64
	order   int
}

var trustedSearchHosts = []string{
	"github.com", "gitlab.com", "stackoverflow.com", "stackexchange.com",
	"developer.mozilla.org", "docs.python.org", "python.org", "pkg.go.dev",
	"go.dev", "golang.org", "scikit-learn.org", "pytorch.org", "tensorflow.org",
	"numpy.org", "pandas.pydata.org", "readthedocs.io", "arxiv.org",
	"wikipedia.org", "wikimedia.org", "developer.apple.com", "learn.microsoft.com",
	"docs.microsoft.com", "cloud.google.com", "aws.amazon.com", "kubernetes.io",
	"docker.com", "rust-lang.org", "crates.io", "nodejs.org", "npmjs.com",
	"react.dev", "vuejs.org", "angular.io", "openai.com", "anthropic.com",
	"huggingface.co", "kaggle.com", "gsocorganizations.dev",
	"summerofcode.withgoogle.com",
}

var adultSubstrings = []string{
	"porn", "xxx", "xnxx", "xvideos", "xhamster", "redtube", "pornhub",
	"onlyfans", "hentai", "nsfw", "camgirl", "sexcam", "sexvideo",
	"adultfriendfinder", "chaturbate", "stripchat", "bongacams", "livejasmin",
	"spankbang", "youporn", "brazzers", "blowjob", "handjob", "hardcore",
	"creampie", "cumshot", "deepthroat", "bdsm", "escort", "milf",
}
var adultTokens = map[string]bool{
	"sex": true, "porno": true, "anal": true, "cum": true, "orgasm": true,
	"fetish": true, "nude": true, "naked": true, "escorts": true,
}

var parkedTerms = []string{
	"domain for sale", "buy this domain", "parked domain", "sedo",
	"afternic", "hugedomains",
}

var spamTitleTerms = []string{
	"casino", "betting", "lottery", "loan", "essay", "assignment",
	"dissertation", "thesis", "exam dumps", "braindumps", "examtopics",
	"coupon", "promo code", "click here", "buy now", "replica",
	"torrent", "crack", "nulled", "warez", "mod apk",
}

var spamHostTerms = []string{
	"casino", "betting", "essay", "assignment", "dissertation", "thesis",
	"examtopics", "braindumps", "nulled", "warez", "porn", "xxx",
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true,
	"to": true, "in": true, "on": true, "for": true, "with": true, "is": true,
	"are": true, "was": true, "were": true, "what": true, "how": true,
	"why": true, "when": true, "where": true, "best": true, "top": true,
}

// rankSearchResults merges the backends, removes poisoned results, rewards
// trusted and cross-engine hits, and keeps a couple of hits per domain.
func rankSearchResults(query string, batches []searchBatch, limit int) []SearchResult {
	query = strings.TrimSpace(query)
	tokens := queryTokens(query)
	lowerQuery := strings.ToLower(query)
	byURL := map[string]*candidate{}
	order := 0
	for _, batch := range batches {
		for pos, r := range batch.results {
			r.URL = canonicalResultURL(r.URL)
			r.Title = strings.TrimSpace(stripTags(r.Title))
			r.Snippet = cleanSnippet(r.Snippet)
			if r.URL == "" || r.Title == "" {
				continue
			}
			if isAdultResult(r) || isParked(r) || isSpamHost(r) {
				continue
			}
			c, ok := byURL[r.URL]
			if !ok {
				c = &candidate{result: r, order: order}
				order++
				byURL[r.URL] = c
			} else {
				if len(r.Snippet) > len(c.result.Snippet) {
					c.result.Snippet = r.Snippet
				}
				if len(r.Title) > len(c.result.Title) && !candidateHasTitle(c, r.Title) {
					c.result.Title = r.Title
				}
			}
			c.engines++
			_ = pos
		}
	}
	if len(byURL) == 0 {
		return nil
	}
	cands := make([]*candidate, 0, len(byURL))
	for _, c := range byURL {
		c.score = scoreCandidate(lowerQuery, tokens, c.result, c.engines)
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].order < cands[j].order
	})
	out := make([]SearchResult, 0, limit)
	perHost := map[string]int{}
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		host := hostnameOf(c.result.URL)
		if perHost[host] >= 2 {
			continue
		}
		perHost[host]++
		out = append(out, c.result)
	}
	return out
}

func candidateHasTitle(c *candidate, title string) bool {
	return strings.EqualFold(c.result.Title, title)
}

func scoreCandidate(query string, tokens []string, r SearchResult, engines int) float64 {
	host := hostnameOf(r.URL)
	title := strings.ToLower(r.Title)
	snippet := strings.ToLower(r.Snippet)
	urlText := strings.ToLower(r.URL)
	score := 0.0
	if trustedHost(host) {
		score += 4.0
	}
	if strings.HasSuffix(host, ".gov") || strings.HasSuffix(host, ".edu") {
		score += 3.5
	}
	for _, token := range tokens {
		if strings.Contains(host, token) {
			score += 2.2
			break
		}
	}
	titleHits, snippetHits, urlHits := 0, 0, 0
	for _, token := range tokens {
		if strings.Contains(title, token) {
			titleHits++
		}
		if strings.Contains(snippet, token) {
			snippetHits++
		}
		if strings.Contains(urlText, token) {
			urlHits++
		}
	}
	score += float64(titleHits)*1.2 + float64(snippetHits)*0.45 + float64(urlHits)*0.7
	if len(tokens) > 1 && strings.Contains(title, strings.Join(tokens, " ")) {
		score += 1.5
	}
	if len(tokens) > 1 && strings.Contains(snippet, strings.Join(tokens, " ")) {
		score += 0.8
	}
	if engines > 1 {
		score += float64(engines-1) * 0.8
	}
	if n := len([]rune(r.Title)); n >= 8 && n <= 120 {
		score += 0.3
	}
	if len([]rune(r.Snippet)) >= 60 {
		score += 0.2
	}
	if strings.HasPrefix(r.URL, "https://") {
		score += 0.2
	}
	spam := 0
	for _, term := range spamTitleTerms {
		if strings.Contains(title, term) || strings.Contains(snippet, term) {
			spam++
		}
	}
	if spam > 0 {
		score -= float64(spam) * 2.5
	}
	if strings.Contains(query, "site:") {
		hostPart := strings.TrimSpace(strings.SplitN(query, "site:", 2)[1])
		hostPart = strings.Fields(hostPart)[0]
		if strings.Contains(host, hostPart) {
			score += 4
		}
	}
	return score
}

func queryTokens(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, token := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(token) < 2 || stopwords[token] || seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	return out
}

func trustedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range trustedSearchHosts {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func hostnameOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
}

func isAdultResult(r SearchResult) bool {
	text := strings.ToLower(r.Title + " " + r.Snippet + " " + r.URL)
	host := hostnameOf(r.URL)
	for _, term := range adultSubstrings {
		if strings.Contains(text, term) {
			return true
		}
	}
	for _, token := range strings.FieldsFunc(host+" "+text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	}) {
		if adultTokens[token] {
			return true
		}
	}
	return false
}

func isParked(r SearchResult) bool {
	text := strings.ToLower(r.Title + " " + r.Snippet)
	for _, term := range parkedTerms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func isSpamHost(r SearchResult) bool {
	host := hostnameOf(r.URL)
	for _, term := range spamHostTerms {
		if strings.Contains(host, term) {
			return true
		}
	}
	return false
}

func cleanSnippet(s string) string {
	s = stripTags(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > 320 {
		s = string(runes[:319]) + "…"
	}
	return s
}
