// Wombat phone client. Plain ES module, no build step.
// Everything that comes from the agent is rendered with textContent or
// createElement. Nothing from the agent is ever passed to innerHTML.

const TOKEN_KEY = "wombat.token";
const RETRY_MAX_MS = 15000;
const MAX_RESULT_CHARS = 20000;
const coarsePointer = matchMedia("(pointer: coarse)").matches;

const $ = (id) => document.getElementById(id);

const ui = {
  log: $("log"),
  input: $("input"),
  sendBtn: $("send-btn"),
  menuBtn: $("menu-btn"),
  commandsBtn: $("commands-btn"),
  title: $("title"),
  subtitle: $("subtitle"),
  titles: document.querySelector(".titles"),
  status: $("status"),
  toast: $("toast"),
  busy: $("busy"),
  contextFill: $("context-fill"),
  contextText: $("context-text"),
  todos: $("todos"),
  todosToggle: $("todos-toggle"),
  todosList: $("todos-list"),
  sheet: $("sheet"),
  backdrop: $("sheet-backdrop"),
  signin: $("signin"),
  signinForm: $("signin-form"),
  tokenInput: $("token-input"),
  signinError: $("signin-error"),
};

const st = {
  token: localStorage.getItem(TOKEN_KEY) || "",
  ws: null,
  open: false,
  everOpened: false,
  attempts: 0,
  delay: 1000,
  timer: 0,
  ended: false,
  info: null, // last state object from the bridge
  infoLine: "",
  commands: [],
  themes: [],
  thinking: true,
  streaming: false,
  missingKey: "",
  busyText: "",
  usage: { in: 0, out: 0, cached: 0, history: 0, window: 0 },
  todos: [],
  todosOpen: true,
  current: null, // streaming assistant entry
  running: [], // running tool entries
  stick: true,
  renderQueued: false,
  prompts: [], // queued permission/question requests
  activePrompt: null,
  sheetBlocking: false,
};

// ---------- DOM helpers ----------

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function button(label, className, onClick) {
  const b = el("button", className, label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

function clip(text, max) {
  const s = String(text || "");
  return s.length > max ? s.slice(0, max) + "\n… (truncated)" : s;
}

function formatMs(ms) {
  if (!ms) return "";
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

function formatCount(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n || 0);
}

function scrollIfSticky() {
  if (st.stick) ui.log.scrollTop = ui.log.scrollHeight;
}

function append(node) {
  ui.log.append(node);
  scrollIfSticky();
}

ui.log.addEventListener(
  "scroll",
  () => {
    st.stick = ui.log.scrollHeight - ui.log.scrollTop - ui.log.clientHeight < 80;
  },
  { passive: true },
);

// ---------- toast, busy line, header ----------

let toastTimer = 0;
function showToast(text, error) {
  ui.toast.textContent = text;
  ui.toast.className = "toast" + (error ? " error" : "");
  ui.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    ui.toast.hidden = true;
  }, error ? 4500 : 2500);
}

function setBusyText(text) {
  st.busyText = text || "";
  renderBusy();
}

function renderBusy() {
  let text = st.busyText;
  if (!text && st.missingKey) text = `no API key for ${st.missingKey} — menu → Provider`;
  if (!text && st.streaming) text = "working…";
  ui.busy.textContent = text;
  ui.busy.hidden = !text;
}

function setStatus(state) {
  ui.status.dataset.state = state;
  renderHeaderLine();
}

function renderHeaderLine() {
  if (st.open) ui.subtitle.textContent = st.infoLine || "connected";
  else if (st.ended) ui.subtitle.textContent = "ended · tap to reopen";
  else if (!st.token) ui.subtitle.textContent = "not signed in";
  else if (st.everOpened) ui.subtitle.textContent = "disconnected · reconnecting…";
  else ui.subtitle.textContent = "connecting…";
}

function renderSendButton() {
  ui.sendBtn.dataset.mode = st.streaming ? "stop" : "send";
  ui.sendBtn.textContent = st.streaming ? "Stop" : "Send";
  ui.sendBtn.setAttribute("aria-label", st.streaming ? "stop" : "send");
}

