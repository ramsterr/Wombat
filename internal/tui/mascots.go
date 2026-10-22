package tui

import "math"

// Every theme gets its own wombat pose. The poses share one braille canvas and
// a common visual language: a filled body, negative-space eye, bright nose and
// paws, and a faint ground line. The result is a small field guide rather than
// a set of unrelated ASCII doodles.

var themeMarks = map[string]string{
	"wombat":     "◈",
	"tokyo":      "⛩",
	"catppuccin": "ฅ",
	"gruvbox":    "▚",
	"nord":       "❄",
	"rosepine":   "❀",
	"dracula":    "◐",
	"everforest": "❧",
	"kanagawa":   "≋",
	"solarized":  "☀",
	"monokai":    "◉",
	"iceberg":    "❅",
	"nightfox":   "☾",
}

var themePoses = map[string]string{
	"wombat":     "walking",
	"tokyo":      "branch",
	"catppuccin": "heart",
	"gruvbox":    "sitting",
	"nord":       "standing",
	"rosepine":   "sitting",
	"dracula":    "sleeping",
	"everforest": "sniffing",
	"kanagawa":   "running",
	"solarized":  "waving",
	"monokai":    "digging",
	"iceberg":    "burrow",
	"nightfox":   "roll",
}

func themeMark(name string) string {
	if mark, ok := themeMarks[name]; ok {
		return mark
	}
	return themeMarks[themeDefault]
}

type artSize int

const (
	artMini artSize = iota
	artSmall
	artFull
)

// themeArt renders the theme mascot at half-block resolution. Full is the
// welcome hero, small is the compact hero, and mini is the theme-picker preview.
func themeArt(name string, size artSize) []string {
	t, ok := themeCatalog[name]
	if !ok {
		t = themeCatalog[themeDefault]
	}
	pose := themePoses[name]
	if pose == "" {
		pose = themePoses[themeDefault]
	}
	behind, front := themeAccessories(name)
	shapes := append([]artShape{}, behind...)
	shapes = append(shapes, poseShapes(pose)...)
	shapes = append(shapes, front...)

	w, h := 60, 24
	switch size {
	case artMini:
		w, h = 32, 16
	case artSmall:
		w, h = 40, 16
	}
	canvas := newArtCanvas(w, h)
	for _, s := range shapes {
		canvas.draw(s)
	}
	canvas.clearSoftUnderBody()
	return renderArt(canvas, t)
}

func poseShapes(name string) []artShape {
	switch name {
	case "branch":
		return poseWalking(true)
	case "heart":
		return poseSitting(false, true)
	case "sitting":
		return poseSitting(false, false)
	case "standing":
		return poseStanding(false)
	case "waving":
		return poseStanding(true)
	case "sleeping":
		return poseSleeping()
	case "sniffing":
		return poseSniffing()
	case "digging":
		return poseDigging()
	case "running":
		return poseRunning()
	case "burrow":
		return poseBurrow()
	case "roll":
		return poseRoll()
	default:
		return poseWalking(false)
	}
}

func poseWalking(branch bool) []artShape {
	sh := []artShape{
		artLine(artLayerSoft, 0.10, 0.88, 0.92, 0.88, 0.02),
		artEllipse(artLayerBody, 0.09, 0.56, 0.035, 0.032),
		artEllipse(artLayerBody, 0.42, 0.55, 0.29, 0.19),
		artRoundRect(artLayerBody, 0.26, 0.62, 0.07, 0.21, 0.30),
		artRoundRect(artLayerBody, 0.40, 0.63, 0.065, 0.18, 0.30),
		artRoundRect(artLayerBody, 0.68, 0.62, 0.07, 0.21, 0.30),
		artRoundRect(artLayerBody, 0.80, 0.63, 0.06, 0.19, 0.30),
		artRoundRect(artLayerBody, 0.63, 0.34, 0.22, 0.18, 0.25),
		artEllipse(artLayerBody, 0.76, 0.50, 0.10, 0.075),
		artEllipse(artLayerBody, 0.86, 0.49, 0.075, 0.06),
		artEllipse(artLayerBody, 0.67, 0.30, 0.045, 0.045),
		artEllipse(artLayerBody, 0.79, 0.31, 0.045, 0.045),
		artClearEllipse(0.80, 0.42, 0.022, 0.022),
		artEllipse(artLayerDetail, 0.91, 0.49, 0.022, 0.020),
		artLine(artLayerDetail, 0.86, 0.53, 0.89, 0.55, 0.02),
	}
	for _, x := range []float64{0.28, 0.31, 0.34, 0.70, 0.73, 0.76} {
		sh = append(sh, artEllipse(artLayerDetail, x, 0.84, 0.008, 0.010))
	}
	if branch {
		sh = append(sh,
			artLine(artLayerDetail, 0.87, 0.51, 0.99, 0.38, 0.03),
			artEllipse(artLayerDetail, 0.95, 0.34, 0.025, 0.016),
			artEllipse(artLayerDetail, 0.99, 0.31, 0.025, 0.016),
			artEllipse(artLayerDetail, 0.91, 0.42, 0.022, 0.014),
		)
	}
	return sh
}

