package trace

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Source is a site or file the session touched.
type Source struct {
	// Value is the URL or the file path.
	Value string
	// Calls is how many times it was used.
	Calls int
}

// urlPattern finds links in tool output and commands.
var urlPattern = regexp.MustCompile(`https?://[^\s"'` + "`" + `<>)\]]+`)

// Sources reads a session event log and returns the sites and files the
// session touched, most used first.
func Sources(path string) (urls []Source, files []Source, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	urlCount := map[string]int{}
	fileCount := map[string]int{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Kind != "tool_start" {
			continue
		}
		for _, u := range urlPattern.FindAllString(event.Detail, -1) {
			urlCount[strings.TrimRight(u, ".,;:")]++
		}
		switch event.Name {
		case "read", "write", "edit":
			if event.Detail != "" {
				fileCount[event.Detail]++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	urls = sortedSources(urlCount)
	files = sortedSources(fileCount)
	return urls, files, nil
}

func sortedSources(counts map[string]int) []Source {
	out := make([]Source, 0, len(counts))
	for value, n := range counts {
		out = append(out, Source{Value: value, Calls: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Value < out[j].Value
	})
	return out
}