function renderContext() {
  const u = st.usage;
  const pct = u.window > 0 ? Math.min(100, (u.history / u.window) * 100) : 0;
  ui.contextFill.style.width = pct.toFixed(1) + "%";
  ui.contextText.textContent = u.window > 0
    ? `${formatCount(u.history)} / ${formatCount(u.window)} · in ${formatCount(u.in)} out ${formatCount(u.out)}`
    : `in ${formatCount(u.in)} out ${formatCount(u.out)}`;
}

// ---------- connection ----------

function send(message) {
  if (!st.ws || st.ws.readyState !== WebSocket.OPEN) {
    showToast("not connected", true);
    return false;
  }
  st.ws.send(JSON.stringify(message));
  return true;
}

function connect() {
  clearTimeout(st.timer);
  st.timer = 0;
  st.ended = false;
  if (!st.token) {
    showSignin("");
    return;
  }
  if (st.ws) {
    const old = st.ws;
    st.ws = null;
    old.close();
  }
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams({ token: st.token });
  if (st.info && st.info.session) params.set("session", st.info.session);
  const ws = new WebSocket(`${proto}//${location.host}/ws?${params.toString()}`);
  st.ws = ws;
  setStatus("connecting");
  ws.onopen = () => {
    if (st.ws !== ws) return;
    st.open = true;
    st.everOpened = true;
    st.attempts = 0;
    st.delay = 1000;
    setStatus("open");
  };
  ws.onmessage = (ev) => {
    if (st.ws !== ws) return;
    let msg;
    try {
      msg = JSON.parse(ev.data);
    } catch {
      return;
    }
    handle(msg);
  };
  ws.onclose = () => {
    if (st.ws !== ws) return;
    st.ws = null;
    st.open = false;
    onDisconnect();
  };
}

function onDisconnect() {
  setStatus("closed");
  st.streaming = false;
  st.current = null;
  st.running = [];
  setBusyText("");
  renderSendButton();
  dropPrompts();
  if (st.ended || !st.token) return;
  st.attempts++;
  if (!st.everOpened && st.attempts >= 3) {
    showSignin("Could not connect. Check the token, and that wombat serve is still running.");
    return;
  }
  st.timer = setTimeout(connect, st.delay);
  st.delay = Math.min(st.delay * 2, RETRY_MAX_MS);
}

function reconnectNow() {
  clearTimeout(st.timer);
  st.attempts = 0;
  st.delay = 1000;
  connect();
}

function endSession() {
  st.ended = true;
  if (st.ws) {
    const ws = st.ws;
    st.ws = null;
    ws.close();
  }
  st.open = false;
  setStatus("closed");
  dropPrompts();
}

// ---------- sign in ----------

function showSignin(errorText) {
  ui.signin.hidden = false;
  ui.signinError.hidden = !errorText;
  ui.signinError.textContent = errorText || "";
  ui.tokenInput.value = "";
  setTimeout(() => ui.tokenInput.focus(), 50);
}

function hideSignin() {
  ui.signin.hidden = true;
}

ui.signinForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const token = ui.tokenInput.value.trim();
  if (!token) return;
  st.token = token;
  localStorage.setItem(TOKEN_KEY, token);
  st.everOpened = false;
  st.info = null;
  hideSignin();
  reconnectNow();
});

