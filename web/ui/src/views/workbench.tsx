import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";

import { errorMessage } from "../api/client";
import {
  createWorkbenchSession,
  getWorkbenchLayout,
  killWorkbenchSession,
  listWorkbenchSessions,
  putWorkbenchLayout,
  restartWorkbenchSession,
  subscribeWorkbench,
  type WorkbenchSession,
} from "../api/workbench";
import { Ic } from "../ui/icons";
import {
  AGENT_PROGRAMS,
  KEY_SEQUENCES,
  STATUS_LABEL,
  STATUS_ORDER,
  activeSpace as pickSpace,
  activeTab as pickTab,
  addSpace,
  addTab,
  age,
  closeTab,
  ctrlKey,
  emptyLayout,
  focusPane,
  isAgent,
  locate,
  mergeRemote,
  paneSessions,
  parseLayout,
  removeSession,
  removeSpace,
  selectSpace,
  selectTab,
  sessionTitle,
  programName,
  setGrouping,
  spaceSessions,
  renameTab,
  renameSpace,
  folderName,
  spaceCwd,
  spaceLabel,
  splitPane,
  splitSizes,
  statusOf,
  tabSessions,
  toggleZoom,
  type PaneNode,
  type Status,
  type WbLayout,
  type WbSpace,
  type WbTab,
} from "./workbench-model";
import { TermPane, type SendInput } from "./workbench-term";
import { AgentLogo } from "./agent-logo";
import { ClaudeAccountsPanel, ClaudeAccountsPopover, type AccountActions } from "./workbench-accounts";
import { toolReady, usePluginTool } from "./plugins";
import { chime, setSoundEnabled, soundEnabled, unlockAudio } from "./workbench-sound";
import { ActionMenu, CloseSpaceDialog, RenameDialog, longPress, type DialogState, type MenuTarget } from "./workbench-actions";

/* Spec 55: the Workbench page. Desktop: panel (Spaces + Agents), tab strip,
   split panes (REQ-WB-051..054). Phone: home, space view with one pane at a
   time, all-tabs sheet, read-only until Take over (REQ-WB-055). */

const PHONE_QUERY = "(max-width: 640px)";

/* The shell topbar is hidden on this page (App.tsx), so the phone drawer
   opens from here. */
function openNav(): void {
  window.dispatchEvent(new Event("lana:open-nav"));
}

function subscribePhone(onChange: () => void): () => void {
  if (typeof window.matchMedia !== "function") return () => {};
  const query = window.matchMedia(PHONE_QUERY);
  query.addEventListener?.("change", onChange);
  return () => query.removeEventListener?.("change", onChange);
}

function readPhone(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(PHONE_QUERY).matches;
}

type Sessions = Record<string, WorkbenchSession>;

/* ------------------------------------------------------------------ */
/* Small pieces                                                        */
/* ------------------------------------------------------------------ */

function hashColor(text: string): string {
  const colors = ["#B21FB8", "#3B6FD8", "#7652C2", "#2F9E5B", "#D9824B", "#3E9C8F"];
  let h = 0;
  for (let i = 0; i < text.length; i++) h = (h * 31 + text.charCodeAt(i)) >>> 0;
  return colors[h % colors.length] ?? "#7652C2";
}

export function StatusDot({ status }: { status: Status }) {
  return <span className={"wb-status wb-status-" + status} aria-label={STATUS_LABEL[status]} title={STATUS_LABEL[status]} />;
}

function ProgramIcon({ session, size = 20 }: { session?: WorkbenchSession; size?: number }) {
  /* A recognized coding agent shows its real logo (claude, codex, cursor,
     gemini, opencode, copilot, …); anything else gets an outline terminal
     mark, as in GoRex. */
  const program = (session?.program || "").toLowerCase();
  const agent = (session?.agent || (AGENT_PROGRAMS.includes(program) ? program : "")).toLowerCase();
  if (agent) return <AgentLogo agent={agent} size={size} />;
  return (
    <span className="wb-prog wb-prog-plain" style={{ width: size, height: size }} aria-hidden="true">
      <Ic name="square-terminal" size={Math.round(size * 0.9)} />
    </span>
  );
}

function LanaChip({ session, compact }: { session?: WorkbenchSession; compact?: boolean }) {
  const agent = session?.labels?.["lana.agent"];
  if (!agent) return null;
  const task = session?.labels?.["lana.task"];
  return (
    <span className="wb-lana-chip" title={"Run by Lana agent " + agent}>
      <span className="wb-lana-av" style={{ background: hashColor(agent) }}>
        {agent[0]?.toUpperCase()}
      </span>
      {compact ? null : <span>{task ? agent + " · " + task : agent}</span>}
    </span>
  );
}

function SpaceTile({ space, label, size = 22 }: { space: WbSpace; label: string; size?: number }) {
  const letter = label === "~" ? "~" : label.replace(/^[^\p{L}\p{N}]+/u, "")[0]?.toUpperCase() || "?";
  return (
    <span className="wb-tile" style={{ background: space.color, width: size, height: size }}>
      {letter}
    </span>
  );
}

function tabStatus(tab: WbTab, sessions: Sessions): Status {
  const statuses = tabSessions(tab).map((id) => statusOf(sessions[id]));
  return STATUS_ORDER.find((s) => statuses.includes(s)) || "idle";
}

function tabLabel(tab: WbTab, sessions: Sessions): string {
  if (tab.title) return tab.title;
  const focus = tab.focus || tabSessions(tab)[0];
  return sessionTitle(focus ? sessions[focus] : undefined);
}

/* ------------------------------------------------------------------ */
/* Panel: Spaces + Agents (herdr layout)                               */
/* ------------------------------------------------------------------ */

interface PanelProps {
  layout: WbLayout;
  sessions: Sessions;
  onSelectSpace: (id: string) => void;
  onNewSpace: () => void;
  onMenu: (target: MenuTarget) => void;
  onRenameSpace: (spaceId: string) => void;
  soundOn: boolean;
  onToggleSound: () => void;
  onOpenSession: (id: string) => void;
  onGrouping: (grouping: "space" | "status") => void;
  orphans: WorkbenchSession[];
  onAdoptOrphans: () => void;
  focusSession?: string;
  /* Desktop only: hides the panel (the phone shows it as a page). */
  onCollapse?: () => void;
  width?: number;
  onResizeStart?: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizeReset?: () => void;
}

