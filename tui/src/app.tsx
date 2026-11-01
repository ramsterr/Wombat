import {
  For,
  Show,
  batch,
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  onMount,
  untrack,
} from "solid-js"
import { createStore, produce } from "solid-js/store"
import { useKeyboard, useRenderer, useSelectionHandler, useTerminalDimensions } from "@opentui/solid"
import {
  TextAttributes,
  createHostClipboard,
  type InputRenderable,
  type Selection,
  type TextareaRenderable,
} from "@opentui/core"
import * as fs from "node:fs"
import * as os from "node:os"
import * as path from "node:path"
import { BridgeClient } from "./bridge.js"
import { wordmarkLarge, wombatFigure } from "./ascii.js"
import { Markdown } from "./markdown.js"
import type {
  Attachment,
  BridgeEvent,
  CommandInfo,
  Entry,
  ListItem,
  NoteEntry,
  Overlay,
  PermissionRequest,
  PreviewItem,
  QuestionRequest,
  ReferenceItem,
  SessionState,
  ThemeInfo,
  TodoItem,
  UsageStats,
  UserEntry,
} from "./protocol.js"
import { fallbackTheme, resolveTheme, themeColors } from "./theme.js"
import { clip, contextBar, contextPercent, formatDuration, humanTokens, oneLine, shortPath } from "./util.js"

const spinnerFrames = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]

const emptyState: SessionState = {
  provider: "",
  model: "",
  mode: "create",
  cwd: "",
  branch: "",
  session: "",
  title: "",
  auto: false,
  thinking: false,
  reasoning: "",
  window: 0,
  missing_key: "",
  pending: [],
  streaming: false,
  theme: "wombat",
  refs: [],
}

function Spinner(props: { colors: () => ReturnType<typeof themeColors>; active?: () => boolean; quiet?: () => boolean }) {
  const [frame, setFrame] = createSignal(0)
  onMount(() => {
    const timer = setInterval(() => setFrame((value) => (value + 1) % spinnerFrames.length), 80)
    onCleanup(() => clearInterval(timer))
  })
  return (
    <text fg={props.quiet?.() ? props.colors().faint : props.colors().warn}>
      {spinnerFrames[frame()] ?? "⠋"}
    </text>
  )
}

function KeyHint(props: { key: string; label: string; colors: () => ReturnType<typeof themeColors> }) {
  return (
    <text fg={props.colors().faint} wrapMode="none">
      <span style={{ fg: props.colors().accent }}>{props.key}</span> {props.label}
    </text>
  )
}

function FooterMeta(props: {
  colors: () => ReturnType<typeof themeColors>
  usage: UsageStats
  state: SessionState
  width: number
}) {
  const model = () =>
    props.state.provider ? `${props.state.provider}/${props.state.model}` : props.state.model
  const contextPercent = () =>
    props.usage.window > 0
      ? ((props.usage.history / props.usage.window) * 100).toFixed(1)
      : "?"
  const contextColor = () => {
    if (props.usage.window <= 0) return props.colors().faint
    const pct = (props.usage.history / props.usage.window) * 100
    if (pct > 90) return props.colors().red
    if (pct > 70) return props.colors().warn
    return props.colors().faint
  }

  return (
    <box flexDirection="row" alignItems="center" gap={1} flexGrow={1} minWidth={0}>
      <Show when={props.usage.in > 0}>
        <text fg={props.colors().dim} wrapMode="none">↑{humanTokens(props.usage.in)}</text>
      </Show>
      <Show when={props.usage.out > 0}>
        <text fg={props.colors().dim} wrapMode="none">↓{humanTokens(props.usage.out)}</text>
      </Show>
      <Show when={props.usage.cached > 0}>
        <text fg={props.colors().dim} wrapMode="none">R{humanTokens(props.usage.cached)}</text>
      </Show>
      <text fg={contextColor()} wrapMode="none">
        {contextPercent()}%/{props.usage.window > 0 ? humanTokens(props.usage.window) + " (auto)" : "?"}
      </text>

      <Show when={props.state.missing_key && props.width >= 84}>
        <text fg={props.colors().warn} wrapMode="none">no key</text>
      </Show>
      <Show when={props.state.auto && props.width >= 92}>
        <text fg={props.colors().warn} wrapMode="none">yolo</text>
      </Show>

      <box flexGrow={1} />
      <Show when={props.state.model && props.width >= 76}>
        <text fg={props.colors().dim} wrapMode="none">
          {clip(model(), Math.max(10, Math.min(42, props.width - 48)))}
        </text>
      </Show>
      <Show when={props.state.mode && props.width >= 96}>
        <text fg={props.colors().ink} bg={props.colors().accent} attributes={TextAttributes.BOLD} wrapMode="none">
          {" "}{props.state.mode}{" "}
        </text>
      </Show>
    </box>
  )
}

function Welcome(props: {
  colors: () => ReturnType<typeof themeColors>
  height: number
  width: number
  version: string
}) {
  const compact = () => props.width < 48
  const exact = () => !compact() && props.width >= 89 && props.height >= 47
  const artLines = createMemo(() => (exact() ? wombatFigure.map((line) => line.trimEnd()) : []))
  const artWidth = createMemo(() => Math.max(1, ...artLines().map((line) => Array.from(line).length)))

  return (
    <box flexGrow={1} justifyContent="center" alignItems="center" flexDirection="column" minHeight={0}>
      <Show
        when={exact()}
        fallback={
          <box flexDirection="column" alignItems="center" gap={1} minHeight={0}>
            <Show
              when={!compact()}
              fallback={
                <text
                  fg={props.colors().accent}
                  attributes={TextAttributes.BOLD}
                  selectable
                  selectionBg={props.colors().accent}
                  selectionFg={props.colors().ink}
                >
                  wombat
                </text>
              }
            >
              <box flexDirection="column" width={47} alignItems="flex-start" alignSelf="center">
                <For each={wordmarkLarge}>
                  {(line, index) => (
                    <text
                      fg={props.colors().accent}
                      attributes={TextAttributes.BOLD}
                      wrapMode="none"
                      selectable
                      selectionBg={props.colors().accent}
                      selectionFg={props.colors().ink}
                    >
                      {line || " "}
                    </text>
                  )}
                </For>
              </box>
            </Show>
            <Show when={props.height > 12 && !compact()}>
              <text fg={props.colors().faint}>ask anything · / for commands · @ for files</text>
            </Show>
          </box>
        }
      >
        <box flexDirection="column" width={artWidth()} alignItems="flex-start">
          <For each={artLines()}>
            {(line, index) => (
              <text
                fg={props.colors().accent}
                attributes={TextAttributes.BOLD}
                wrapMode="none"
                selectable
                selectionBg={props.colors().accent}
                selectionFg={props.colors().ink}
              >
                {line || " "}
              </text>
            )}
          </For>
        </box>
      </Show>
    </box>
  )
}

function UserMessage(props: { entry: UserEntry; colors: () => ReturnType<typeof themeColors> }) {
  return (
    <box flexDirection="column" marginBottom={1}>
      <box flexDirection="row" gap={1}>
        <text fg={props.colors().accent2} attributes={TextAttributes.BOLD}>you</text>
        <Show when={props.entry.attachments.length > 0}>
          <text fg={props.colors().faint}>
            {props.entry.attachments.map((attachment) => `${attachment.kind}:${attachment.name}`).join(" · ")}
          </text>
        </Show>
      </box>
      <box border={["left"]} borderColor={props.colors().accent} paddingLeft={2} paddingTop={0}>
        <text
          wrapMode="word"
          fg={props.colors().text}
          selectable
          selectionBg={props.colors().accent}
          selectionFg={props.colors().ink}
        >
          {props.entry.text}
        </text>
      </box>
    </box>
  )
}

function NoteView(props: { entry: NoteEntry; colors: () => ReturnType<typeof themeColors> }) {
  return (
    <text
      fg={props.entry.error ? props.colors().red : props.colors().faint}
      wrapMode="word"
      selectable
      selectionBg={props.colors().accent}
      selectionFg={props.colors().ink}
    >
      {props.entry.error ? "! " : "· "}
      {props.entry.text}
    </text>
  )
}

