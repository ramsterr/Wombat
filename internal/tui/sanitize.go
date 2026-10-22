package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	csiEscapePattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscEscapePattern = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	// Mouse reports occasionally arrive without their ESC[ prefix when a
	// terminal is slow or a sequence is split. Keep them out of the composer
	// and the transcript.
	orphanMousePattern = regexp.MustCompile(`(?i)<\s*\d+\s*[,;]\s*\d+\s*[,;]\s*\d+\s*[Mm]`)
)

// sanitizeTerminalText removes control sequences and orphan mouse reports
// from model, tool, and user text before it is rendered or stored.
func sanitizeTerminalText(s string) string {
	s = oscEscapePattern.ReplaceAllString(s, "")
	s = csiEscapePattern.ReplaceAllString(s, "")
	s = orphanMousePattern.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "\x1b", "")
}

// scrubInput removes terminal noise that arrived as text, such as a mouse
// report split across reads. It returns true when the value changed.
func (c *chat) scrubInput() bool {
	value := c.ta.Value()
	clean := sanitizeTerminalText(value)
	if clean == value {
		return false
	}
	c.ta.SetValue(clean)
	c.ta.CursorEnd()
	return true
}

// ansiEscapeEnd returns the byte offset just past the escape sequence at i.
// It understands CSI, OSC, and single-character escapes.
func ansiEscapeEnd(s string, i int) int {
	if i >= len(s) || s[i] != 0x1b {
		return i
	}
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	case ']':
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(s)
	default:
		if i+2 > len(s) {
			return len(s)
		}
		return i + 2
	}
}

// plainLen counts visible runes while ignoring ANSI escapes.
func plainLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = ansiEscapeEnd(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			break
		}
		i += size
		n++
	}
	return n
}
