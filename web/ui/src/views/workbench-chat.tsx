import { createContext, memo, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Ic } from "../ui/icons";
import { Markdown, type PathLinker } from "../lib/markdown";
import { matchLinksInLine, type LinkMatch } from "./workbench-links";
import { errorMessage } from "../api/client";
import { previewWorkbenchFile, type WorkbenchSession } from "../api/workbench";
import {
  chatImageURL,
  chatKeys,
  getChat,
  mergeItems,
  subscribeChat,
  type ChatItem,
  type ChatLive,
  type ChatMeta,
  type ChatUpdate,
} from "../api/workbench-chat";
import { Composer, modelLabel } from "./workbench-chat-composer";
import { AgentLogo } from "./agent-logo";
import { FallbackCard, PermissionCard, PlanCard, QuestionCard, ScreenQuestionCard, type CardActions } from "./workbench-chat-cards";
import { BackgroundTray, SubagentRow, TasksContext } from "./workbench-chat-tasks";

/* Spec 71: the Claude Code chat view of a Workbench pane. It reads the
   daemon's chat stream (the transcript plus a mirror of the screen) and is
   built from plain elements (owner decision 3). */

export interface ChatPaneProps {
  sessionId: string;
  session?: WorkbenchSession;
  /* The view is showing; the stream only runs while it is. */
  active: boolean;
  readOnly: boolean;
  compact: boolean;
  onOpenTerminal: () => void;
  onOpenFiles?: () => void;
  /* Opens a path in the Files panel, as ⌘+click does in the terminal. */
  onOpenFile?: (link: string, fallback?: string) => void;
}

interface Pending {
  key: string;
  text: string;
  images: string[];
  queued: boolean;
}

type Block =
  | { type: "user"; item: ChatItem }
  | { type: "command"; item: ChatItem }
  | { type: "output"; item: ChatItem }
  | { type: "head"; key: string; model: string; at: string }
  | { type: "text"; item: ChatItem }
  | { type: "thinking"; item: ChatItem }
  | { type: "tools"; key: string; items: ChatItem[] }
  | { type: "question"; item: ChatItem }
  | { type: "plan"; item: ChatItem }
  | { type: "divider"; item: ChatItem }
  | { type: "turn_end"; item: ChatItem; text: string };