function ToolView(props: { entry: Extract<Entry, { kind: "tool" }>; colors: () => ReturnType<typeof themeColors>; compact?: boolean }) {
  const [elapsed, setElapsed] = createSignal(props.entry.durationMs)
  createEffect(() => {
    if (!props.entry.running) {
      setElapsed(props.entry.durationMs)
      return
    }
    const timer = setInterval(() => setElapsed(Date.now() - props.entry.started), 250)
    onCleanup(() => clearInterval(timer))
  })
  const lines = () => props.entry.result.split("\n").filter((line) => line.trim().length > 0)
  const preview = () => (props.entry.expanded ? lines() : lines().slice(0, 3))
  return (
    <box
      flexDirection="column"
      backgroundColor={props.colors().panel}
      border={["left"]}
      borderColor={props.entry.running ? props.colors().accent2 : props.entry.ok ? props.colors().border : props.colors().red}
      paddingLeft={1}
      paddingRight={1}
      marginBottom={1}
      onMouseUp={() => {
        props.entry.expanded = !props.entry.expanded
      }}
    >
      <box flexDirection="row" gap={1}>
        <Show
          when={props.entry.running}
          fallback={
            <text fg={props.entry.ok ? props.colors().green : props.colors().red} attributes={TextAttributes.BOLD}>
              {props.entry.ok ? "✓" : "✗"}
            </text>
          }
        >
          <Spinner colors={props.colors} />
        </Show>
        <text fg={props.colors().text} attributes={TextAttributes.BOLD}>{props.entry.name}</text>
        <Show when={props.entry.detail}>
          <text fg={props.colors().dim} wrapMode="none" truncate>{oneLine(props.entry.detail)}</text>
        </Show>
        <box flexGrow={1} />
        <text fg={props.colors().faint}>
          {props.entry.running ? formatDuration(elapsed()) : formatDuration(elapsed())}
        </text>
      </box>
      <Show when={preview().length > 0}>
        <box flexDirection="column" paddingLeft={1}>
          <For each={preview()}>
            {(line) => (
              <text
                fg={line.startsWith("+") ? props.colors().green : line.startsWith("-") ? props.colors().red : line.startsWith("@@") ? props.colors().accent2 : props.colors().faint}
                wrapMode="none"
                truncate
                selectable
                selectionBg={props.colors().accent}
                selectionFg={props.colors().ink}
              >
                {line}
              </text>
            )}
          </For>
          <Show when={!props.entry.expanded && lines().length > 3}>
            <text fg={props.colors().faint}>… click to expand</text>
          </Show>
        </box>
      </Show>
    </box>
  )
}

function AssistantView(props: {
  entry: Extract<Entry, { kind: "assistant" }>
  colors: () => ReturnType<typeof themeColors>
  thinking: boolean
}) {
  const hasText = () => props.entry.text.trim().length > 0
  return (
    <box flexDirection="column" marginBottom={1}>
      <Show when={props.entry.reasoning && (props.thinking || props.entry.streaming)}>
        <box
          flexDirection="column"
          border={["left"]}
          borderColor={props.colors().border}
          paddingLeft={2}
          marginBottom={hasText() ? 1 : 0}
        >
          <text fg={props.colors().warn} attributes={TextAttributes.BOLD}>
            {props.thinking ? "thinking" : "thinking…"}
          </text>
          <Show when={props.thinking}>
            <text
              fg={props.colors().dim}
              attributes={TextAttributes.ITALIC}
              wrapMode="word"
              selectable
              selectionBg={props.colors().accent}
              selectionFg={props.colors().ink}
            >
              {props.entry.reasoning}
            </text>
          </Show>
        </box>
      </Show>
      <Show when={hasText()}>
        <Markdown text={props.entry.text} colors={props.colors()} streaming={props.entry.streaming} />
      </Show>
      <Show when={!hasText() && props.entry.streaming}>
        <box flexDirection="row" gap={1}>
          <Spinner colors={props.colors} />
          <text fg={props.colors().faint}>working</text>
        </box>
      </Show>
    </box>
  )
}

function TodoBar(props: { todos: TodoItem[]; colors: () => ReturnType<typeof themeColors> }) {
  const done = () => props.todos.filter((item) => item.status === "completed" || item.status === "cancelled").length
  const current = () => props.todos.find((item) => item.status === "in_progress") ?? props.todos.find((item) => item.status === "pending")
  return (
    <Show when={props.todos.length > 0}>
      <box flexDirection="row" paddingLeft={1} paddingRight={1} backgroundColor={props.colors().panel}>
        <text fg={props.colors().accent2}>☰ todos</text>
        <text fg={props.colors().dim}>
          {" "}{done()}/{props.todos.length}
        </text>
        <Show when={current()}>
          <text fg={props.colors().faint} wrapMode="none" truncate>
            {"  "}{current()?.content}
          </text>
        </Show>
        <box flexGrow={1} />
        <text fg={props.colors().faint}>/todos</text>
      </box>
    </Show>
  )
}

function PreviewBar(props: { previews: PreviewItem[]; colors: () => ReturnType<typeof themeColors> }) {
  const newest = () => props.previews[0]
  return (
    <Show when={newest()}>
      <box flexDirection="row" paddingLeft={1} paddingRight={1} backgroundColor={props.colors().panel}>
        <text fg={props.colors().accent}>●</text>
        <text fg={props.colors().text}> preview ready </text>
        <text fg={props.colors().link} wrapMode="none" truncate>{newest()?.label}</text>
        <box flexGrow={1} />
        <text fg={props.colors().faint}>ctrl+o open · /previews</text>
      </box>
    </Show>
  )
}

function PendingBar(props: { pending: Attachment[]; colors: () => ReturnType<typeof themeColors> }) {
  return (
    <Show when={props.pending.length > 0}>
      <box flexDirection="row" paddingLeft={1} paddingRight={1}>
        <text fg={props.colors().dim}>attached</text>
        <text fg={props.colors().faint}>
          {"  "}{props.pending.map((attachment) => `${attachment.name} (${attachment.kind})`).join("  ·  ")}
        </text>
        <box flexGrow={1} />
        <text fg={props.colors().faint}>/detach</text>
      </box>
    </Show>
  )
}

function permissionLabel(kind: string): string {
  switch (kind.toLowerCase()) {
    case "bash":
      return "shell command"
    case "write":
      return "file write"
    case "edit":
      return "file edit"
    case "outside":
      return "write outside workspace"
    case "network":
      return "network access"
    case "sandbox-write":
      return "sandbox write"
    case "dangerous":
      return "dangerous command"
    case "sensitive-read":
      return "credentials read"
    case "mcp":
      return "MCP tool"
    default:
      return "permission"
  }
}

function PermissionCard(props: {
  request: PermissionRequest
  colors: () => ReturnType<typeof themeColors>
  feedback: boolean
  onFeedbackRef: (input: InputRenderable | undefined) => void
}) {
  const color = () => {
    const perm = props.request.perm.toLowerCase()
    if (perm.includes("danger") || perm.includes("sensitive")) return props.colors().red
    if (perm.includes("outside")) return props.colors().outside
    if (perm.includes("network") || perm.includes("sandbox") || perm.includes("mcp")) return props.colors().sandbox
    return props.colors().warn
  }
  return (
    <box flexDirection="column" border borderStyle="rounded" borderColor={color()} paddingLeft={1} paddingRight={1} backgroundColor={props.colors().panel}>
      <box flexDirection="row" gap={1}>
        <text fg={color()} attributes={TextAttributes.BOLD}>{permissionLabel(props.request.perm)}</text>
        <text fg={props.colors().text} attributes={TextAttributes.BOLD}>{props.request.kind}</text>
      </box>
      <text fg={props.colors().dim} wrapMode="word" selectable selectionBg={props.colors().accent} selectionFg={props.colors().ink}>
        {oneLine(props.request.detail)}
      </text>
      <Show when={props.request.preview}>
        <text fg={props.colors().faint} wrapMode="word" selectable selectionBg={props.colors().accent} selectionFg={props.colors().ink}>
          {props.request.preview.split("\n").slice(0, 8).join("\n")}
        </text>
      </Show>
      <Show
        when={!props.feedback}
        fallback={
          <box flexDirection="column" gap={0}>
            <input
              ref={(input: InputRenderable | undefined) => {
                props.onFeedbackRef(input)
                if (input) queueMicrotask(() => input.focus())
              }}
              focused
              placeholder="tell wombat what to do instead…"
              textColor={props.colors().text}
              focusedTextColor={props.colors().text}
              backgroundColor={props.colors().panel}
              focusedBackgroundColor={props.colors().panel}
              cursorColor={props.colors().accent}
            />
            <text fg={props.colors().faint}>enter send · esc back</text>
          </box>
        }
      >
        <text fg={props.colors().faint}>
          <span style={{ fg: props.colors().green }}>y</span> allow{"   "}
          <span style={{ fg: props.colors().warn }}>a</span> always{"   "}
          <span style={{ fg: props.colors().red }}>n</span> deny{"   "}
          <span style={{ fg: props.colors().accent }}>f</span> feedback
        </text>
      </Show>
    </box>
  )
}

function QuestionCard(props: {
  request: QuestionRequest
  colors: () => ReturnType<typeof themeColors>
  selected: number
  typing: boolean
  onSelect: (index: number) => void
  onInputRef: (input: InputRenderable | undefined) => void
}) {
  return (
    <box flexDirection="column" border borderStyle="rounded" borderColor={props.colors().accent} paddingLeft={1} paddingRight={1} backgroundColor={props.colors().panel}>
      <Show when={props.request.header}>
        <text fg={props.colors().accent} attributes={TextAttributes.BOLD}>{props.request.header}</text>
      </Show>
      <text fg={props.colors().text} wrapMode="word">{props.request.question}</text>
      <For each={props.request.options}>
        {(option, index) => (
          <box
            flexDirection="row"
            gap={1}
            backgroundColor={index() === props.selected ? props.colors().accent : undefined}
            onMouseUp={() => props.onSelect(index())}
          >
            <text fg={index() === props.selected ? props.colors().ink : props.colors().accent}>
              {index() === props.selected ? "›" : " "}
            </text>
            <text
              fg={index() === props.selected ? props.colors().ink : props.colors().text}
              attributes={index() === props.selected ? TextAttributes.BOLD : undefined}
            >
              {option.label}
            </text>
            <Show when={option.description}>
              <text fg={index() === props.selected ? props.colors().ink : props.colors().faint}>{option.description}</text>
            </Show>
          </box>
        )}
      </For>
      <Show
        when={props.typing}
        fallback={
          <text fg={props.colors().faint}>
            {props.request.options.length > 0
              ? "↑↓ choose · enter select · f type your own · esc skip"
              : "type your answer · enter send · esc skip"}
          </text>
        }
      >
        <box flexDirection="column" gap={0}>
          <input
            ref={(input: InputRenderable | undefined) => {
              props.onInputRef(input)
              if (input) queueMicrotask(() => input.focus())
            }}
            focused
            placeholder="type your answer…"
            textColor={props.colors().text}
            focusedTextColor={props.colors().text}
            backgroundColor={props.colors().panel}
            focusedBackgroundColor={props.colors().panel}
            cursorColor={props.colors().accent}
          />
          <text fg={props.colors().faint}>enter send · esc back</text>
        </box>
      </Show>
    </box>
  )
}

