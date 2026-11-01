package tui

import "github.com/charmbracelet/lipgloss"

// Wombat figure from the session banner.
var wombatFigure = []string{
	"             z@@@@@r+_",
	"             @@@@@@$ `~y_   ________",
	"             @@@@@@F    `````````````~~~Trr=-w__        _yag$@@@$g_",
	"             $@@@F`                            `~Tr===rT4@@@@@@@@@@",
	"             `@F`                                      $0@@@@@@@@@@",
	"             y                                         @4@@@@@@@@@F",
	"           _*`                                         ?x~@@@@@@@P",
	"         _z~                                             #@$@@@@F",
	"        v^        ,_                                       `GwagR",
	"       /        y@y@@                                        ==*L_",
	"     _F         `PPF~    ___          _ y_                   ``~?=L",
	"    _y^              _a$@@@@@@$y_     @a@@%                        7+_",
	"    `^              a@@@@@@@@@@@@$    `=RP~                          `?w",
	"    $               $@@@@@@@@@@@@@[                                     ~*,",
	"    F               `@@@@@@@@@@@@@                                        ~a,",
	"    [                 R@@@@@@@@P~                                           ~w",
	"    [_                 _?@PF~\"                                                =_",
	"    %J,                7a$____,                                                ~a",
	"    4 t_                 ~~FF~                                                   =_",
	"     \\ 7L                                                                         `y",
	"        ~W                                                                         `_",
	"         l%                                                                         \"_",
	"         44                                                                          $,",
	"           L                                                                     `+  4$",
	"           $                                                                       X, $L",
	"           $                                                                        %[$$",
	"           $                                                                       ,4$$@,",
	"           Ej                                                                      $ @@@L",
	"           $1           _ z                                                        M@@@@$",
	"           '4           _a`@   z                         y w'__                   ,@@@@@F",
	"             l          4g@@K$WF                       _fy@$F~                    @@@@@@F",
	"             4)          5@@@@@_ r                  _'a@@@~                    a g@@@@@@",
	"              \\%        t @@@@@@E\"                ryy@@@F                     $$y@@@@@@F",
	"                t,       t_@@@@@@                aW@@@@F                   t  $$@@@@@@$",
	"                 7y       ~$@@@@@L.             a@@@@@F                    J y@@@@@@@@",
	"                  `a_ . \\ @$@@@@@$[           y@@@@@@@,                    ay@@@@@@@@",
	"                    ~ayg_$$@@@@@@@$        _yg@@@@@@@h'                   g@@@@@@@@F",
	"                      ~R@@@@@@@@@@@y      y@@@@@@@@@@@                  _a@@@@@@@@~",
	"                      y$@@@@@@@@@@@@@$$gg@@@@@@@@@@@@@%               _a@@@@@@@@F`",
	"                  _a$@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@$y_         _y$@@@@@@@@P'",
	"                 R@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@ggggg$@@@@@@@@@@F`",
	"                    ~~~~~~~``` ~FFFRRRRRRPPFF~~~4@@@@@@@@@@@@@@@@@@@@RRFF~`",
	"                                                     `~~~~~~~~``",
}

// Compact WOMBAT wordmark for the chat footer.
var wombatWordmark = []string{
	"\u2588 \u2588 \u2588\u2580\u2588 \u2588\u2580\u2584\u2580\u2588 \u2588\u2580\u2584 \u2588\u2580\u2588 \u2580\u2588\u2580",
	"\u2588 \u2588 \u2588 \u2588 \u2588 \u2588 \u2588 \u2588\u2584\u2580 \u2588\u2580\u2588  \u2588",
	"\u2580\u2580\u2580 \u2580\u2580\u2580 \u2580   \u2580 \u2580 \u2580 \u2580 \u2580  \u2580",
}

// Large WOMBAT wordmark for the welcome plate.
var wombatWordmarkLarge = []string{
	"\u2588   \u2588  \u2588\u2588\u2588  \u2588   \u2588 \u2588\u2588\u2588\u2588   \u2588\u2588\u2588  \u2588\u2588\u2588\u2588\u2588",
	"\u2588   \u2588 \u2588   \u2588 \u2588\u2588 \u2588\u2588 \u2588   \u2588 \u2588   \u2588   \u2588",
	"\u2588 \u2588 \u2588 \u2588   \u2588 \u2588 \u2588 \u2588 \u2588\u2588\u2588\u2588  \u2588\u2588\u2588\u2588\u2588   \u2588",
	"\u2588\u2588 \u2588\u2588 \u2588   \u2588 \u2588   \u2588 \u2588   \u2588 \u2588   \u2588   \u2588",
	"\u2588   \u2588  \u2588\u2588\u2588  \u2588   \u2588 \u2588\u2588\u2588\u2588  \u2588   \u2588   \u2588",
}

// wombatGradient paints mascot art from the first accent into the second, so
// the large welcome plate and the footer mark pick up both signature colours.
func wombatGradient(lines []string) []string {
	out := make([]string, len(lines))
	from := colorHex(cAccent)
	to := colorHex(cAccent2)
	for i, line := range lines {
		at := 0.0
		if len(lines) > 1 {
			at = float64(i) / float64(len(lines)-1)
		}
		out[i] = lipgloss.NewStyle().Foreground(mixHex(from, to, at)).Render(line)
	}
	return out
}