/* AskUserQuestion results read `…"Question?"="Answer"…`; show the answers. */
export function answerText(raw: string): string {
  const picks = [...raw.matchAll(/"[^"]*"="([^"]*)"/g)].map((m) => m[1]);
  if (picks.length) return picks.join(" · ");
  return raw.replace(/^User has answered[^:]*:\s*/i, "").slice(0, 160);
}

/* One row per question: the question and its answer. */
export function answerPairs(item: ChatItem): [string, string][] {
  const raw = item.answer || "";
  const pairs = [...raw.matchAll(/"([^"]*)"="([^"]*)"/g)].map((m) => [m[1]!, m[2]!] as [string, string]);
  if (pairs.length) return pairs;
  const q = item.questions?.[0]?.question || "Question";
  return [[q, raw ? answerText(raw) : ""]];
}

function hhmm(at?: string): string {
  if (!at) return "";
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

function duration(ms: number): string {
  const s = Math.round(ms / 1000);
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m " + (s % 60) + "s";
  return Math.floor(m / 60) + "h " + (m % 60) + "m";
}

function tokens(n: number): string {
  return n >= 1000 ? (n / 1000).toFixed(1) + "k tokens" : n + " tokens";
}

/* Claude Code can write the same tool_use twice (the second time with an
   empty input). Keep one item per tool_use_id: the first position, the
   richest input and the latest status and result. */
export function dedupeTools(items: ChatItem[]): ChatItem[] {
  const at = new Map<string, number>();
  const out: ChatItem[] = [];
  for (const item of items) {
    const key = item.tool_use_id;
    if (!key || (item.kind !== "tool" && item.kind !== "plan" && item.kind !== "question")) {
      out.push(item);
      continue;
    }
    const i = at.get(key);
    if (i === undefined) {
      at.set(key, out.length);
      out.push(item);
      continue;
    }
    const prev = out[i]!;
    const richer = (x: ChatItem) => JSON.stringify(x.input ?? {}).length + (x.plan?.length ?? 0) + (x.questions?.length ?? 0);
    const base = richer(item) > richer(prev) ? item : prev;
    out[i] = {
      ...base,
      id: prev.id,
      status: item.status && item.status !== "running" ? item.status : prev.status,
      result: item.result ?? prev.result,
      state: item.state && item.state !== "pending" ? item.state : prev.state,
      answered: item.answered || prev.answered,
      answer: item.answer || prev.answer,
      agent: item.agent ?? prev.agent,
    };
  }
  return out;
}

/* ExitPlanMode's input can lag behind the plan file Claude just wrote (Claude
   Code shows the file). Use the content of the last Write to a plans/ file
   before the plan item, when there is one. */
export function planTextFor(items: ChatItem[], plan: ChatItem): string {
  const at = items.findIndex((i) => i.id === plan.id);
  for (let i = at - 1; i >= 0; i--) {
    const it = items[i]!;
    if (it.kind === "plan") break;
    const path = typeof it.input?.file_path === "string" ? (it.input.file_path as string) : "";
    if (it.kind === "tool" && it.name === "Write" && /\/plans\/[^/]+\.md$/.test(path)) {
      const content = typeof it.input?.content === "string" ? (it.input.content as string) : it.diff?.new || "";
      if (content.trim()) return content;
    }
  }
  return "";
}

/* A block that shows nothing once the turn moves on: thinking, or an
   assistant line with no text. */
function isQuiet(b: Block): boolean {
  return b.type === "thinking" || (b.type === "text" && !(b.item.text || "").trim());
}

/* Groups the transcript items into what the chat shows (REQ-WC-052). */
export function toBlocks(items: ChatItem[]): Block[] {
  const out: Block[] = [];
  let needHead = true;
  let texts: string[] = [];
  for (const item of items) {
    switch (item.kind) {
      case "user":
        /* Claude Code records an Esc as a user line; show it as a marker. */
        if (/^\[Request interrupted/.test(item.text || "") && !item.images?.length) {
          out.push({ type: "divider", item: { ...item, kind: "divider", text: "Interrupted" } });
        } else {
          out.push({ type: "user", item });
        }
        needHead = true;
        break;
      case "command":
      case "output":
        out.push({ type: item.kind, item } as Block);
        needHead = true;
        break;
      case "divider":
        out.push({ type: "divider", item });
        needHead = true;
        break;
      case "turn_end": {
        const parts = [];
        if (item.duration_ms) parts.push(duration(item.duration_ms));
        if (item.output_tokens) parts.push(tokens(item.output_tokens));
        out.push({ type: "turn_end", item, text: texts.join("\n\n") || parts.join(" · ") });
        (out[out.length - 1] as { text: string }).text = parts.join(" · ");
        texts = [];
        needHead = true;
        break;
      }
      default: {
        if (needHead) {
          out.push({ type: "head", key: "h:" + item.id, model: item.model || "", at: item.at || "" });
          needHead = false;
        }
        if (item.kind === "assistant") {
          texts.push(item.text || "");
          out.push({ type: "text", item });
        } else if (item.kind === "thinking") {
          out.push({ type: "thinking", item });
        } else if (item.kind === "question") {
          out.push({ type: "question", item });
        } else if (item.kind === "plan") {
          out.push({ type: "plan", item });
        } else if (item.kind === "tool") {
          /* Thinking right before a tool goes into the step list, so it does
             not split the group and stays readable there (owner, 2026-11-23).
             Empty assistant lines in between are dropped. */
          let back = out.length - 1;
          while (back >= 0 && isQuiet(out[back]!)) back--;
          const moved = out.splice(back + 1).flatMap((b) => (b.type === "thinking" && (b.item.text || "").trim() ? [b.item] : []));
          const last = out[out.length - 1];
          if (last && last.type === "tools") last.items.push(...moved, item);
          else out.push({ type: "tools", key: "g:" + (moved[0] ?? item).id, items: [...moved, item] });
        }
      }
    }
  }
  /* The head shows the first model of its turn. */
  for (let i = 0; i < out.length; i++) {
    const b = out[i]!;
    if (b.type === "head" && !b.model) {
      for (let j = i + 1; j < out.length; j++) {
        const n = out[j]!;
        if (n.type === "head") break;
        if (n.type === "text" && n.item.model) {
          b.model = n.item.model;
          break;
        }
      }
    }
  }
  return out;
}

const VERB_TONE: Record<string, string> = { Write: "green", Edit: "amber", MultiEdit: "amber", Bash: "blue", Read: "", Grep: "", Glob: "" };

function groupSummary(items: ChatItem[]): string {
  let created = 0;
  let edited = 0;
  let commands = 0;
  let agents = 0;
  let running = 0;
  for (const t of items) {
    if (isAgentTool(t)) agents++;
    else if (t.name === "Write") created++;
    else if (t.name === "Edit" || t.name === "MultiEdit" || t.name === "NotebookEdit") edited++;
    else if (t.name === "Bash") commands++;
    if (t.status === "running") running++;
  }
  if (running) return "running…";
  const parts = [];
  if (created) parts.push(created + " written");
  if (edited) parts.push(edited + " edited");
  if (commands) parts.push(commands + (commands === 1 ? " command" : " commands"));
  if (agents) parts.push(agents + (agents === 1 ? " subagent" : " subagents"));
  return parts.join(" · ");
}

function DiffView({ diff }: { diff: { old: string; new: string } }) {
  const old = diff.old ? diff.old.split("\n") : [];
  const next = diff.new ? diff.new.split("\n") : [];
  return (
    <pre className="wbc-out wbc-diff">
      {old.map((l, i) => (
        <span key={"o" + i} className="del">
          {"- " + l + "\n"}
        </span>
      ))}
      {next.map((l, i) => (
        <span key={"n" + i} className="add">
          {"+ " + l + "\n"}
        </span>
      ))}
    </pre>
  );
}

function isAgentTool(item: ChatItem): boolean {
  return !!item.agent || item.name === "Agent" || item.name === "Task";
}

/* File paths in chat text open the Files panel (owner, 2026-11-23: "like
   in the terminal"). Plain text only links paths with a slash, so version
   numbers and "e.g" stay text; an inline code span may also be a bare file
   name with a letter extension. */
export function chatPathMatches(text: string): LinkMatch[] {
  return matchLinksInLine(text).filter((m) => m.text.replace(/:\d+(?::\d+)?$/, "").includes("/"));
}

export function chatCodePath(text: string): string | null {
  const s = text.trim();
  if (!s || s.includes("\n")) return null;
  /* A code span that is one absolute or ~/ path may hold spaces. */
  if (/\s/.test(s)) return /^(\/|~\/)/.test(s) && !s.includes("  ") ? s : null;
  const m = matchLinksInLine(s);
  if (m.length !== 1 || m[0]!.start !== 0 || m[0]!.end !== s.length) return null;
  const base = m[0]!.text.replace(/:\d+(?::\d+)?$/, "");
  if (!base.includes("/") && !/\.[A-Za-z][A-Za-z0-9]{0,7}$/.test(base)) return null;
  return m[0]!.text;
}

const OpenFileContext = createContext<((link: string, fallback?: string) => void) | null>(null);

function PathLink({ path, short, children }: { path: string; short?: string; children: ReactNode }) {
  const open = useContext(OpenFileContext);
  if (!open) return <>{children}</>;
  return (
    <span
      role="link"
      tabIndex={0}
      className="wbc-path"
      title={"Open " + path + " in Files"}
      onClick={(e) => {
        e.stopPropagation();
        open(path, short);
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") open(path, short);
      }}
    >
      {children}
    </span>
  );
}

const PATH_LINKER: PathLinker = {
  match: chatPathMatches,
  code: chatCodePath,
  render: (path, node, key, short) => (
    <PathLink key={key} path={path} short={short}>
      {node}
    </PathLink>
  ),
};

/* Plain text with its paths linked. */
function LinkedText({ text }: { text: string }) {
  const out: ReactNode[] = [];
  let at = 0;
  for (const m of chatPathMatches(text)) {
    if (m.start > at) out.push(text.slice(at, m.start));
    out.push(
      <PathLink key={m.start} path={m.text} short={m.short}>
        {text.slice(m.start, m.end)}
      </PathLink>,
    );
    at = m.end;
  }
  if (at < text.length) out.push(text.slice(at));
  return <>{out}</>;
}

function ToolRow({ item }: { item: ChatItem }) {
  if (isAgentTool(item)) return <SubagentRow item={item} />;
  return <PlainToolRow item={item} />;
}

function PlainToolRow({ item: raw }: { item: ChatItem }) {
  const [open, setOpen] = useState(false);
  /* A background shell's row follows its task, not the launch result. */
  const { tasks } = useContext(TasksContext);
  const bg = raw.input?.run_in_background === true ? tasks.find((t) => t.tool_use_id === raw.tool_use_id) : undefined;
  const item: ChatItem = bg
    ? {
        ...raw,
        status: bg.status === "running" ? "running" : bg.status === "completed" ? "done" : "error",
        summary: { verb: raw.summary?.verb || "Bash", target: raw.summary?.target || "", meta: bg.status === "running" ? "background" : "background · " + bg.status },
      }
    : raw;
  const s = item.summary;
  const verb = s?.verb || item.name || "Tool";
  const has = !!(item.diff || item.result?.text);
  const openFile = useContext(OpenFileContext);
  const fileInput = item.input?.file_path ?? item.input?.notebook_path;
  const filePath = typeof fileInput === "string" && fileInput ? fileInput : "";
  return (
    <div className={"wbc-tool-row" + (open ? " is-open" : "")}>
      <div className="wbc-tool-head">
      <button type="button" className="wbc-tool-line" onClick={() => has && setOpen(!open)} aria-expanded={has ? open : undefined}>
        <span className={"wbc-tool-st st-" + (item.status || "running")}>
          {item.status === "done" ? <Ic name="check" size={13} /> : item.status === "error" ? <Ic name="x" size={13} /> : <Ic name="loader" size={13} />}
        </span>
        <span className={"wbc-verb tone-" + (VERB_TONE[verb] ?? "violet")}>{verb}</span>
        <span className="wbc-target">{s?.target || ""}</span>
        <span className="wb-spacer" />
        {s?.meta ? <span className={"wbc-meta" + (verb === "Write" ? " tone-green" : verb === "Edit" ? " tone-amber" : "")}>{s.meta}</span> : null}
        {has ? <Ic name={open ? "chevron-up" : "chevron-down"} size={12} /> : null}
      </button>
      {filePath && openFile ? (
        <button type="button" className="wbc-tool-open" aria-label={"Open " + filePath + " in Files"} data-tip="Open in Files" onClick={() => openFile(filePath)}>
          <Ic name="file-search" size={13} />
        </button>
      ) : null}
      </div>
      {open ? (
        item.diff ? (
          <DiffView diff={item.diff} />
        ) : (
          <pre className={"wbc-out" + (item.result?.is_error ? " is-error" : "")}>
            {item.result?.text}
            {item.result?.truncated ? "\n… " + item.result.lines + " lines" : ""}
          </pre>
        )
      ) : null}
    </div>
  );
}

export function ToolGroup({ items }: { items: ChatItem[] }) {
  /* Open while a step runs, folded once all are done (owner, 2026-11-23);
     a click overrides either way. */
  const tools = items.filter((i) => i.kind === "tool");
  const running = tools.some((i) => i.status === "running");
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = userOpen ?? running;
  const setOpen = (v: boolean) => setUserOpen(v);
  return (
    <div className="wbc-group">
      <button type="button" className="wbc-group-head" onClick={() => setOpen(!open)} aria-expanded={open}>
        <Ic name={open ? "chevron-down" : "chevron-right"} size={13} />
        <span className="wbc-group-title">
          {tools.length} {tools.length === 1 ? "step" : "steps"}
        </span>
        <span className="wbc-group-sub">{groupSummary(tools)}</span>
      </button>
      {open ? (
        <div className="wbc-group-rows">
          {items.map((t) => (t.kind === "thinking" ? <ThinkingRow key={t.id} item={t} /> : <ToolRow key={t.id} item={t} />))}
        </div>
      ) : null}
    </div>
  );
}

/* A thinking step inside a tool group: its first line, the rest on click. */
function ThinkingRow({ item }: { item: ChatItem }) {
  const [open, setOpen] = useState(false);
  const text = (item.text || "").trim();
  const first = text.split("\n").find((l) => l.trim()) || "";
  return (
    <div className={"wbc-tool-row wbc-think-row" + (open ? " is-open" : "")}>
      <div className="wbc-tool-head">
        <button type="button" className="wbc-tool-line" onClick={() => setOpen(!open)} aria-expanded={open}>
          <span className="wbc-tool-st">
            <Ic name="brain" size={13} />
          </span>
          <span className="wbc-verb">Thinking</span>
          <span className="wbc-target">{open ? "" : first}</span>
          <span className="wb-spacer" />
          <Ic name={open ? "chevron-up" : "chevron-down"} size={12} />
        </button>
      </div>
      {open ? <div className="wbc-think-text">{text}</div> : null}
    </div>
  );
}

function Thinking({ item }: { item: ChatItem }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="wbc-thinking">
      <button type="button" onClick={() => item.text && setOpen(!open)}>
        <Ic name={open ? "chevron-down" : "chevron-right"} size={12} />
        Thinking
      </button>
      {open ? <div className="wbc-thinking-text">{item.text}</div> : null}
    </div>
  );
}

/* Pasted text arrives in the transcript wrapped as
   <pasted_content id="…">…</pasted_content id="…">. */
const PASTED = /<pasted_content id="([^"]*)">\n?([\s\S]*?)\n?<[/\\]pasted_content id="[^"]*["”]>/g;