function Panel(props: PanelProps) {
  const { layout, sessions, focusSession } = props;
  const grouping = layout.grouping || "space";
  const space = pickSpace(layout);

  const agents = Object.values(sessions).filter(isAgent);
  const spaceOf = (id: string) => locate(layout, id)?.space;

  const row = (s: WorkbenchSession, showSpace: boolean) => {
    const owner = s.labels?.["lana.agent"];
    const sp = spaceOf(s.id);
    return (
      <button
        key={s.id}
        type="button"
        className={"wb-agent" + (s.id === focusSession ? " is-active" : "")}
        onClick={() => props.onOpenSession(s.id)}
      >
        <ProgramIcon session={s} />
        <span className="wb-agent-label">
          {showSpace ? <b>{sp ? spaceLabel(sp, sessions) : "unplaced"}</b> : null}
          <span className={showSpace ? "wb-agent-title muted" : "wb-agent-title"}>
            {showSpace ? " · " : ""}
            {sessionTitle(s)}
          </span>
        </span>
        {owner ? (
          <span className="wb-lana-av" style={{ background: hashColor(owner) }} title={"Lana agent " + owner}>
            {owner[0]?.toUpperCase()}
          </span>
        ) : null}
        <span className="wb-age">{age(s.last_output || s.created)}</span>
        <StatusDot status={statusOf(s)} />
      </button>
    );
  };

  let groups: ReactNode[] = [];
  if (grouping === "space") {
    const placed = new Set<string>();
    groups = layout.spaces.map((sp) => {
      const ids = sp.tabs.flatMap(tabSessions);
      const items = agents.filter((a) => ids.includes(a.id));
      items.forEach((a) => placed.add(a.id));
      if (!items.length) return null;
      return (
        <div key={sp.id} className="wb-group">
          <div className="wb-group-head">{spaceLabel(sp, sessions)}</div>
          {items.map((a) => row(a, false))}
        </div>
      );
    });
    const rest = agents.filter((a) => !placed.has(a.id));
    if (rest.length) {
      groups.push(
        <div key="_unplaced" className="wb-group">
          <div className="wb-group-head">unplaced</div>
          {rest.map((a) => row(a, false))}
        </div>
      );
    }
  } else {
    groups = STATUS_ORDER.map((status) => {
      const items = agents.filter((a) => statusOf(a) === status);
      if (!items.length) return null;
      return (
        <div key={status} className="wb-group">
          <div className="wb-group-head">
            {STATUS_LABEL[status]} <span className="wb-count">{items.length}</span>
          </div>
          {items.map((a) => row(a, true))}
        </div>
      );
    });
  }

  return (
    <aside className="wb-panel" aria-label="Workbench" style={props.width ? { width: props.width, flexBasis: props.width } : undefined}>
      <section className="wb-spaces">
        <div className="wb-sec-head">
          <span>Spaces</span>
          <span className="wb-sec-tools">
            <button type="button" className="wb-icon-btn" aria-label="New space" data-tip="New space" onClick={props.onNewSpace}>
              <Ic name="plus" size={16} />
            </button>
            {props.onCollapse ? (
              <button type="button" className="wb-icon-btn" aria-label="Hide panel" data-tip="Hide panel" onClick={props.onCollapse}>
                <Ic name="panel-left" size={16} />
              </button>
            ) : null}
          </span>
        </div>
        {layout.spaces.map((sp) => (
          <div
            key={sp.id}
            className={"wb-space" + (space?.id === sp.id ? " is-active" : "")}
            onContextMenu={(e) => {
              e.preventDefault();
              props.onMenu({ kind: "space", spaceId: sp.id, anchor: e.currentTarget });
            }}
            {...longPress((el) => props.onMenu({ kind: "space", spaceId: sp.id, anchor: el }))}
          >
            <button
              type="button"
              className="wb-space-btn"
              onClick={() => props.onSelectSpace(sp.id)}
              onDoubleClick={() => props.onRenameSpace(sp.id)}
              title={spaceCwd(sp, sessions) || undefined}
            >
              <SpaceTile space={sp} label={spaceLabel(sp, sessions)} />
              <span className="wb-space-name">{spaceLabel(sp, sessions)}</span>
              <span className="wb-space-meta">{sp.tabs.length ? sp.tabs.length + (sp.tabs.length === 1 ? " tab" : " tabs") : ""}</span>
            </button>
            <button
              type="button"
              className="wb-icon-btn wb-space-more"
              aria-label={"Space actions: " + spaceLabel(sp, sessions)}
              onClick={(e) => props.onMenu({ kind: "space", spaceId: sp.id, anchor: e.currentTarget })}
            >
              <Ic name="ellipsis" size={15} />
            </button>
          </div>
        ))}
        {props.orphans.length ? (
          <button type="button" className="wb-orphans" onClick={props.onAdoptOrphans}>
            {props.orphans.length} unplaced {props.orphans.length === 1 ? "terminal" : "terminals"} · open here
          </button>
        ) : null}
      </section>
      <section className="wb-agents">
        <div className="wb-sec-head">
          <span>Agents</span>
          <button
            type="button"
            className={"wb-icon-btn wb-sound" + (props.soundOn ? " is-on" : "")}
            aria-pressed={props.soundOn}
            aria-label={props.soundOn ? "Sound on when an agent finishes" : "Sound off"}
            data-tip={props.soundOn ? "Sound on" : "Sound off"}
            onClick={props.onToggleSound}
          >
            <Ic name={props.soundOn ? "bell-ring" : "bell-off"} size={15} />
          </button>
          <span className="wb-toggle" role="group" aria-label="Group agents">
            <button
              type="button"
              className={grouping === "space" ? "is-on" : ""}
              aria-pressed={grouping === "space"}
              aria-label="Group by space"
              data-tip="Group by space"
              onClick={() => props.onGrouping("space")}
            >
              <Ic name="layers" size={15} />
            </button>
            <button
              type="button"
              className={grouping === "status" ? "is-on" : ""}
              aria-pressed={grouping === "status"}
              aria-label="Group by status"
              data-tip="Group by status"
              onClick={() => props.onGrouping("status")}
            >
              <Ic name="clock" size={15} />
            </button>
          </span>
        </div>
        {agents.length ? groups : <p className="wb-empty-note">Claude Code, Codex and Lana runs show up here.</p>}
      </section>
      {props.onResizeStart ? (
        <div
          className="wb-resizer"
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize panel"
          title="Drag to resize · double-click to reset"
          onPointerDown={props.onResizeStart}
          onDoubleClick={props.onResizeReset}
        />
      ) : null}
    </aside>
  );
}

