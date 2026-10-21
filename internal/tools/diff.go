package tools

import (
	"fmt"
	"strings"
)

const maxDiffPreviewLines = 400

// DiffPreview renders a compact unified diff for a permission prompt. It is
// a review aid, not a patch format: the whole point is that the user sees
// what will change before approving it.
func DiffPreview(path, old, new string) string {
	if old == new {
		return ""
	}
	oldLines := splitLines(old)
	newLines := splitLines(new)
	header := fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path)
	if len(oldLines) == 0 {
		return header + addAll(newLines)
	}
	if len(newLines) == 0 {
		return header + removeAll(oldLines)
	}
	var body []string
	if len(oldLines)*len(newLines) <= 1_000_000 {
		body = lcsDiff(oldLines, newLines)
	} else {
		body = prefixSuffixDiff(oldLines, newLines)
	}
	if len(body) > maxDiffPreviewLines {
		body = append(body[:maxDiffPreviewLines], "… diff truncated at 400 lines …")
	}
	return header + strings.Join(body, "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func addAll(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func removeAll(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// lcsDiff is a plain longest-common-subsequence diff. Files at this size are
// previews, so clarity is worth more than the fastest algorithm.
func lcsDiff(a, b []string) []string {
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, " "+a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+"+b[j])
	}
	return out
}

func prefixSuffixDiff(a, b []string) []string {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	out := make([]string, 0, len(a)+len(b))
	for _, line := range a[:prefix] {
		out = append(out, " "+line)
	}
	for _, line := range a[prefix : len(a)-suffix] {
		out = append(out, "-"+line)
	}
	for _, line := range b[prefix : len(b)-suffix] {
		out = append(out, "+"+line)
	}
	for _, line := range a[len(a)-suffix:] {
		out = append(out, " "+line)
	}
	return out
}
