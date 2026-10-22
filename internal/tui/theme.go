package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// theme is a complete palette for the TUI. The TUI deliberately uses a small
// set of semantic colours so every screen changes together.
type theme struct {
	name    string
	label   string
	blurb   string
	ink     lipgloss.TerminalColor
	surface lipgloss.TerminalColor
	panel   lipgloss.TerminalColor
	border  lipgloss.TerminalColor
	text    lipgloss.TerminalColor
	dim     lipgloss.TerminalColor
	faint   lipgloss.TerminalColor
	accent  lipgloss.TerminalColor
	accent2 lipgloss.TerminalColor
	green   lipgloss.TerminalColor
	red     lipgloss.TerminalColor
	warn    lipgloss.TerminalColor
	link    lipgloss.TerminalColor
	code    lipgloss.TerminalColor
	sandbox lipgloss.TerminalColor
	outside lipgloss.TerminalColor
}

const themeDefault = "wombat"

// themeOrder is also the order shown by /theme and ctrl+t.
var themeOrder = []string{"wombat", "tokyo", "catppuccin", "gruvbox", "nord", "rosepine", "dracula"}

var themeCatalog = map[string]theme{
	"wombat": {
		name:    "wombat",
		label:   "Wombat",
		blurb:   "warm amber on charcoal",
		ink:     lipgloss.Color("#17130C"),
		surface: lipgloss.Color("#17191F"),
		panel:   lipgloss.Color("#1D2028"),
		border:  lipgloss.Color("#3A3F4B"),
		text:    lipgloss.Color("#E6E1D7"),
		dim:     lipgloss.Color("#9A9284"),
		faint:   lipgloss.Color("#625D55"),
		accent:  lipgloss.Color("#F5B86B"),
		accent2: lipgloss.Color("#8BD5CA"),
		green:   lipgloss.Color("#9ECE6A"),
		red:     lipgloss.Color("#F7768E"),
		warn:    lipgloss.Color("#E0AF68"),
		link:    lipgloss.Color("#7DCFFF"),
		code:    lipgloss.Color("#C0CAF5"),
		sandbox: lipgloss.Color("#BB9AF7"),
		outside: lipgloss.Color("#7AA2F7"),
	},
	"tokyo": {
		name:    "tokyo",
		label:   "Tokyo Night",
		blurb:   "midnight blue and neon violet",
		ink:     lipgloss.Color("#15161E"),
		surface: lipgloss.Color("#1A1B26"),
		panel:   lipgloss.Color("#1F2335"),
		border:  lipgloss.Color("#3B4261"),
		text:    lipgloss.Color("#C0CAF5"),
		dim:     lipgloss.Color("#A9B1D6"),
		faint:   lipgloss.Color("#565F89"),
		accent:  lipgloss.Color("#7AA2F7"),
		accent2: lipgloss.Color("#BB9AF7"),
		green:   lipgloss.Color("#9ECE6A"),
		red:     lipgloss.Color("#F7768E"),
		warn:    lipgloss.Color("#E0AF68"),
		link:    lipgloss.Color("#7DCFFF"),
		code:    lipgloss.Color("#C0CAF5"),
		sandbox: lipgloss.Color("#BB9AF7"),
		outside: lipgloss.Color("#2AC3DE"),
	},
	"catppuccin": {
		name:    "catppuccin",
		label:   "Catppuccin",
		blurb:   "soft pastel mauve",
		ink:     lipgloss.Color("#11111B"),
		surface: lipgloss.Color("#181825"),
		panel:   lipgloss.Color("#1E1E2E"),
		border:  lipgloss.Color("#45475A"),
		text:    lipgloss.Color("#CDD6F4"),
		dim:     lipgloss.Color("#BAC2DE"),
		faint:   lipgloss.Color("#6C7086"),
		accent:  lipgloss.Color("#CBA6F7"),
		accent2: lipgloss.Color("#89B4FA"),
		green:   lipgloss.Color("#A6E3A1"),
		red:     lipgloss.Color("#F38BA8"),
		warn:    lipgloss.Color("#F9E2AF"),
		link:    lipgloss.Color("#89DCEB"),
		code:    lipgloss.Color("#F5E0DC"),
		sandbox: lipgloss.Color("#F5C2E7"),
		outside: lipgloss.Color("#89B4FA"),
	},
	"gruvbox": {
		name:    "gruvbox",
		label:   "Gruvbox",
		blurb:   "retro warm earth tones",
		ink:     lipgloss.Color("#1D2021"),
		surface: lipgloss.Color("#1D2021"),
		panel:   lipgloss.Color("#282828"),
		border:  lipgloss.Color("#504945"),
		text:    lipgloss.Color("#EBDBB2"),
		dim:     lipgloss.Color("#D5C4A1"),
		faint:   lipgloss.Color("#928374"),
		accent:  lipgloss.Color("#FABD2F"),
		accent2: lipgloss.Color("#83A598"),
		green:   lipgloss.Color("#B8BB26"),
		red:     lipgloss.Color("#FB4934"),
		warn:    lipgloss.Color("#FE8019"),
		link:    lipgloss.Color("#83A598"),
		code:    lipgloss.Color("#EBDBB2"),
		sandbox: lipgloss.Color("#D3869B"),
		outside: lipgloss.Color("#83A598"),
	},
	"nord": {
		name:    "nord",
		label:   "Nord",
		blurb:   "cool arctic blue",
		ink:     lipgloss.Color("#2E3440"),
		surface: lipgloss.Color("#2E3440"),
		panel:   lipgloss.Color("#3B4252"),
		border:  lipgloss.Color("#4C566A"),
		text:    lipgloss.Color("#ECEFF4"),
		dim:     lipgloss.Color("#D8DEE9"),
		faint:   lipgloss.Color("#7B88A1"),
		accent:  lipgloss.Color("#88C0D0"),
		accent2: lipgloss.Color("#B48EAD"),
		green:   lipgloss.Color("#A3BE8C"),
		red:     lipgloss.Color("#BF616A"),
		warn:    lipgloss.Color("#EBCB8B"),
		link:    lipgloss.Color("#81A1C1"),
		code:    lipgloss.Color("#E5E9F0"),
		sandbox: lipgloss.Color("#B48EAD"),
		outside: lipgloss.Color("#5E81AC"),
	},
	"rosepine": {
		name:    "rosepine",
		label:   "Rosé Pine",
		blurb:   "muted rose and iris",
		ink:     lipgloss.Color("#191724"),
		surface: lipgloss.Color("#1F1D2E"),
		panel:   lipgloss.Color("#26233A"),
		border:  lipgloss.Color("#403D52"),
		text:    lipgloss.Color("#E0DEF4"),
		dim:     lipgloss.Color("#C5C3DA"),
		faint:   lipgloss.Color("#6E6A86"),
		accent:  lipgloss.Color("#EBBCBA"),
		accent2: lipgloss.Color("#C4A7E7"),
		green:   lipgloss.Color("#9CCFD8"),
		red:     lipgloss.Color("#EB6F92"),
		warn:    lipgloss.Color("#F6C177"),
		link:    lipgloss.Color("#9CCFD8"),
		code:    lipgloss.Color("#E0DEF4"),
		sandbox: lipgloss.Color("#C4A7E7"),
		outside: lipgloss.Color("#31748F"),
	},
	"dracula": {
		name:    "dracula",
		label:   "Dracula",
		blurb:   "purple night, vivid accents",
		ink:     lipgloss.Color("#191A21"),
		surface: lipgloss.Color("#21222C"),
		panel:   lipgloss.Color("#282A36"),
		border:  lipgloss.Color("#44475A"),
		text:    lipgloss.Color("#F8F8F2"),
		dim:     lipgloss.Color("#C8CAD8"),
		faint:   lipgloss.Color("#6272A4"),
		accent:  lipgloss.Color("#BD93F9"),
		accent2: lipgloss.Color("#8BE9FD"),
		green:   lipgloss.Color("#50FA7B"),
		red:     lipgloss.Color("#FF5555"),
		warn:    lipgloss.Color("#F1FA8C"),
		link:    lipgloss.Color("#8BE9FD"),
		code:    lipgloss.Color("#F8F8F2"),
		sandbox: lipgloss.Color("#FF79C6"),
		outside: lipgloss.Color("#6272A4"),
	},
}

