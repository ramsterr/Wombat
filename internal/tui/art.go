package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A tiny braille canvas. It gives the mascots real curves and negative-space
// detail instead of hand-drawn ASCII, and keeps every theme's art on the same
// visual grid.
const (
	artLayerBody = iota
	artLayerDetail
	artLayerSoft
	artLayerClear
)

type artPoint struct {
	x, y float64
}

type artShape struct {
	kind   string
	layer  int
	x, y   float64
	w, h   float64
	rx, ry float64
	r      float64
	thick  float64
	pts    []artPoint
}

func artEllipse(layer int, cx, cy, rx, ry float64) artShape {
	return artShape{kind: "ellipse", layer: layer, x: cx, y: cy, rx: rx, ry: ry}
}

func artClearEllipse(cx, cy, rx, ry float64) artShape {
	return artShape{kind: "ellipse", layer: artLayerClear, x: cx, y: cy, rx: rx, ry: ry}
}

func artRect(layer int, x, y, w, h float64) artShape {
	return artShape{kind: "rect", layer: layer, x: x, y: y, w: w, h: h}
}

func artRoundRect(layer int, x, y, w, h, r float64) artShape {
	return artShape{kind: "rrect", layer: layer, x: x, y: y, w: w, h: h, r: r}
}

func artLine(layer int, x1, y1, x2, y2, thick float64) artShape {
	return artShape{kind: "line", layer: layer, thick: thick, pts: []artPoint{{x1, y1}, {x2, y2}}}
}

func artPoly(layer int, pts ...artPoint) artShape {
	return artShape{kind: "poly", layer: layer, pts: pts}
}

type artCanvas struct {
	w, h   int
	layers [3][]bool
}

func newArtCanvas(w, h int) *artCanvas {
	c := &artCanvas{w: w, h: h}
	for i := range c.layers {
		c.layers[i] = make([]bool, w*h)
	}
	return c
}

func (c *artCanvas) inside(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.w && y < c.h
}

func (c *artCanvas) idx(x, y int) int { return y*c.w + x }

func (c *artCanvas) clearSoftUnderBody() {
	for i := range c.layers[artLayerBody] {
		if c.layers[artLayerBody][i] {
			c.layers[artLayerSoft][i] = false
		}
	}
}

func (c *artCanvas) set(layer, x, y int) {
	if !c.inside(x, y) {
		return
	}
	if layer == artLayerClear {
		for i := range c.layers {
			c.layers[i][c.idx(x, y)] = false
		}
		return
	}
	c.layers[layer][c.idx(x, y)] = true
}

func (c *artCanvas) draw(s artShape) {
	switch s.kind {
	case "ellipse":
		c.drawEllipse(s)
	case "rect":
		c.drawRect(s)
	case "rrect":
		c.drawRoundRect(s)
	case "line":
		c.drawLine(s)
	case "poly":
		c.drawPoly(s)
	}
}

func (c *artCanvas) drawEllipse(s artShape) {
	cx := s.x * float64(c.w-1)
	cy := s.y * float64(c.h-1)
	rx := math.Max(0.5, s.rx*float64(c.w-1))
	ry := math.Max(0.5, s.ry*float64(c.h-1))
	x0, x1 := int(math.Floor(cx-rx)), int(math.Ceil(cx+rx))
	y0, y1 := int(math.Floor(cy-ry)), int(math.Ceil(cy+ry))
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			dx := (float64(x) - cx) / rx
			dy := (float64(y) - cy) / ry
			if dx*dx+dy*dy <= 1 {
				c.set(s.layer, x, y)
			}
		}
	}
}

func (c *artCanvas) drawRect(s artShape) {
	x0 := int(math.Round(s.x * float64(c.w-1)))
	y0 := int(math.Round(s.y * float64(c.h-1)))
	x1 := int(math.Round((s.x + s.w) * float64(c.w-1)))
	y1 := int(math.Round((s.y + s.h) * float64(c.h-1)))
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			c.set(s.layer, x, y)
		}
	}
}

func (c *artCanvas) drawRoundRect(s artShape) {
	x0 := int(math.Round(s.x * float64(c.w-1)))
	y0 := int(math.Round(s.y * float64(c.h-1)))
	x1 := int(math.Round((s.x + s.w) * float64(c.w-1)))
	y1 := int(math.Round((s.y + s.h) * float64(c.h-1)))
	rad := s.r * math.Min(float64(x1-x0), float64(y1-y0))
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			// Keep the four corner circles round.
			cx, cy := x, y
			if x < x0+int(rad) {
				cx = x0 + int(rad)
			} else if x > x1-int(rad) {
				cx = x1 - int(rad)
			}
			if y < y0+int(rad) {
				cy = y0 + int(rad)
			} else if y > y1-int(rad) {
				cy = y1 - int(rad)
			}
			dx := float64(x - cx)
			dy := float64(y - cy)
			if dx*dx+dy*dy <= rad*rad || (x >= x0+int(rad) && x <= x1-int(rad)) || (y >= y0+int(rad) && y <= y1-int(rad)) {
				c.set(s.layer, x, y)
			}
		}
	}
}

