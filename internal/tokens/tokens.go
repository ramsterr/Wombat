package tokens

import "strings"

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

func Window(model string) int {
	m := strings.ToLower(model)
	for _, w := range modelWindows {
		if strings.Contains(m, w.match) {
			return w.ctx
		}
	}
	return DefaultWindow
}
