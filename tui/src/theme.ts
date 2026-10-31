import type { ThemeColors, ThemeInfo } from "./protocol.js"

export const fallbackTheme: ThemeInfo = {
  name: "wombat",
  label: "Wombat",
  blurb: "warm amber on charcoal",
  colors: {
    ink: "#17130C",
    surface: "#17191F",
    panel: "#1D2028",
    border: "#3A3F4B",
    text: "#E6E1D7",
    dim: "#9A9284",
    faint: "#625D55",
    accent: "#F5B86B",
    accent2: "#8BD5CA",
    green: "#9ECE6A",
    red: "#F7768E",
    warn: "#E0AF68",
    link: "#7DCFFF",
    code: "#C0CAF5",
    sandbox: "#BB9AF7",
    outside: "#7AA2F7",
  },
}

export function resolveTheme(name: string, themes: ThemeInfo[]): ThemeInfo {
  return themes.find((theme) => theme.name === name) ?? themes[0] ?? fallbackTheme
}

export function themeColors(theme: ThemeInfo): ThemeColors {
  return { ...fallbackTheme.colors, ...theme.colors }
}
