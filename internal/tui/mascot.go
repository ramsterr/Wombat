package tui

// wombatMascot is the single sitting wombat used across the interface. It is
// deliberately plain line art: no per-theme gradients, no generated shapes.
var wombatMascot = []string{
	"        ╭─╮   ╭─╮",
	"       ╭╯ ╰───╯ ╰╮",
	"      ╭╯  ●   ●  ╰╮",
	"      │     ▄▄     │",
	"      │    ▀▀▀▀    │",
	"     ╭╯            ╰╮",
	"    ╭╯              ╰╮",
	"   ╭╯                ╰╮",
	"   │                  │",
	"   ╰───╮          ╭───╯",
	"       ╰──────────╯",
}

func wombatArt() []string {
	out := make([]string, len(wombatMascot))
	for i, line := range wombatMascot {
		out[i] = styleAccent.Render(line)
	}
	return out
}