function OverlayView(props: {
  overlay: Overlay
  colors: () => ReturnType<typeof themeColors>
  width: number
  height: number
  themes: ThemeInfo[]
  version: string
  commands: CommandInfo[]
  onSelect: (item: ListItem) => void
  onClose: () => void
  onFormRef: (index: number, input: InputRenderable | undefined) => void
  formIndex: number
  formValues: string[]
  onFormChange: (index: number, value: string) => void
  onFormSubmit: () => void
  onTextScroll: (scroll: any) => void
}) {
  let listScroll: any
  const cardWidth = () => Math.max(30, Math.min(props.width - 2, 86))
  const cardHeight = () => {
    if (props.overlay.kind === "theme") return Math.max(7, Math.min(props.height - 2, props.themes.length + 5))
    if (props.overlay.kind === "list") return Math.max(7, Math.min(props.height - 2, props.overlay.items.length + 5))
    if (props.overlay.kind === "form") return Math.max(10, Math.min(props.height - 2, props.overlay.fields.length * 3 + 6))
    return Math.max(8, Math.min(props.height - 2, 24))
  }
  const selectedIndex = () => {
    if (props.overlay.kind === "list" || props.overlay.kind === "theme") return props.overlay.selected
    return 0
  }
  createEffect(() => {
    const current = props.overlay
    if (current.kind !== "list" && current.kind !== "theme") return
    const rows = Math.max(1, cardHeight() - 4)
    const top = Math.max(0, selectedIndex() - rows + 1)
    if (listScroll) listScroll.scrollTop = top
  })
  return (
    <box flexGrow={1} justifyContent="center" alignItems="center" minHeight={0}>
      <box position="absolute" left={0} top={0} width="100%" height="100%" backgroundColor={props.colors().surface} />
      <box
        width={cardWidth()}
        height={cardHeight()}
        flexDirection="column"
        border
        borderStyle="rounded"
        borderColor={props.colors().accent}
        backgroundColor={props.colors().surface}
        paddingLeft={1}
        paddingRight={1}
        paddingTop={0}
      >
        <box flexDirection="row" height={1} flexShrink={0}>
          <text fg={props.colors().accent} attributes={TextAttributes.BOLD}>{props.overlay.title || "select"}</text>
          <box flexGrow={1} />
          <text fg={props.colors().faint}>esc close</text>
        </box>
        <Show when={props.overlay.kind === "list"}>
          <box flexDirection="column" flexGrow={1} minHeight={0}>
            <Show when={props.overlay.kind === "list" && props.overlay.subtitle}>
              <text fg={props.colors().faint} wrapMode="none" truncate>{props.overlay.kind === "list" ? props.overlay.subtitle : ""}</text>
            </Show>
            <scrollbox ref={(el: any) => (listScroll = el)} flexGrow={1} minHeight={0} scrollbarOptions={{ visible: false }} contentOptions={{ flexDirection: "column" }}>
              <For each={props.overlay.kind === "list" ? props.overlay.items : []}>
                {(item, index) => (
                  <box
                    flexDirection="row"
                    gap={1}
                    paddingLeft={1}
                    paddingRight={1}
                    backgroundColor={index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? props.colors().accent : undefined}
                    onMouseUp={() => props.onSelect(item)}
                  >
                    <text fg={index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? props.colors().ink : props.colors().faint}>
                      {index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? "›" : " "}
                    </text>
                    <Show when={item.icon}>
                      <text fg={index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? props.colors().ink : props.colors().accent2}>{item.icon}</text>
                    </Show>
                    <text
                      fg={index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? props.colors().ink : item.current ? props.colors().accent : props.colors().text}
                      attributes={item.current ? TextAttributes.BOLD : undefined}
                    >
                      {item.label}
                    </text>
                    <Show when={item.detail}>
                      <text fg={index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? props.colors().ink : props.colors().faint} wrapMode="none" truncate>
                        {item.detail}
                      </text>
                    </Show>
                    <box flexGrow={1} />
                    <Show when={item.current}>
                      <text fg={index() === (props.overlay.kind === "list" ? props.overlay.selected : 0) ? props.colors().ink : props.colors().accent2}>current</text>
                    </Show>
                  </box>
                )}
              </For>
            </scrollbox>
          </box>
        </Show>
        <Show when={props.overlay.kind === "text"}>
          <scrollbox ref={(el: any) => props.onTextScroll(el)} flexGrow={1} minHeight={0} scrollbarOptions={{ visible: false }}>
            <text fg={props.colors().text} wrapMode="word" selectable selectionBg={props.colors().accent} selectionFg={props.colors().ink}>
              {props.overlay.kind === "text" ? props.overlay.body : ""}
            </text>
          </scrollbox>
        </Show>
        <Show when={props.overlay.kind === "theme"}>
          <scrollbox ref={(el: any) => (listScroll = el)} flexGrow={1} minHeight={0} scrollbarOptions={{ visible: false }} contentOptions={{ flexDirection: "column" }}>
            <For each={props.themes}>
              {(theme, index) => {
                const selected = () => index() === (props.overlay.kind === "theme" ? props.overlay.selected : 0)
                return (
                  <box flexDirection="row" gap={1} paddingLeft={1} paddingRight={1} backgroundColor={selected() ? props.colors().accent : undefined}>
                    <text fg={selected() ? props.colors().ink : props.colors().faint}>{selected() ? "›" : " "}</text>
                    <text fg={selected() ? props.colors().ink : props.colors().text} attributes={TextAttributes.BOLD}>{theme.label}</text>
                    <text fg={selected() ? props.colors().ink : props.colors().faint} wrapMode="none" truncate>{theme.blurb}</text>
                    <box flexGrow={1} />
                    <text fg={selected() ? props.colors().ink : themeColors(theme).accent}>{"━".repeat(6)}</text>
                  </box>
                )
              }}
            </For>
          </scrollbox>
        </Show>
        <Show when={props.overlay.kind === "form"}>
          <box flexDirection="column" flexGrow={1} gap={1} paddingTop={1}>
            <For each={props.overlay.kind === "form" ? props.overlay.fields : []}>
              {(field, index) => (
                <box flexDirection="column" gap={0}>
                  <text fg={props.colors().faint}>{field.label}</text>
                  <box
                    height={1}
                    minHeight={1}
                    flexShrink={0}
                    flexDirection="row"
                    backgroundColor={props.colors().panel}
                    paddingLeft={1}
                    paddingRight={1}
                  >
                    <text fg={index() === props.formIndex ? props.colors().accent : props.colors().faint}>› </text>
                    <input
                      ref={(input: InputRenderable | undefined) => props.onFormRef(index(), input)}
                      focused={index() === props.formIndex}
                      flexGrow={1}
                      value={field.value}
                      placeholder={field.secret ? "••••••••" : field.label}
                      textColor={props.colors().text}
                      focusedTextColor={props.colors().text}
                      backgroundColor={props.colors().panel}
                      focusedBackgroundColor={props.colors().panel}
                      cursorColor={props.colors().accent}
                      onContentChange={() => props.onFormChange(index(), field.value)}
                    />
                  </box>
                </box>
              )}
            </For>
            <text fg={props.colors().faint}>tab next · enter save · esc cancel</text>
          </box>
        </Show>
        <Show when={props.overlay.kind === "help"}>
          <box flexDirection="column" flexGrow={1} minHeight={0}>
            <HelpLines colors={props.colors} commands={props.commands} onScroll={props.onTextScroll} />
          </box>
        </Show>
        <Show when={props.overlay.kind === "list" || props.overlay.kind === "theme"}>
          <text fg={props.colors().faint} height={1} flexShrink={0}>
            {props.overlay.kind === "theme"
              ? "↑↓ preview · enter apply · esc restore"
              : "↑↓ choose · enter select · esc close"}
          </text>
        </Show>
        <Show when={props.overlay.kind === "text" || props.overlay.kind === "help"}>
          <text fg={props.colors().faint} height={1} flexShrink={0}>
            ↑↓ scroll · pgup/pgdn page · esc close
          </text>
        </Show>
      </box>
    </box>
  )
}