func (c *artCanvas) drawLine(s artShape) {
	if len(s.pts) < 2 {
		return
	}
	x0 := s.pts[0].x * float64(c.w-1)
	y0 := s.pts[0].y * float64(c.h-1)
	x1 := s.pts[1].x * float64(c.w-1)
	y1 := s.pts[1].y * float64(c.h-1)
	thick := math.Max(0.8, s.thick*float64(c.h-1))
	half := thick / 2
	minX := int(math.Floor(math.Min(x0, x1) - half))
	maxX := int(math.Ceil(math.Max(x0, x1) + half))
	minY := int(math.Floor(math.Min(y0, y1) - half))
	maxY := int(math.Ceil(math.Max(y0, y1) + half))
	dx, dy := x1-x0, y1-y0
	den := dx*dx + dy*dy
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			t := 0.0
			if den > 0 {
				t = ((float64(x)-x0)*dx + (float64(y)-y0)*dy) / den
				t = math.Max(0, math.Min(1, t))
			}
			px := x0 + t*dx
			py := y0 + t*dy
			ddx := float64(x) - px
			ddy := float64(y) - py
			if ddx*ddx+ddy*ddy <= half*half {
				c.set(s.layer, x, y)
			}
		}
	}
}

func (c *artCanvas) drawPoly(s artShape) {
	if len(s.pts) < 3 {
		return
	}
	pts := make([]artPoint, len(s.pts))
	minX, minY := 1.0, 1.0
	maxX, maxY := 0.0, 0.0
	for i, p := range s.pts {
		pts[i] = artPoint{p.x * float64(c.w-1), p.y * float64(c.h-1)}
		minX = math.Min(minX, pts[i].x)
		minY = math.Min(minY, pts[i].y)
		maxX = math.Max(maxX, pts[i].x)
		maxY = math.Max(maxY, pts[i].y)
	}
	for y := int(math.Floor(minY)); y <= int(math.Ceil(maxY)); y++ {
		for x := int(math.Floor(minX)); x <= int(math.Ceil(maxX)); x++ {
			if pointInPoly(float64(x), float64(y), pts) {
				c.set(s.layer, x, y)
			}
		}
	}
}

func pointInPoly(x, y float64, pts []artPoint) bool {
	inside := false
	j := len(pts) - 1
	for i := range pts {
		yi, yj := pts[i].y, pts[j].y
		xi, xj := pts[i].x, pts[j].x
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			inside = !inside
		}
		j = i
	}
	return inside
}

// renderArt turns the dot canvas into solid half-block cells. A filled
// silhouette reads far better than braille at terminal sizes, while the
// detail layer keeps the eye, nose, and paws legible.
func renderArt(c *artCanvas, t theme) []string {
	cw := c.w
	ch := c.h / 2
	top := colorHex(t.accent)
	bottom := colorHex(t.accent2)
	detail := colorHex(t.link)
	soft := colorHex(t.faint)
	rows := make([]string, 0, ch)
	for cy := 0; cy < ch; cy++ {
		var b strings.Builder
		run := ""
		runColor := ""
		flush := func() {
			if run != "" {
				b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(runColor)).Render(run))
				run = ""
			}
		}
		for x := 0; x < cw; x++ {
			topY := cy * 2
			botY := topY + 1
			bodyTop := c.layers[artLayerBody][c.idx(x, topY)]
			bodyBot := c.layers[artLayerBody][c.idx(x, botY)]
			detTop := c.layers[artLayerDetail][c.idx(x, topY)]
			detBot := c.layers[artLayerDetail][c.idx(x, botY)]
			softTop := c.layers[artLayerSoft][c.idx(x, topY)]
			softBot := c.layers[artLayerSoft][c.idx(x, botY)]
			body := bodyTop || bodyBot
			det := detTop || detBot
			sft := softTop || softBot
			if !body && !det && !sft {
				flush()
				run += " "
				continue
			}
			chRune := "█"
			switch {
			case bodyTop && !bodyBot:
				chRune = "▀"
			case bodyBot && !bodyTop:
				chRune = "▄"
			}
			if det && !body {
				if detTop && !detBot {
					chRune = "▀"
				} else if detBot && !detTop {
					chRune = "▄"
				}
			}
			color := detail
			switch {
			case det:
				color = detail
			case sft:
				color = soft
			default:
				tt := 0.0
				if ch > 1 {
					tt = float64(cy) / float64(ch-1)
				}
				color = mixHex(top, bottom, tt)
			}
			if color != runColor {
				flush()
				runColor = color
			}
			run += chRune
		}
		flush()
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return rows
}

func mixHex(a, b string, t float64) string {
	ar, ag, ab, ok1 := hexRGB(a)
	br, bg, bb, ok2 := hexRGB(b)
	if !ok1 || !ok2 {
		return a
	}
	t = math.Max(0, math.Min(1, t))
	return fmt.Sprintf("#%02x%02x%02x",
		int(float64(ar)+float64(br-ar)*t),
		int(float64(ag)+float64(bg-ag)*t),
		int(float64(ab)+float64(bb-ab)*t))
}
