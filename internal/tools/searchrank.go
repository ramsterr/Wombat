package tools

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// searchBatch is the result set from one backend.
type searchBatch struct {
	engine  string
	results []SearchResult
	err     error
}

type candidate struct {
	result   SearchResult
	engines  map[string]bool
	bestRank int
	hits     int
	score    float64
	order    int
}

type rankOptions struct {
	Freshness  string
	MaxPerHost int
}

var trustedSearchHosts = []string{
	"github.com", "gitlab.com", "bitbucket.org", "stackoverflow.com", "stackexchange.com",
	"sourcegraph.com", "developer.mozilla.org", "web.dev", "caniuse.com", "w3.org",
	"whatwg.org", "ietf.org", "rfc-editor.org", "docs.python.org", "python.org",
	"peps.python.org", "pkg.go.dev", "go.dev", "golang.org", "git-scm.com",
	"scikit-learn.org", "pytorch.org", "tensorflow.org", "numpy.org", "pandas.pydata.org",
	"readthedocs.io", "arxiv.org", "wikipedia.org", "wikimedia.org",
	"developer.apple.com", "learn.microsoft.com", "docs.microsoft.com", "cloud.google.com",
	"aws.amazon.com", "kubernetes.io", "helm.sh", "docker.com", "rust-lang.org",
	"crates.io", "nodejs.org", "npmjs.com", "pypi.org", "react.dev", "vuejs.org",
	"angular.io", "typescriptlang.org", "spring.io", "kotlinlang.org", "swift.org",
	"php.net", "ruby-lang.org", "postgresql.org", "sqlite.org", "mysql.com",
	"redis.io", "mongodb.com", "elastic.co", "grafana.com", "prometheus.io",
	"terraform.io", "ansible.com", "jenkins.io", "postman.com", "openai.com",
	"anthropic.com", "huggingface.co", "kaggle.com", "gsocorganizations.dev",
	"summerofcode.withgoogle.com", "nature.com", "science.org", "acm.org",
	"ieee.org", "springer.com", "sciencedirect.com", "pubmed.ncbi.nlm.nih.gov",
	"ncbi.nlm.nih.gov", "who.int", "cdc.gov", "nih.gov", "un.org", "europa.eu",
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

var searchEngineBoost = map[string]float64{
	"exa": 0.9, "tavily": 0.5, "brave": 0.4, "serper": 0.4,
	"bing": 0.15, "duckduckgo": 0.1, "mojeek": 0.1,
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "of": true,
	"to": true, "in": true, "on": true, "for": true, "with": true, "is": true,
	"are": true, "was": true, "were": true, "what": true, "how": true,
	"why": true, "when": true, "where": true, "best": true, "top": true,
}

// rankSearchResults merges the backends, removes poisoned results, rewards
// trusted and cross-engine hits, and limits how many results one domain can
// contribute. It is the test-friendly default used with three hits per host.
func rankSearchResults(query string, batches []searchBatch, limit int) []SearchResult {
	return rankSearchResultsWithOptions(query, batches, limit, rankOptions{MaxPerHost: 3})
}

func rankSearchResultsWithOptions(query string, batches []searchBatch, limit int, opts rankOptions) []SearchResult {
	query = strings.TrimSpace(query)
	tokens := queryTokens(query)
	lowerQuery := strings.ToLower(query)
	maxPerHost := opts.MaxPerHost
	if maxPerHost <= 0 {
		maxPerHost = 3
	}
	cutoff, filterFreshness := freshnessCutoff(opts.Freshness)
	byURL := map[string]*candidate{}
	order := 0
	for _, batch := range batches {
		for _, r := range batch.results {
			r.URL = canonicalResultURL(r.URL)
			r.Title = strings.TrimSpace(stripTags(r.Title))
			r.Snippet = cleanSnippet(r.Snippet)
			r.Author = cleanMetaValue(r.Author)
			r.Published = normalizeDate(r.Published)
			if r.URL == "" || r.Title == "" {
				continue
			}
			if isAdultResult(r) || isParked(r) || isSpamHost(r) {
				continue
			}
			if filterFreshness {
				if t, ok := publishedTime(r.Published); ok && t.Before(cutoff) {
					continue
				}
			}
			c, ok := byURL[r.URL]
			if !ok {
				c = &candidate{result: r, engines: map[string]bool{}, order: order}
				order++
				byURL[r.URL] = c
			} else {
				if len(r.Snippet) > len(c.result.Snippet) {
					c.result.Snippet = r.Snippet
				}
				if len(r.Title) > len(c.result.Title) {
					c.result.Title = r.Title
				}
				if c.result.Published == "" && r.Published != "" {
					c.result.Published = r.Published
				}
				if c.result.Author == "" && r.Author != "" {
					c.result.Author = r.Author
				}
			}
			if r.engine != "" {
				c.engines[r.engine] = true
			}
			if r.rank > 0 && (c.bestRank == 0 || r.rank < c.bestRank) {
				c.bestRank = r.rank
			}
			c.hits++
		}
	}
	if len(byURL) == 0 {
		return nil
	}
	cands := make([]*candidate, 0, len(byURL))
	for _, c := range byURL {
		c.score = scoreCandidate(lowerQuery, tokens, c, opts.Freshness)
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].order < cands[j].order
	})

	// First give every domain one slot, then fill the rest. This keeps a
	// strong site from crowding out the whole result set while still allowing
	// several useful pages from it.
	out := make([]SearchResult, 0, limit)
	picked := map[string]bool{}
	perHost := map[string]int{}
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		host := resultHost(c.result.URL)
		if perHost[host] == 0 {
			perHost[host]++
			picked[c.result.URL] = true
			out = append(out, c.result)
		}
	}
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		if picked[c.result.URL] {
			continue
		}
		host := resultHost(c.result.URL)
		if perHost[host] >= maxPerHost {
			continue
		}
		perHost[host]++
		picked[c.result.URL] = true
		out = append(out, c.result)
	}
	return out
}