function HelpLines(props: {
  colors: () => ReturnType<typeof themeColors>
  commands: CommandInfo[]
  onScroll: (scroll: any) => void
}) {
  const rows: [string, string][] = [
    ["enter", "send message"],
    ["shift+enter", "new line"],
    ["esc", "stop a run / close a panel"],
    ["/", "command palette"],
    ["@", "file picker"],
    ["ctrl+g", "this help"],
    ["ctrl+o", "open preview"],
    ["ctrl+r", "references"],
    ["ctrl+t", "cycle theme"],
    ["ctrl+y", "copy last reply"],
    ["ctrl+p/n", "prompt history"],
    ["alt+2/3/5", "sessions · models · doctor"],
    ["pgup/pgdn", "scroll transcript"],
    ["drag", "select text to copy"],
  ]
  return (
    <scrollbox ref={(el: any) => props.onScroll(el)} flexGrow={1} minHeight={0} scrollbarOptions={{ visible: false }}>
      <text fg={props.colors().text} attributes={TextAttributes.BOLD}>keys</text>
      <For each={rows}>
        {([key, label]) => (
          <text fg={props.colors().faint}>
            {" "}<span style={{ fg: props.colors().accent }}>{key}</span>{"  "}{label}
          </text>
        )}
      </For>
      <text> </text>
      <text fg={props.colors().text} attributes={TextAttributes.BOLD}>commands</text>
      <For each={props.commands.map((command) => ({ usage: command.name + (command.args ? " " + command.args : ""), summary: command.summary }))}>
        {(row) => (
          <text fg={props.colors().faint}>
            {" "}<span style={{ fg: props.colors().accent }}>{row.usage}</span>{"  "}{row.summary}
          </text>
        )}
      </For>
    </scrollbox>
  )
}

