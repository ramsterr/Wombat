import { For, Show, createMemo } from "solid-js"
import { TextAttributes } from "@opentui/core"
import type { ThemeColors } from "./protocol.js"

type InlineToken = { text: string; kind: "plain" | "code" | "bold" | "italic" | "link" }

const inlinePattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g

function inlineTokens(value: string): InlineToken[] {
  const out: InlineToken[] = []
  let last = 0
  for (const match of value.matchAll(inlinePattern)) {
    const index = match.index ?? 0
    if (index > last) out.push({ text: value.slice(last, index), kind: "plain" })
    const token = match[0]
    if (token.startsWith("`")) out.push({ text: token.slice(1, -1), kind: "code" })
    else if (token.startsWith("**")) out.push({ text: token.slice(2, -2), kind: "bold" })
    else if (token.startsWith("*")) out.push({ text: token.slice(1, -1), kind: "italic" })
    else {
      const link = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(token)
      out.push({ text: link?.[1] ?? token, kind: "link" })
    }
    last = index + token.length
  }
  if (last < value.length) out.push({ text: value.slice(last), kind: "plain" })
  return out
}

function Inline(props: { value: string; colors: ThemeColors }) {
  const tokens = createMemo(() => inlineTokens(props.value))
  return (
    <For each={tokens()}>
      {(token) => {
        switch (token.kind) {
          case "code":
            return <span style={{ fg: props.colors.code }}>{token.text}</span>
          case "bold":
            return <strong>{token.text}</strong>
          case "italic":
            return <em>{token.text}</em>
          case "link":
            return <u><span style={{ fg: props.colors.link }}>{token.text}</span></u>
          default:
            return token.text
        }
      }}
    </For>
  )
}

function DiffLine(props: { line: string; colors: ThemeColors }) {
  const color = createMemo(() => {
    if (props.line.startsWith("+++") || props.line.startsWith("---")) return props.colors.faint
    if (props.line.startsWith("@@")) return props.colors.accent2
    if (props.line.startsWith("+")) return props.colors.green
    if (props.line.startsWith("-")) return props.colors.red
    return props.colors.dim
  })
  return (
    <text fg={color()} wrapMode="none" truncate selectable selectionBg={props.colors.accent} selectionFg={props.colors.ink}>
      {props.line || " "}
    </text>
  )
}

function CodeBlock(props: { lines: string[]; lang: string; colors: ThemeColors }) {
  const diff = () => ["diff", "patch"].includes(props.lang.toLowerCase())
  return (
    <box
      flexDirection="column"
      backgroundColor={props.colors.panel}
      border={["left"]}
      borderColor={props.colors.border}
      paddingLeft={1}
      paddingRight={1}
      marginTop={1}
      marginBottom={1}
    >
      <text fg={props.colors.faint} attributes={TextAttributes.BOLD}>
        {props.lang || "code"}
      </text>
      <For each={props.lines}>
        {(line) =>
          diff() ? (
            <DiffLine line={line} colors={props.colors} />
          ) : (
            <text fg={props.colors.code} wrapMode="none" truncate selectable selectionBg={props.colors.accent} selectionFg={props.colors.ink}>
              {line || " "}
            </text>
          )
        }
      </For>
    </box>
  )
}

type Block =
  | { kind: "blank" }
  | { kind: "text"; text: string; heading?: number; bullet?: boolean; number?: string; quote?: boolean }
  | { kind: "rule" }
  | { kind: "code"; lines: string[]; lang: string }
  | { kind: "table"; rows: string[][]; header: boolean }