function initToken() {
  const m = /(?:^|[#&])token=([^&]+)/.exec(location.hash);
  if (m) {
    st.token = decodeURIComponent(m[1]);
    localStorage.setItem(TOKEN_KEY, st.token);
    history.replaceState(null, "", location.pathname + location.search);
  }
}

// ---------- incoming events ----------

function handle(msg) {
  switch (msg.type) {
    case "ready":
      st.commands = msg.commands || [];
      st.themes = msg.themes || [];
      applyState(msg.state);
      applyTheme(msg.state && msg.state.theme);
      clearLog();
      renderHistory(msg.history || []);
      setTodos(msg.todos || []);
      setBusyText("");
      break;
    case "state":
      applyState(msg.state);
      break;
    case "user":
      appendUser(msg.text, msg.attachments);
      break;
    case "assistant_start":
      startAssistant();
      break;
    case "text_delta":
      ensureAssistant().text += msg.text || "";
      requestRender();
      break;
    case "reasoning_delta":
      ensureAssistant().reasoningText += msg.text || "";
      requestRender();
      break;
    case "assistant_reset":
      if (st.current) {
        st.current.text = "";
        st.current.reasoning = "";
        renderAssistant(st.current);
      }
      break;
    case "assistant_end":
      finishAssistant();
      setBusyText("");
      break;
    case "tool_start":
      startTool(msg.name, msg.detail);
      break;
    case "tool_end":
      endTool(msg.name, msg.result, msg.ok !== false, msg.duration_ms);
      break;
    case "note":
      setBusyText("");
      appendNote(msg.text, !!msg.error);
      break;
    case "clear":
      clearLog();
      break;
    case "history":
      clearLog();
      renderHistory(msg.items || []);
      break;
    case "usage":
      st.usage = {
        in: msg.in || 0,
        out: msg.out || 0,
        cached: msg.cached || 0,
        history: msg.history || 0,
        window: msg.window || 0,
      };
      renderContext();
      break;
    case "todos":
      setTodos(msg.items || []);
      break;
    case "permission":
      enqueuePrompt("permission", msg);
      break;
    case "question":
      enqueuePrompt("question", msg);
      break;
    case "list":
      setBusyText("");
      openList(msg);
      break;
    case "text":
      setBusyText("");
      openText(msg.title || "", msg.body || "");
      break;
    case "form":
      setBusyText("");
      openForm(msg);
      break;
    case "picker":
      setBusyText("");
      if (msg.kind === "theme") openThemes(msg.value);
      break;
    case "theme":
      applyTheme(msg.name);
      break;
    case "loading":
      setBusyText(msg.message || "");
      break;
    case "retry":
      setBusyText(msg.message || "retrying");
      break;
    case "toast":
      setBusyText("");
      showToast(msg.message || "", !!msg.error);
      break;
    case "exit":
      endSession();
      break;
    case "setup":
      appendNote("run `wombat setup` on this computer to add a provider", true);
      break;
    default:
      break;
  }
}

function applyState(s) {
  if (!s) return;
  st.info = s;
  st.thinking = !!s.thinking;
  st.streaming = !!s.streaming;
  st.missingKey = s.missing_key || "";
  ui.title.textContent = s.title || "New session";
  const model = s.provider && s.model ? `${s.provider} · ${s.model}` : s.model || s.provider || "";
  st.infoLine = [model, s.mode, s.branch ? `⎇ ${s.branch}` : ""].filter(Boolean).join("  ·  ");
  if (s.window > 0) st.usage.window = s.window;
  renderHeaderLine();
  renderBusy();
  renderSendButton();
  renderContext();
}

// ---------- transcript ----------

function clearLog() {
  ui.log.replaceChildren();
  st.current = null;
  st.running = [];
}

function appendUser(text, attachments) {
  const wrap = el("div", "entry entry-user", text || "");
  if (attachments && attachments.length) {
    wrap.append(el("div", "attachments", attachments.map((a) => `${a.name} (${a.kind})`).join(" · ")));
  }
  st.stick = true;
  append(wrap);
}

function startAssistant() {
  finishAssistant();
  const wrap = el("div", "entry entry-assistant");
  const reasoning = el("div", "reasoning");
  const body = el("div", "md");
  wrap.hidden = true;
  wrap.append(reasoning, body);
  append(wrap);
  st.current = { wrap, reasoning, body, text: "", reasoningText: "" };
  return st.current;
}

function ensureAssistant() {
  return st.current || startAssistant();
}

function finishAssistant() {
  if (!st.current) return;
  renderAssistant(st.current);
  st.current = null;
}

function renderAssistant(cur) {
  cur.body.replaceChildren();
  markdown(cur.body, cur.text);
  const showReasoning = st.thinking && cur.reasoningText.trim() !== "";
  cur.reasoning.hidden = !showReasoning;
  cur.reasoning.textContent = showReasoning ? cur.reasoningText : "";
  cur.wrap.hidden = cur.text.trim() === "" && !showReasoning;
}

// Coalesce streaming updates to one render per animation frame.
function requestRender() {
  if (st.renderQueued) return;
  st.renderQueued = true;
  requestAnimationFrame(() => {
    st.renderQueued = false;
    if (st.current) {
      renderAssistant(st.current);
      scrollIfSticky();
    }
  });
}

function buildTool(name, detail) {
  const node = el("details", "entry tool running");
  const summary = el("summary");
  summary.append(
    el("span", "tool-dot"),
    el("span", "tool-name", name || "tool"),
    el("span", "tool-detail", detail || ""),
    el("span", "tool-ms", ""),
  );
  node.append(summary);
  return node;
}

function startTool(name, detail) {
  finishAssistant();
  const node = buildTool(name, detail);
  append(node);
  st.running.push({ node, name });
}

function endTool(name, result, ok, durationMs) {
  let node = null;
  for (let i = st.running.length - 1; i >= 0; i--) {
    if (st.running[i].name === name) {
      node = st.running[i].node;
      st.running.splice(i, 1);
      break;
    }
  }
  if (!node) {
    node = buildTool(name, "");
    append(node);
  }
  fillTool(node, result, ok, durationMs);
}

function fillTool(node, result, ok, durationMs) {
  node.classList.remove("running");
  node.classList.add(ok ? "ok" : "fail");
  node.querySelector(".tool-ms").textContent = formatMs(durationMs);
  if (result) node.append(el("pre", "tool-result", clip(result, MAX_RESULT_CHARS)));
}

function appendNote(text, error) {
  finishAssistant();
  append(el("div", "entry entry-note" + (error ? " error" : ""), text || ""));
}

function renderHistory(items) {
  st.stick = true;
  for (const item of items) {
    switch (item.kind) {
      case "user":
        appendUser(item.text, item.attachments);
        break;
      case "assistant": {
        const cur = startAssistant();
        cur.text = item.text || "";
        cur.reasoningText = item.reasoning || "";
        renderAssistant(cur);
        break;
      }
      case "tool": {
        const node = buildTool(item.name, item.detail);
        append(node);
        fillTool(node, item.result, item.ok !== false, item.duration_ms);
        break;
      }
      case "note":
        append(el("div", "entry entry-note", item.text || ""));
        break;
      default:
        break;
    }
  }
  finishAssistant();
}

// ---------- minimal, safe markdown ----------

function inline(parent, text) {
  const pattern = /`([^`\n]+)`|\*\*([^*\n]+)\*\*|\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g;
  let last = 0;
  let m;
  while ((m = pattern.exec(text)) !== null) {
    if (m.index > last) parent.append(text.slice(last, m.index));
    if (m[1] !== undefined) {
      parent.append(el("code", null, m[1]));
    } else if (m[2] !== undefined) {
      parent.append(el("strong", null, m[2]));
    } else {
      const a = el("a", null, m[3]);
      a.href = m[4];
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      parent.append(a);
    }
    last = pattern.lastIndex;
  }
  if (last < text.length) parent.append(text.slice(last));
}

function markdown(container, text) {
  const lines = String(text || "").replace(/\r\n?/g, "\n").split("\n");
  let para = [];
  let list = null;
  const flushPara = () => {
    if (para.length === 0) return;
    const p = el("p");
    inline(p, para.join("\n"));
    container.append(p);
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^```/.test(line)) {
      flushPara();
      list = null;
      const code = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      const pre = el("pre");
      pre.append(el("code", null, code.join("\n")));
      container.append(pre);
      continue;
    }
    const heading = /^#{1,6}\s+(.*)$/.exec(line);
    if (heading) {
      flushPara();
      list = null;
      const h = el("h4");
      inline(h, heading[1]);
      container.append(h);
      continue;
    }
    const item = /^\s*([-*]|\d+[.)])\s+(.*)$/.exec(line);
    if (item) {
      flushPara();
      const tag = /\d/.test(item[1]) ? "ol" : "ul";
      if (!list || list.tag !== tag) {
        list = { tag, node: el(tag) };
        container.append(list.node);
      }
      const li = el("li");
      inline(li, item[2]);
      list.node.append(li);
      continue;
    }
    if (line.trim() === "") {
      flushPara();
      list = null;
      continue;
    }
    list = null;
    para.push(line);
  }
  flushPara();
}

