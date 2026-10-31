import * as os from "node:os"
import * as path from "node:path"

export function shortPath(value: string, max = 48): string {
  if (!value) return ""
  const home = os.homedir()
  let out = value
  if (home && (value === home || value.startsWith(home + path.sep))) {
    out = "~" + value.slice(home.length)
  }
  if (out.length <= max) return out
  return "…" + out.slice(out.length - max + 1)
}

export function humanTokens(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0"
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 10_000) return `${Math.round(n / 1_000)}k`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return `${Math.round(n)}`
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return ""
  if (ms < 1000) return "<1s"
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`
  return `${Math.floor(ms / 60_000)}m${String(Math.round((ms % 60_000) / 1000)).padStart(2, "0")}s`
}

export function contextBar(used: number, total: number, cells = 8): string {
  const pct = total > 0 ? Math.max(0, Math.min(100, Math.round((used / total) * 100))) : 0
  const filled = total > 0 ? Math.max(used > 0 ? 1 : 0, Math.round((pct / 100) * cells)) : 0
  return "▰".repeat(filled) + "▱".repeat(Math.max(0, cells - filled))
}

export function contextPercent(used: number, total: number): number {
  if (total <= 0) return 0
  return Math.max(0, Math.min(100, Math.round((used / total) * 100)))
}

export function oneLine(value: string): string {
  return value.replace(/\s+/g, " ").trim()
}

export function clip(value: string, max: number): string {
  if (max <= 0) return ""
  const chars = Array.from(value)
  if (chars.length <= max) return value
  return chars.slice(0, Math.max(1, max - 1)).join("") + "…"
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