export type UserPart = { kind: "text"; text: string } | { kind: "pasted"; text: string };

export function userParts(text: string): UserPart[] {
  const parts: UserPart[] = [];
  let last = 0;
  for (const m of text.matchAll(PASTED)) {
    const before = text.slice(last, m.index).trim();
    if (before) parts.push({ kind: "text", text: before });
    parts.push({ kind: "pasted", text: m[2] ?? "" });
    last = (m.index ?? 0) + m[0].length;
  }
  const rest = text.slice(last).trim();
  if (rest) parts.push({ kind: "text", text: rest });
  /* A message that is one paste (multi-line text from the chat composer
     arrives this way too) reads as plain text. */
  if (parts.length === 1 && parts[0]!.kind === "pasted") return [{ kind: "text", text: parts[0]!.text }];
  return parts.length ? parts : [{ kind: "text", text }];
}

export function plainUserText(text: string): string {
  return userParts(text)
    .map((p) => p.text)
    .join("\n\n");
}

function PastedBlock({ text }: { text: string }) {
  const lines = text.split("\n").length;
  const long = lines > 6 || text.length > 600;
  const [open, setOpen] = useState(!long);
  return (
    <div className={"wbc-pasted" + (open ? " is-open" : "")}>
      <button type="button" className="wbc-pasted-head" onClick={() => long && setOpen(!open)} aria-expanded={open}>
        <Ic name="clipboard" size={12} />
        <span>Pasted text · {lines} {lines === 1 ? "line" : "lines"}</span>
        {long ? <Ic name={open ? "chevron-up" : "chevron-down"} size={12} /> : null}
      </button>
      <div className={"wbc-pasted-text" + (open ? "" : " is-clipped")}>
        <LinkedText text={text} />
      </div>
    </div>
  );
}