// ---------- todos ----------

const TODO_MARKS = { pending: "○", in_progress: "◉", completed: "✓", cancelled: "×" };

function setTodos(items) {
  st.todos = items;
  ui.todos.hidden = items.length === 0;
  const done = items.filter((t) => t.status === "completed").length;
  ui.todosToggle.textContent = `${st.todosOpen ? "▾" : "▸"} todos ${done}/${items.length}`;
  ui.todosList.hidden = !st.todosOpen;
  ui.todosList.replaceChildren();
  for (const t of items) {
    const li = el("li", t.status);
    li.append(el("span", "mark", TODO_MARKS[t.status] || "·"), el("span", null, t.content || ""));
    ui.todosList.append(li);
  }
}

ui.todosToggle.addEventListener("click", () => {
  st.todosOpen = !st.todosOpen;
  setTodos(st.todos);
});

// ---------- theme ----------

const THEME_VARS = {
  ink: "--bg",
  surface: "--surface",
  panel: "--panel",
  border: "--border",
  text: "--text",
  dim: "--dim",
  faint: "--faint",
  accent: "--accent",
  accent2: "--accent2",
  green: "--ok",
  red: "--err",
  warn: "--warn",
  link: "--link",
  code: "--code-fg",
};

function applyTheme(name) {
  const theme = st.themes.find((t) => t.name === name);
  if (!theme || !theme.colors) return;
  for (const [key, value] of Object.entries(theme.colors)) {
    const cssVar = THEME_VARS[key];
    if (cssVar && value) document.documentElement.style.setProperty(cssVar, value);
  }
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta && theme.colors.ink) meta.setAttribute("content", theme.colors.ink);
}

