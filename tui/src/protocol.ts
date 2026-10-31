export type ThemeColors = {
  ink: string
  surface: string
  panel: string
  border: string
  text: string
  dim: string
  faint: string
  accent: string
  accent2: string
  green: string
  red: string
  warn: string
  link: string
  code: string
  sandbox: string
  outside: string
}

export type ThemeInfo = {
  name: string
  label: string
  blurb: string
  colors: ThemeColors
}

export type CommandOption = {
  value: string
  summary: string
}

export type CommandInfo = {
  name: string
  args?: string
  summary: string
  group?: string
  options?: CommandOption[]
}

export type Attachment = {
  kind: string
  name: string
  size: number
  path?: string
}

export type TodoItem = {
  content: string
  status: "pending" | "in_progress" | "completed" | "cancelled"
  priority?: "high" | "medium" | "low"
}

export type PreviewItem = {
  ref: string
  kind: string
  label: string
  at?: string
}

export type ReferenceItem = {
  value: string
  kind: string
  added?: string
}

export type SessionState = {
  provider: string
  model: string
  mode: string
  cwd: string
  branch: string
  session: string
  title: string
  auto: boolean
  thinking: boolean
  reasoning: string
  window: number
  missing_key: string
  pending: Attachment[]
  streaming: boolean
  theme: string
  refs: ReferenceItem[] | null
}

export type EntryBase = {
  id: number
}

export type UserEntry = EntryBase & {
  kind: "user"
  text: string
  attachments: Attachment[]
}

export type AssistantEntry = EntryBase & {
  kind: "assistant"
  text: string
  reasoning: string
  streaming: boolean
}

export type ToolEntry = EntryBase & {
  kind: "tool"
  name: string
  detail: string
  result: string
  running: boolean
  ok: boolean
  durationMs: number
  started: number
  expanded: boolean
}

export type NoteEntry = EntryBase & {
  kind: "note"
  text: string
  error: boolean
}

export type Entry = UserEntry | AssistantEntry | ToolEntry | NoteEntry

export type ListItem = {
  label: string
  detail?: string
  value: string
  current?: boolean
  icon?: string
}

export type Overlay =
  | {
      kind: "list"
      id: string
      title: string
      subtitle?: string
      action?: string
      items: ListItem[]
      multi?: boolean
      selected: number
    }
  | { kind: "text"; id: string; title: string; body: string; scroll: number }
  | {
      kind: "form"
      id: string
      title: string
      action: string
      fields: { name: string; label: string; value: string; secret?: boolean }[]
      meta?: Record<string, string>
      selected: number
      error?: string
    }
  | {
      kind: "theme"
      id: string
      title: string
      selected: number
      original: string
    }
  | { kind: "help"; id: string; title: string; selected: number }

export type PermissionRequest = {
  id: string
  kind: string
  perm: string
  detail: string
  preview: string
}

export type QuestionRequest = {
  id: string
  header: string
  question: string
  options: { label: string; description?: string }[]
}

export type UsageStats = {
  in: number
  out: number
  cached: number
  history: number
  window: number
}

export type BridgeEvent = Record<string, any> & { type: string }
