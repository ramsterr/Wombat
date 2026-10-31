import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process"
import * as fs from "node:fs"
import * as os from "node:os"
import * as path from "node:path"
import type { BridgeEvent } from "./protocol.js"

export type BridgeHandlers = {
  onEvent: (event: BridgeEvent) => void
  onExit: (code: number | null, signal: NodeJS.Signals | null) => void
  onError: (error: Error) => void
}

export class BridgeClient {
  private child: ChildProcessWithoutNullStreams | undefined
  private buffer = ""
  private stopped = false
  private log: fs.WriteStream | undefined

  constructor(private handlers: BridgeHandlers) {}

  start(): void {
    const exe = process.env.WOMBAT_BRIDGE_EXE || "wombat"
    const args = ["bridge"]
    const session = process.env.WOMBAT_SESSION_ID
    if (session) args.push("--session", session)

    try {
      const home = process.env.WOMBAT_HOME || path.join(os.homedir(), ".wombat")
      fs.mkdirSync(home, { recursive: true })
      this.log = fs.createWriteStream(path.join(home, "ui.log"), { flags: "a" })
      this.log.write(`\n--- ui ${new Date().toISOString()} ---\n`)
    } catch {
      this.log = undefined
    }

    try {
      this.child = spawn(exe, args, {
        cwd: process.cwd(),
        env: { ...process.env, WOMBAT_UI: "1" },
        stdio: ["pipe", "pipe", "pipe"],
      })
    } catch (error) {
      this.handlers.onError(error instanceof Error ? error : new Error(String(error)))
      return
    }

    this.child.stdout.setEncoding("utf8")
    this.child.stdout.on("data", (chunk: string) => this.consume(chunk))
    this.child.stderr.setEncoding("utf8")
    this.child.stderr.on("data", (chunk: string) => this.log?.write(chunk))
    this.child.on("error", (error) => this.handlers.onError(error))
    this.child.on("exit", (code, signal) => {
      if (this.stopped) return
      this.handlers.onExit(code, signal)
    })
    this.child.stdin.on("error", () => {})
  }

  private consume(chunk: string): void {
    this.buffer += chunk
    while (true) {
      const index = this.buffer.indexOf("\n")
      if (index < 0) break
      const line = this.buffer.slice(0, index).trim()
      this.buffer = this.buffer.slice(index + 1)
      if (!line) continue
      try {
        const event = JSON.parse(line) as BridgeEvent
        this.handlers.onEvent(event)
      } catch (error) {
        this.log?.write(`bad event: ${line}\n${String(error)}\n`)
      }
    }
  }

  send(message: Record<string, unknown>): void {
    if (!this.child || this.child.stdin.destroyed) return
    this.child.stdin.write(JSON.stringify(message) + "\n")
  }

  stop(): void {
    this.stopped = true
    try {
      this.child?.stdin.write(JSON.stringify({ type: "shutdown" }) + "\n")
    } catch {}
    try {
      this.child?.kill()
    } catch {}
    this.log?.end()
  }
}