/* ------------------------------------------------------------------ */
/* Desktop panes                                                       */
/* ------------------------------------------------------------------ */

interface PaneActions {
  onFocus: (id: string) => void;
  onSplit: (id: string, dir: "row" | "col") => void;
  onZoom: (id: string) => void;
  onClose: (id: string) => void;
  onRestart: (id: string) => void;
}

function PaneView({ id, session, focused, zoomed, actions, style }: { id: string; session?: WorkbenchSession; focused: boolean; zoomed: boolean; actions: PaneActions; style?: CSSProperties }) {
  const status = statusOf(session);
  return (
    <div className={"wb-pane" + (focused ? " is-focused" : "")} style={style}>
      <div className="wb-pane-head" onMouseDown={() => actions.onFocus(id)}>
        <ProgramIcon session={session} />
        <span className="wb-pane-title">{programName(session)}</span>
        {session?.cwd || session?.dir ? (
          <span className="wb-pane-dir">{(session.cwd || session.dir).replace(/^\/Users\/[^/]+/, "~")}</span>
        ) : null}
        <LanaChip session={session} />
        <span className={"wb-pane-state wb-pane-state-" + status}>
          <StatusDot status={status} />
          {status === "exited" ? "exited " + (session?.exit_code ?? 0) : status === "blocked" ? "needs you" : status}
        </span>
        <span className="wb-pane-tools">
          <button type="button" aria-label="Split right" data-tip="Split right" onClick={() => actions.onSplit(id, "row")}>
            <Ic name="columns-2" size={14} />
          </button>
          <button type="button" aria-label="Split down" data-tip="Split down" onClick={() => actions.onSplit(id, "col")}>
            <Ic name="rows-2" size={14} />
          </button>
          <button type="button" aria-label={zoomed ? "Unzoom" : "Zoom"} data-tip={zoomed ? "Unzoom" : "Zoom"} onClick={() => actions.onZoom(id)}>
            <Ic name={zoomed ? "minimize-2" : "maximize-2"} size={14} />
          </button>
          <button type="button" aria-label="Close" data-tip="Close" onClick={() => actions.onClose(id)}>
            <Ic name="x" size={14} />
          </button>
        </span>
      </div>
      <div className="wb-pane-body">
        {session ? (
          <TermPane
            key={id + ":" + session.pid}
            sessionId={id}
            mode="control"
            focused={focused}
            onFocus={() => actions.onFocus(id)}
            sessionSize={{ cols: session.cols, rows: session.rows }}
          />
        ) : (
          <p className="wb-pane-missing">This terminal is gone.</p>
        )}
      </div>
      {status === "exited" ? (
        <div className="wb-exited">
          <StatusDot status="exited" />
          <span>Process exited {session?.exit_code ?? 0}</span>
          <span className="wb-spacer" />
          <button type="button" className="wb-dark-btn" onClick={() => actions.onRestart(id)}>
            Restart
          </button>
          <button type="button" className="wb-dark-btn" onClick={() => actions.onClose(id)}>
            Close
          </button>
        </div>
      ) : null}
    </div>
  );
}

function PaneTree({ node, tab, sessions, actions, grow }: { node: PaneNode; tab: WbTab; sessions: Sessions; actions: PaneActions; grow?: number }) {
  /* Spec 58 REQ-DW-022: a split's sizes set its children's flex-grow. */
  const style = grow === undefined ? undefined : { flexGrow: grow };
  if (node.type === "pane") {
    return <PaneView id={node.session} session={sessions[node.session]} focused={tab.focus === node.session} zoomed={tab.zoom === node.session} actions={actions} style={style} />;
  }
  const sizes = splitSizes(node);
  return (
    <div className={"wb-split wb-split-" + node.dir} style={style}>
      {node.children.map((child, i) => (
        <PaneTree key={child.type === "pane" ? child.session : "s" + i} node={child} tab={tab} sessions={sessions} actions={actions} grow={sizes[i]} />
      ))}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Phone                                                               */
/* ------------------------------------------------------------------ */

interface PhoneProps {
  onMenu: (target: MenuTarget) => void;
  onCloseTab: (spaceId: string, tabId: string) => void;
  /* The Claude accounts plugin, when its tool is installed. */
  onAccounts?: () => void;
  layout: WbLayout;
  sessions: Sessions;
  panel: ReactNode;
  view: "home" | "space";
  setView: (v: "home" | "space") => void;
  onNewTerminal: () => void;
  onNewSpace: () => void;
  onSelectTab: (spaceId: string, tabId: string) => void;
  onFocus: (spaceId: string, tabId: string, id: string) => void;
  onRestart: (id: string) => void;
  onClose: (id: string) => void;
}

const PHONE_FONT_KEY = "lana.workbench.phoneFont";
const PHONE_FONT_MIN = 7;
const PHONE_FONT_MAX = 14;
const PHONE_FONT = 9;

function readPhoneFont(): number {
  try {
    const n = Number(window.localStorage.getItem(PHONE_FONT_KEY));
    return n >= PHONE_FONT_MIN && n <= PHONE_FONT_MAX ? n : PHONE_FONT;
  } catch {
    return PHONE_FONT;
  }
}

/* The visual viewport (the part above the on-screen keyboard). `keyboard` is
   true when it is clearly shorter than the layout viewport. */
function useVisualViewport(): { top: number; height: number; keyboard: boolean } | null {
  const read = () => {
    const vv = typeof window !== "undefined" ? window.visualViewport : null;
    if (!vv) return null;
    return { top: vv.offsetTop, height: vv.height, keyboard: window.innerHeight - vv.height > 120 };
  };
  const [state, setState] = useState(read);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const update = () => setState(read());
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update);
    return () => {
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
    };
  }, []);
  return state;
}

