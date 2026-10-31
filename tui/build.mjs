import * as esbuild from "esbuild"
import { copyFileSync, existsSync, mkdirSync } from "node:fs"
import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { createRequire } from "node:module"
import { transformAsync } from "@babel/core"
import solid from "babel-preset-solid"
import typescript from "@babel/preset-typescript"

const require = createRequire(import.meta.url)
const watch = process.argv.includes("--watch")

const solidPlugin = {
  name: "solid",
  setup(build) {
    build.onResolve({ filter: /^solid-js$/ }, () => ({ path: require.resolve("solid-js/dist/solid.js") }))
    build.onResolve({ filter: /^solid-js\/store$/ }, () => ({ path: require.resolve("solid-js/store/dist/store.js") }))
    build.onLoad({ filter: /\.(tsx|ts|jsx)$/ }, async (args) => {
      if (args.path.includes("node_modules")) return
      const code = await readFile(args.path, "utf8")
      const out = await transformAsync(code, {
        filename: args.path,
        configFile: false,
        babelrc: false,
        presets: [
          [solid, { moduleName: "@opentui/solid", generate: "universal" }],
          [typescript],
        ],
      })
      return { contents: out?.code ?? code, loader: "js" }
    })
  },
}

const options = {
  entryPoints: ["src/index.tsx"],
  bundle: true,
  outfile: "dist/app.mjs",
  platform: "node",
  format: "esm",
  target: "node26",
  plugins: [solidPlugin],
  external: ["@opentui/core-*"],
  logLevel: "info",
  sourcemap: true,
}

async function copyNativeRenderer() {
  const platform = process.platform
  const arch = process.arch
  const packageName = `@opentui/core-${platform}-${arch}`
  const fileName = platform === "darwin" ? "libopentui.dylib" : platform === "win32" ? "opentui.dll" : "libopentui.so"
  try {
    const mod = await import(packageName)
    const source = mod.default
    if (!source || !existsSync(source)) return
    mkdirSync("native", { recursive: true })
    copyFileSync(source, join("native", fileName))
    console.log(`native renderer  native/${fileName}`)
  } catch (error) {
    console.warn(`could not stage ${packageName}: ${error instanceof Error ? error.message : String(error)}`)
  }
}

if (watch) {
  const ctx = await esbuild.context(options)
  await ctx.watch()
  await copyNativeRenderer()
  console.log("watching src/")
} else {
  await esbuild.build(options)
  await copyNativeRenderer()
}