func poseSitting(pawsUp, heart bool) []artShape {
	sh := []artShape{
		artLine(artLayerSoft, 0.12, 0.88, 0.88, 0.88, 0.02),
		artEllipse(artLayerBody, 0.42, 0.64, 0.24, 0.22),
		artRoundRect(artLayerBody, 0.48, 0.34, 0.20, 0.18, 0.25),
		artEllipse(artLayerBody, 0.60, 0.47, 0.09, 0.07),
		artEllipse(artLayerBody, 0.68, 0.45, 0.07, 0.06),
		artEllipse(artLayerBody, 0.50, 0.29, 0.04, 0.04),
		artEllipse(artLayerBody, 0.62, 0.30, 0.04, 0.04),
		artClearEllipse(0.62, 0.40, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.72, 0.45, 0.02, 0.018),
		artLine(artLayerDetail, 0.68, 0.49, 0.71, 0.51, 0.02),

		artEllipse(artLayerBody, 0.30, 0.84, 0.09, 0.045),
		artEllipse(artLayerBody, 0.52, 0.84, 0.09, 0.045),
	}
	if pawsUp {
		sh = append(sh,
			artRoundRect(artLayerDetail, 0.52, 0.56, 0.05, 0.13, 0.30),
			artRoundRect(artLayerDetail, 0.60, 0.57, 0.05, 0.12, 0.30),
		)
	} else {
		sh = append(sh,
			artRoundRect(artLayerDetail, 0.46, 0.66, 0.05, 0.16, 0.30),
			artRoundRect(artLayerDetail, 0.55, 0.66, 0.05, 0.16, 0.30),
		)
	}
	if heart {
		sh = append(sh,
			artEllipse(artLayerDetail, 0.76, 0.26, 0.03, 0.025),
			artEllipse(artLayerDetail, 0.81, 0.26, 0.03, 0.025),
			artPoly(artLayerDetail, artPoint{0.73, 0.27}, artPoint{0.84, 0.27}, artPoint{0.785, 0.35}),
		)
	}
	return sh
}

func poseStanding(wave bool) []artShape {
	sh := []artShape{
		artLine(artLayerSoft, 0.14, 0.90, 0.86, 0.90, 0.02),
		artEllipse(artLayerBody, 0.40, 0.61, 0.20, 0.27),
		artRoundRect(artLayerBody, 0.38, 0.27, 0.20, 0.18, 0.25),
		artEllipse(artLayerBody, 0.50, 0.40, 0.09, 0.07),
		artEllipse(artLayerBody, 0.58, 0.39, 0.07, 0.06),
		artEllipse(artLayerBody, 0.40, 0.22, 0.04, 0.04),
		artEllipse(artLayerBody, 0.52, 0.23, 0.04, 0.04),
		artClearEllipse(0.52, 0.33, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.62, 0.39, 0.02, 0.018),

		artEllipse(artLayerBody, 0.32, 0.86, 0.08, 0.04),
		artEllipse(artLayerBody, 0.48, 0.86, 0.08, 0.04),
	}
	if wave {
		sh = append(sh,
			artLine(artLayerDetail, 0.50, 0.52, 0.62, 0.36, 0.04),
			artEllipse(artLayerDetail, 0.64, 0.34, 0.035, 0.035),
		)
	} else {
		sh = append(sh,
			artRoundRect(artLayerDetail, 0.38, 0.58, 0.05, 0.23, 0.30),
			artRoundRect(artLayerDetail, 0.47, 0.58, 0.05, 0.23, 0.30),
		)
	}
	return sh
}

