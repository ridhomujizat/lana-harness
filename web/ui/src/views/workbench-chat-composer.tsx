import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Ic } from "../ui/icons";
import { errorMessage } from "../api/client";
import { uploadWorkbenchDrops } from "../api/workbench";
import { resolveGitDirs, isGitInfo, searchGitFiles } from "../api/workbench-git";
import {
  chatCommands,
  chatFiles,
  chatKeys,
  chatMode,
  chatModel,
  chatSay,
  type ChatCommand,
  type ChatContext,
  type ChatMode,
} from "../api/workbench-chat";

/* Spec 71 REQ-WC-055 … 057: the chat composer. An outer shell with 6 px of
   padding around the field, the round send button inside the field, and the
   toolbar in the bottom padding (decision 4). Every action becomes terminal
   input on the daemon side. */

export const MODES: { id: ChatMode; label: string; short: string; desc: string; tone: string }[] = [
  { id: "default", label: "⏸ Manual", short: "⏸ manual", desc: "Ask before edits and commands", tone: "" },
  { id: "acceptEdits", label: "⏵⏵ Accept edits", short: "⏵⏵ accept edits", desc: "Edit files without asking, still ask for commands", tone: "amber" },
  { id: "plan", label: "⏸ Plan mode", short: "⏸ plan mode", desc: "Plan first, never changes files", tone: "blue" },
  { id: "auto", label: "⏵⏵ Auto mode", short: "⏵⏵ auto mode", desc: "Claude decides, risky actions are still checked", tone: "green" },
  { id: "bypassPermissions", label: "⏵⏵ Bypass permissions", short: "⏵⏵ bypass", desc: "Never asks (only when enabled)", tone: "red" },
];

const MODELS = [
  { id: "opus", label: "Opus", desc: "Most capable, for hard work", match: /opus/i },
  { id: "sonnet", label: "Sonnet", desc: "Fast, for everyday work", match: /sonnet/i },
  { id: "haiku", label: "Haiku", desc: "Fastest and cheapest", match: /haiku/i },
];
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];

export function modelLabel(model: string): string {
  const m = /claude-(opus|sonnet|haiku|fable)-(\d+)-(\d+)/i.exec(model || "");
  if (m) return m[1]!.charAt(0).toUpperCase() + m[1]!.slice(1) + " " + m[2] + "." + m[3];
  return model ? model.replace(/^claude-/, "") : "Model";
}