// ---------- sheets ----------

function openSheet(children, blocking) {
  ui.sheet.replaceChildren(...children);
  ui.sheet.hidden = false;
  ui.backdrop.hidden = false;
  ui.sheet.scrollTop = 0;
  st.sheetBlocking = !!blocking;
}

function closeSheet() {
  ui.sheet.hidden = true;
  ui.backdrop.hidden = true;
  ui.sheet.replaceChildren();
  st.sheetBlocking = false;
}

ui.backdrop.addEventListener("click", () => {
  if (!st.sheetBlocking) closeSheet();
});

function cancelButton() {
  const row = el("div", "sheet-actions");
  row.append(button("Close", "", closeSheet));
  return row;
}

// Permission and question requests block the agent until answered, so they
// queue and show one at a time. They are never dismissed by tapping outside.
function enqueuePrompt(kind, msg) {
  st.prompts.push({ kind, msg });
  if (!st.sheetBlocking) showNextPrompt();
}

function showNextPrompt() {
  const next = st.prompts.shift();
  if (!next) return;
  st.activePrompt = next;
  if (next.kind === "permission") renderPermission(next.msg);
  else renderQuestion(next.msg);
}

function promptDone() {
  st.activePrompt = null;
  closeSheet();
  showNextPrompt();
}

function dropPrompts() {
  st.prompts = [];
  if (st.activePrompt) {
    st.activePrompt = null;
    closeSheet();
  }
}

function renderPermission(msg) {
  const children = [el("h2", null, `Approve ${msg.kind || "action"}?`)];
  if (msg.perm) children.push(el("p", "hint", `permission: ${msg.perm}`));
  if (msg.detail) children.push(el("pre", null, msg.detail));
  if (msg.preview) children.push(el("pre", null, msg.preview));
  const note = el("textarea");
  note.rows = 2;
  note.placeholder = "optional note for the agent (sent with Deny)";
  const actions = el("div", "sheet-actions");
  actions.append(
    button("Allow", "allow", () => answerPermission(true, false, "")),
    button("Always", "always", () => answerPermission(true, true, "")),
    button("Deny", "deny", () => answerPermission(false, false, note.value.trim())),
  );
  children.push(note, actions);
  openSheet(children, true);
}

function answerPermission(allow, always, feedback) {
  const req = st.activePrompt && st.activePrompt.msg;
  if (!req) return;
  send({ type: "permission", id: req.id, allow, always, feedback });
  promptDone();
}