func poseSleeping() []artShape {
	return []artShape{
		artLine(artLayerSoft, 0.10, 0.86, 0.90, 0.86, 0.02),
		artEllipse(artLayerBody, 0.42, 0.66, 0.33, 0.19),
		artRoundRect(artLayerBody, 0.62, 0.51, 0.18, 0.16, 0.25),
		artEllipse(artLayerBody, 0.73, 0.63, 0.08, 0.06),
		artEllipse(artLayerBody, 0.80, 0.61, 0.06, 0.05),
		artEllipse(artLayerBody, 0.64, 0.47, 0.04, 0.04),
		artLine(artLayerDetail, 0.72, 0.57, 0.76, 0.57, 0.02),
		artEllipse(artLayerDetail, 0.85, 0.61, 0.018, 0.016),

		artLine(artLayerDetail, 0.80, 0.30, 0.88, 0.30, 0.02),
		artLine(artLayerDetail, 0.88, 0.30, 0.80, 0.38, 0.02),
		artLine(artLayerDetail, 0.80, 0.38, 0.88, 0.38, 0.02),
		artLine(artLayerDetail, 0.88, 0.18, 0.94, 0.18, 0.02),
		artLine(artLayerDetail, 0.94, 0.18, 0.88, 0.24, 0.02),
		artLine(artLayerDetail, 0.88, 0.24, 0.94, 0.24, 0.02),
	}
}

func poseRunning() []artShape {
	return []artShape{
		artLine(artLayerSoft, 0.06, 0.88, 0.94, 0.88, 0.02),
		artEllipse(artLayerBody, 0.40, 0.53, 0.34, 0.17),
		artRoundRect(artLayerBody, 0.66, 0.32, 0.20, 0.17, 0.25),
		artEllipse(artLayerBody, 0.78, 0.47, 0.09, 0.07),
		artEllipse(artLayerBody, 0.88, 0.46, 0.07, 0.06),
		artEllipse(artLayerBody, 0.69, 0.28, 0.04, 0.04),
		artEllipse(artLayerBody, 0.81, 0.29, 0.04, 0.04),
		artClearEllipse(0.82, 0.40, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.93, 0.46, 0.02, 0.018),

		artLine(artLayerBody, 0.24, 0.62, 0.14, 0.84, 0.06),
		artLine(artLayerBody, 0.34, 0.64, 0.26, 0.86, 0.06),
		artLine(artLayerBody, 0.64, 0.62, 0.72, 0.84, 0.06),
		artLine(artLayerBody, 0.76, 0.60, 0.86, 0.82, 0.06),
		artEllipse(artLayerBody, 0.08, 0.45, 0.045, 0.03),
		artLine(artLayerSoft, 0.04, 0.50, 0.14, 0.50, 0.02),
		artLine(artLayerSoft, 0.02, 0.56, 0.10, 0.56, 0.02),
	}
}

func poseSniffing() []artShape {
	return []artShape{
		artLine(artLayerSoft, 0.10, 0.88, 0.92, 0.88, 0.02),
		artEllipse(artLayerBody, 0.40, 0.50, 0.30, 0.19),
		artRoundRect(artLayerBody, 0.27, 0.58, 0.07, 0.22, 0.30),
		artRoundRect(artLayerBody, 0.40, 0.59, 0.07, 0.19, 0.30),
		artRoundRect(artLayerBody, 0.68, 0.58, 0.07, 0.22, 0.30),
		artRoundRect(artLayerBody, 0.62, 0.50, 0.20, 0.17, 0.25),
		artEllipse(artLayerBody, 0.75, 0.64, 0.09, 0.07),
		artEllipse(artLayerBody, 0.85, 0.72, 0.075, 0.06),
		artEllipse(artLayerBody, 0.66, 0.45, 0.04, 0.04),
		artEllipse(artLayerBody, 0.78, 0.46, 0.04, 0.04),
		artClearEllipse(0.79, 0.56, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.90, 0.72, 0.022, 0.020),
	}
}