var (
	cInk     lipgloss.TerminalColor
	cSurface lipgloss.TerminalColor
	cPanel   lipgloss.TerminalColor
	cDim     lipgloss.TerminalColor
	cFaint   lipgloss.TerminalColor
	cText    lipgloss.TerminalColor
	cGreen   lipgloss.TerminalColor
	cRed     lipgloss.TerminalColor
	cWarn    lipgloss.TerminalColor
	cAccent  lipgloss.TerminalColor
	cAccent2 lipgloss.TerminalColor
	cLink    lipgloss.TerminalColor
	cBorder  lipgloss.TerminalColor
	cCode    lipgloss.TerminalColor
	// cSandbox marks requests that go beyond the sandbox, so they are easy
	// to tell apart from ordinary tool permission.
	cSandbox lipgloss.TerminalColor
	// cOutside marks writes that leave the working directory.
	cOutside lipgloss.TerminalColor

	activeTheme = themeDefault
	themeLabel  = "Wombat"
	// terminalBackground is set by Run from the user's config. When true, the
	// terminal's own background is tinted to the theme surface and restored
	// on exit.
	terminalBackground bool
)

var (
	styleTitle        lipgloss.Style
	styleBrand        lipgloss.Style
	styleDim          lipgloss.Style
	styleFaint        lipgloss.Style
	styleThought      lipgloss.Style
	styleGood         lipgloss.Style
	styleBad          lipgloss.Style
	styleWarn         lipgloss.Style
	styleAccent       lipgloss.Style
	styleAccent2      lipgloss.Style
	styleLink         lipgloss.Style
	styleSandbox      lipgloss.Style
	styleOutside      lipgloss.Style
	styleButton       lipgloss.Style
	styleKey          lipgloss.Style
	stylePanel        lipgloss.Style
	stylePanelTitle   lipgloss.Style
	styleCode         lipgloss.Style
	styleInputBox     lipgloss.Style
	styleInputIdle    lipgloss.Style
	styleInputBusy    lipgloss.Style
	styleRule         lipgloss.Style
	stylePreview      lipgloss.Style
	stylePreviewText  lipgloss.Style
	styleUserRail     lipgloss.Style
	styleToolName     lipgloss.Style
	styleSelected     lipgloss.Style
	styleSelectedSoft lipgloss.Style
	styleSection      lipgloss.Style
	styleChip         lipgloss.Style
	styleChipAccent   lipgloss.Style
	styleDiffAdd      lipgloss.Style
	styleDiffDel      lipgloss.Style
	styleDiffHunk     lipgloss.Style
	styleModal        lipgloss.Style
	styleModalTitle   lipgloss.Style
	styleToast        lipgloss.Style
	styleTree         lipgloss.Style
)