function renderQuestion(msg) {
  const children = [];
  if (msg.header) children.push(el("p", "hint", msg.header));
  children.push(el("h2", null, msg.question || "Question"));
  const options = msg.options || [];
  if (options.length) {
    const list = el("ul", "sheet-list");
    for (const opt of options) {
      const item = button(opt.label, "", () => answerQuestion(opt.label));
      if (opt.description) item.append(el("span", "detail", opt.description));
      const li = el("li");
      li.append(item);
      list.append(li);
    }
    children.push(list);
  }
  const input = el("textarea");
  input.rows = 2;
  input.placeholder = options.length ? "or type your own answer" : "your answer";
  const actions = el("div", "sheet-actions");
  actions.append(
    button("Send", "allow", () => {
      const v = input.value.trim();
      if (v) answerQuestion(v);
    }),
    button("Skip", "", () => answerQuestion("")),
  );
  children.push(input, actions);
  openSheet(children, true);
}

function answerQuestion(answer) {
  const req = st.activePrompt && st.activePrompt.msg;
  if (!req) return;
  send({ type: "question", id: req.id, answer });
  promptDone();
}

function openList(msg) {
  const children = [el("h2", null, msg.title || "list")];
  if (msg.subtitle) children.push(el("p", "hint", msg.subtitle));
  const items = msg.items || [];
  const list = el("ul", "sheet-list");
  if (items.length === 0) list.append(el("li", "hint", "nothing here"));
  for (const item of items) {
    const label = `${msg.multi && item.current ? "✓ " : ""}${item.icon ? item.icon + "  " : ""}${item.label}`;
    const detail = [item.detail, item.configured ? "configured" : ""].filter(Boolean).join(" · ");
    const b = button(label, item.current ? "current" : "", () => {
      if (msg.action) {
        send({ type: "select", action: msg.action, value: item.value });
        if (!msg.multi) closeSheet();
      } else {
        closeSheet();
      }
    });
    if (detail) b.append(el("span", "detail", detail));
    const li = el("li");
    li.append(b);
    list.append(li);
  }
  children.push(list, cancelButton());
  openSheet(children, false);
}

function openText(title, body) {
  const pre = el("pre", null, body);
  openSheet([el("h2", null, title || "info"), pre, cancelButton()], false);
}

function openForm(msg) {
  const inputs = {};
  const form = el("form", "form");
  for (const f of msg.fields || []) {
    const input = el("input");
    input.type = f.secret ? "password" : "text";
    input.name = f.name;
    input.value = f.value || "";
    input.autocomplete = "off";
    input.autocapitalize = "off";
    input.spellcheck = false;
    inputs[f.name] = input;
    form.append(el("label", null, f.label || f.name), input);
  }
  if (msg.error) form.append(el("p", "error", msg.error));
  const actions = el("div", "sheet-actions");
  const save = el("button", "primary", "Save");
  save.type = "submit";
  actions.append(save, button("Cancel", "", closeSheet));
  form.append(actions);
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const values = {};
    for (const [name, input] of Object.entries(inputs)) values[name] = input.value;
    send({ type: "form_submit", action: msg.action, values, meta: msg.meta || {} });
    closeSheet();
  });
  openSheet([el("h2", null, msg.title || "form"), form], false);
}

function openThemes(current) {
  const list = el("ul", "sheet-list");
  for (const theme of st.themes) {
    const b = button(theme.label || theme.name, theme.name === current ? "current" : "", () => {
      send({ type: "command", line: `/theme ${theme.name}` });
      applyTheme(theme.name);
      closeSheet();
    });
    if (theme.blurb) b.append(el("span", "detail", theme.blurb));
    const li = el("li");
    li.append(b);
    list.append(li);
  }
  openSheet([el("h2", null, "themes"), list, cancelButton()], false);
}

// ---------- commands and menu ----------

const MENU_ITEMS = [
  ["New session", "/new"],
  ["Sessions", "/sessions"],
  ["Models", "/models"],
  ["Provider", "/provider"],
  ["Mode", "/mode"],
  ["Reasoning", "/reasoning"],
  ["Agents", "/agents"],
  ["Previews", "/previews"],
  ["References", "/reference"],
  ["Todos", "/todos"],
  ["Status", "/status"],
  ["Cost", "/cost"],
  ["Compact context", "/compact"],
  ["Auto-approve (yolo)", "/yolo"],
  ["Theme", "/theme"],
  ["Doctor", "/doctor"],
  ["Help", "/help"],
];

