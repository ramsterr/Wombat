export const wombatFigure: string[] = [
  "         z@@@@@r+_",
  "         @@@@@@$ `~y_   ________",
  "         @@@@@@F    `````````````~~~Trr=-w__        _yag$@@@$g_",
  "         $@@@F`                            `~Tr===rT4@@@@@@@@@@",
  "         `@F`                                      $0@@@@@@@@@@",
  "         y                                         @4@@@@@@@@@F",
  "       _*`                                         ?x~@@@@@@@P",
  "     _z~                                             #@$@@@@F",
  "    v^        ,_                                       `GwagR",
  "   /        y@y@@                                        ==*L_",
  " _F         `PPF~    ___          _ y_                   ``~?=L",
  "_y^              _a$@@@@@@$y_     @a@@%                        7+_",
  "`^              a@@@@@@@@@@@@$    `=RP~                          `?w",
  "$               $@@@@@@@@@@@@@[                                     ~*,",
  "F               `@@@@@@@@@@@@@                                        ~a,",
  "[                 R@@@@@@@@P~                                           ~w",
  "[_                 _?@PF~\"                                                =_",
  "%J,                7a$____,                                                ~a",
  "4 t_                 ~~FF~                                                   =_",
  " \\ 7L                                                                         `y",
  "    ~W                                                                         `_",
  "     l%                                                                         \"_",
  "     44                                                                          $,",
  "       L                                                                     `+  4$",
  "       $                                                                       X, $L",
  "       $                                                                        %[$$",
  "       $                                                                       ,4$$@,",
  "       Ej                                                                      $ @@@L",
  "       $1           _ z                                                        M@@@@$",
  "       '4           _a`@   z                         y w'__                   ,@@@@@F",
  "         l          4g@@K$WF                       _fy@$F~                    @@@@@@F",
  "         4)          5@@@@@_ r                  _'a@@@~                    a g@@@@@@",
  "          \\%        t @@@@@@E\"                ryy@@@F                     $$y@@@@@@F",
  "            t,       t_@@@@@@                aW@@@@F                   t  $$@@@@@@$",
  "             7y       ~$@@@@@L.             a@@@@@F                    J y@@@@@@@@",
  "              `a_ . \\ @$@@@@@$[           y@@@@@@@,                    ay@@@@@@@@",
  "                ~ayg_$$@@@@@@@$        _yg@@@@@@@h'                   g@@@@@@@@F",
  "                  ~R@@@@@@@@@@@y      y@@@@@@@@@@@                  _a@@@@@@@@~",
  "                  y$@@@@@@@@@@@@@$$gg@@@@@@@@@@@@@%               _a@@@@@@@@F`",
  "              _a$@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@$y_         _y$@@@@@@@@P'",
  "             R@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@ggggg$@@@@@@@@@@F`",
  "                ~~~~~~~``` ~FFFRRRRRRPPFF~~~4@@@@@@@@@@@@@@@@@@@@RRFF~`",
  "                                                 `~~~~~~~~``",
]

function parseHex(value: string): [number, number, number] | null {
  const text = value.trim().replace(/^#/, "")
  if (text.length !== 6) return null
  const n = Number.parseInt(text, 16)
  if (Number.isNaN(n)) return null
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

export function mixHex(a: string, b: string, t: number): string {
  const left = parseHex(a)
  const right = parseHex(b)
  if (!left || !right) return a
  const at = Math.max(0, Math.min(1, t))
  const mix = (x: number, y: number) => Math.round(x + (y - x) * at)
  return `#${[mix(left[0], right[0]), mix(left[1], right[1]), mix(left[2], right[2])]
    .map((value) => value.toString(16).padStart(2, "0"))
    .join("")}`
}

export const wordmarkLarge: string[] = [
  "\u2588   \u2588  \u2588\u2588\u2588  \u2588   \u2588 \u2588\u2588\u2588\u2588   \u2588\u2588\u2588  \u2588\u2588\u2588\u2588\u2588",
  "\u2588   \u2588 \u2588   \u2588 \u2588\u2588 \u2588\u2588 \u2588   \u2588 \u2588   \u2588   \u2588",
  "\u2588 \u2588 \u2588 \u2588   \u2588 \u2588 \u2588 \u2588 \u2588\u2588\u2588\u2588  \u2588\u2588\u2588\u2588\u2588   \u2588",
  "\u2588\u2588 \u2588\u2588 \u2588   \u2588 \u2588   \u2588 \u2588   \u2588 \u2588   \u2588   \u2588",
  "\u2588   \u2588  \u2588\u2588\u2588  \u2588   \u2588 \u2588\u2588\u2588\u2588  \u2588   \u2588   \u2588",
]