/* Images sent from the chat (a phone upload goes to the daemon's drops
   folder and reaches Claude as a path) show as images, not paths (owner,
   2026-11-23). */
const IMAGE_PATH = /\.(png|jpe?g|gif|webp|heic|bmp|svg)$/i;

export function splitImagePaths(text: string): { text: string; images: string[] } {
  const images: string[] = [];
  let out = "";
  let at = 0;
  for (const m of chatPathMatches(text)) {
    if (!m.text.startsWith("/") || !IMAGE_PATH.test(m.text)) continue;
    images.push(m.text);
    out += text.slice(at, m.start);
    at = m.end;
  }
  out += text.slice(at);
  if (!images.length) return { text, images };
  /* Drop the quotes left around a removed path and the blank runs. */
  const cleaned = out
    .replace(/(['"])[ \t]*\1/g, "")
    .replace(/[ \t]{2,}/g, " ")
    .replace(/[ \t]+$/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
  return { text: cleaned, images };
}

const previewCache = new Map<string, Promise<string>>();

function PathImage({ path }: { path: string }) {
  const { sessionId } = useContext(TasksContext);
  const open = useContext(OpenFileContext);
  const [src, setSrc] = useState("");
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let alive = true;
    const key = sessionId + "\u0000" + path;
    let p = previewCache.get(key);
    if (!p) {
      p = previewWorkbenchFile(sessionId, path).then((d) => d.raw_url || d.url);
      previewCache.set(key, p);
      p.catch(() => previewCache.delete(key));
    }
    p.then((u) => alive && setSrc(u)).catch(() => alive && setFailed(true));
    return () => {
      alive = false;
    };
  }, [sessionId, path]);
  const name = path.replace(/^.*\//, "");
  if (failed) return <span className="wbc-img-missing">{name}</span>;
  return (
    <button type="button" className="wbc-img-thumb" title={name} onClick={() => open?.(path)}>
      {src ? <img src={src} alt={name} loading="lazy" /> : <span className="wbc-img-wait" />}
    </button>
  );
}

function UserText({ text, className }: { text: string; className?: string }) {
  const split = splitImagePaths(text);
  const parts = split.text ? userParts(split.text) : [];
  return (
    <>
      {split.images.length ? (
        <div className="wbc-user-images">
          {split.images.map((p) => (
            <PathImage key={p} path={p} />
          ))}
        </div>
      ) : null}
      {parts.length ? <UserBubble parts={parts} className={className} /> : null}
    </>
  );
}

function UserBubble({ parts, className }: { parts: UserPart[]; className?: string }) {
  return (
    <div className={"wbc-bubble" + (className ? " " + className : "")}>
      {parts.map((p, i) =>
        p.kind === "pasted" ? (
          <PastedBlock key={i} text={p.text} />
        ) : (
          <span key={i} className="wbc-bubble-text">
            <LinkedText text={p.text} />
          </span>
        ),
      )}
    </div>
  );
}

function CopyButton({ text, label = "Copy reply" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  if (!text) return null;
  return (
    <button
      type="button"
      className="wbc-copy"
      aria-label={label}
      data-tip={done ? "Copied" : label}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          window.setTimeout(() => setDone(false), 1200);
        });
      }}
    >
      <Ic name={done ? "check" : "copy"} size={13} />
    </button>
  );
}