export function App() {
  const renderer = useRenderer()
  const dims = useTerminalDimensions()
  const clipboard = createHostClipboard()

  const [ready, setReady] = createSignal(false)
  const [fatal, setFatal] = createSignal("")
  const [version, setVersion] = createSignal("")
  const [state, setState] = createStore<SessionState>({ ...emptyState })
  const [entries, setEntries] = createStore<{ list: Entry[] }>({ list: [] })
  const [themes, setThemes] = createSignal<ThemeInfo[]>([fallbackTheme])
  const [themeName, setThemeName] = createSignal("wombat")
  const [commands, setCommands] = createSignal<CommandInfo[]>([])
  const [usage, setUsage] = createStore<UsageStats>({ in: 0, out: 0, cached: 0, history: 0, window: 0 })
  const [todos, setTodos] = createStore<{ list: TodoItem[] }>({ list: [] })
  const [previews, setPreviews] = createStore<{ list: PreviewItem[] }>({ list: [] })
  const [permission, setPermission] = createSignal<PermissionRequest | null>(null)
  const [permissionFeedback, setPermissionFeedback] = createSignal(false)
  const [question, setQuestion] = createSignal<QuestionRequest | null>(null)
  const [questionSelected, setQuestionSelected] = createSignal(0)
  const [questionTyping, setQuestionTyping] = createSignal(false)
  const [overlay, setOverlay] = createSignal<Overlay | null>(null)
  const [formIndex, setFormIndex] = createSignal(0)
  const [formValues, setFormValues] = createSignal<string[]>([])
  const [toast, setToast] = createSignal<{ text: string; error: boolean } | null>(null)
  const [busy, setBusy] = createSignal("")
  const [promptText, setPromptText] = createSignal("")
  const [promptHistory, setPromptHistory] = createSignal<string[]>([])
  const [historyIndex, setHistoryIndex] = createSignal(-1)
  const [paletteIndex, setPaletteIndex] = createSignal(0)
  const [fileIndex, setFileIndex] = createSignal(0)
  const [files, setFiles] = createSignal<{ path: string; dir: boolean }[]>([])
  const [filesLoaded, setFilesLoaded] = createSignal(false)
  const [streamStarted, setStreamStarted] = createSignal(0)
  const [clock, setClock] = createSignal(Date.now())
  let questionInput: InputRenderable | undefined
  let permissionInput: InputRenderable | undefined

  let textarea: TextareaRenderable | undefined
  let textScroll: any
  let formRefs: (InputRenderable | undefined)[] = []
  let bridge: BridgeClient | undefined
  let intentional = false
  let idSeq = 1
  let toastTimer: ReturnType<typeof setTimeout> | undefined
  let elapsedTimer: ReturnType<typeof setInterval> | undefined

  const themeInfo = createMemo(() => resolveTheme(themeName(), themes()))
  const colors = createMemo(() => themeColors(themeInfo()))
  const appVersion = () => {
    const value = version().trim()
    if (!value) return ""
    const match = /^v?\d+\.\d+\.\d+[^\s(]*/.exec(value)
    if (match) return match[0].startsWith("v") ? match[0] : "v" + match[0]
    return clip(value, 12)
  }
  const columnWidth = createMemo(() => {
    const width = dims().width
    if (width < 40) return Math.max(18, width - 2)
    return Math.max(40, Math.min(100, width - 6))
  })
  const promptWidth = createMemo(() => Math.max(24, dims().width - 4))
  const transcriptWidth = createMemo(() => Math.max(24, Math.min(dims().width - 4, 128)))
  const isStreaming = () => state.streaming
  const hasEntries = () => entries.list.length > 0
  const home = () => !hasEntries() && !permission() && !question() && !overlay()

  const filteredCommands = createMemo(() => {
    const value = promptText().trimStart()
    if (!value.startsWith("/") || value.includes(" ") || value.includes("\n")) return []
    const query = value.toLowerCase()
    return commands().filter((command) => command.name.startsWith(query))
  })
  const paletteOpen = () => filteredCommands().length > 0 && !permission() && !question() && !overlay()
  const paletteLimit = createMemo(() => Math.max(4, Math.min(9, dims().height - 20)))
  const paletteTop = createMemo(() => {
    const total = filteredCommands().length
    const limit = paletteLimit()
    const maxTop = Math.max(0, total - limit)
    return Math.max(0, Math.min(maxTop, paletteIndex() - Math.floor(limit / 2)))
  })
  const commandColumn = createMemo(() => Math.max(22, Math.min(42, Math.floor(columnWidth() * 0.4))))

  const fileQuery = createMemo(() => {
    const value = promptText()
    const match = /(?:^|\s)@([^\s]*)$/.exec(value)
    return match ? (match[1] ?? "") : null
  })
  const filteredFiles = createMemo(() => {
    const query = fileQuery()
    if (query === null) return []
    if (!filesLoaded()) loadFiles()
    const needle = query.toLowerCase()
    return files().filter((file) => file.path.toLowerCase().includes(needle)).slice(0, 12)
  })
  const fileOpen = () => filteredFiles().length > 0 && !paletteOpen() && !permission() && !question() && !overlay()

  createEffect(() => {
    filteredCommands()
    setPaletteIndex(0)
  })
  createEffect(() => {
    fileQuery()
    setFileIndex(0)
  })
  createEffect(() => {
    const current = state.theme
    if (current && current !== themeName()) setThemeName(current)
  })
  createEffect(() => {
    renderer.setBackgroundColor(colors().surface)
  })
  function forceRepaint() {
    // OpenTUI 0.8 can leave cells behind when a card moves or is replaced.
    // A full repaint keeps modal swaps and session loads visually clean.
    try {
      ;(renderer as any).forceFullRepaintRequested = true
    } catch {}
    renderer.requestRender()
  }
  createEffect(() => {
    ready()
    overlay()
    entries.list.length
    queueMicrotask(() => forceRepaint())
  })
  createEffect(() => {
    if (isStreaming() && streamStarted() === 0) setStreamStarted(Date.now())
    if (!isStreaming()) setStreamStarted(0)
  })
  createEffect(() => {
    if (!isStreaming()) return
    const timer = setInterval(() => setClock(Date.now()), 500)
    onCleanup(() => clearInterval(timer))
  })

  function showToast(text: string, error = false) {
    setToast({ text, error })
    if (toastTimer) clearTimeout(toastTimer)
    toastTimer = setTimeout(() => setToast(null), 1800)
  }

  function copyText(text: string, label = "selection") {
    if (!text.trim()) return
    try {
      clipboard.writeText(text, { selection: "clipboard" }).catch(() => {
        renderer.copyToClipboardOSC52(text)
      })
      renderer.copyToClipboardOSC52(text)
      showToast(`copied ${label}`)
    } catch {
      renderer.copyToClipboardOSC52(text)
      showToast(`copied ${label}`)
    }
  }

  function transcriptText(): string {
    return entries.list
      .map((entry) => {
        if (entry.kind === "user") return `You:\n${entry.text}`
        if (entry.kind === "assistant") return `Wombat:\n${entry.text}`
        if (entry.kind === "tool") return `${entry.name}:\n${entry.result}`
        return entry.text
      })
      .join("\n\n")
  }

  function lastAssistantText(): string {
    for (let index = entries.list.length - 1; index >= 0; index--) {
      const entry = entries.list[index]
      if (entry?.kind === "assistant" && entry.text.trim()) return entry.text
    }
    return ""
  }

  function quit(code = 0) {
    intentional = true
    bridge?.stop()
    try {
      renderer.destroy()
    } catch {}
    process.exit(code)
  }

  function pushEntry(entry: Entry) {
    setEntries("list", entries.list.length, entry)
  }

  function addHistoryItems(items: any[] | null | undefined) {
    for (const item of items ?? []) {
      if (!item) continue
      if (item.kind === "user") {
        pushEntry({ id: idSeq++, kind: "user", text: item.text ?? "", attachments: item.attachments ?? [] })
      } else if (item.kind === "assistant") {
        pushEntry({
          id: idSeq++,
          kind: "assistant",
          text: item.text ?? "",
          reasoning: item.reasoning ?? "",
          streaming: false,
        })
      } else if (item.kind === "tool") {
        pushEntry({
          id: idSeq++,
          kind: "tool",
          name: item.name ?? "tool",
          detail: item.detail ?? "",
          result: item.result ?? "",
          running: false,
          ok: item.ok !== false,
          durationMs: item.duration_ms ?? 0,
          started: Date.now(),
          expanded: false,
        })
      } else if (item.kind === "note") {
        pushEntry({ id: idSeq++, kind: "note", text: item.text ?? "", error: !!item.error })
      }
    }
  }

  function applyTheme(name: string, persist = false) {
    if (!themes().some((theme) => theme.name === name)) return
    setThemeName(name)
    setState("theme", name)
    if (persist) bridge?.send({ type: "set_theme", theme: name })
  }

  function cycleTheme() {
    const list = themes()
    if (list.length === 0) return
    const index = list.findIndex((theme) => theme.name === themeName())
    const next = list[(index + 1) % list.length]
    if (next) {
      applyTheme(next.name, true)
      showToast(`${next.label}`)
    }
  }

  function closeOverlay(restoreTheme = false) {
    const current = overlay()
    if (restoreTheme && current?.kind === "theme") applyTheme(current.original)
    setOverlay(null)
    setFormValues([])
    formRefs = []
    queueMicrotask(() => textarea?.focus())
  }

  function openHelp() {
    setOverlay({ kind: "help", id: "help", title: "help", selected: 0 })
    textarea?.blur()
  }

  function openThemePicker() {
    const list = themes()
    const selected = Math.max(0, list.findIndex((theme) => theme.name === themeName()))
    setOverlay({ kind: "theme", id: "theme", title: "themes", selected, original: themeName() })
    textarea?.blur()
  }

  function openCopyPicker() {
    const items = entries.list
      .filter((entry): entry is UserEntry | Extract<Entry, { kind: "assistant" }> => (entry.kind === "user" || entry.kind === "assistant") && entry.text.trim().length > 0)
      .reverse()
      .map((entry, index) => ({
        label: clip(oneLine(entry.text), 64),
        detail: `${entries.list.length - index}. ${entry.kind}`,
        value: entry.text,
      }))
    if (items.length === 0) {
      showToast("nothing to copy", true)
      return
    }
    setOverlay({ kind: "list", id: "copy", title: "copy", subtitle: "choose a message", items, selected: 0 })
    textarea?.blur()
  }

  function handleListSelection(item: ListItem) {
    const current = overlay()
    if (!current || current.kind !== "list") return
    if (current.action) {
      bridge?.send({ type: "select", action: current.action, value: item.value })
      if (current.multi) {
        // The bridge sends a fresh list after toggling.
        return
      }
      closeOverlay()
      return
    }
    copyText(item.value, item.label)
    closeOverlay()
  }

  function openOverlayFromEvent(event: BridgeEvent) {
    if (event.type === "list") {
      const list = event as any
      setOverlay({
        kind: "list",
        id: list.id ?? "list",
        title: list.title ?? "select",
        subtitle: list.subtitle,
        action: list.action,
        items: list.items ?? [],
        multi: list.multi,
        selected: 0,
      })
      textarea?.blur()
      return
    }
    if (event.type === "text") {
      setOverlay({ kind: "text", id: "text", title: event.title ?? "info", body: event.body ?? "", scroll: 0 })
      textarea?.blur()
      return
    }
    if (event.type === "form") {
      formRefs = []
      setFormValues((event.fields ?? []).map((field: any) => field.value ?? ""))
      setFormIndex(0)
      setOverlay({
        kind: "form",
        id: event.id ?? "form",
        title: event.title ?? "form",
        action: event.action ?? "",
        fields: event.fields ?? [],
        meta: event.meta,
        selected: 0,
      })
      textarea?.blur()
      return
    }
    if (event.type === "picker" && event.kind === "theme") {
      openThemePicker()
    }
  }

  function submitForm() {
    const current = overlay()
    if (!current || current.kind !== "form") return
    const values: Record<string, string> = { ...(current.meta ?? {}) }
    for (let index = 0; index < current.fields.length; index++) {
      const field = current.fields[index]
      if (!field) continue
      values[field.name] = formRefs[index]?.value ?? formValues()[index] ?? field.value ?? ""
    }
    bridge?.send({ type: "form_submit", action: current.action, values, meta: current.meta ?? {} })
    closeOverlay()
  }

  function moveList(delta: number) {
    const current = overlay()
    if (!current || current.kind !== "list") return
    const total = current.items.length
    if (total === 0) return
    const next = (current.selected + delta + total) % total
    setOverlay({ ...current, selected: next })
  }

  function moveTheme(delta: number) {
    const current = overlay()
    if (!current || current.kind !== "theme") return
    const list = themes()
    if (list.length === 0) return
    const next = (current.selected + delta + list.length) % list.length
    setOverlay({ ...current, selected: next })
    const theme = list[next]
    if (theme) applyTheme(theme.name)
  }

  function commitTheme() {
    const current = overlay()
    if (!current || current.kind !== "theme") return
    const theme = themes()[current.selected]
    if (theme) {
      applyTheme(theme.name, true)
      showToast(theme.label)
    }
    closeOverlay()
  }

  function scrollOverlay(delta: number) {
    const current = overlay()
    if (!current || current.kind !== "text") return
    // The scrollbox owns the real offset; key scrolling is handled through
    // renderable focus when available. This signal is a no-op fallback.
    setOverlay({ ...current, scroll: Math.max(0, current.scroll + delta) })
  }

  function decidePermission(allow: boolean, always: boolean, feedback?: string) {
    const current = permission()
    if (!current) return
    bridge?.send({ type: "permission", id: current.id, allow, always, feedback: feedback ?? "" })
    setPermission(null)
    setPermissionFeedback(false)
    setPromptText("")
    queueMicrotask(() => textarea?.focus())
  }

  function answerQuestion(answer: string) {
    const current = question()
    if (!current) return
    bridge?.send({ type: "question", id: current.id, answer })
    setQuestion(null)
    setQuestionTyping(false)
    setQuestionSelected(0)
    queueMicrotask(() => textarea?.focus())
  }

  function submitPrompt() {
    const value = textarea?.plainText.trim() ?? ""
    if (!value) return
    if (permission() || question() || overlay()) return
    if (value === "/exit" || value === "/quit" || value === "/q") return quit(0)
    if (value === "/setup") return quit(3)
    if (value === "/help" || value === "/keys") {
      textarea?.clear()
      setPromptText("")
      openHelp()
      return
    }
    if (value === "/theme" || value === "/themes") {
      textarea?.clear()
      setPromptText("")
      openThemePicker()
      return
    }
    if (value === "/copy all") {
      copyText(transcriptText(), "transcript")
      textarea?.clear()
      setPromptText("")
      return
    }
    if (value === "/copy") {
      textarea?.clear()
      setPromptText("")
      openCopyPicker()
      return
    }
    if (/^\/copy\s+\d+$/.test(value)) {
      const index = Number(value.split(/\s+/)[1]) - 1
      const target = entries.list
        .filter((entry): entry is UserEntry | Extract<Entry, { kind: "assistant" }> => (entry.kind === "user" || entry.kind === "assistant") && entry.text.trim().length > 0)
        .reverse()[index]
      if (target) copyText(target.text, "message")
      else showToast("no message at that index", true)
      textarea?.clear()
      setPromptText("")
      return
    }
    if (value === "/clear") {
      setEntries("list", [])
      bridge?.send({ type: "command", line: value })
      textarea?.clear()
      setPromptText("")
      return
    }
    const command = commands().find((command) => value === command.name || value.startsWith(command.name + " "))
    if (command) bridge?.send({ type: "command", line: value })
    else bridge?.send({ type: "submit", text: value })
    setPromptHistory((history) => [...history, value].slice(-50))
    setHistoryIndex(-1)
    textarea?.clear()
    setPromptText("")
  }

  function choosePalette() {
    const entry = filteredCommands()[paletteIndex()]
    if (!entry) return
    textarea?.setText(entry.name + " ")
    setPromptText(entry.name + " ")
    textarea?.focus()
    queueMicrotask(() => textarea?.gotoLineEnd?.())
  }

  function runPaletteEntry() {
    const entry = filteredCommands()[paletteIndex()]
    if (!entry) return
    textarea?.clear()
    setPromptText("")
    if (entry.name === "/help" || entry.name === "/keys") {
      openHelp()
      return
    }
    if (entry.name === "/theme" || entry.name === "/themes") {
      openThemePicker()
      return
    }
    if (entry.name === "/copy") {
      openCopyPicker()
      return
    }
    if (entry.name === "/exit" || entry.name === "/quit") {
      quit(0)
      return
    }
    if (entry.name === "/setup") {
      quit(3)
      return
    }
    if (entry.name === "/clear") {
      setEntries("list", [])
      bridge?.send({ type: "command", line: entry.name })
      return
    }
    bridge?.send({ type: "command", line: entry.name })
  }

  function completeFile() {
    const entry = filteredFiles()[fileIndex()]
    if (!entry) return
    const value = textarea?.plainText ?? ""
    const at = value.lastIndexOf("@")
    if (at < 0) return
    const replacement = "@" + entry.path + (entry.dir ? "/" : "")
    const next = value.slice(0, at) + replacement
    textarea?.setText(next)
    setPromptText(next)
    textarea?.focus()
    queueMicrotask(() => textarea?.gotoLineEnd?.())
  }

  function loadFiles() {
    if (filesLoaded()) return
    setFilesLoaded(true)
    const root = state.cwd || process.cwd()
    const out: { path: string; dir: boolean }[] = []
    const skip = new Set(["node_modules", ".git", "dist", ".next", "target", "build", ".cache", "vendor"])
    const walk = (dir: string, prefix: string, depth: number) => {
      if (out.length >= 3000 || depth > 6) return
      let items: fs.Dirent[] = []
      try {
        items = fs.readdirSync(dir, { withFileTypes: true })
      } catch {
        return
      }
      for (const item of items) {
        if (item.name.startsWith(".") && item.name !== ".env") continue
        if (skip.has(item.name)) continue
        const relative = prefix ? `${prefix}/${item.name}` : item.name
        if (item.isDirectory()) {
          out.push({ path: relative, dir: true })
          walk(path.join(dir, item.name), relative, depth + 1)
        } else {
          out.push({ path: relative, dir: false })
        }
        if (out.length >= 3000) return
      }
    }
    walk(root, "", 0)
    setFiles(out.sort((a, b) => a.path.localeCompare(b.path)))
  }

  function handleEvent(event: BridgeEvent) {
    switch (event.type) {
      case "ready": {
        batch(() => {
          setReady(true)
          setVersion(event.version ?? "")
          if (event.state) {
            setState(event.state)
            if (event.state.window > 0) setUsage("window", event.state.window)
          }
          if (Array.isArray(event.commands)) setCommands(event.commands)
          if (Array.isArray(event.themes) && event.themes.length > 0) setThemes(event.themes)
          if (event.state?.theme) setThemeName(event.state.theme)
          setEntries("list", [])
          addHistoryItems(event.history)
          setPreviews("list", event.previews ?? [])
          setTodos("list", event.todos ?? [])
        })
        return
      }
      case "state": {
        if (event.state) {
          const previousTheme = state.theme
          setState(event.state)
          if (event.state.window > 0) setUsage("window", event.state.window)
          if (event.state.theme && event.state.theme !== previousTheme) setThemeName(event.state.theme)
        }
        return
      }
      case "user": {
        pushEntry({ id: idSeq++, kind: "user", text: event.text ?? "", attachments: event.attachments ?? [] })
        return
      }
      case "assistant_start": {
        pushEntry({ id: idSeq++, kind: "assistant", text: "", reasoning: "", streaming: true })
        return
      }
      case "text_delta": {
        const last = entries.list[entries.list.length - 1]
        if (last?.kind === "assistant" && last.streaming) {
          setEntries("list", entries.list.length - 1, produce((entry) => {
            if (entry.kind === "assistant") entry.text += event.text ?? ""
          }))
        } else {
          pushEntry({ id: idSeq++, kind: "assistant", text: event.text ?? "", reasoning: "", streaming: true })
        }
        return
      }
      case "reasoning_delta": {
        const last = entries.list[entries.list.length - 1]
        if (last?.kind === "assistant" && last.streaming) {
          setEntries("list", entries.list.length - 1, produce((entry) => {
            if (entry.kind === "assistant") entry.reasoning += event.text ?? ""
          }))
        } else {
          pushEntry({ id: idSeq++, kind: "assistant", text: "", reasoning: event.text ?? "", streaming: true })
        }
        return
      }
      case "assistant_reset": {
        const last = entries.list[entries.list.length - 1]
        if (last?.kind === "assistant") {
          setEntries("list", entries.list.length - 1, produce((entry) => {
            if (entry.kind === "assistant") {
              entry.text = ""
              entry.reasoning = ""
            }
          }))
        }
        return
      }
      case "assistant_end": {
        const last = entries.list[entries.list.length - 1]
        if (last?.kind === "assistant") {
          setEntries("list", entries.list.length - 1, produce((entry) => {
            if (entry.kind === "assistant") entry.streaming = false
          }))
        }
        return
      }
      case "tool_start": {
        const last = entries.list[entries.list.length - 1]
        if (last?.kind === "assistant" && last.streaming) {
          setEntries("list", entries.list.length - 1, produce((entry) => {
            if (entry.kind === "assistant") entry.streaming = false
          }))
        }
        pushEntry({
          id: idSeq++,
          kind: "tool",
          name: event.name ?? "tool",
          detail: event.detail ?? "",
          result: "",
          running: true,
          ok: true,
          durationMs: 0,
          started: Date.now(),
          expanded: false,
        })
        return
      }
      case "tool_end": {
        for (let index = entries.list.length - 1; index >= 0; index--) {
          const entry = entries.list[index]
          if (entry?.kind === "tool" && entry.running && entry.name === event.name) {
            setEntries("list", index, produce((item) => {
              if (item.kind !== "tool") return
              item.running = false
              item.ok = event.ok !== false
              item.result = event.result ?? ""
              item.durationMs = event.duration_ms ?? 0
            }))
            break
          }
        }
        return
      }
      case "note": {
        setBusy("")
        pushEntry({ id: idSeq++, kind: "note", text: event.text ?? "", error: !!event.error })
        return
      }
      case "clear": {
        setEntries("list", [])
        return
      }
      case "history": {
        setEntries("list", [])
        addHistoryItems(event.items)
        return
      }
      case "usage": {
        setUsage({
          in: event.in ?? 0,
          out: event.out ?? 0,
          cached: event.cached ?? 0,
          history: event.history ?? 0,
          window: event.window ?? 0,
        })
        return
      }
      case "todos": {
        setTodos("list", event.items ?? [])
        return
      }
      case "previews": {
        setPreviews("list", event.items ?? [])
        return
      }
      case "permission": {
        setPermission({
          id: event.id,
          kind: event.kind ?? "permission",
          perm: event.perm ?? "allow",
          detail: event.detail ?? "",
          preview: event.preview ?? "",
        })
        setPermissionFeedback(false)
        setOverlay(null)
        textarea?.blur()
        return
      }
      case "question": {
        setQuestion({ id: event.id, header: event.header ?? "", question: event.question ?? "", options: event.options ?? [] })
        setQuestionSelected(0)
        const typing = (event.options ?? []).length === 0
        setQuestionTyping(typing)
        if (typing) queueMicrotask(() => questionInput?.focus())
        setOverlay(null)
        textarea?.blur()
        return
      }
      case "list":
      case "text":
      case "form":
      case "picker": {
        setBusy("")
        openOverlayFromEvent(event)
        return
      }
      case "theme": {
        if (event.name) applyTheme(event.name)
        return
      }
      case "loading": {
        setBusy(event.message ?? "")
        return
      }
      case "retry": {
        setBusy(event.message ?? "retrying")
        return
      }
      case "toast": {
        setBusy("")
        showToast(event.message ?? "", !!event.error)
        return
      }
      case "exit": {
        quit(0)
        return
      }
      case "setup": {
        quit(3)
        return
      }
    }
  }

  useSelectionHandler((selection: Selection) => {
    const text = selection.getSelectedText()
    if (text.trim()) copyText(text)
  })

  useKeyboard((key) => {
    if (!ready()) {
      if (key.ctrl && key.name === "c") quit(0)
      return
    }
    if (permission()) {
      if (permissionFeedback()) {
        if (key.name === "escape") {
          key.preventDefault()
          setPermissionFeedback(false)
          setPermission(null)
          queueMicrotask(() => textarea?.focus())
          return
        }
        if (key.name === "return") {
          key.preventDefault()
          decidePermission(false, false, permissionInput?.value ?? "")
          return
        }
        return
      }
      switch (key.name) {
        case "y":
          key.preventDefault()
          decidePermission(true, false)
          return
        case "a":
          key.preventDefault()
          decidePermission(true, true)
          return
        case "n":
        case "escape":
          key.preventDefault()
          decidePermission(false, false)
          return
        case "f":
          key.preventDefault()
          setPermissionFeedback(true)
          queueMicrotask(() => permissionInput?.focus())
          return
        case "c":
          if (key.ctrl) {
            key.preventDefault()
            decidePermission(false, false)
          }
          return
      }
      return
    }
    if (question()) {
      const request = question()
      if (!request) return
      if (questionTyping()) {
        if (key.name === "escape") {
          key.preventDefault()
          setQuestionTyping(false)
          setQuestionSelected(0)
          return
        }
        if (key.name === "return") {
          key.preventDefault()
          answerQuestion(questionInput?.value ?? "")
          return
        }
        return
      }
      if (key.name === "up" || key.name === "down") {
        key.preventDefault()
        const count = request.options.length
        if (count > 0) {
          setQuestionSelected((value) => (value + (key.name === "up" ? -1 : 1) + count) % count)
        }
        return
      }
      if (key.name === "return") {
        key.preventDefault()
        const option = request.options[questionSelected()]
        if (option) answerQuestion(option.label)
        else {
          setQuestionTyping(true)
          queueMicrotask(() => questionInput?.focus())
        }
        return
      }
      if (key.name === "f") {
        key.preventDefault()
        setQuestionTyping(true)
        queueMicrotask(() => questionInput?.focus())
        return
      }
      if (key.name === "escape") {
        key.preventDefault()
        answerQuestion("")
        return
      }
      if (/^[1-9]$/.test(key.name)) {
        const option = request.options[Number(key.name) - 1]
        if (option) {
          key.preventDefault()
          answerQuestion(option.label)
        }
        return
      }
      return
    }
    if (overlay()) {
      const current = overlay()
      if (!current) return
      if (current.kind === "form") {
        if (key.name === "escape") {
          key.preventDefault()
          closeOverlay()
          return
        }
        if (key.name === "tab") {
          key.preventDefault()
          const count = current.fields.length
          if (count > 0) {
            const next = (formIndex() + 1) % count
            setFormIndex(next)
            formRefs[next]?.focus()
          }
          return
        }
        if (key.name === "return") {
          key.preventDefault()
          submitForm()
          return
        }
        return
      }
      if (current.kind === "list") {
        if (key.name === "up" || (key.name === "k" && !key.ctrl)) {
          key.preventDefault()
          moveList(-1)
          return
        }
        if (key.name === "down" || (key.name === "j" && !key.ctrl)) {
          key.preventDefault()
          moveList(1)
          return
        }
        if (key.name === "return") {
          key.preventDefault()
          const item = current.items[current.selected]
          if (item) handleListSelection(item)
          return
        }
        if (key.name === "escape") {
          key.preventDefault()
          closeOverlay()
          return
        }
        return
      }
      if (current.kind === "theme") {
        if (key.name === "up" || key.name === "down") {
          key.preventDefault()
          moveTheme(key.name === "up" ? -1 : 1)
          return
        }
        if (key.name === "return") {
          key.preventDefault()
          commitTheme()
          return
        }
        if (key.name === "escape") {
          key.preventDefault()
          closeOverlay(true)
          return
        }
        return
      }
      if (current.kind === "text" || current.kind === "help") {
        if (key.name === "escape") {
          key.preventDefault()
          closeOverlay()
          return
        }
        if (key.name === "up" || key.name === "k") {
          key.preventDefault()
          if (textScroll) textScroll.scrollTop = Math.max(0, (textScroll.scrollTop ?? 0) - 1)
          return
        }
        if (key.name === "down" || key.name === "j") {
          key.preventDefault()
          if (textScroll) textScroll.scrollTop = (textScroll.scrollTop ?? 0) + 1
          return
        }
        if (key.name === "pageup") {
          key.preventDefault()
          if (textScroll) textScroll.scrollTop = Math.max(0, (textScroll.scrollTop ?? 0) - Math.max(3, Math.floor(dims().height / 3)))
          return
        }
        if (key.name === "pagedown") {
          key.preventDefault()
          if (textScroll) textScroll.scrollTop = (textScroll.scrollTop ?? 0) + Math.max(3, Math.floor(dims().height / 3))
          return
        }
        return
      }
      return
    }
    if (paletteOpen()) {
      if (key.name === "up" || (key.name === "p" && key.ctrl)) {
        key.preventDefault()
        setPaletteIndex((value) => (value - 1 + filteredCommands().length) % filteredCommands().length)
        return
      }
      if (key.name === "down" || (key.name === "n" && key.ctrl)) {
        key.preventDefault()
        setPaletteIndex((value) => (value + 1) % filteredCommands().length)
        return
      }
      if (key.name === "tab") {
        key.preventDefault()
        choosePalette()
        return
      }
      if (key.name === "return") {
        key.preventDefault()
        runPaletteEntry()
        return
      }
      if (key.name === "escape") {
        key.preventDefault()
        textarea?.clear()
        setPromptText("")
        return
      }
    }
    if (fileOpen()) {
      if (key.name === "up") {
        key.preventDefault()
        setFileIndex((value) => (value - 1 + filteredFiles().length) % filteredFiles().length)
        return
      }
      if (key.name === "down") {
        key.preventDefault()
        setFileIndex((value) => (value + 1) % filteredFiles().length)
        return
      }
      if (key.name === "tab" || key.name === "return") {
        key.preventDefault()
        completeFile()
        return
      }
      if (key.name === "escape") {
        key.preventDefault()
        return
      }
    }
    if (key.ctrl && key.name === "c") {
      key.preventDefault()
      if (isStreaming()) bridge?.send({ type: "interrupt" })
      else quit(0)
      return
    }
    if (key.name === "escape") {
      key.preventDefault()
      if (isStreaming()) bridge?.send({ type: "interrupt" })
      else if (promptText()) {
        textarea?.clear()
        setPromptText("")
      } else if (historyIndex() >= 0) {
        setHistoryIndex(-1)
      }
      return
    }
    if (key.ctrl && key.name === "t") {
      key.preventDefault()
      cycleTheme()
      return
    }
    if (key.name === "?" && !promptText()) {
      key.preventDefault()
      openHelp()
      return
    }
    if (key.ctrl && key.name === "g") {
      key.preventDefault()
      openHelp()
      return
    }
    if (key.ctrl && key.name === "o") {
      key.preventDefault()
      bridge?.send({ type: "command", line: "/preview" })
      return
    }
    if (key.ctrl && key.name === "r") {
      key.preventDefault()
      bridge?.send({ type: "command", line: "/reference" })
      return
    }
    if (key.ctrl && key.name === "y") {
      key.preventDefault()
      const text = lastAssistantText()
      if (text) copyText(text, "reply")
      else showToast("nothing to copy", true)
      return
    }
    if (key.ctrl && key.name === "p") {
      key.preventDefault()
      const history = promptHistory()
      if (history.length === 0) return
      const next = historyIndex() < 0 ? history.length - 1 : Math.max(0, historyIndex() - 1)
      setHistoryIndex(next)
      textarea?.setText(history[next] ?? "")
      setPromptText(history[next] ?? "")
      return
    }
    if (key.ctrl && key.name === "n") {
      key.preventDefault()
      const history = promptHistory()
      if (historyIndex() < 0) return
      const next = historyIndex() + 1
      if (next >= history.length) {
        setHistoryIndex(-1)
        textarea?.clear()
        setPromptText("")
      } else {
        setHistoryIndex(next)
        textarea?.setText(history[next] ?? "")
        setPromptText(history[next] ?? "")
      }
      return
    }
    if (key.name === "pageup" || (key.ctrl && key.name === "up")) {
      key.preventDefault()
      scrollTranscript(-1)
      return
    }
    if (key.name === "pagedown" || (key.ctrl && key.name === "down")) {
      key.preventDefault()
      scrollTranscript(1)
      return
    }
    if (key.meta && key.name === "2") {
      key.preventDefault()
      bridge?.send({ type: "command", line: "/sessions" })
      return
    }
    if (key.meta && key.name === "3") {
      key.preventDefault()
      bridge?.send({ type: "command", line: "/models" })
      return
    }
    if (key.meta && key.name === "4") {
      key.preventDefault()
      bridge?.send({ type: "command", line: "/usage" })
      return
    }
    if (key.meta && key.name === "5") {
      key.preventDefault()
      bridge?.send({ type: "command", line: "/doctor" })
      return
    }
  })

  let transcript: any
  function scrollTranscript(direction: number) {
    transcript?.scrollBy({ x: 0, y: direction * Math.max(2, Math.floor(dims().height / 3)) })
  }

  onMount(() => {
    bridge = new BridgeClient({
      onEvent: handleEvent,
      onExit: (code, signal) => {
        if (intentional) return
        if (!ready()) setFatal(`wombat bridge stopped${signal ? ` (${signal})` : code !== null ? ` (${code})` : ""}`)
        else showToast(`wombat bridge stopped${signal ? ` (${signal})` : code !== null ? ` (${code})` : ""}`, true)
      },
      onError: (error) => {
        if (!ready()) setFatal(`could not start wombat bridge: ${error.message}`)
        else showToast(error.message, true)
      },
    })
    bridge.start()
    queueMicrotask(() => textarea?.focus())
    onCleanup(() => {
      intentional = true
      bridge?.stop()
      if (toastTimer) clearTimeout(toastTimer)
      if (elapsedTimer) clearInterval(elapsedTimer)
    })
  })

  function updatePrompt() {
    const value = textarea?.plainText ?? ""
    setPromptText(value)
  }

  const promptHeight = createMemo(() => {
    const lines = promptText().split("\n").length
    return Math.max(1, Math.min(6, lines))
  })
  const hintText = createMemo(() => {
    if (isStreaming()) {
      const elapsed = streamStarted() ? formatDuration(clock() - streamStarted()) : "0s"
      return `${elapsed}`
    }
    return ""
  })

  return (
    <box flexGrow={1} flexDirection="column" backgroundColor={colors().surface} paddingLeft={2} paddingRight={2} paddingTop={0} paddingBottom={0}>
      <Show when={fatal()}>
        <box flexGrow={1} justifyContent="center" alignItems="center" flexDirection="column" gap={1}>
          <text fg={colors().red} attributes={TextAttributes.BOLD}>{fatal()}</text>
          <text fg={colors().faint}>press ctrl+c to exit</text>
        </box>
      </Show>

      <Show when={!fatal()}>
        <Show when={!ready()}>
          <box flexGrow={1} justifyContent="center" alignItems="center" flexDirection="row" gap={1}>
            <Spinner colors={colors} />
            <text fg={colors().faint}>starting wombat…</text>
          </box>
        </Show>

        <Show when={ready()}>
          <Show when={home()}>
            <Welcome colors={colors} height={dims().height} width={dims().width} version={appVersion()} />
          </Show>

          <Show when={!home()}>
            <Show when={overlay()} fallback={
              <box flexGrow={1} flexShrink={1} flexBasis={0} minHeight={0} width={transcriptWidth()} alignSelf="center">
              <scrollbox
                ref={(el: any) => (transcript = el)}
                flexGrow={1}
                flexShrink={1}
                flexBasis={0}
                minHeight={0}
                scrollbarOptions={{ visible: false }}
                scrollX={false}
                stickyScroll
                stickyStart="bottom"
                contentOptions={{ flexDirection: "column", paddingRight: 1 }}
              >
                <box flexGrow={1} flexDirection="column" justifyContent="flex-end">
                <For each={entries.list}>
                  {(entry) => {
                    if (entry.kind === "user") return <UserMessage entry={entry} colors={colors} />
                    if (entry.kind === "assistant") return <AssistantView entry={entry} colors={colors} thinking={state.thinking} />
                    if (entry.kind === "tool") return <ToolView entry={entry} colors={colors} />
                    return <NoteView entry={entry} colors={colors} />
                  }}
                </For>
                </box>
              </scrollbox>
              </box>
            }>
              <OverlayView
                overlay={overlay()!}
                colors={colors}
                width={columnWidth()}
                height={dims().height - 8}
                themes={themes()}
                version={appVersion()}
                commands={commands()}
                onSelect={handleListSelection}
                onClose={() => closeOverlay()}
                onFormRef={(index, input) => {
                  formRefs[index] = input
                  if (index === formIndex() && input) queueMicrotask(() => input.focus())
                }}
                formIndex={formIndex()}
                formValues={formValues()}
                onFormChange={(index, value) => setFormValues((values) => {
                  const next = [...values]
                  next[index] = value
                  return next
                })}
                onFormSubmit={submitForm}
                onTextScroll={(scroll) => (textScroll = scroll)}
              />
            </Show>
          </Show>

          <box flexDirection="column" width={promptWidth()} alignSelf="center" flexShrink={0}>
            <Show when={!overlay()}>
              <Show when={previews.list.length > 0}>
                <PreviewBar previews={previews.list} colors={colors} />
              </Show>
              <Show when={todos.list.length > 0}>
                <TodoBar todos={todos.list} colors={colors} />
              </Show>
              <Show when={state.pending.length > 0}>
                <PendingBar pending={state.pending} colors={colors} />
              </Show>

              <Show when={permission()}>
                <box width={columnWidth()} alignSelf="center">
                <PermissionCard
                  request={permission()!}
                  colors={colors}
                  feedback={permissionFeedback()}
                  onFeedbackRef={(input) => (permissionInput = input)}
                />
                </box>
              </Show>

              <Show when={question()}>
                <box width={columnWidth()} alignSelf="center">
                <QuestionCard
                  request={question()!}
                  colors={colors}
                  selected={questionSelected()}
                  typing={questionTyping()}
                  onInputRef={(input) => (questionInput = input)}
                  onSelect={(index) => {
                    setQuestionSelected(index)
                    const option = question()?.options[index]
                    if (option) answerQuestion(option.label)
                  }}
                />
                </box>
              </Show>

              <Show when={paletteOpen()}>
                <box
                  flexDirection="column"
                  width={columnWidth()}
                  alignSelf="center"
                  border
                  borderStyle="rounded"
                  borderColor={colors().border}
                  backgroundColor={colors().surface}
                  paddingLeft={1}
                  paddingRight={1}
                  marginBottom={1}
                >
                  <box flexDirection="row">
                    <text fg={colors().accent2} attributes={TextAttributes.BOLD}>commands</text>
                    <text fg={colors().faint}>  {filteredCommands().length} matches</text>
                    <box flexGrow={1} />
                    <text fg={colors().faint} wrapMode="none">
                      {paletteIndex() + 1}/{filteredCommands().length} · enter run · tab complete
                    </text>
                  </box>
                  <For each={filteredCommands().slice(paletteTop(), paletteTop() + paletteLimit())}>
                    {(command, index) => {
                      const absolute = () => paletteTop() + index()
                      const selected = () => absolute() === paletteIndex()
                      const label = () => command.name + (command.args ? " " + command.args : "")
                      return (
                        <box
                          flexDirection="row"
                          gap={1}
                          paddingLeft={1}
                          paddingRight={1}
                          backgroundColor={selected() ? colors().accent : undefined}
                          onMouseUp={() => {
                            setPaletteIndex(absolute())
                            const item = filteredCommands()[absolute()]
                            if (item) {
                              textarea?.setText(item.name + " ")
                              setPromptText(item.name + " ")
                              textarea?.focus()
                            }
                          }}
                        >
                          <text fg={selected() ? colors().ink : colors().faint} wrapMode="none">
                            {selected() ? "›" : " "}
                          </text>
                          <box width={commandColumn()} flexShrink={0}>
                            <text
                              fg={selected() ? colors().ink : colors().accent}
                              attributes={TextAttributes.BOLD}
                              wrapMode="none"
                            >
                              {clip(label(), commandColumn())}
                            </text>
                          </box>
                          <box flexGrow={1} minWidth={0}>
                            <text fg={selected() ? colors().ink : colors().faint} wrapMode="none" truncate>
                              {command.summary}
                            </text>
                          </box>
                        </box>
                      )
                    }}
                  </For>
                  <text fg={colors().faint}>↑↓ select · tab complete · enter run · esc dismiss</text>
                </box>
              </Show>

              <Show when={fileOpen()}>
                <box
                  flexDirection="column"
                  width={columnWidth()}
                  alignSelf="center"
                  border
                  borderStyle="rounded"
                  borderColor={colors().border}
                  backgroundColor={colors().surface}
                  paddingLeft={1}
                  paddingRight={1}
                  marginBottom={1}
                >
                  <box flexDirection="row">
                    <text fg={colors().accent2} attributes={TextAttributes.BOLD}>files</text>
                    <text fg={colors().faint}>  @{fileQuery() ?? ""}</text>
                    <box flexGrow={1} />
                    <text fg={colors().faint}>tab complete</text>
                  </box>
                  <For each={filteredFiles().slice(0, 9)}>
                    {(file, index) => (
                      <box
                        flexDirection="row"
                        gap={1}
                        paddingLeft={1}
                        paddingRight={1}
                        backgroundColor={index() === fileIndex() ? colors().accent : undefined}
                        onMouseUp={() => {
                          setFileIndex(index())
                          completeFile()
                        }}
                      >
                        <text fg={index() === fileIndex() ? colors().ink : colors().faint}>
                          {index() === fileIndex() ? "›" : " "}
                        </text>
                        <text fg={index() === fileIndex() ? colors().ink : colors().text} wrapMode="none" truncate>
                          {file.path}{file.dir ? "/" : ""}
                        </text>
                      </box>
                    )}
                  </For>
                  <text fg={colors().faint}>↑↓ select · tab / enter complete · esc dismiss</text>
                </box>
              </Show>

              <Show when={!permission() && !question()}>
                <box
                  flexDirection="column"
                  border
                  borderStyle="rounded"
                  borderColor={colors().accent}
                  backgroundColor={colors().surface}
                  paddingLeft={1}
                  paddingRight={1}
                >
                  <box flexDirection="row" gap={0}>
                    <text fg={colors().accent} attributes={TextAttributes.BOLD}>❯ </text>
                    <textarea
                      ref={(input: TextareaRenderable) => {
                        textarea = input
                        queueMicrotask(() => input.focus())
                      }}
                      flexGrow={1}
                      height={promptHeight()}
                      placeholder={state.mode === "chat" ? "Ask about the code, or /help" : "Describe what to build, or /help"}
                      placeholderColor={colors().dim}
                      textColor={colors().text}
                      focusedTextColor={colors().text}
                      backgroundColor={colors().surface}
                      focusedBackgroundColor={colors().surface}
                      cursorColor={colors().accent}
                      selectionBg={colors().accent}
                      selectionFg={colors().ink}
                      keyBindings={[
                        { name: "return", action: "submit" },
                        { name: "return", shift: true, action: "newline" },
                        { name: "return", meta: true, action: "newline" },
                        { name: "linefeed", action: "submit" },
                        { name: "kpenter", action: "submit" },
                      ]}
                      onContentChange={updatePrompt}
                      onSubmit={submitPrompt}
                    />
                  </box>
                </box>
              </Show>
            </Show>

            <box flexDirection="row" paddingLeft={1} paddingRight={1} flexShrink={0} gap={2}>
              <Show when={dims().width >= 96 && (isStreaming() || busy() || permission() || question())}>
                <Show
                  when={permission() || question()}
                  fallback={
                    <Show
                      when={isStreaming()}
                      fallback={
                        <box flexDirection="row" gap={1}>
                          <Spinner colors={colors} />
                          <text fg={colors().warn} wrapMode="none">{clip(busy(), Math.max(10, dims().width - 30))}</text>
                        </box>
                      }
                    >
                      <box flexDirection="row" gap={1}>
                        <Spinner colors={colors} />
                        <text fg={colors().warn} wrapMode="none">working {hintText()}</text>
                      </box>
                    </Show>
                  }
                >
                  <text fg={colors().warn} wrapMode="none">
                    {permission() ? "waiting for permission" : "waiting for your answer"}
                  </text>
                </Show>
              </Show>
              <FooterMeta colors={colors} usage={usage} state={state} width={dims().width - 6} />
            </box>
          </box>
        </Show>
      </Show>

      <Show when={toast()}>
        <box position="absolute" right={2} bottom={8} backgroundColor={colors().panel} border borderStyle="rounded" borderColor={toast()?.error ? colors().red : colors().accent2} paddingLeft={1} paddingRight={1}>
          <text fg={toast()?.error ? colors().red : colors().text}>{toast()?.text}</text>
        </box>
      </Show>
    </box>
  )
}
