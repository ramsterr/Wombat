package tokens

import (
	"strings"

	"github.com/ramsterr/Wombat/internal/modelinfo"
)

const CharsPerToken = 3.5

func Estimate(s string) int {
	n := int(float64(len(s))/CharsPerToken + 0.5)
	if n < 1 && len(s) > 0 {
		return 1
	}
	return n
}

func EstimateLines(lines []string) int {
	total := 0
	for _, l := range lines {
		total += Estimate(l) + 1
	}
	return total
}

var modelWindows = []struct {
	match string
	ctx   int
}{
	{"claude", 200000},
	{"gpt-5", 256000},
	{"gpt-4.1", 256000},
	{"gpt-4o", 128000},
	{"o3", 200000},
	{"o4", 200000},
	{"deepseek", 128000},
	{"qwen", 128000},
	{"llama", 128000},
	{"mistral", 128000},
	{"gemma", 128000},
	{"phi", 128000},
	{"glm", 128000},
	{"grok", 128000},
}

const DefaultWindow = 128000
const OutputReserve = 16000

// Capabilities describes what a model can accept. The provider catalogue is
// the source of truth; the model name is only a fallback for local models and
// offline machines. An unknown model gets conservative text-only defaults.
type Capabilities struct {
	Context   int
	Vision    bool
	Reasoning bool
	ToolCall  bool
	Source    string
}

// Discover returns the detected capabilities for a model name, using only
// the built-in families. Prefer DiscoverFor when the provider is known.
func Discover(model string) Capabilities {
	c := discoverName(model)
	c.Vision = SupportsImages(model)
	return c
}

// DiscoverFor returns the capabilities published by the provider catalogue
// for provider/model, falling back to the built-in model families for any
// field the catalogue does not cover.
func DiscoverFor(provider, model string) Capabilities {
	c := discoverName(model)
	c.Vision = SupportsImages(model)
	c.Source = "default"
	if c.Context != DefaultWindow {
		c.Source = "model-name"
	}
	info, ok := modelinfo.Lookup(provider, model)
	if !ok {
		return c
	}
	c.Source = "provider"
	if info.Context > 0 {
		c.Context = info.Context
	}
	c.Vision = info.Vision
	c.Reasoning = info.Reasoning
	c.ToolCall = info.ToolCall
	return c
}

func discoverName(model string) Capabilities {
	m := strings.ToLower(strings.TrimSpace(model))
	c := Capabilities{Context: DefaultWindow, Source: "default"}
	if m == "" {
		return c
	}
	for _, w := range modelWindows {
		if strings.Contains(m, w.match) {
			c.Context = w.ctx
			c.Source = "model-name"
			break
		}
	}
	return c
}

// Window returns the context window for a model name, using the built-in
// families only.
func Window(model string) int {
	return Discover(model).Context
}

// WindowFor returns the context window the provider catalogue publishes for
// provider/model, falling back to the built-in families.
func WindowFor(provider, model string) int {
	return DiscoverFor(provider, model).Context
}