function fmtK(n: number): string {
  if (n >= 1000000) return (n / 1000000).toFixed(n >= 10000000 ? 0 : 1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function Donut({ pct, size = 18 }: { pct: number; size?: number }) {
  const r = (size - 4) / 2;
  const c = 2 * Math.PI * r;
  const p = Math.max(0, Math.min(100, pct));
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className={"wbc-donut" + (p >= 70 ? " is-high" : "")} aria-hidden="true">
      <circle cx={size / 2} cy={size / 2} r={r} className="wbc-donut-track" />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        className="wbc-donut-fill"
        strokeDasharray={`${(c * p) / 100} ${c}`}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
      />
    </svg>
  );
}

type MenuName = "attach" | "mode" | "model" | "ctx" | null;

interface Attachment {
  key: string;
  name: string;
  image: boolean;
  url?: string;
  size: number;
  path?: string;
  error?: string;
}

export interface ComposerProps {
  sessionId: string;
  cwd: string;
  locked: string; // non-empty: why the field is locked
  working: boolean;
  readOnly: boolean;
  compact: boolean;
  mode: string;
  model: string;
  effort: string;
  context?: ChatContext;
  onSent: (text: string, images: string[]) => void;
  onError: (message: string) => void;
  onOpenFiles?: () => void;
  dropFiles: File[] | null;
  onDropsTaken: () => void;
  /* Background shells and subagents, shown inside the shell above the field. */
  tray?: ReactNode;
}

interface MenuEntry {
  key: string;
  label: string;
  desc?: string;
  meta?: string;
  icon?: string;
  section: string;
  insert: string;
}

export function Composer(props: ComposerProps) {
  const { sessionId, cwd, locked, working, readOnly, compact } = props;
  const [text, setText] = useState("");
  const [menu, setMenu] = useState<MenuName>(null);
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [commands, setCommands] = useState<ChatCommand[] | null>(null);
  const [files, setFiles] = useState<string[]>([]);
  const [worktree, setWorktree] = useState<string>("");
  const [hl, setHl] = useState(0);
  const [caret, setCaret] = useState(0);
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const shell = useRef<HTMLDivElement>(null);
  const disabled = !!locked || readOnly;

  /* Close the toolbar menus on an outside click. */
  useEffect(() => {
    if (!menu) return;
    const down = (e: MouseEvent) => {
      if (shell.current && !shell.current.contains(e.target as Node)) setMenu(null);
    };
    window.addEventListener("mousedown", down);
    return () => window.removeEventListener("mousedown", down);
  }, [menu]);

  /* Autosize the field. */
  useEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = Math.min(220, Math.max(compact ? 24 : 44, el.scrollHeight)) + "px";
  }, [text, compact]);

  /* Files dropped on the chat (REQ-WC-057). */
  useEffect(() => {
    if (props.dropFiles && props.dropFiles.length) {
      void addFiles(props.dropFiles);
      props.onDropsTaken();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.dropFiles]);

  /* The `/` and `@` menus (REQ-WC-055). */
  const before = text.slice(0, caret);
  const slash = /^\/([^\s]*)$/.exec(before);
  const at = /(?:^|\s)@([^\s@]*)$/.exec(before);
  const trigger: "slash" | "at" | null = slash ? "slash" : at ? "at" : null;
  const query = slash ? slash[1]! : at ? at[1]! : "";

  useEffect(() => {
    if (trigger === "slash" && commands === null) {
      chatCommands(sessionId)
        .then((r) => setCommands(r.commands || []))
        .catch(() => setCommands([]));
    }
  }, [trigger, commands, sessionId]);

  useEffect(() => {
    if (trigger !== "at") return;
    let cancelled = false;
    const run = async () => {
      try {
        const r = await chatFiles(sessionId, query);
        if (!cancelled) {
          setFiles(r.files || []);
          setWorktree(r.root || "");
        }
      } catch {
        /* An older daemon without the route: fall back to the git search. */
        try {
          const res = await resolveGitDirs([cwd]);
          const info = res.dirs[cwd];
          const root = isGitInfo(info) ? info.worktree : "";
          const r = root ? await searchGitFiles(root, query) : { files: [] };
          if (!cancelled) {
            setFiles(r.files || []);
            setWorktree(root || "-");
          }
        } catch {
          if (!cancelled) setFiles([]);
        }
      }
    };
    const t = window.setTimeout(run, 80);
    return () => {
      cancelled = true;
      window.clearTimeout(t);
    };
  }, [trigger, query, cwd, sessionId]);

  const entries: MenuEntry[] = useMemo(() => {
    if (trigger === "slash") {
      const q = query.toLowerCase();
      const list = (commands || []).filter((c) => c.name.replace(/^\//, "").toLowerCase().includes(q));
      list.sort((a, b) => {
        const as = a.name.replace(/^\//, "").toLowerCase().startsWith(q) ? 0 : 1;
        const bs = b.name.replace(/^\//, "").toLowerCase().startsWith(q) ? 0 : 1;
        return as - bs;
      });
      return list.map((c) => ({
        key: c.name + c.source,
        label: c.name.startsWith("/") ? c.name : "/" + c.name,
        desc: c.description,
        meta: c.source,
        section: c.source === "built-in" ? "Built-in" : /skill/.test(c.source) ? "Skills" : "Commands",
        insert: (c.name.startsWith("/") ? c.name : "/" + c.name) + " ",
      }));
    }
    if (trigger === "at") {
      return files.map((f) => ({ key: f, label: f, icon: f.endsWith("/") ? "folder" : "file", section: query ? "Files" : "Files", insert: f }));
    }
    return [];
  }, [trigger, query, commands, files]);

  const sections = useMemo(() => {
    const order: string[] = [];
    const by: Record<string, MenuEntry[]> = {};
    for (const e of entries) {
      if (!by[e.section]) {
        by[e.section] = [];
        order.push(e.section);
      }
      by[e.section]!.push(e);
    }
    return order.map((s) => ({ name: s, entries: by[s]! }));
  }, [entries]);
  const flat = sections.flatMap((s) => s.entries);


  const insert = (entry: MenuEntry) => {
    const start = trigger === "slash" ? 0 : caret - query.length - 1;
    const next = text.slice(0, start) + (trigger === "at" ? "@" : "") + entry.insert + (trigger === "at" ? " " : "") + text.slice(caret);
    setText(next);
    const pos = start + (trigger === "at" ? 1 : 0) + entry.insert.length + (trigger === "at" ? 1 : 0);
    window.requestAnimationFrame(() => {
      const el = field.current;
      if (el) {
        el.focus();
        el.setSelectionRange(pos, pos);
        setCaret(pos);
      }
    });
  };

  async function addFiles(list: File[]) {
    const added: Attachment[] = list.map((f, i) => ({
      key: Date.now() + ":" + i + ":" + f.name,
      name: f.name,
      image: f.type.startsWith("image/"),
      url: f.type.startsWith("image/") ? URL.createObjectURL(f) : undefined,
      size: f.size,
    }));
    setAttachments((cur) => [...cur, ...added]);
    try {
      const paths = await uploadWorkbenchDrops(list);
      setAttachments((cur) => cur.map((a) => {
        const i = added.findIndex((x) => x.key === a.key);
        return i >= 0 ? { ...a, path: paths[i] } : a;
      }));
    } catch (err) {
      const message = errorMessage(err);
      setAttachments((cur) => cur.map((a) => (added.some((x) => x.key === a.key) ? { ...a, error: message } : a)));
      props.onError(message);
    }
  }

  const uploading = attachments.some((a) => !a.path && !a.error);

  const send = async () => {
    const body = text.trim();
    const paths = attachments.filter((a) => a.path).map((a) => a.path!);
    if ((!body && !paths.length) || disabled || uploading || busy) return;
    setBusy(true);
    try {
      await chatSay(sessionId, body, paths);
      props.onSent(body, attachments.filter((a) => a.image && a.url).map((a) => a.url!));
      setText("");
      setAttachments([]);
    } catch (err) {
      props.onError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const keys = (k: string[]) => {
    chatKeys(sessionId, k).catch((err) => props.onError(errorMessage(err)));
  };

  const pickMode = (m: ChatMode) => {
    setMenu(null);
    chatMode(sessionId, m).catch((err) => props.onError(errorMessage(err)));
  };
  const pickModel = (input: { model?: string; effort?: string }) => {
    chatModel(sessionId, input).catch((err) => props.onError(errorMessage(err)));
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (trigger && flat.length) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setHl((h) => (h + 1) % flat.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setHl((h) => (h - 1 + flat.length) % flat.length);
        return;
      }
      if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey && trigger === "at")) {
        e.preventDefault();
        insert(flat[hl]!);
        return;
      }
      if (e.key === "Enter" && !e.shiftKey && trigger === "slash") {
        e.preventDefault();
        const entry = flat[hl]!;
        if (text.trim() === entry.label || text.trim() === entry.label.trim()) {
          void send();
        } else {
          insert(entry);
        }
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setText(text.replace(/[/@][^\s]*$/, (m) => m));
        setCaret(-1);
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
      return;
    }
    if (e.key === "Tab" && e.shiftKey) {
      e.preventDefault();
      keys(["shift+tab"]);
      return;
    }
    if (e.key === "Escape" && !text) {
      e.preventDefault();
      keys(["esc"]);
    }
  };

  const modeInfo = MODES.find((m) => m.id === props.mode) ?? MODES[0]!;
  const ctx = props.context;
  const pct = ctx && ctx.window ? Math.round((ctx.used / ctx.window) * 100) : 0;
  const menuOpen = !!trigger && caret >= 0 && !disabled;

  let row = -1;
  return (
    <div className={"wbc-composer" + (compact ? " is-compact" : "")} ref={shell}>
      {menuOpen ? (
        <div className="wbc-bigmenu" role="listbox" aria-label={trigger === "slash" ? "Commands" : "Files"} onMouseDown={(e) => e.preventDefault()}>
          <div className="wbc-bigmenu-head">
            <span className="wbc-bigmenu-title">{trigger === "slash" ? "Commands" : "Files"}</span>
            <span className="wbc-bigmenu-count">{trigger === "slash" ? (commands ? flat.length : "…") : flat.length + (worktree && worktree !== "-" ? " · " + worktree.replace(/^.*\//, "") : "")}</span>
            <span className="wb-spacer" />
            <span className="wbc-bigmenu-hint">type to filter</span>
          </div>
          <div className="wbc-bigmenu-list">
            {flat.length === 0 ? (
              <div className="wbc-bigmenu-empty">
                {trigger === "slash" ? (commands ? "No command matches" : "Loading commands…") : "No file matches"}
              </div>
            ) : (
              sections.map((s) => (
                <div key={s.name}>
                  <div className="wbc-bigmenu-sec">{s.name}</div>
                  {s.entries.map((entry) => {
                    row += 1;
                    const i = row;
                    return (
                      <button
                        key={entry.key}
                        type="button"
                        role="option"
                        aria-selected={i === hl}
                        className={"wbc-bigmenu-row" + (i === hl ? " is-hl" : "")}
                        ref={i === hl ? (el) => el?.scrollIntoView({ block: "nearest" }) : undefined}
                        onMouseEnter={() => setHl(i)}
                        onClick={() => insert(entry)}
                      >
                        {entry.icon ? <Ic name={entry.icon} size={14} /> : null}
                        <span className="wbc-bigmenu-label">{entry.label}</span>
                        {entry.desc ? <span className="wbc-bigmenu-desc">{entry.desc}</span> : null}
                        {entry.meta ? <span className="wbc-bigmenu-meta">{entry.meta}</span> : null}
                      </button>
                    );
                  })}
                </div>
              ))
            )}
          </div>
          <div className="wbc-bigmenu-foot">↑↓ choose · ⇥ or Enter insert · Esc close</div>
        </div>
      ) : null}
      {props.tray}
      <div className={"wbc-field" + (disabled ? " is-locked" : "")}>
        {attachments.length ? (
          <div className="wbc-attachments">
            {attachments.map((a) => (
              <div key={a.key} className={"wbc-att" + (a.image ? " is-image" : "") + (a.error ? " is-error" : "")} title={a.error || a.path || a.name}>
                {a.image && a.url ? (
                  <img src={a.url} alt={a.name} />
                ) : (
                  <>
                    <span className="wbc-att-ic">
                      <Ic name="file" size={15} />
                    </span>
                    <span className="wbc-att-body">
                      <span className="wbc-att-name">{a.name}</span>
                      {a.path || a.error ? (
                        <span className="wbc-att-meta">{a.error ? "upload failed" : fmtK(a.size) + "B"}</span>
                      ) : (
                        <span className="wbc-att-progress">
                          <i />
                        </span>
                      )}
                    </span>
                  </>
                )}
                <button type="button" className="wbc-att-x" aria-label={"Remove " + a.name} onClick={() => setAttachments((cur) => cur.filter((x) => x.key !== a.key))}>
                  <Ic name="x" size={11} />
                </button>
              </div>
            ))}
          </div>
        ) : null}
        <div className="wbc-field-row">
          {locked ? <Ic name="lock" size={14} className="wbc-lock" /> : null}
          <textarea
            ref={field}
            rows={1}
            value={text}
            disabled={disabled}
            placeholder={locked || (readOnly ? "View only: control is locked" : compact ? "Message Claude Code…" : "Ask Claude anything…  / commands · @ files")}
            onChange={(e) => {
              setText(e.target.value);
              setCaret(e.target.selectionStart ?? e.target.value.length);
              setHl(0);
            }}
            onSelect={(e) => setCaret((e.target as HTMLTextAreaElement).selectionStart ?? 0)}
            onKeyDown={onKeyDown}
            onPaste={(e) => {
              const pasted = Array.from(e.clipboardData.files || []);
              if (pasted.length) {
                e.preventDefault();
                void addFiles(pasted);
              }
            }}
          />
          {working && !text ? (
            <button type="button" className="wbc-send is-stop" aria-label="Stop (Esc)" data-tip="Stop · Esc" disabled={readOnly} onClick={() => keys(["esc"])}>
              <i />
            </button>
          ) : (
            <button type="button" className="wbc-send" aria-label="Send" disabled={disabled || uploading || busy || (!text.trim() && !attachments.length)} onClick={() => void send()}>
              <Ic name="arrow-up" size={16} />
            </button>
          )}
        </div>
      </div>
      <div className="wbc-toolbar">
        <Anchor open={menu === "attach"}>
          <button type="button" className={"wbc-tool" + (menu === "attach" ? " is-on" : "")} aria-label="Attach" disabled={disabled} onClick={() => setMenu(menu === "attach" ? null : "attach")}>
            <Ic name="plus" size={15} />
          </button>
          <Pop className="wbc-pop-left" width={260}>
            <PopRow
              label="Upload file…"
              right="⌘U"
              onClick={() => {
                setMenu(null);
                fileInput.current?.click();
              }}
            />
            <PopRow
              label="Paste image from clipboard"
              right="⌘V"
              onClick={async () => {
                setMenu(null);
                try {
                  const itemsList = await navigator.clipboard.read();
                  const out: File[] = [];
                  for (const it of itemsList) {
                    const type = it.types.find((t) => t.startsWith("image/"));
                    if (type) {
                      const blob = await it.getType(type);
                      out.push(new File([blob], "pasted." + type.split("/")[1], { type }));
                    }
                  }
                  if (out.length) void addFiles(out);
                  else props.onError("The clipboard has no image.");
                } catch {
                  props.onError("The browser did not allow reading the clipboard. Press ⌘V in the field instead.");
                }
              }}
            />
            {props.onOpenFiles ? (
              <PopRow
                label="Pick from the Files panel"
                onClick={() => {
                  setMenu(null);
                  props.onOpenFiles?.();
                }}
              />
            ) : null}
            <div className="wbc-pop-foot">Attachments are file paths Claude reads</div>
          </Pop>
        </Anchor>
        <input
          ref={fileInput}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            const list = Array.from(e.target.files || []);
            e.target.value = "";
            if (list.length) void addFiles(list);
          }}
        />
        <Anchor open={menu === "mode"}>
          <button type="button" className={"wbc-tool wbc-mode tone-" + (modeInfo.tone || "plain") + (menu === "mode" ? " is-on" : "")} disabled={disabled} onClick={() => setMenu(menu === "mode" ? null : "mode")}>
            <span>{modeInfo.short}</span>
            {!compact ? <kbd>⇧⇥</kbd> : null}
            <Ic name="chevron-down" size={12} />
          </button>
          <Pop className="wbc-pop-left" width={330}>
            {MODES.map((m) => (
              <PopRow key={m.id} label={m.label} desc={m.desc} tone={m.tone} checked={m.id === modeInfo.id} onClick={() => pickMode(m.id)} />
            ))}
            <div className="wbc-pop-foot">⇧⇥ to cycle · sent as Shift+Tab</div>
          </Pop>
        </Anchor>
        <span className="wb-spacer" />
        {!compact ? (
          <>
            <Anchor open={menu === "model"}>
              <button type="button" className={"wbc-tool" + (menu === "model" ? " is-on" : "")} disabled={disabled} onClick={() => setMenu(menu === "model" ? null : "model")}>
                <span>
                  {modelLabel(props.model)}
                  {props.effort ? " · " + props.effort : ""}
                </span>
                <Ic name="chevron-down" size={12} />
              </button>
              <Pop className="wbc-pop-right" width={300}>
                <div className="wbc-pop-sec">Model</div>
                {MODELS.map((m) => (
                  <PopRow key={m.id} label={m.label} desc={m.desc} checked={m.match.test(props.model)} onClick={() => pickModel({ model: m.id })} />
                ))}
                <div className="wbc-pop-sec">Effort</div>
                <div className="wbc-seg">
                  {EFFORTS.map((e) => (
                    <button key={e} type="button" className={e === props.effort ? "is-on" : ""} onClick={() => pickModel({ effort: e })}>
                      {e}
                    </button>
                  ))}
                </div>
                <div className="wbc-pop-foot">Sent to the terminal as /model and /effort{working ? " · wait until Claude is idle" : ""}</div>
              </Pop>
            </Anchor>
            <Anchor open={menu === "ctx"} hover>
              <button type="button" className={"wbc-tool wbc-ctx" + (menu === "ctx" ? " is-on" : "")} aria-label="Context" onClick={() => setMenu(menu === "ctx" ? null : "ctx")}>
                <Donut pct={pct} />
                <span>{ctx ? pct + "%" : "–"}</span>
              </button>
              <Pop className="wbc-pop-right" width={280}>
                <div className="wbc-ctx-head">
                  <Donut pct={pct} size={36} />
                  <div>
                    <div className="wbc-ctx-big">
                      {ctx ? fmtK(ctx.used) + " / " + fmtK(ctx.window) + " tokens" : "No usage yet"}
                    </div>
                    <div className="wbc-ctx-sub">{pct}% of context used</div>
                  </div>
                </div>
                {ctx ? (
                  <dl className="wbc-ctx-rows">
                    <dt>Cache read</dt>
                    <dd>{fmtK(ctx.cache_read)}</dd>
                    <dt>New input</dt>
                    <dd>{fmtK(ctx.input)}</dd>
                    <dt>Last turn output</dt>
                    <dd>{fmtK(ctx.output)}</dd>
                    <dt>Left before auto-compact</dt>
                    <dd>~{fmtK(Math.max(0, Math.round(ctx.window * 0.8) - ctx.used))}</dd>
                  </dl>
                ) : null}
                <button
                  type="button"
                  className="wbc-pop-action"
                  disabled={disabled || working}
                  onClick={() => {
                    setMenu(null);
                    chatSay(sessionId, "/compact").catch((err) => props.onError(errorMessage(err)));
                  }}
                >
                  Compact now <span>/compact</span>
                </button>
              </Pop>
            </Anchor>
          </>
        ) : null}
      </div>
    </div>
  );
}

/* A toolbar menu opens right above its own button (decision 5). */
function Anchor({ open, hover, children }: { open: boolean; hover?: boolean; children: ReactNode }) {
  return <span className={"wbc-anchor" + (open ? " is-open" : "") + (hover ? " is-hover" : "")}>{children}</span>;
}

function Pop({ children, className, width }: { children: ReactNode; className: string; width: number }) {
  return (
    <div className={"wbc-pop " + className} style={{ width }} onMouseDown={(e) => e.stopPropagation()}>
      {children}
    </div>
  );
}

function PopRow({ label, desc, right, tone, checked, onClick }: { label: string; desc?: string; right?: string; tone?: string; checked?: boolean; onClick: () => void }) {
  return (
    <button type="button" className={"wbc-pop-row" + (checked ? " is-checked" : "")} onClick={onClick}>
      <span className="wbc-pop-text">
        <span className={"wbc-pop-label" + (tone ? " tone-" + tone : "")}>{label}</span>
        {desc ? <span className="wbc-pop-desc">{desc}</span> : null}
      </span>
      {right ? <kbd>{right}</kbd> : null}
      {checked ? <Ic name="check" size={14} /> : null}
    </button>
  );
}