const BlockView = memo(function BlockView({ block, sessionId, replyText }: { block: Block; sessionId: string; replyText: string }) {
  switch (block.type) {
    case "user": {
      const it = block.item;
      return (
        <div className="wbc-user">
          {it.images?.length ? (
            <div className="wbc-user-images">
              {it.images.map((img) => {
                const [line, idx] = img.ref.split(/:(?=[^:]*$)/);
                return <img key={img.ref} src={chatImageURL(sessionId, line || img.ref, Number(idx || 0))} alt="" loading="lazy" />;
              })}
            </div>
          ) : null}
          {it.text ? <UserText text={it.text} /> : null}
          {it.text ? (
            <div className="wbc-msg-actions">
              <CopyButton text={plainUserText(it.text)} label="Copy message" />
            </div>
          ) : null}
        </div>
      );
    }
    case "command":
      return (
        <div className="wbc-user">
          <div className="wbc-bubble is-command">
            {block.item.name}
            {block.item.args ? " " + block.item.args : ""}
          </div>
        </div>
      );
    case "output":
      return block.item.text ? <pre className="wbc-out wbc-local">{block.item.text}</pre> : null;
    case "head":
      return (
        <div className="wbc-head">
          <span className="wbc-head-tile">
            <AgentLogo agent="claude" size={18} />
          </span>
          <span className="wbc-head-name">Claude</span>
          <span className="wbc-head-meta">
            {[block.model ? modelLabel(block.model) : "", hhmm(block.at)].filter(Boolean).join(" · ")}
          </span>
        </div>
      );
    case "text":
      return (
        <div className="wbc-text">
          <Markdown text={block.item.text} pathLink={PATH_LINKER} />
          <div className="wbc-msg-actions">
            <CopyButton text={block.item.text || ""} label="Copy reply" />
          </div>
        </div>
      );
    case "thinking":
      return <Thinking item={block.item} />;
    case "tools":
      return <ToolGroup items={block.items} />;
    case "divider":
      return (
        <div className="wbc-divider">
          <span>{block.item.text}</span>
        </div>
      );
    case "turn_end":
      return (
        <div className="wbc-turn-end">
          <span>{block.text}</span>
          <CopyButton text={replyText} label="Copy the whole reply" />
        </div>
      );
    default:
      return null;
  }
});

