package tui

import (
	"strings"
	"unicode/utf8"
)

// welcome is the session-opening plate. The wombat art sits in the centre of
// the conversation column, the way opencode centres its mark above the prompt.
// On shorter terminals the art is scaled down with block shading so it still
// keeps the middle of the screen.
func (c *chat) welcome() string {
	w := c.contentWidth()
	h := c.vp.Height
	if c.w <= 0 || c.h <= 0 || w <= 0 || h <= 0 {
		return ""
	}
	art := fitWombatArt(w, h)
	if len(art) > 0 {
		block := strings.Join(wombatGradient(art), "\n")
		return centerBlockIn(w, h, block)
	}
	for _, mark := range [][]string{wombatWordmarkLarge, wombatWordmark, {"WOMBAT"}} {
		if blockFits(mark, w, h) {
			return centerBlockIn(w, h, strings.Join(wombatGradient(mark), "\n"))
		}
	}
	return centerBlockIn(w, h, styleAccent.Bold(true).Render("WOMBAT"))
}

// fitWombatArt returns the largest version of the figure that fits. Full art
// is used when the terminal is tall; 2x and 3x block reductions keep the
// silhouette on ordinary and short terminals.
func fitWombatArt(width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	base := dedentLines(wombatFigure)
	for factor := 1; factor <= 3; factor++ {
		art := scaleArt(base, factor)
		if blockFits(art, width, height) {
			return art
		}
	}
	return nil
}

// dedentLines removes the shared left margin from the banner art so centring
// is based on the drawing itself, not on its source indentation.
func dedentLines(lines []string) []string {
	margin := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " "))
		if margin < 0 || n < margin {
			margin = n
		}
	}
	if margin <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= margin {
			out[i] = line[margin:]
		}
	}
	return out
}

// scaleArt reduces the figure by sampling a factor-sized cell. A 2x reduction
// uses quadrant block characters, which keeps the line drawing recognisable;
// larger reductions fall back to a soft density shade.
func scaleArt(lines []string, factor int) []string {
	if factor <= 1 {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = strings.TrimRight(line, " ")
		}
		return out
	}
	width := 0
	for _, line := range lines {
		if n := utf8.RuneCountInString(line); n > width {
			width = n
		}
	}
	out := make([]string, 0, (len(lines)+factor-1)/factor)
	for y := 0; y < len(lines); y += factor {
		var b strings.Builder
		for x := 0; x < width; x += factor {
			ink := 0
			for dy := 0; dy < factor && y+dy < len(lines); dy++ {
				row := []rune(lines[y+dy])
				for dx := 0; dx < factor && x+dx < len(row); dx++ {
					if row[x+dx] != ' ' {
						ink++
					}
				}
			}
			if factor == 2 {
				b.WriteRune(quadrant(lines, x, y))
				continue
			}
			switch {
			case ink == 0:
				b.WriteByte(' ')
			case ink <= 1:
				b.WriteRune('░')
			case ink <= 3:
				b.WriteRune('▒')
			case ink <= 6:
				b.WriteRune('▓')
			default:
				b.WriteRune('█')
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

// quadrant maps a 2x2 sample of the drawing into one of the sixteen Unicode
// quadrant blocks, so a half-size wombat still looks like a wombat.
func quadrant(lines []string, x, y int) rune {
	sample := func(dx, dy int) bool {
		if y+dy >= len(lines) {
			return false
		}
		row := []rune(lines[y+dy])
		if x+dx >= len(row) {
			return false
		}
		return row[x+dx] != ' '
	}
	mask := 0
	if sample(0, 0) {
		mask |= 1
	}
	if sample(1, 0) {
		mask |= 2
	}
	if sample(0, 1) {
		mask |= 4
	}
	if sample(1, 1) {
		mask |= 8
	}
	blocks := [16]rune{' ', '▘', '▝', '▀', '▖', '▌', '▞', '▛', '▗', '▚', '▐', '▜', '▄', '▙', '▟', '█'}
	return blocks[mask]
}

// blockFits reports whether a rendered block fits inside a rectangle.
func blockFits(lines []string, width, height int) bool {
	if len(lines) == 0 || len(lines) > height {
		return false
	}
	for _, line := range lines {
		if utf8.RuneCountInString(line) > width {
			return false
		}
	}
	return true
}