function PhoneWorkbench(props: PhoneProps) {
  const { layout, sessions, view } = props;
  const space = pickSpace(layout);
  const tab = pickTab(space);
  const [sheet, setSheet] = useState(false);
  const [ctrl, setCtrl] = useState(false);
  const [fontSize, setFontSize] = useState(readPhoneFont);
  const keyboardRef = useRef<{ focus: () => void; blur: () => void } | null>(null);
  const viewport = useVisualViewport();
  const changeFont = (delta: number) => {
    setFontSize((size) => {
      const next = Math.min(PHONE_FONT_MAX, Math.max(PHONE_FONT_MIN, size + delta));
      try {
        window.localStorage.setItem(PHONE_FONT_KEY, String(next));
      } catch {
        /* Storage is optional. */
      }
      return next;
    });
  };
  const sendRef = useRef<SendInput | null>(null);
  const claimRef = useRef<(() => void) | null>(null);
  const [owner, setOwner] = useState(true);
  const transformRef = useRef<((data: string) => string) | null>(null);
  useEffect(() => {
    transformRef.current = ctrl
      ? (data) => {
          setCtrl(false);
          return ctrlKey(data);
        }
      : null;
  }, [ctrl]);

  if (view === "home" || !space) {
    return (
      <main id="main" className="wb-page wb-phone">
        <header className="wb-phone-head">
          <button type="button" className="wb-back" aria-label="Open navigation" onClick={openNav}>
            <Ic name="menu" size={20} />
          </button>
          <h1>Workbench</h1>
          <button type="button" className="wb-icon-btn wb-icon-btn-lg" aria-label="New space" onClick={props.onNewSpace}>
            <Ic name="plus" size={18} />
          </button>
        </header>
        <div className="wb-phone-panel">{props.panel}</div>
      </main>
    );
  }

  const ids = tab ? tabSessions(tab) : [];
  const current = tab && tab.focus && ids.includes(tab.focus) ? tab.focus : ids[0];
  const index = Math.max(0, ids.indexOf(current || ""));
  const session = current ? sessions[current] : undefined;
  const status = statusOf(session);
  const counts = space.tabs.reduce(
    (acc, t) => {
      const s = tabStatus(t, sessions);
      if (s === "working") acc.working++;
      if (s === "blocked") acc.blocked++;
      return acc;
    },
    { working: 0, blocked: 0 }
  );
  const summary =
    space.tabs.length + (space.tabs.length === 1 ? " tab" : " tabs") +
    (counts.working ? " · " + counts.working + " working" : "") +
    (counts.blocked ? " · " + counts.blocked + " need you" : "");
  const go = (delta: number) => {
    if (!tab || ids.length < 2) return;
    const next = ids[(index + delta + ids.length) % ids.length];
    if (next) props.onFocus(space.id, tab.id, next);
  };
  const key = (k: string) => {
    if (k === "ctrl") {
      setCtrl((v) => !v);
      return;
    }
    sendRef.current?.(KEY_SEQUENCES[k] ?? k);
  };

  /* The space view pins itself to the visual viewport, so the key bar rides
     on top of the phone keyboard instead of hiding behind it. */
  const pinned = viewport
    ? { position: "fixed" as const, top: viewport.top, left: 0, right: 0, height: viewport.height, zIndex: 40 }
    : undefined;

  return (
    <main id="main" className="wb-page wb-phone wb-phone-space" style={pinned}>
      <header className="wb-phone-top">
        <button type="button" className="wb-back" aria-label="Back" onClick={() => props.setView("home")}>
          <Ic name="chevron-left" size={22} />
        </button>
        <SpaceTile space={space} label={spaceLabel(space, sessions)} size={26} />
        <div className="wb-phone-title">
          <b>{spaceLabel(space, sessions)}</b>
          <span>
            <StatusDot status={counts.blocked ? "blocked" : counts.working ? "working" : "idle"} /> {summary}
          </span>
        </div>
        <span className="wb-font" role="group" aria-label="Text size">
          <button type="button" aria-label="Smaller text" onClick={() => changeFont(-1)} disabled={fontSize <= PHONE_FONT_MIN}>
            A<sup>−</sup>
          </button>
          <button type="button" aria-label="Larger text" onClick={() => changeFont(1)} disabled={fontSize >= PHONE_FONT_MAX}>
            A<sup>+</sup>
          </button>
        </span>
        <button type="button" className="wb-icon-btn wb-icon-btn-lg" aria-label="New terminal" onClick={props.onNewTerminal}>
          <Ic name="plus" size={18} />
        </button>
        {props.onAccounts ? (
          <button type="button" className="wb-icon-btn wb-icon-btn-lg" aria-label="Claude accounts" onClick={props.onAccounts}>
            <AgentLogo agent="claude" size={18} />
          </button>
        ) : null}
        <button
          type="button"
          className="wb-icon-btn wb-icon-btn-lg"
          aria-label="Space actions"
          onClick={(e) => props.onMenu({ kind: "space", spaceId: space.id, anchor: e.currentTarget })}
        >
          <Ic name="ellipsis" size={18} />
        </button>
      </header>
      <div className="wb-strip">
        <div className="wb-strip-scroll">
          {space.tabs.map((t) => {
            const s = tabStatus(t, sessions);
            return (
              <button
                key={t.id}
                type="button"
                className={"wb-chip" + (t.id === tab?.id ? " is-on" : "")}
                onClick={() => props.onSelectTab(space.id, t.id)}
                onContextMenu={(e) => {
                  e.preventDefault();
                  props.onMenu({ kind: "tab", spaceId: space.id, tabId: t.id, anchor: e.currentTarget });
                }}
                {...longPress((el) => props.onMenu({ kind: "tab", spaceId: space.id, tabId: t.id, anchor: el }))}
              >
                <ProgramIcon session={sessions[t.focus || tabSessions(t)[0] || ""]} size={14} />
                <span className="wb-tab-label">{tabLabel(t, sessions)}</span>
                {s === "working" || s === "blocked" ? <StatusDot status={s} /> : null}
              </button>
            );
          })}
        </div>
        <button type="button" className="wb-strip-count" aria-label="All tabs" onClick={() => setSheet(true)}>
          <span>{space.tabs.length}</span>
        </button>
      </div>
      {tab && current ? (
        <>
          {ids.length > 1 ? (
          <div className="wb-switcher">
            <button type="button" aria-label="Previous pane" onClick={() => go(-1)} disabled={ids.length < 2}>
              <Ic name="chevron-left" size={16} />
            </button>
            <ProgramIcon session={session} size={18} />
            <b>{programName(session)}</b>
            <LanaChip session={session} compact />
            <span className="wb-spacer" />
            {ids.length > 1 ? (
              <span className="wb-dots">
                {ids.map((id) => (
                  <i key={id} className={id === current ? "is-on" : ""} />
                ))}
              </span>
            ) : null}
            <span className="wb-switch-count">
              {index + 1}/{ids.length}
            </span>
            <button type="button" aria-label="Next pane" onClick={() => go(1)} disabled={ids.length < 2}>
              <Ic name="chevron-right" size={16} />
            </button>
          </div>
          ) : null}
          {!owner ? (
            <button type="button" className="wb-size-bar" onClick={() => claimRef.current?.()}>
              <Ic name="smartphone" size={14} />
              <span>Size follows another screen</span>
              <b>Fit to this screen</b>
            </button>
          ) : null}
          <div className="wb-phone-term">
            <TermPane
              key={current + ":" + (session?.pid ?? 0)}
              sessionId={current}
              mode="control"
              fontSize={fontSize}
              sendRef={sendRef}
              transformRef={transformRef}
              sessionSize={session ? { cols: session.cols, rows: session.rows } : undefined}
              claimRef={claimRef}
              keyboardRef={keyboardRef}
              onOwnerChange={setOwner}
            />
          </div>
          {status === "exited" ? (
            <div className="wb-phone-bar">
              <div>
                <b>Process exited {session?.exit_code ?? 0}</b>
              </div>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => props.onClose(current)}>
                Close
              </button>
              <button type="button" className="btn btn-primary btn-sm" onClick={() => props.onRestart(current)}>
                Restart
              </button>
            </div>
          ) : (
            <div className="wb-keybar">
              <div className="wb-keybar-keys">
                {["esc", "tab", "ctrl", "^C", "up", "down", "left", "right", "/"].map((k) => (
                  <button
                    key={k}
                    type="button"
                    className={k === "ctrl" && ctrl ? "is-on" : ""}
                    /* Keep the terminal focused so the phone keyboard stays up. */
                    onMouseDown={(e) => e.preventDefault()}
                    onPointerDown={(e) => e.preventDefault()}
                    onClick={() => key(k)}
                  >
                    {{ up: "↑", down: "↓", left: "←", right: "→", "^C": "⌃C" }[k] ?? k}
                  </button>
                ))}
              </div>
              <button
                type="button"
                className="wb-keybar-done"
                aria-label={viewport?.keyboard ? "Hide keyboard" : "Show keyboard"}
                onMouseDown={(e) => e.preventDefault()}
                onPointerDown={(e) => e.preventDefault()}
                onClick={() => (viewport?.keyboard ? keyboardRef.current?.blur() : keyboardRef.current?.focus())}
              >
                <Ic name={viewport?.keyboard ? "keyboard-off" : "keyboard"} size={16} />
              </button>
            </div>
          )}
        </>
      ) : (
        <div className="wb-empty">
          <p>No terminals in {spaceLabel(space, sessions)} yet.</p>
          <button type="button" className="btn btn-primary" onClick={props.onNewTerminal}>
            New terminal
          </button>
        </div>
      )}
      {sheet ? (
        <div className="wb-sheet-back" onClick={() => setSheet(false)}>
          <div className="wb-sheet" role="dialog" aria-label="All tabs" onClick={(e) => e.stopPropagation()}>
            <div className="wb-sheet-grip" />
            <div className="wb-sheet-head">
              <div>
                <b>{spaceLabel(space, sessions)}</b>
                <span>
                  {space.tabs.length} tabs · sorted by status
                </span>
              </div>
              <button
                type="button"
                className="btn btn-primary btn-sm"
                onClick={() => {
                  setSheet(false);
                  props.onNewTerminal();
                }}
              >
                <Ic name="plus" size={14} /> New terminal
              </button>
            </div>
            <div className="wb-sheet-grid">
              {[...space.tabs]
                .sort((a, b) => STATUS_ORDER.indexOf(tabStatus(a, sessions)) - STATUS_ORDER.indexOf(tabStatus(b, sessions)))
                .map((t) => {
                  const s = tabStatus(t, sessions);
                  const n = tabSessions(t).length;
                  const focusId = t.focus || tabSessions(t)[0];
                  const focus = focusId ? sessions[focusId] : undefined;
                  return (
                    <div key={t.id} className="wb-card-wrap">
                    <button
                      type="button"
                      className={"wb-card" + (t.id === tab?.id ? " is-on" : "")}
                      onClick={() => {
                        props.onSelectTab(space.id, t.id);
                        setSheet(false);
                      }}
                      {...longPress((el) => props.onMenu({ kind: "tab", spaceId: space.id, tabId: t.id, anchor: el }))}
                    >
                      <span className="wb-card-preview">
                        <ProgramIcon session={focus} size={18} />
                        <span>{focus?.program || "shell"}</span>
                      </span>
                      <span className="wb-card-foot">
                        <span>
                          <b>{tabLabel(t, sessions)}</b>
                          <span className={"wb-card-sub wb-card-sub-" + s}>
                            {s === "blocked" ? "needs you" : STATUS_LABEL[s].toLowerCase()} · {n} {n === 1 ? "pane" : "panes"}
                          </span>
                        </span>
                        <StatusDot status={s} />
                      </span>
                    </button>
                    <button
                      type="button"
                      className="wb-card-x"
                      aria-label={"Close tab " + tabLabel(t, sessions)}
                      onClick={() => props.onCloseTab(space.id, t.id)}
                    >
                      <Ic name="x" size={14} />
                    </button>
                    </div>
                  );
                })}
            </div>
          </div>
        </div>
      ) : null}
    </main>
  );
}