func poseDigging() []artShape {
	return []artShape{
		artLine(artLayerSoft, 0.10, 0.88, 0.92, 0.88, 0.02),
		artEllipse(artLayerSoft, 0.48, 0.82, 0.36, 0.10),
		artEllipse(artLayerBody, 0.38, 0.56, 0.30, 0.20),
		artRoundRect(artLayerBody, 0.62, 0.56, 0.20, 0.17, 0.25),
		artEllipse(artLayerBody, 0.74, 0.70, 0.09, 0.07),
		artEllipse(artLayerBody, 0.84, 0.76, 0.075, 0.06),
		artEllipse(artLayerBody, 0.66, 0.50, 0.04, 0.04),
		artEllipse(artLayerBody, 0.78, 0.51, 0.04, 0.04),
		artClearEllipse(0.79, 0.61, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.89, 0.76, 0.022, 0.020),

		artRoundRect(artLayerBody, 0.25, 0.64, 0.075, 0.20, 0.30),
		artRoundRect(artLayerBody, 0.38, 0.65, 0.07, 0.17, 0.30),
		artLine(artLayerDetail, 0.72, 0.74, 0.86, 0.84, 0.05),
		artEllipse(artLayerDetail, 0.92, 0.82, 0.012, 0.012),
		artEllipse(artLayerDetail, 0.95, 0.78, 0.012, 0.012),
		artEllipse(artLayerDetail, 0.88, 0.80, 0.012, 0.012),
		artEllipse(artLayerDetail, 0.98, 0.86, 0.012, 0.012),
	}
}

func poseRoll() []artShape {
	return []artShape{
		artLine(artLayerSoft, 0.08, 0.88, 0.92, 0.88, 0.02),
		artEllipse(artLayerBody, 0.42, 0.64, 0.30, 0.16),
		artEllipse(artLayerBody, 0.10, 0.60, 0.035, 0.03),
		artRoundRect(artLayerBody, 0.66, 0.50, 0.20, 0.16, 0.25),
		artEllipse(artLayerBody, 0.78, 0.62, 0.09, 0.07),
		artEllipse(artLayerBody, 0.88, 0.62, 0.07, 0.06),
		artEllipse(artLayerBody, 0.68, 0.44, 0.04, 0.04),
		artEllipse(artLayerBody, 0.80, 0.45, 0.04, 0.04),
		artClearEllipse(0.82, 0.56, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.93, 0.62, 0.02, 0.018),
		artLine(artLayerBody, 0.28, 0.52, 0.24, 0.40, 0.06),
		artLine(artLayerBody, 0.38, 0.50, 0.37, 0.36, 0.06),
		artLine(artLayerBody, 0.52, 0.50, 0.55, 0.36, 0.06),
		artLine(artLayerBody, 0.62, 0.52, 0.66, 0.40, 0.06),
		artEllipse(artLayerDetail, 0.23, 0.39, 0.018, 0.014),
		artEllipse(artLayerDetail, 0.36, 0.35, 0.018, 0.014),
		artEllipse(artLayerDetail, 0.56, 0.35, 0.018, 0.014),
		artEllipse(artLayerDetail, 0.67, 0.39, 0.018, 0.014),
	}
}

func poseBurrow() []artShape {
	return []artShape{
		artLine(artLayerSoft, 0.08, 0.86, 0.92, 0.86, 0.02),
		artEllipse(artLayerSoft, 0.45, 0.78, 0.36, 0.14),
		artClearEllipse(0.45, 0.78, 0.16, 0.06),
		artEllipse(artLayerBody, 0.45, 0.54, 0.26, 0.22),
		artRoundRect(artLayerBody, 0.47, 0.30, 0.20, 0.18, 0.25),
		artEllipse(artLayerBody, 0.59, 0.43, 0.09, 0.07),
		artEllipse(artLayerBody, 0.67, 0.41, 0.07, 0.06),
		artEllipse(artLayerBody, 0.49, 0.25, 0.04, 0.04),
		artEllipse(artLayerBody, 0.61, 0.26, 0.04, 0.04),
		artClearEllipse(0.61, 0.36, 0.02, 0.02),
		artEllipse(artLayerDetail, 0.71, 0.41, 0.02, 0.018),
	}
}