func init() { applyTheme(themeDefault) }

// applyTheme switches every semantic colour and derived style. Unknown names
// fall back to the Wombat theme so an old config can never break the UI.
func applyTheme(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	t, ok := themeCatalog[name]
	if !ok {
		t = themeCatalog[themeDefault]
		name = themeDefault
	}
	activeTheme = name
	themeLabel = t.label

	cInk = t.ink
	cSurface = t.surface
	cPanel = t.panel
	cText = t.text
	cDim = t.dim
	cFaint = t.faint
	cAccent = t.accent
	cAccent2 = t.accent2
	cGreen = t.green
	cRed = t.red
	cWarn = t.warn
	cLink = t.link
	cBorder = t.border
	cCode = t.code
	cSandbox = t.sandbox
	cOutside = t.outside

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(cText)
	styleBrand = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleDim = lipgloss.NewStyle().Foreground(cDim)
	styleFaint = lipgloss.NewStyle().Foreground(cFaint)
	styleThought = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	styleGood = lipgloss.NewStyle().Foreground(cGreen)
	styleBad = lipgloss.NewStyle().Foreground(cRed)
	styleWarn = lipgloss.NewStyle().Foreground(cWarn)
	styleAccent = lipgloss.NewStyle().Foreground(cAccent)
	styleAccent2 = lipgloss.NewStyle().Foreground(cAccent2)
	styleLink = lipgloss.NewStyle().Foreground(cLink)
	styleSandbox = lipgloss.NewStyle().Foreground(cSandbox)
	styleOutside = lipgloss.NewStyle().Foreground(cOutside)
	styleButton = lipgloss.NewStyle().Background(cAccent).Foreground(cInk).Bold(true)
	styleKey = lipgloss.NewStyle().Foreground(cInk).Background(cAccent).Bold(true)
	stylePanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
	stylePanelTitle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleCode = lipgloss.NewStyle().Foreground(cCode)
	styleInputBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cAccent).
		Padding(0, 1)
	styleInputIdle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
	styleInputBusy = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cWarn).
		Padding(0, 1)
	styleRule = lipgloss.NewStyle().Foreground(cBorder)
	stylePreview = lipgloss.NewStyle().Background(cAccent).Foreground(cInk).Bold(true)
	stylePreviewText = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleUserRail = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
	styleToolName = lipgloss.NewStyle().Foreground(cText).Bold(true)
	styleSelected = lipgloss.NewStyle().Background(cAccent).Foreground(cInk).Bold(true)
	styleSelectedSoft = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleSection = lipgloss.NewStyle().Foreground(cAccent2).Bold(true)
	styleChip = lipgloss.NewStyle().
		Foreground(cDim).
		Background(cPanel).
		Padding(0, 1)
	styleChipAccent = lipgloss.NewStyle().
		Foreground(cInk).
		Background(cAccent2).
		Bold(true).
		Padding(0, 1)
	styleDiffAdd = lipgloss.NewStyle().Foreground(cGreen)
	styleDiffDel = lipgloss.NewStyle().Foreground(cRed)
	styleDiffHunk = lipgloss.NewStyle().Foreground(cAccent2)
	styleModal = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cAccent).
		Background(cSurface).
		Padding(1, 2)
	styleModalTitle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleToast = lipgloss.NewStyle().Foreground(cInk).Background(cAccent2).Bold(true).Padding(0, 1)
	styleTree = lipgloss.NewStyle().Foreground(cFaint)
	applyTerminalBackground(t)
	return activeTheme
}