/* ------------------------------------------------------------------ */
/* Page                                                                */
/* ------------------------------------------------------------------ */

const PANEL_KEY = "lana.workbench.panel";
const PANEL_WIDTH_KEY = "lana.workbench.panelWidth";
const PANEL_WIDTH = 288;
const PANEL_MIN = 200;
const PANEL_MAX = 520;

function readPanelWidth(): number {
  try {
    const n = Number(window.localStorage.getItem(PANEL_WIDTH_KEY));
    return n >= PANEL_MIN && n <= PANEL_MAX ? n : PANEL_WIDTH;
  } catch {
    return PANEL_WIDTH;
  }
}

function savePanelWidth(width: number): void {
  try {
    window.localStorage.setItem(PANEL_WIDTH_KEY, String(Math.round(width)));
  } catch {
    /* Storage is optional. */
  }
}

function readPanelOpen(): boolean {
  try {
    return window.localStorage.getItem(PANEL_KEY) !== "closed";
  } catch {
    return true;
  }
}

export function WorkbenchPage() {
  const phone = useSyncExternalStore(subscribePhone, readPhone, () => false);
  const [panelOpen, setPanelOpen] = useState(readPanelOpen);
  const [panelWidth, setPanelWidth] = useState(readPanelWidth);
  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = panelWidth;
    let latest = startWidth;
    const move = (e: PointerEvent) => {
      latest = Math.min(PANEL_MAX, Math.max(PANEL_MIN, startWidth + e.clientX - startX));
      setPanelWidth(latest);
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      document.body.classList.remove("wb-resizing");
      savePanelWidth(latest);
    };
    document.body.classList.add("wb-resizing");
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  const togglePanel = () => {
    setPanelOpen((open) => {
      try {
        window.localStorage.setItem(PANEL_KEY, open ? "closed" : "open");
      } catch {
        /* Storage is optional. */
      }
      return !open;
    });
  };
  const [sessions, setSessions] = useState<Sessions>({});
  const sessionsRef = useRef(sessions);
  const chimedAt = useRef<Record<string, number>>({});
  const [soundOn, setSoundOn] = useState(soundEnabled);
  useEffect(() => {
    /* Browsers allow audio only after a gesture: unlock on the first one. */
    const unlock = () => unlockAudio();
    window.addEventListener("pointerdown", unlock);
    window.addEventListener("keydown", unlock);
    return () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", unlock);
    };
  }, []);
  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);
  const [layout, setLayoutState] = useState<WbLayout>(emptyLayout());
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [phoneView, setPhoneView] = useState<"home" | "space">("home");
  const layoutRef = useRef(layout);
  const savedRef = useRef("");
  const saveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const commit = useCallback((next: WbLayout) => {
    layoutRef.current = next;
    setLayoutState(next);
    clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      const json = JSON.stringify(next);
      if (json === savedRef.current) return;
      savedRef.current = json;
      putWorkbenchLayout(next).catch((err) => setError(errorMessage(err)));
    }, 250);
  }, []);

  const update = useCallback((fn: (l: WbLayout) => WbLayout) => commit(fn(layoutRef.current)), [commit]);

  const load = useCallback(async () => {
    try {
      const [list, rawLayout] = await Promise.all([listWorkbenchSessions(), getWorkbenchLayout()]);
      const map: Sessions = {};
      for (const s of list.sessions || []) map[s.id] = s;
      setSessions(map);
      let next = parseLayout(rawLayout);
      savedRef.current = JSON.stringify(next);
      /* Drop panes whose session no longer exists in the core. */
      const placed = next.spaces.flatMap((s) => s.tabs.flatMap(tabSessions));
      for (const id of placed) {
        if (!map[id]) next = removeSession(next, id);
      }
      if (!next.spaces.length) next = addSpace(next, "~", "");
      commit(next);
      setError("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoaded(true);
    }
  }, [commit]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch is async; state lands after the await.
    void load();
    const stop = subscribeWorkbench(
      (event) => {
        if (event.type === "layout.changed") {
          const json = JSON.stringify(event.layout);
          if (json === savedRef.current) return;
          const next = mergeRemote(layoutRef.current, parseLayout(event.layout));
          savedRef.current = json;
          layoutRef.current = next;
          setLayoutState(next);
          return;
        }
        if (event.type === "session.removed") {
          setSessions((prev) => {
            const next = { ...prev };
            delete next[event.session.id];
            return next;
          });
          return;
        }
        const before = sessionsRef.current[event.session.id];
        const after = event.session;
        if (before && after.agent) {
          const now = Date.now();
          const last = chimedAt.current[after.id] || 0;
          let kind: "done" | "needs" | null = null;
          if (!before.needs_you && after.needs_you) kind = "needs";
          else if (before.state === "working" && after.state === "idle") kind = "done";
          if (kind && now - last > 10000) {
            chimedAt.current[after.id] = now;
            chime(kind);
          }
        }
        setSessions((prev) => ({ ...prev, [event.session.id]: event.session }));
      },
      () => void load()
    );
    return () => {
      stop();
      clearTimeout(saveTimer.current);
    };
  }, [load]);

  const space = pickSpace(layout);
  const tab = pickTab(space);

  const placedIds = useMemo(() => new Set(layout.spaces.flatMap((s) => s.tabs.flatMap(tabSessions))), [layout]);
  const orphans = useMemo(() => Object.values(sessions).filter((s) => !placedIds.has(s.id)), [sessions, placedIds]);

  const create = useCallback(
    async (dir: string, command?: string[], title?: string) => {
      const s = await createWorkbenchSession({ dir: dir || undefined, command, title });
      setSessions((prev) => ({ ...prev, [s.id]: s }));
      return s;
    },
    []
  );

  /* herdr behaviour: a new terminal opens in the folder of the space's
     active terminal; in an empty space it opens in the home folder. */
  const newTerminal = useCallback(async () => {
    const sp = pickSpace(layoutRef.current);
    if (!sp) return;
    try {
      const s = await create(spaceCwd(sp, sessionsRef.current));
      update((l) => addTab(l, sp.id, s.id));
      setPhoneView("space");
    } catch (err) {
      setError(errorMessage(err));
    }
  }, [create, update]);

  /* "+" in Spaces: a new space with one terminal in the home folder, named
     after its folder as the terminal moves. */
  const newSpace = useCallback(async () => {
    try {
      const s = await create("");
      update((l) => {
        const next = addSpace(l, "~", "");
        return addTab(next, next.activeSpace || "", s.id);
      });
      setPhoneView("space");
    } catch (err) {
      setError(errorMessage(err));
    }
  }, [create, update]);

  const actions: PaneActions = {
    onFocus: (id) => {
      const sp = pickSpace(layoutRef.current);
      const t = pickTab(sp);
      if (sp && t && t.focus !== id) update((l) => focusPane(l, sp.id, t.id, id));
    },
    onSplit: async (id, dir) => {
      const sp = pickSpace(layoutRef.current);
      const t = pickTab(sp);
      if (!sp || !t) return;
      try {
        const s = await create(sessions[id]?.cwd || sp.dir);
        update((l) => splitPane(l, sp.id, t.id, id, s.id, dir));
      } catch (err) {
        setError(errorMessage(err));
      }
    },
    onZoom: (id) => {
      const sp = pickSpace(layoutRef.current);
      const t = pickTab(sp);
      if (sp && t) update((l) => toggleZoom(l, sp.id, t.id, id));
    },
    onClose: (id) => {
      update((l) => removeSession(l, id));
      killWorkbenchSession(id).catch((err) => setError(errorMessage(err)));
    },
    onRestart: (id) => {
      restartWorkbenchSession(id)
        .then((s) => setSessions((prev) => ({ ...prev, [s.id]: s })))
        .catch((err) => setError(errorMessage(err)));
    },
  };

  /* Spec 56: the Claude accounts plugin, on once claude-swap is installed. */
  const claudeTool = usePluginTool("claude-accounts");
  const accountsReady = toolReady(claudeTool);
  const [accountsAnchor, setAccountsAnchor] = useState<HTMLElement | null>(null);
  const [accountsSheet, setAccountsSheet] = useState(false);
  const accountActions: AccountActions = {
    newTab: (argv, title) => {
      const sp = pickSpace(layoutRef.current);
      if (!sp) return;
      create(spaceCwd(sp, sessionsRef.current), argv, title)
        .then((s) => {
          update((l) => addTab(l, sp.id, s.id, title));
          setPhoneView("space");
        })
        .catch((err) => setError(errorMessage(err)));
    },
    beside: (argv) => {
      const sp = pickSpace(layoutRef.current);
      const t = pickTab(sp);
      const target = t?.focus || (t ? tabSessions(t)[0] : undefined);
      if (!sp || !t || !target) {
        accountActions.newTab(argv, "claude");
        return;
      }
      const dir = sessionsRef.current[target]?.cwd || spaceCwd(sp, sessionsRef.current);
      create(dir, argv)
        .then((s) => update((l) => splitPane(l, sp.id, t.id, target, s.id, "row")))
        .catch((err) => setError(errorMessage(err)));
    },
  };
  const accountsOverlay =
    accountsReady && claudeTool ? (
      <>
        {accountsAnchor ? (
          <ClaudeAccountsPopover
            anchor={accountsAnchor}
            binary={claudeTool.binary_path}
            actions={accountActions}
            onClose={() => setAccountsAnchor(null)}
          />
        ) : null}
        {accountsSheet ? (
          <div className="wb-sheet-back" onClick={() => setAccountsSheet(false)}>
            <div className="wb-sheet" role="dialog" aria-label="Claude accounts" onClick={(e) => e.stopPropagation()}>
              <div className="wb-sheet-grip" />
              <ClaudeAccountsPanel binary={claudeTool.binary_path} actions={accountActions} onClose={() => setAccountsSheet(false)} />
            </div>
          </div>
        ) : null}
      </>
    ) : null;

  /* Rename and close (spaces and tabs). */
  const [menu, setMenu] = useState<MenuTarget | null>(null);
  const [dialog, setDialog] = useState<DialogState>(null);
  const findSpace = (id: string) => layoutRef.current.spaces.find((s) => s.id === id);
  const openRenameSpace = (spaceId: string) => {
    const sp = findSpace(spaceId);
    if (sp) setDialog({ kind: "rename-space", spaceId, value: sp.auto === false ? sp.name : "" });
  };
  const openRenameTab = (spaceId: string, tabId: string) => {
    const t = findSpace(spaceId)?.tabs.find((x) => x.id === tabId);
    if (t) setDialog({ kind: "rename-tab", spaceId, tabId, value: t.title || "" });
  };
  const closeTabNow = (spaceId: string, tabId: string) => {
    const t = findSpace(spaceId)?.tabs.find((x) => x.id === tabId);
    if (!t) return;
    const ids = tabSessions(t);
    update((l) => closeTab(l, spaceId, tabId));
    for (const id of ids) killWorkbenchSession(id).catch(() => {});
  };
  const closeSpaceNow = (spaceId: string) => {
    const sp = findSpace(spaceId);
    if (!sp) return;
    const ids = spaceSessions(sp);
    update((l) => {
      const next = removeSpace(l, spaceId);
      return next.spaces.length ? next : addSpace(next, "~", "");
    });
    for (const id of ids) killWorkbenchSession(id).catch(() => {});
  };
  const askCloseSpace = (spaceId: string) => {
    const sp = findSpace(spaceId);
    if (!sp) return;
    const n = spaceSessions(sp).length;
    if (n === 0) closeSpaceNow(spaceId);
    else setDialog({ kind: "close-space", spaceId, label: spaceLabel(sp, sessionsRef.current), terminals: n });
  };
  const overlays = (
    <>
      {menu ? (
        <ActionMenu
          target={menu}
          onClose={() => setMenu(null)}
          onRename={() => (menu.kind === "space" ? openRenameSpace(menu.spaceId) : openRenameTab(menu.spaceId, menu.tabId))}
          onCloseTarget={() => (menu.kind === "space" ? askCloseSpace(menu.spaceId) : closeTabNow(menu.spaceId, menu.tabId))}
        />
      ) : null}
      {dialog?.kind === "rename-space" ? (
        <RenameDialog
          title="Rename space"
          initial={dialog.value}
          placeholder={(() => {
            const sp = layout.spaces.find((x) => x.id === dialog.spaceId);
            return sp ? folderName(spaceCwd(sp, sessions)) : "";
          })()}
          hint="Leave it empty to name the space after its folder again."
          onCancel={() => setDialog(null)}
          onSave={(value) => {
            update((l) => renameSpace(l, dialog.spaceId, value));
            setDialog(null);
          }}
        />
      ) : null}
      {dialog?.kind === "rename-tab" ? (
        <RenameDialog
          title="Rename tab"
          initial={dialog.value}
          placeholder="e.g. dev server"
          hint="Leave it empty to show the program's name again."
          onCancel={() => setDialog(null)}
          onSave={(value) => {
            update((l) => renameTab(l, dialog.spaceId, dialog.tabId, value));
            setDialog(null);
          }}
        />
      ) : null}
      {dialog?.kind === "close-space" ? (
        <CloseSpaceDialog
          label={dialog.label}
          terminals={dialog.terminals}
          onCancel={() => setDialog(null)}
          onConfirm={() => {
            closeSpaceNow(dialog.spaceId);
            setDialog(null);
          }}
        />
      ) : null}
    </>
  );

  const openSession = (id: string) => {
    const where = locate(layoutRef.current, id);
    if (where) {
      update((l) => focusPane(selectTab(selectSpace(l, where.space.id), where.space.id, where.tab.id), where.space.id, where.tab.id, id));
    } else {
      const sp = pickSpace(layoutRef.current);
      if (sp) update((l) => addTab(l, sp.id, id));
    }
    setPhoneView("space");
  };

  const panel = (
    <Panel
      layout={layout}
      sessions={sessions}
      focusSession={tab?.focus}
      orphans={orphans}
      onSelectSpace={(id) => {
        update((l) => selectSpace(l, id));
        setPhoneView("space");
      }}
      onNewSpace={newSpace}
      onMenu={setMenu}
      onRenameSpace={openRenameSpace}
      soundOn={soundOn}
      onToggleSound={() => {
        const next = !soundOn;
        setSoundOn(next);
        setSoundEnabled(next);
        unlockAudio();
        if (next) setTimeout(() => chime("done"), 30);
      }}
      onOpenSession={openSession}
      onGrouping={(g) => update((l) => setGrouping(l, g))}
      onAdoptOrphans={() => {
        const sp = pickSpace(layoutRef.current);
        if (!sp) return;
        update((l) => orphans.reduce((acc, s) => addTab(acc, sp.id, s.id), l));
      }}
      {...(phone
        ? {}
        : {
            onCollapse: togglePanel,
            width: panelWidth,
            onResizeStart: startResize,
            onResizeReset: () => {
              setPanelWidth(PANEL_WIDTH);
              savePanelWidth(PANEL_WIDTH);
            },
          })}
    />
  );

  const banner = error ? (
    <div className="wb-error" role="alert">
      {error}
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => void load()}>
        Retry
      </button>
    </div>
  ) : null;

  if (phone) {
    return (
      <>
        {banner}
        {overlays}
        {accountsOverlay}
        <PhoneWorkbench
          layout={layout}
          sessions={sessions}
          panel={panel}
          view={phoneView}
          setView={setPhoneView}
          onNewTerminal={newTerminal}
          onNewSpace={newSpace}
          onMenu={setMenu}
          onCloseTab={closeTabNow}
          onAccounts={accountsReady ? () => setAccountsSheet(true) : undefined}
          onSelectTab={(sid, tid) => update((l) => selectTab(l, sid, tid))}
          onFocus={(sid, tid, id) => update((l) => focusPane(l, sid, tid, id))}
          onRestart={actions.onRestart}
          onClose={actions.onClose}
        />
      </>
    );
  }

  const zoomed = tab?.zoom && tab && paneSessions(tab.root).includes(tab.zoom) ? tab.zoom : undefined;
  const attached = tab ? tabSessions(tab).reduce((n, id) => Math.max(n, sessions[id]?.attached || 0), 0) : 0;

  return (
    <main id="main" className="wb-page">
      {overlays}
      {accountsOverlay}
      {panelOpen ? panel : null}
      <section className="wb-main">
        {banner}
        <div className="wb-tabs" role="tablist">
          {!panelOpen ? (
            <button
              type="button"
              className="wb-tab-new wb-panel-toggle"
              aria-label="Show spaces and agents"
              data-tip="Show panel"
              onClick={togglePanel}
            >
              <Ic name="panel-left" size={16} />
            </button>
          ) : null}
          {!panelOpen && space ? (
            <span className="wb-tabs-space">
              <SpaceTile space={space} label={spaceLabel(space, sessions)} size={18} />
              {spaceLabel(space, sessions)}
            </span>
          ) : null}
          {space?.tabs.map((t) => {
            const s = tabStatus(t, sessions);
            return (
              <div
                key={t.id}
                className={"wb-tab" + (t.id === tab?.id ? " is-on" : "")}
                role="tab"
                aria-selected={t.id === tab?.id}
                onContextMenu={(e) => {
                  e.preventDefault();
                  if (space) setMenu({ kind: "tab", spaceId: space.id, tabId: t.id, anchor: e.currentTarget });
                }}
              >
                <button
                  type="button"
                  className="wb-tab-btn"
                  onClick={() => space && update((l) => selectTab(l, space.id, t.id))}
                  onDoubleClick={() => space && openRenameTab(space.id, t.id)}
                  title={tabLabel(t, sessions)}
                >
                  <ProgramIcon session={sessions[t.focus || tabSessions(t)[0] || ""]} size={15} />
                  <span className="wb-tab-label">{tabLabel(t, sessions)}</span>
                  {s === "working" || s === "blocked" ? <StatusDot status={s} /> : null}
                </button>
                <button
                  type="button"
                  className="wb-tab-x"
                  aria-label="Close tab"
                  onClick={() => space && closeTabNow(space.id, t.id)}
                >
                  <Ic name="x" size={12} />
                </button>
              </div>
            );
          })}
          <button type="button" className="wb-tab-new" aria-label="New terminal" onClick={newTerminal}>
            <Ic name="plus" size={15} />
          </button>
          <span className="wb-spacer" />
          {accountsReady ? (
            <button
              type="button"
              className="wb-acc-btn"
              aria-label="Claude accounts"
              data-tip="Claude accounts"
              onClick={(e) => setAccountsAnchor(accountsAnchor ? null : e.currentTarget)}
            >
              <AgentLogo agent="claude" size={16} />
              <span>Accounts</span>
            </button>
          ) : null}
          {tab ? (
            <span className="wb-attached">
              <StatusDot status="working" /> {attached > 1 ? "Attached here · " + attached + " clients" : "Attached here"}
            </span>
          ) : null}
        </div>
        <div className="wb-workspace">
          {!loaded ? (
            <p className="wb-empty-note">Opening Workbench…</p>
          ) : tab ? (
            zoomed ? (
              <PaneView id={zoomed} session={sessions[zoomed]} focused zoomed actions={actions} />
            ) : (
              <PaneTree node={tab.root} tab={tab} sessions={sessions} actions={actions} />
            )
          ) : (
            <div className="wb-empty">
              <Ic name="terminal" size={28} />
              <p>{space ? "No terminals in " + spaceLabel(space, sessions) + " yet." : "No space yet."}</p>
              <button type="button" className="btn btn-primary" onClick={newTerminal}>
                New terminal
              </button>
            </div>
          )}
        </div>
      </section>
    </main>
  );
}