func resultHost(raw string) string {
	if host := hostnameOf(raw); host != "" {
		return host
	}
	return "_"
}

func scoreCandidate(query string, tokens []string, c *candidate, freshness string) float64 {
	r := c.result
	host := hostnameOf(r.URL)
	title := strings.ToLower(r.Title)
	snippet := strings.ToLower(r.Snippet)
	urlText := strings.ToLower(r.URL)
	score := 0.0
	if trustedHost(host) {
		score += 4.0
	}
	if trustedSuffix(host) {
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
	if len(c.engines) > 1 {
		score += float64(len(c.engines)-1) * 0.9
	}
	boost := 0.0
	for engine := range c.engines {
		if b := searchEngineBoost[engine]; b > boost {
			boost = b
		}
	}
	score += boost
	if c.bestRank > 0 {
		score += 1.6 / float64(c.bestRank)
	}
	if c.hits > len(c.engines) {
		extra := float64(c.hits - len(c.engines))
		if extra > 3 {
			extra = 3
		}
		score += extra * 0.1
	}
	recency := recencyBoost(r.Published)
	if strings.TrimSpace(freshness) != "" {
		score += recency * 1.5
	} else {
		score += recency * 0.3
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
	if i := strings.Index(query, "site:"); i >= 0 {
		fields := strings.Fields(query[i+5:])
		if len(fields) > 0 && strings.Contains(host, strings.Trim(fields[0], "/")) {
			score += 4
		}
	}
	return score
}

func freshnessCutoff(freshness string) (time.Time, bool) {
	days := 0
	switch strings.ToLower(strings.TrimSpace(freshness)) {
	case "day":
		days = 1
	case "week":
		days = 7
	case "month":
		days = 30
	case "year":
		days = 365
	}
	if days == 0 {
		return time.Time{}, false
	}
	return time.Now().AddDate(0, 0, -days), true
}

func recencyBoost(published string) float64 {
	t, ok := publishedTime(published)
	if !ok {
		return 0
	}
	age := time.Since(t)
	switch {
	case age < 7*24*time.Hour:
		return 1
	case age < 30*24*time.Hour:
		return 0.6
	case age < 365*24*time.Hour:
		return 0.25
	}
	return 0
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

func trustedSuffix(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, suffix := range []string{".gov", ".edu", ".mil", ".int", ".ac.uk", ".edu.au", ".ac.jp", ".ac.nz", ".ac.za", ".ac.in", ".edu.sg"} {
		if strings.HasSuffix(host, suffix) {
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