// applyTerminalBackground tints the terminal's default background with OSC 11
// so reset sequences and blank cells share the theme surface. Terminals that
// do not support OSC 11 simply ignore it.
func applyTerminalBackground(t theme) {
	if !terminalBackground || !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	hex := colorHex(t.surface)
	if len(hex) == 7 {
		fmt.Fprintf(os.Stdout, "\x1b]11;%s\x1b\\", hex)
	}
}

// resetTerminalBackground restores the terminal's configured background.
func resetTerminalBackground() {
	if !terminalBackground || !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	fmt.Fprint(os.Stdout, "\x1b]111\x1b\\")
}

// nextTheme returns the next theme in the displayed order, optionally
// walking backwards.
func nextTheme(current string, backwards bool) string {
	idx := 0
	for i, name := range themeOrder {
		if name == current {
			idx = i
			break
		}
	}
	if backwards {
		idx = (idx - 1 + len(themeOrder)) % len(themeOrder)
	} else {
		idx = (idx + 1) % len(themeOrder)
	}
	return themeOrder[idx]
}

// themeEntries returns the themes in display order.
func themeEntries() []theme {
	out := make([]theme, 0, len(themeOrder))
	for _, name := range themeOrder {
		out = append(out, themeCatalog[name])
	}
	return out
}

// swatches renders a compact colour strip for a theme picker.
func swatches(t theme) string {
	colors := []lipgloss.TerminalColor{t.accent, t.accent2, t.green, t.warn, t.red, t.link}
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("━"))
	}
	return b.String()
}

func colorHex(c lipgloss.TerminalColor) string {
	s := fmt.Sprint(c)
	if strings.HasPrefix(s, "#") {
		return s
	}
	return "#F5B86B"
}