// themeAccessories adds one restrained background/foreground motif per theme.
func themeAccessories(name string) (behind, front []artShape) {
	switch name {
	case "tokyo":
		behind = append(behind,
			artRect(artLayerSoft, 0.02, 0.28, 0.18, 0.022),
			artRect(artLayerSoft, 0.05, 0.35, 0.12, 0.018),
			artRect(artLayerSoft, 0.05, 0.28, 0.018, 0.22),
			artRect(artLayerSoft, 0.17, 0.28, 0.018, 0.22),
			artEllipse(artLayerSoft, 0.11, 0.18, 0.035, 0.035),
		)
	case "nord":
		for _, p := range []artPoint{{0.08, 0.22}, {0.90, 0.24}, {0.86, 0.70}} {
			front = append(front,
				artLine(artLayerSoft, p.x-0.025, p.y, p.x+0.025, p.y, 0.015),
				artLine(artLayerSoft, p.x, p.y-0.04, p.x, p.y+0.04, 0.015),
			)
		}
	case "rosepine":
		for _, p := range []artPoint{{0.08, 0.30}, {0.92, 0.22}, {0.84, 0.78}, {0.14, 0.72}} {
			front = append(front, artEllipse(artLayerDetail, p.x, p.y, 0.020, 0.014))
		}
	case "dracula":
		behind = append(behind,
			artEllipse(artLayerSoft, 0.08, 0.22, 0.055, 0.055),
			artClearEllipse(0.10, 0.20, 0.045, 0.045),
		)
	case "everforest":
		front = append(front,
			artLine(artLayerDetail, 0.12, 0.86, 0.17, 0.78, 0.02),
			artEllipse(artLayerDetail, 0.18, 0.77, 0.025, 0.015),
			artEllipse(artLayerDetail, 0.14, 0.81, 0.022, 0.014),
		)
	case "kanagawa":
		behind = append(behind,
			artEllipse(artLayerSoft, 0.09, 0.20, 0.045, 0.045),
			artLine(artLayerSoft, 0.02, 0.92, 0.30, 0.88, 0.02),
			artLine(artLayerSoft, 0.20, 0.94, 0.52, 0.90, 0.02),
		)
	case "solarized":
		behind = append(behind, artEllipse(artLayerSoft, 0.09, 0.20, 0.045, 0.045))
		for _, a := range []float64{0, 0.785, 1.57, 2.355, 3.14, 3.925, 4.71, 5.495} {
			x := 0.09 + 0.075*math.Cos(a)
			y := 0.20 + 0.075*math.Sin(a)
			behind = append(behind, artLine(artLayerSoft, 0.09+0.055*math.Cos(a), 0.20+0.055*math.Sin(a), x, y, 0.012))
		}
	case "monokai":
		for _, p := range []artPoint{{0.08, 0.30}, {0.92, 0.20}, {0.90, 0.72}, {0.10, 0.76}} {
			front = append(front, artEllipse(artLayerDetail, p.x, p.y, 0.012, 0.012))
		}
	case "iceberg":
		for _, p := range []artPoint{{0.08, 0.20}, {0.92, 0.24}, {0.86, 0.74}} {
			front = append(front,
				artLine(artLayerSoft, p.x-0.025, p.y, p.x+0.025, p.y, 0.015),
				artLine(artLayerSoft, p.x, p.y-0.04, p.x, p.y+0.04, 0.015),
			)
		}
	case "nightfox":
		behind = append(behind, artEllipse(artLayerSoft, 0.09, 0.18, 0.045, 0.045))
		for _, p := range []artPoint{{0.18, 0.12}, {0.90, 0.18}, {0.82, 0.78}, {0.12, 0.72}} {
			front = append(front, artEllipse(artLayerDetail, p.x, p.y, 0.010, 0.010))
		}
	case "gruvbox":
		behind = append(behind,
			artRect(artLayerSoft, 0.02, 0.34, 0.17, 0.26),
			artRect(artLayerClear, 0.035, 0.37, 0.14, 0.20),
			artLine(artLayerSoft, 0.05, 0.46, 0.08, 0.48, 0.02),
			artLine(artLayerSoft, 0.09, 0.46, 0.12, 0.48, 0.02),
		)
	}
	return behind, front
}