function blocks(value: string): Block[] {
  const lines = value.replace(/\r\n/g, "\n").split("\n")
  const out: Block[] = []
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i] ?? ""
    const trimmed = raw.trim()
    if (trimmed.startsWith("```")) {
      const lang = trimmed.slice(3).trim()
      const code: string[] = []
      i++
      while (i < lines.length && !(lines[i] ?? "").trim().startsWith("```")) {
        code.push(lines[i] ?? "")
        i++
      }
      out.push({ kind: "code", lines: code, lang })
      continue
    }
    if (!trimmed) {
      out.push({ kind: "blank" })
      continue
    }
    if (/^([-*_])\1\1+$/.test(trimmed)) {
      out.push({ kind: "rule" })
      continue
    }
    if (trimmed.includes("|") && i + 1 < lines.length && /^\s*\|?[\s:-]+\|[\s:|-]*$/.test(lines[i + 1] ?? "")) {
      const rows: string[][] = [splitRow(trimmed)]
      i += 2
      while (i < lines.length && (lines[i] ?? "").trim().includes("|")) {
        rows.push(splitRow(lines[i] ?? ""))
        i++
      }
      i--
      out.push({ kind: "table", rows, header: true })
      continue
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(trimmed)
    if (heading) {
      out.push({ kind: "text", text: heading[2] ?? "", heading: heading[1]?.length ?? 1 })
      continue
    }
    if (/^[-*+]\s+/.test(trimmed)) {
      out.push({ kind: "text", text: trimmed.replace(/^[-*+]\s+/, ""), bullet: true })
      continue
    }
    const numbered = /^(\d+[.)])\s+(.*)$/.exec(trimmed)
    if (numbered) {
      out.push({ kind: "text", text: numbered[2] ?? "", number: numbered[1] })
      continue
    }
    if (trimmed.startsWith("> ")) {
      out.push({ kind: "text", text: trimmed.slice(2), quote: true })
      continue
    }
    out.push({ kind: "text", text: raw.trimEnd() })
  }
  return out
}

function splitRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((cell) => cell.trim())
}

function Table(props: { rows: string[][]; colors: ThemeColors }) {
  const cols = createMemo(() => Math.max(...props.rows.map((row) => row.length), 1))
  const widths = createMemo(() => {
    const out = Array.from({ length: cols() }, () => 0)
    for (const row of props.rows) {
      for (let i = 0; i < out.length; i++) {
        out[i] = Math.max(out[i] ?? 0, Array.from(row[i] ?? "").length)
      }
    }
    return out
  })
  return (
    <box flexDirection="column" marginTop={1} marginBottom={1}>
      <For each={props.rows}>
        {(row, index) => (
          <text wrapMode="none" truncate fg={index() === 0 ? props.colors.text : props.colors.dim}>
            <span style={{ fg: props.colors.faint }}>│ </span>
            <For each={row}>
              {(cell, cellIndex) => (
                <>
                  <span style={{ fg: index() === 0 ? props.colors.accent : props.colors.text }}>{cell}</span>
                  <span style={{ fg: props.colors.faint }}>
                    {(cellIndex() < cols() - 1 ? " ".repeat(Math.max(1, (widths()[cellIndex()] ?? 0) - Array.from(cell).length + 2)) + "│ " : "")}
                  </span>
                </>
              )}
            </For>
          </text>
        )}
      </For>
    </box>
  )
}

export function Markdown(props: { text: string; colors: ThemeColors; streaming?: boolean }) {
  const parsed = createMemo(() => blocks(props.text))
  return (
    <box flexDirection="column">
      <For each={parsed()}>
        {(block, index) => {
          switch (block.kind) {
            case "blank":
              return <text> </text>
            case "rule":
              return <text fg={props.colors.faint}>{"─".repeat(40)}</text>
            case "code":
              return <CodeBlock lines={block.lines} lang={block.lang} colors={props.colors} />
            case "table":
              return <Table rows={block.rows} colors={props.colors} />
            case "text": {
              const last = () => index() === parsed().length - 1
              const heading = block.heading ?? 0
              return (
                <text
                  wrapMode="word"
                  selectable
                  selectionBg={props.colors.accent}
                  selectionFg={props.colors.ink}
                  fg={
                    heading
                      ? heading <= 2
                        ? props.colors.text
                        : props.colors.accent2
                      : block.quote
                        ? props.colors.dim
                        : props.colors.text
                  }
                  attributes={heading ? TextAttributes.BOLD : undefined}
                  paddingLeft={block.quote ? 1 : 0}
                >
                  <Show when={block.bullet}>
                    <span style={{ fg: props.colors.accent }}>• </span>
                  </Show>
                  <Show when={block.number}>
                    <span style={{ fg: props.colors.accent }}>{block.number} </span>
                  </Show>
                  <Show when={block.quote}>
                    <span style={{ fg: props.colors.border }}>│ </span>
                  </Show>
                  <Inline value={block.text} colors={props.colors} />
                  <Show when={props.streaming && last()}>
                    <span style={{ fg: props.colors.accent }}>▍</span>
                  </Show>
                </text>
              )
            }
          }
        }}
      </For>
    </box>
  )
}
