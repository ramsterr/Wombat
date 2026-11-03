import { mixHex } from "./ascii.js"
import type { ThemeColors, ThemeInfo } from "./protocol.js"

export const fallbackTheme: ThemeInfo = {
  name: "monokai",
  label: "Monokai",
  blurb: "high-contrast neon on charcoal",
  colors: {
    ink: "#19181A",
    surface: "#1E1F1C",
    panel: "#272822",
    border: "#49483E",
    text: "#F8F8F2",
    dim: "#CFCFC2",
    faint: "#75715E",
    accent: "#A6E22E",
    accent2: "#66D9EF",
    green: "#A6E22E",
    red: "#F92672",
    warn: "#E6DB74",
    link: "#66D9EF",
    code: "#F8F8F2",
    sandbox: "#AE81FF",
    outside: "#FD971F",
  },
}

const nearBlack = "#05070A"

/**
 * Darken the page and lift the ink. The terminal UI is meant to read like a
 * high-contrast terminal theme, not a soft editor theme.
 */
export function highContrastColors(input: ThemeColors): ThemeColors {
  const darken = (value: string, amount: number) => mixHex(value, nearBlack, amount)
  const lift = (value: string, amount: number) => mixHex(value, "#FFFFFF", amount)
  return {
    ...input,
    ink: darken(input.ink, 0.55),
    surface: darken(input.surface, 0.72),
    panel: darken(input.panel, 0.58),
    border: lift(input.border, 0.18),
    text: lift(input.text, 0.16),
    dim: lift(input.dim, 0.18),
    faint: lift(input.faint, 0.24),
    accent: lift(input.accent, 0.1),
    accent2: lift(input.accent2, 0.1),
    green: lift(input.green, 0.08),
    red: lift(input.red, 0.08),
    warn: lift(input.warn, 0.08),
    link: lift(input.link, 0.08),
    code: lift(input.code, 0.1),
    sandbox: lift(input.sandbox, 0.08),
    outside: lift(input.outside, 0.08),
  }
}

export function resolveTheme(name: string, themes: ThemeInfo[]): ThemeInfo {
  return themes.find((theme) => theme.name === name) ?? themes[0] ?? fallbackTheme
}

export function themeColors(theme: ThemeInfo): ThemeColors {
  return highContrastColors({ ...fallbackTheme.colors, ...theme.colors })
}