function runCommand(line) {
  closeSheet();
  send({ type: "command", line });
}

function showMenu() {
  const list = el("ul", "sheet-list");
  const add = (label, onClick) => {
    const li = el("li");
    li.append(button(label, "", onClick));
    list.append(li);
  };
  add(`Toggle thinking (now ${st.thinking ? "on" : "off"})`, () => runCommand(`/thinking ${st.thinking ? "off" : "on"}`));
  for (const [label, line] of MENU_ITEMS) add(label, () => runCommand(line));
  add("All commands…", () => showCommands(""));
  add("Change token", () => {
    closeSheet();
    showSignin("");
  });
  add("Disconnect", () => {
    closeSheet();
    endSession();
  });
  openSheet([el("h2", null, "menu"), list, cancelButton()], false);
}

function showCommands(prefix) {
  const filter = el("input");
  filter.type = "text";
  filter.placeholder = "filter commands";
  filter.value = prefix || "";
  const list = el("ul", "sheet-list");
  const render = () => {
    list.replaceChildren();
    const q = filter.value.trim().toLowerCase();
    for (const c of st.commands) {
      if (q && !`${c.name} ${c.summary || ""}`.toLowerCase().includes(q)) continue;
      const b = button(c.name + (c.args ? " " + c.args : ""), "", () => pickCommand(c));
      if (c.summary) b.append(el("span", "detail", c.summary));
      const li = el("li");
      li.append(b);
      list.append(li);
    }
  };
  filter.addEventListener("input", render);
  render();
  openSheet([el("h2", null, "commands"), filter, list, cancelButton()], false);
  setTimeout(() => filter.focus(), 50);
}

function pickCommand(c) {
  if (c.options && c.options.length) {
    const list = el("ul", "sheet-list");
    for (const opt of c.options) {
      const b = button(opt.value, "", () => runCommand(`${c.name} ${opt.value}`));
      if (opt.summary) b.append(el("span", "detail", opt.summary));
      const li = el("li");
      li.append(b);
      list.append(li);
    }
    openSheet([el("h2", null, c.name), list, button("Back", "", () => showCommands(""))], false);
    return;
  }
  if (c.args) {
    closeSheet();
    ui.input.value = `${c.name} `;
    autosize();
    ui.input.focus();
    return;
  }
  runCommand(c.name);
}

// ---------- composer ----------

function autosize() {
  ui.input.style.height = "auto";
  ui.input.style.height = Math.min(ui.input.scrollHeight, window.innerHeight * 0.4) + "px";
}

function submitInput() {
  const text = ui.input.value.trim();
  if (!text) return;
  if (!st.open) {
    showToast("not connected", true);
    return;
  }
  if (st.streaming && !text.startsWith("/")) {
    showToast("a run is in progress — stop it first", true);
    return;
  }
  if (text.startsWith("/")) send({ type: "command", line: text });
  else send({ type: "submit", text });
  st.stick = true;
  ui.input.value = "";
  autosize();
}

ui.input.addEventListener("input", autosize);
ui.input.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !coarsePointer) {
    e.preventDefault();
    submitInput();
  }
});
ui.input.addEventListener("focus", () => {
  setTimeout(() => {
    st.stick = true;
    scrollIfSticky();
  }, 300);
});

ui.sendBtn.addEventListener("click", () => {
  if (st.streaming) send({ type: "interrupt" });
  else submitInput();
});
ui.menuBtn.addEventListener("click", showMenu);
ui.commandsBtn.addEventListener("click", () => showCommands(""));
ui.titles.addEventListener("click", () => {
  if (!st.open && st.token && ui.signin.hidden) reconnectNow();
});

// Phones suspend background tabs and drop sockets; reconnect when visible.
document.addEventListener("visibilitychange", () => {
  if (document.hidden || !st.token || st.ended) return;
  if (!ui.signin.hidden) return;
  if (st.ws && st.ws.readyState <= WebSocket.OPEN) return;
  reconnectNow();
});

// ---------- start ----------

initToken();
renderSendButton();
renderContext();
if (st.token) connect();
else showSignin("");
