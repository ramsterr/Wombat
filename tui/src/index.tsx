import { render } from "@opentui/solid"
import { setRenderLibPath } from "@opentui/core"
import * as fs from "node:fs"
import * as path from "node:path"
import { App } from "./app.js"
import { fallbackTheme } from "./theme.js"

function findNativeLib(): string | undefined {
  if (process.env.WOMBAT_OTUI_LIB) return process.env.WOMBAT_OTUI_LIB
  const name =
    process.platform === "darwin" ? "libopentui.dylib" : process.platform === "win32" ? "opentui.dll" : "libopentui.so"
  const roots = [
    path.resolve(import.meta.dirname, "..", "native"),
    path.resolve(import.meta.dirname, ".."),
    path.resolve(process.cwd(), "tui", "native"),
  ]
  for (const root of roots) {
    const candidate = path.join(root, name)
    if (fs.existsSync(candidate)) return candidate
  }
  return undefined
}

const lib = findNativeLib()
if (lib) setRenderLibPath(lib)

render(() => <App />, {
  backgroundColor: fallbackTheme.colors.surface,
  exitOnCtrlC: false,
  targetFps: 30,
})