export function ChatPane(props: ChatPaneProps) {
  const { sessionId, session, active, readOnly, compact } = props;
  const [items, setItems] = useState<ChatItem[]>([]);
  const [meta, setMeta] = useState<ChatMeta | null>(null);
  const [live, setLive] = useState<ChatLive | null>(null);
  const [state, setState] = useState<{ available: boolean; reason: string; conversation: string; started: string; truncated: boolean }>({
    available: false,
    reason: "",
    conversation: "",
    started: "",
    truncated: false,
  });
  const [loaded, setLoaded] = useState(false);
  const [pending, setPending] = useState<Pending[]>([]);
  const [toast, setToast] = useState("");
  const [drops, setDrops] = useState<File[] | null>(null);
  const [dragging, setDragging] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  const apply = useCallback((u: ChatUpdate) => {
    setLoaded(true);
    setState((s) => ({
      available: u.available,
      reason: u.reason || "",
      conversation: u.conversation?.id ?? (u.reset ? "" : s.conversation),
      started: u.conversation?.started ?? (u.reset ? "" : s.started),
      truncated: u.reset ? !!u.truncated_before : s.truncated,
    }));
    if (u.items || u.reset) setItems((cur) => mergeItems(cur, u));
    if (u.meta) setMeta(u.meta);
    if (u.live !== undefined) setLive(u.live ?? null);
  }, []);

  useEffect(() => {
    if (!active) return;
    let stopped = false;
    const stop = subscribeChat(sessionId, (u) => !stopped && apply(u));
    return () => {
      stopped = true;
      stop();
    };
  }, [sessionId, active, apply]);

  /* A sent message shows until the transcript has it (REQ-WC-053). */
  const shownPending = useMemo(() => {
    if (!pending.length) return pending;
    const users = items.filter((i) => i.kind === "user" || i.kind === "command").slice(-20);
    return pending.filter(
      (p) =>
        !users.some((u) => {
          const t = (u.kind === "command" ? (u.name || "") + (u.args ? " " + u.args : "") : plainUserText(u.text || "")).trim();
          return !!t && (t === p.text || t.startsWith(p.text.slice(0, 60)));
        }),
    );
  }, [pending, items]);

  useEffect(() => {
    if (!toast) return;
    const t = window.setTimeout(() => setToast(""), 5000);
    return () => window.clearTimeout(t);
  }, [toast]);

  const view = useMemo(() => dedupeTools(items), [items]);
  const blocks = useMemo(() => toBlocks(view), [view]);

  /* Keep the view pinned to the bottom unless the owner scrolled up. */
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [blocks, shownPending, live?.status, live?.dialog, active]);

  const loadOlder = async () => {
    const first = items[0];
    if (!first) return;
    try {
      const u = await getChat(sessionId, first.id);
      const older = u.items ?? [];
      setItems((cur) => [...older.filter((o) => !cur.some((c) => c.id === o.id)), ...cur]);
      setState((s) => ({ ...s, truncated: !!u.truncated_before }));
    } catch (err) {
      setToast(errorMessage(err));
    }
  };

  const actions: CardActions = {
    keys: (k) => {
      chatKeys(sessionId, k).catch((err) => setToast(errorMessage(err)));
    },
    openTerminal: props.onOpenTerminal,
    disabled: readOnly,
  };

  const working = live?.state === "working" || session?.agent_state === "working";
  const blocked = live?.state === "blocked" || session?.agent_state === "blocked";
  const dialog = live?.dialog ?? null;
  /* The transcript question still waiting: the newest one, unanswered, with
     no user message after it (an Esc leaves it unanswered for good). */
  const openQuestion = useMemo(() => {
    for (let i = view.length - 1; i >= 0; i--) {
      const it = view[i]!;
      if (it.kind === "user" || it.kind === "command") return undefined;
      if (it.kind === "question") return it.answered ? undefined : it;
    }
    return undefined;
  }, [view]);
  const lastPlanId = [...view].reverse().find((i) => i.kind === "plan")?.id;
  const openPlan = [...view].reverse().find((i) => i.kind === "plan" && i.state === "pending");
  const pendingTool = [...view].reverse().find((i) => i.kind === "tool" && i.status === "running");
  const mode = live?.mode || meta?.mode || "default";

  /* The reply text of each turn, for its copy button. */
  const replyFor = useMemo(() => {
    const map = new Map<string, string>();
    let texts: string[] = [];
    for (const b of blocks) {
      if (b.type === "text") texts.push(b.item.text || "");
      else if (b.type === "turn_end") {
        map.set(b.item.id, texts.join("\n\n"));
        texts = [];
      } else if (b.type === "user" || b.type === "command") texts = [];
    }
    return map;
  }, [blocks]);

  let lockedReason = "";
  if (blocked && (dialog || openQuestion || openPlan)) lockedReason = "Answer Claude's question above first";
  else if (blocked) lockedReason = "Answer in the terminal first";

  const cardFor = () => {
    if (!blocked && !dialog) return null;
    if (openQuestion && (blocked || dialog?.kind === "question")) return null; /* shown inline */
    if (openPlan && (blocked || dialog?.kind === "plan")) return null; /* shown inline */
    if (dialog && dialog.options.length && dialog.kind === "question") return <ScreenQuestionCard dialog={dialog} actions={actions} />;
    if (dialog && dialog.options.length) return <PermissionCard dialog={dialog} pending={pendingTool} actions={actions} />;
    if (blocked) return <FallbackCard actions={actions} />;
    return null;
  };

  /* A new Claude session has no transcript yet: show an empty chat with
     the composer, so the first message can be typed here (owner feedback
     2026-11-23). */
  const fresh = loaded && !state.available && state.reason === "waiting";
  if (loaded && !state.available && !fresh) {
    return (
      <div className="wbc">
        <div className="wbc-empty">
          {state.reason === "waiting" ? <Ic name="loader" size={18} className="wbc-spin" /> : <Ic name="message-square" size={18} />}
          <div className="wbc-empty-title">
            {state.reason === "waiting"
              ? "Waiting for the Claude Code conversation…"
              : state.reason === "unsupported_agent"
                ? "Chat is not available for this agent yet"
                : "Claude Code is not running in this pane"}
          </div>
          <div className="wbc-empty-text">
            {state.reason === "waiting"
              ? "The chat appears once the conversation id is known and the first message is in the transcript."
              : "Start claude in the terminal, then switch to Chat."}
          </div>
          {dialog && dialog.options.length ? (
            <div className="wbc-empty-card">
              <PermissionCard dialog={dialog} actions={actions} />
            </div>
          ) : (
            <button type="button" className="wbc-btn" onClick={props.onOpenTerminal}>
              Open terminal
            </button>
          )}
        </div>
      </div>
    );
  }

  const tasks = meta?.tasks ?? [];
  /* Foreground subagents that are still running join the tray too. */
  const trayTasks = [
    ...tasks,
    ...view
      .filter((i) => i.kind === "tool" && (i.name === "Agent" || i.name === "Task") && i.status === "running")
      .filter((i) => !tasks.some((t) => t.tool_use_id === i.tool_use_id || (i.agent && t.id === i.agent.agent_id)))
      .map((i) => ({
        id: i.id,
        kind: "agent",
        tool_use_id: i.tool_use_id,
        title: (typeof i.input?.description === "string" ? (i.input.description as string) : "") || i.summary?.target || "Subagent",
        agent_type: typeof i.input?.subagent_type === "string" ? (i.input.subagent_type as string) : "",
        status: "running",
        started: i.at,
      })),
  ];
  return (
    <OpenFileContext.Provider value={props.onOpenFile ?? null}>
    <TasksContext.Provider value={{ sessionId, tasks }}>
    <div
      className={"wbc" + (compact ? " is-compact" : "")}
      onDragOver={(e) => {
        if (readOnly || !Array.from(e.dataTransfer.types).includes("Files")) return;
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(e) => {
        if (e.currentTarget === e.target) setDragging(false);
      }}
      onDrop={(e) => {
        if (readOnly) return;
        e.preventDefault();
        setDragging(false);
        const list = Array.from(e.dataTransfer.files || []);
        if (list.length) setDrops(list);
      }}
    >
      <div
        className="wbc-scroll"
        ref={scroller}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
        }}
      >
        <div className="wbc-col">
          {meta && meta.unknown_lines > 0 ? (
            <div className="wbc-banner">
              <Ic name="triangle-alert" size={14} />
              <span>
                Claude Code {meta.version}: {meta.unknown_lines} unknown transcript {meta.unknown_lines === 1 ? "line was" : "lines were"} skipped.
              </span>
              <span className="wb-spacer" />
              <button type="button" className="wbc-link" onClick={props.onOpenTerminal}>
                Open terminal
              </button>
            </div>
          ) : null}
          {state.truncated ? (
            <button type="button" className="wbc-older" onClick={() => void loadOlder()}>
              Show earlier messages
            </button>
          ) : null}
          {state.conversation ? (
            <div className="wbc-divider is-session">
              <span>
                Conversation {state.conversation.slice(0, 8)} · from the Claude Code transcript
                {state.started ? " · started " + hhmm(state.started) : ""}
              </span>
            </div>
          ) : null}
          {!loaded ? <div className="wbc-loading">Loading the conversation…</div> : null}
          {fresh && !items.length ? (
            <div className="wbc-fresh">
              <span className="wbc-head-tile">
                <AgentLogo agent="claude" size={28} />
              </span>
              <div className="wbc-fresh-title">New Claude Code conversation</div>
              <div className="wbc-fresh-text">Ask anything below. The message is typed into this pane's claude.</div>
            </div>
          ) : null}
          {blocks.map((b, bi) => {
            const key = "item" in b ? b.item.id : b.key;
            /* Thinking says nothing once the step is past: it shows only as
               the live tail of a running turn (owner, 2026-11-23). */
            if (b.type === "thinking" && !(working && bi === blocks.length - 1)) return null;
            if (b.type === "question") {
              const isOpen = !b.item.answered && b.item.id === openQuestion?.id && blocked;
              return isOpen ? (
                <QuestionCard key={key} item={b.item} dialog={dialog} actions={actions} />
              ) : (
                <div key={key} className="wbc-answered">
                  {answerPairs(b.item).map(([q, a], n) => (
                    <div key={n} className="wbc-answered-row">
                      <Ic name="message-square" size={13} />
                      <span>{q}</span>
                      {a ? <em>{a}</em> : null}
                    </div>
                  ))}
                </div>
              );
            }
            if (b.type === "plan") {
              const text = planTextFor(view, b.item);
              /* A pending plan followed by a newer plan was sent back. */
              const superseded = b.item.state === "pending" && b.item.id !== lastPlanId;
              const shown = { ...b.item, ...(text ? { plan: text } : {}), ...(superseded ? { state: "rejected" as const } : {}) };
              return <PlanCard key={key} item={shown} dialog={b.item.id === openPlan?.id ? dialog : null} actions={actions} />;
            }
            return <BlockView key={key} block={b} sessionId={sessionId} replyText={b.type === "turn_end" ? replyFor.get(b.item.id) || "" : ""} />;
          })}
          {working && live?.status ? (
            <div className="wbc-status">
              <Ic name="loader" size={13} className="wbc-spin" />
              <span className="wbc-status-text">{live.status.replace(/^[·✢✳✶✻✽*]\s+/, "")}</span>
            </div>
          ) : working ? (
            <div className="wbc-status is-quiet">
              <Ic name="loader" size={13} className="wbc-spin" />
              <span className="wbc-status-text">Working…</span>
            </div>
          ) : null}
          {cardFor()}
          {shownPending.map((p) => (
            <div key={p.key} className="wbc-user">
              {p.images.length ? (
                <div className="wbc-user-images">
                  {p.images.map((src) => (
                    <img key={src} src={src} alt="" />
                  ))}
                </div>
              ) : null}
              {p.text ? <UserText text={p.text} className={p.queued ? "is-queued" : "is-sending"} /> : null}
              {p.queued ? (
                <div className="wbc-queued">
                  Queued · sent when Claude finishes ·{" "}
                  <button type="button" className="wbc-link" onClick={() => setPending((cur) => cur.filter((x) => x.key !== p.key))}>
                    Hide
                  </button>
                </div>
              ) : null}
            </div>
          ))}
        </div>
      </div>
      {dragging ? (
        <div className="wbc-drop">
          <Ic name="file" size={20} />
          <div className="wbc-drop-title">Drop files to attach</div>
          <div className="wbc-drop-text">Images, text, PDF · the daemon stores the file and its path goes into the message</div>
        </div>
      ) : null}
      {toast ? (
        <div className="wbc-toast" role="status">
          {toast}
        </div>
      ) : null}
      <div className="wbc-dock">
        <Composer
          sessionId={sessionId}
          cwd={session?.cwd || session?.dir || ""}
          locked={lockedReason}
          working={!!working}
          readOnly={readOnly}
          compact={compact}
          mode={mode}
          model={meta?.model || ""}
          effort={live?.effort || meta?.effort || ""}
          context={items.length ? meta?.context : undefined}
          onSent={(text, images) => {
            stick.current = true;
            setPending((cur) => [...cur, { key: Date.now() + ":" + cur.length, text, images, queued: !!working }]);
          }}
          onError={setToast}
          onOpenFiles={props.onOpenFiles}
          dropFiles={drops}
          onDropsTaken={() => setDrops(null)}
          tray={trayTasks.length ? <BackgroundTray tasks={trayTasks} /> : null}
        />
        {!compact ? (
          <div className="wbc-hint">
            {working
              ? "Claude is working: new messages join its queue · ■ = Esc"
              : "Enter to send · ⇧Enter new line · Esc to stop — typed into the same claude terminal"}
          </div>
        ) : null}
      </div>
    </div>
    </TasksContext.Provider>
    </OpenFileContext.Provider>
  );
}
