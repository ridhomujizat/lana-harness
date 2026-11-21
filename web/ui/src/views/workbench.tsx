import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";

import { errorMessage, ApiError } from "../api/client";
import {
  createWorkbenchSession,
  getWorkbenchLayout,
  killWorkbenchSession,
  listWorkbenchSessions,
  previewWorkbenchFile,
  putWorkbenchLayout,
  restartWorkbenchSession,
  subscribeWorkbench,
  type WorkbenchPreview,
  type WorkbenchSession,
} from "../api/workbench";
import { Ic } from "../ui/icons";
import {
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
  focusAfterClose,
  focusPane,
  forgetFocus,
  isAgent,
  noteFocus,
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
  statusOf,
  tabSessions,
  toggleZoom,
  paneInDirection,
  tabAtShortcut,
  workbenchShortcut,
  type PaneNode,
  type WbShortcut,
  type Status,
  type WbLayout,
  type WbSpace,
  type WbTab,
} from "./workbench-model";
import { TermPane, type SendInput } from "./workbench-term";
import { prune as pruneTermCache } from "./workbench-term-cache";
import { WbGitContext, useWbGit, useWorkbenchGit } from "./workbench-git-state";
import { AgentLogo } from "./agent-logo";
import { ProgramTile, TabTiles } from "./workbench-tile";
import { ClaudeAccountsPanel, ClaudeAccountsPopover, type AccountActions } from "./workbench-accounts";
import { toolReady, usePluginTool } from "./plugins";
import { chime, setSoundEnabled, soundEnabled, unlockAudio } from "./workbench-sound";
import { ActionMenu, CloseSpaceDialog, RenameDialog, longPress, type DialogState, type MenuTarget } from "./workbench-actions";
import { Menu, MenuItem, MenuSeparator } from "../ui/gr8r";
import { LAYOUT_MOTION_MS, holdLayout, reducedMotion } from "../app/layout-motion";
import { PaneAccessContext, RemoteControlBar, usePaneAccess, useRemoteControl, type PaneAccess } from "./workbench-remote";

/* Spec 55: the Workbench page. Desktop: panel (Spaces + Agents), tab strip,
   split panes (REQ-WB-051..054). Phone: home, space view with one pane at a
   time, all-tabs sheet, read-only until Take over (REQ-WB-055). */

const PHONE_QUERY = "(max-width: 640px)";

/* Spec 61 REQ-WP-023: how long a pane fades in and out. The close delay is a
   little longer than the CSS fade, so the fade is seen before the kill goes. */
const ENTER_FADE_MS = 200;
const CLOSE_FADE_MS = 170;

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

/* Spec 60 REQ-WR-041: the session came back after a restart. */
function RestoredChip({ session }: { session?: WorkbenchSession }) {
  if (!session?.restored || session.state === "exited") return null;
  const tip = session.resumed_agent ? "Restored after restart · resumed " + session.resumed_agent : "Restored after restart";
  return (
    <span className="wb-restored-chip" title={tip}>
      <Ic name="history" size={11} />
      Restored
    </span>
  );
}

/* Spec 66 REQ-WG-074: a space whose sessions use several worktrees shows
   their colours and "N worktrees · M terminals"; otherwise its tab count. */
function SpaceText({ space, sessions, label }: { space: WbSpace; sessions: Sessions; label: string }) {
  const git = useWbGit();
  const ids = space.tabs.flatMap(tabSessions);
  const trees = new Map<string, string>();
  for (const id of ids) {
    const info = git?.gitOf(id);
    if (info) trees.set(info.worktree, info.color);
  }
  void sessions;
  if (trees.size > 1) {
    return (
      <span className="wbg-space-text">
        <span className="wb-space-name">{label}</span>
        <span className="wbg-space-line">
          <span className="wbg-dots">
            {Array.from(trees.entries()).map(([w, c]) => (
              <i key={w} className="wbg-wt-dot" style={{ background: c }} />
            ))}
          </span>
          <span className="wbg-space-line-text">
            {trees.size} worktrees · {ids.length} {ids.length === 1 ? "terminal" : "terminals"}
          </span>
        </span>
      </span>
    );
  }
  return (
    <>
      <span className="wb-space-name">{label}</span>
      <span className="wb-space-meta">{space.tabs.length ? space.tabs.length + (space.tabs.length === 1 ? " tab" : " tabs") : ""}</span>
    </>
  );
}

/* Spec 66 REQ-WG-073: hovering a tab whose panes use more than one worktree
   shows which pane works where. Fixed, so the tab strip does not clip it. */
function TabWorktreeCard({ tabId, rect, tab, sessions }: { tabId: string; rect: DOMRect; tab: WbTab | undefined; sessions: Sessions }) {
  const git = useWbGit();
  if (!tab || tab.id !== tabId || !git) return null;
  const ids = tabSessions(tab);
  const groups = new Map<string, { color: string; short: string; items: string[] }>();
  for (const id of ids) {
    const info = git.gitOf(id);
    if (!info) continue;
    const g = groups.get(info.worktree) ?? { color: info.color, short: info.short, items: [] };
    const st = statusOf(sessions[id]);
    g.items.push(programName(sessions[id]) + " · " + (st === "blocked" ? "needs you" : st));
    groups.set(info.worktree, g);
  }
  if (groups.size < 2) return null;
  return (
    <div className="wbg-tab-card" style={{ left: rect.left, top: rect.bottom + 6 }} role="tooltip">
      <div className="wbg-tab-card-head">
        {ids.length} panes · {groups.size} worktrees
      </div>
      {Array.from(groups.entries()).map(([w, g]) => (
        <div key={w} className="wbg-tab-card-row">
          <i className="wbg-wt-dot" style={{ background: g.color }} />
          <span className="mono">{g.short}</span>
          <span className="wbg-tab-card-items">
            {g.items.map((it, i) => (
              <span key={i}>{it}</span>
            ))}
          </span>
        </div>
      ))}
    </div>
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
  /* The panel is sliding open ("in") or closed ("out"). */
  motion?: "in" | "out" | null;
  onResizeStart?: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizeReset?: () => void;
}

function Panel(props: PanelProps) {
  const { layout, sessions, focusSession } = props;
  const grouping = layout.grouping || "space";
  const space = pickSpace(layout);

  const agents = Object.values(sessions).filter(isAgent);
  const spaceOf = (id: string) => locate(layout, id)?.space;

  const git = useWbGit();
  const row = (s: WorkbenchSession, showSpace: boolean) => {
    const owner = s.labels?.["lana.agent"];
    const sp = spaceOf(s.id);
    const wt = git?.gitOf(s.id);
    return (
      <button
        key={s.id}
        type="button"
        className={"wb-agent" + (s.id === focusSession ? " is-active" : "")}
        onClick={() => props.onOpenSession(s.id)}
      >
        <ProgramTile session={s} size="pane" />
        <span className="wb-agent-label">
          {showSpace ? <b>{sp ? spaceLabel(sp, sessions) : "unplaced"}</b> : null}
          <span className={showSpace ? "wb-agent-title muted" : "wb-agent-title"}>
            {showSpace ? " · " : ""}
            {sessionTitle(s)}
          </span>
          {wt ? (
            <span className="wbg-agent-wt" title={wt.branch}>
              <i className="wbg-wt-dot" style={{ background: wt.color }} />
              {wt.short}
            </span>
          ) : null}
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
    <aside
      className={"wb-panel" + (props.motion === "in" ? " is-entering" : props.motion === "out" ? " is-leaving" : "")}
      aria-label="Workbench"
      style={
        props.width
          ? ({ width: props.width, flexBasis: props.width, "--wb-panel-w": props.width + "px" } as CSSProperties)
          : undefined
      }
    >
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
              <SpaceText space={sp} sessions={sessions} label={spaceLabel(sp, sessions)} />
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
  /* Spec 61 REQ-WP-010: a file path was activated in a terminal. */
  onOpenFile: (id: string, link: string, fallback?: string) => void;
}

function PaneView({
  id,
  session,
  focused,
  zoomed,
  closing,
  entering,
  actions,
  fontSize,
}: {
  id: string;
  session?: WorkbenchSession;
  focused: boolean;
  zoomed: boolean;
  closing?: boolean;
  entering?: boolean;
  actions: PaneActions;
  fontSize?: number;
}) {
  const access = usePaneAccess();
  const status = statusOf(session);
  /* Spec 66 REQ-WG-071, 075: the pane's worktree, and a warning when another
     agent works in the same one. */
  const git = useWbGit();
  const info = git?.gitOf(id);
  const shared = git?.shared.get(id);
  return (
    <div
      className={"wb-pane" + (focused ? " is-focused" : "") + (closing ? " is-closing" : "") + (entering ? " is-entering" : "") + (info ? " has-wt" : "")}
      data-session={id}
      style={info ? ({ "--wb-wt": info.color } as CSSProperties) : undefined}
    >
      <div className="wb-pane-head" onMouseDown={() => actions.onFocus(id)}>
        <ProgramTile session={session} size="pane" />
        <span className="wb-pane-title">{programName(session)}</span>
        {info ? (
          <span className={"wbg-pane-chip" + (shared?.length ? " is-shared" : "")} title={info.branch + " · " + info.worktree}>
            <i className="wbg-wt-dot" style={{ background: info.color }} />
            <span className="wbg-pane-chip-label">{info.short}</span>
          </span>
        ) : null}
        {info && shared?.length && git ? (
          <button
            type="button"
            className="wbg-shared"
            title="Two agents are editing this worktree"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={() => git.onShared(id)}
          >
            <Ic name="triangle-alert" size={12} />
            <span>shared with {git.describe(shared[0] ?? "")}</span>
          </button>
        ) : null}
        {session?.cwd || session?.dir ? (
          <span className="wb-pane-dir">{(session.cwd || session.dir).replace(/^\/Users\/[^/]+/, "~")}</span>
        ) : null}
        <LanaChip session={session} />
        <RestoredChip session={session} />
        <span className={"wb-pane-state wb-pane-state-" + status}>
          <StatusDot status={status} />
          {status === "exited" ? "exited " + (session?.exit_code ?? 0) : status === "blocked" ? "needs you" : status}
        </span>
        <span className="wb-pane-tools">
          <button type="button" aria-label="Split right" data-tip="Split right ⌘D" onClick={() => actions.onSplit(id, "row")}>
            <Ic name="columns-2" size={14} />
          </button>
          <button type="button" aria-label="Split down" data-tip="Split down ⇧⌘D" onClick={() => actions.onSplit(id, "col")}>
            <Ic name="rows-2" size={14} />
          </button>
          <button type="button" aria-label={zoomed ? "Unzoom" : "Zoom"} data-tip={zoomed ? "Unzoom ⇧⌘↩" : "Zoom ⇧⌘↩"} onClick={() => actions.onZoom(id)}>
            <Ic name={zoomed ? "minimize-2" : "maximize-2"} size={14} />
          </button>
          <button type="button" aria-label="Close" data-tip="Close ⌘W" onClick={() => actions.onClose(id)}>
            <Ic name="x" size={14} />
          </button>
        </span>
      </div>
      <div className="wb-pane-body">
        {session ? (
          <TermPane
            key={id + ":" + session.pid + ":" + access.mode}
            sessionId={id}
            mode={access.mode}
            pid={session.pid}
            onControlLost={access.onControlLost}
            focused={focused}
            fontSize={fontSize}
            onFocus={() => actions.onFocus(id)}
            onOpenFile={actions.onOpenFile}
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

function PaneTree({
  node,
  tab,
  sessions,
  actions,
  closing,
  entering,
  fontSize,
}: {
  node: PaneNode;
  tab: WbTab;
  sessions: Sessions;
  actions: PaneActions;
  closing: ReadonlySet<string>;
  entering: ReadonlySet<string>;
  fontSize?: number;
}) {
  if (node.type === "pane") {
    return (
      <PaneView
        id={node.session}
        session={sessions[node.session]}
        focused={tab.focus === node.session}
        zoomed={tab.zoom === node.session}
        closing={closing.has(node.session)}
        entering={entering.has(node.session)}
        actions={actions}
        fontSize={fontSize}
      />
    );
  }
  return (
    <div className={"wb-split wb-split-" + node.dir}>
      {node.children.map((child, i) => (
        <PaneTree
          key={child.type === "pane" ? child.session : "s" + i}
          node={child}
          tab={tab}
          sessions={sessions}
          actions={actions}
          closing={closing}
          entering={entering}
          fontSize={fontSize}
        />
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
  /* Spec 61 REQ-WP-010: a file path was tapped in a terminal. */
  onOpenFile: (id: string, link: string, fallback?: string) => void;
  /* Spec 66 REQ-WG-090: the Changes button and its count. */
  onChanges: () => void;
  changeCount: number;
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

/* The phone text size is shared by the terminal and the space menu, where
   A−/A+ live since spec 66 (REQ-WG-090, D3). */
const phoneFontListeners = new Set<() => void>();

function subscribePhoneFont(fn: () => void): () => void {
  phoneFontListeners.add(fn);
  return () => phoneFontListeners.delete(fn);
}

function changePhoneFont(delta: number): void {
  const next = Math.min(PHONE_FONT_MAX, Math.max(PHONE_FONT_MIN, readPhoneFont() + delta));
  try {
    window.localStorage.setItem(PHONE_FONT_KEY, String(next));
  } catch {
    /* Storage is optional. */
  }
  for (const fn of phoneFontListeners) fn();
}

function usePhoneFont(): number {
  return useSyncExternalStore(subscribePhoneFont, readPhoneFont, () => PHONE_FONT);
}

/* REQ-WG-090: the Text size stepper and Files, at the top of the phone's
   space menu. */
function PhoneMenuExtra({ onFiles, onDone }: { onFiles: () => void; onDone: () => void }) {
  const size = usePhoneFont();
  return (
    <>
      <div className="wbg-font-row">
        <span>Text size</span>
        <span className="wb-font" role="group" aria-label="Text size">
          <button type="button" aria-label="Smaller text" onClick={() => changePhoneFont(-1)} disabled={size <= PHONE_FONT_MIN}>
            A<sup>−</sup>
          </button>
          <b>{size}</b>
          <button type="button" aria-label="Larger text" onClick={() => changePhoneFont(1)} disabled={size >= PHONE_FONT_MAX}>
            A<sup>+</sup>
          </button>
        </span>
      </div>
      <button
        type="button"
        role="menuitem"
        className="mi"
        onClick={() => {
          onDone();
          onFiles();
        }}
      >
        <Ic name="files" size={15} />
        <span className="trunc">Files</span>
      </button>
      <div className="msep" role="separator" />
    </>
  );
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
  const access = usePaneAccess();
  const { layout, sessions, view } = props;
  const space = pickSpace(layout);
  const tab = pickTab(space);
  const [sheet, setSheet] = useState(false);
  const [ctrl, setCtrl] = useState(false);
  const fontSize = usePhoneFont();
  const keyboardRef = useRef<{ focus: () => void; blur: () => void } | null>(null);
  const viewport = useVisualViewport();
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
        <button type="button" className="wbg-toggle wbg-toggle-lg" aria-label="Files and changes" onClick={props.onChanges}>
          <Ic name="git-compare" size={18} />
          {props.changeCount ? <span className="wbg-badge">{props.changeCount}</span> : null}
        </button>
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
                <TabTiles ids={tabSessions(t)} focus={t.focus} sessions={sessions} />
                <span className="wb-tab-label">{tabLabel(t, sessions)}</span>
                {s === "working" || s === "blocked" || s === "done" ? <StatusDot status={s} /> : null}
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
            <ProgramTile session={session} size="pane" />
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
              key={current + ":" + (session?.pid ?? 0) + ":" + access.mode}
              sessionId={current}
              mode={access.mode}
              pid={session?.pid ?? 0}
              onControlLost={access.onControlLost}
              fontSize={fontSize}
              sendRef={sendRef}
              transformRef={transformRef}
              sessionSize={session ? { cols: session.cols, rows: session.rows } : undefined}
              claimRef={claimRef}
              keyboardRef={keyboardRef}
              onOwnerChange={setOwner}
              onOpenFile={props.onOpenFile}
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
                        <ProgramTile session={focus} size="pane" />
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

/* A daemon message as a sentence: "no such file: …" -> "No such file: …". */
function capitalize(text: string): string {
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : text;
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

/* herdr's "done" set, kept per browser so a reload keeps the blue dots. */
const DONE_KEY = "lana.workbench.done";

function readDone(): Set<string> {
  try {
    const raw = JSON.parse(window.localStorage.getItem(DONE_KEY) || "[]");
    return new Set(Array.isArray(raw) ? raw.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

function saveDone(ids: Set<string>): void {
  try {
    window.localStorage.setItem(DONE_KEY, JSON.stringify([...ids]));
  } catch {
    /* Storage is optional. */
  }
}

function dropDone(ids: Set<string>, id: string): Set<string> {
  if (!ids.has(id)) return ids;
  const next = new Set(ids);
  next.delete(id);
  return next;
}

function readWindowActive(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "visible" && document.hasFocus();
}

/* The owner is looking at a session: it is the focused pane of the shown tab
   and the window is in front. */
function inView(layout: WbLayout, id: string): boolean {
  const t = pickTab(pickSpace(layout));
  return !!t && t.focus === id && readWindowActive();
}

/* Desktop terminal text size (⌘+ ⌘− ⌘0). */
const DESK_FONT_KEY = "lana.workbench.fontSize";
const DESK_FONT = 13;
const DESK_FONT_MIN = 9;
const DESK_FONT_MAX = 24;

function readDeskFont(): number {
  try {
    const n = Number(window.localStorage.getItem(DESK_FONT_KEY));
    return n >= DESK_FONT_MIN && n <= DESK_FONT_MAX ? n : DESK_FONT;
  } catch {
    return DESK_FONT;
  }
}

function saveDeskFont(n: number): void {
  try {
    window.localStorage.setItem(DESK_FONT_KEY, String(n));
  } catch {
    /* Storage is optional. */
  }
}

/* The pane boxes on screen, for ⌥⌘ arrows. */
function paneRects(): { id: string; left: number; top: number; right: number; bottom: number }[] {
  return Array.from(document.querySelectorAll<HTMLElement>(".wb-workspace .wb-pane[data-session]")).map((el) => {
    const r = el.getBoundingClientRect();
    return { id: el.dataset.session || "", left: r.left, top: r.top, right: r.right, bottom: r.bottom };
  });
}

export function WorkbenchPage() {
  const phone = useSyncExternalStore(subscribePhone, readPhone, () => false);
  /* Spec 57 (REQ-RA-085): a remote browser attaches in view mode until it
     unlocks control. */
  const remoteControl = useRemoteControl();
  const refreshControl = remoteControl.refresh;
  const paneAccess = useMemo<PaneAccess>(
    () => ({
      mode: remoteControl.canControl ? "control" : "view",
      onControlLost: refreshControl,
    }),
    [remoteControl.canControl, refreshControl]
  );
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
  /* The panel slides: opening mounts it and grows it from zero; closing
     shrinks it to zero and unmounts it when the slide ends. */
  const [panelMotion, setPanelMotion] = useState<"in" | "out" | null>(null);
  const panelTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(panelTimer.current), []);
  const togglePanel = () => {
    const next = !panelOpen || panelMotion === "out";
    try {
      window.localStorage.setItem(PANEL_KEY, next ? "open" : "closed");
    } catch {
      /* Storage is optional. */
    }
    clearTimeout(panelTimer.current);
    if (reducedMotion()) {
      setPanelMotion(null);
      setPanelOpen(next);
      return;
    }
    holdLayout();
    setPanelOpen(true);
    setPanelMotion(next ? "in" : "out");
    panelTimer.current = setTimeout(() => {
      setPanelMotion(null);
      if (!next) setPanelOpen(false);
    }, LAYOUT_MOTION_MS);
  };
  const [liveSessions, setSessions] = useState<Sessions>({});
  const sessionsRef = useRef(liveSessions);
  /* herdr's "done": agents that finished while their pane was not in view.
     Kept per browser; cleared when the pane is focused in a focused window. */
  const [doneIds, setDoneIds] = useState<Set<string>>(readDone);
  useEffect(() => saveDone(doneIds), [doneIds]);
  const sessions = useMemo<Sessions>(() => {
    if (doneIds.size === 0) return liveSessions;
    const next: Sessions = { ...liveSessions };
    for (const id of doneIds) {
      const s = next[id];
      if (s) next[id] = { ...s, done: true };
    }
    return next;
  }, [liveSessions, doneIds]);
  const [windowActive, setWindowActive] = useState(readWindowActive);
  useEffect(() => {
    const sync = () => setWindowActive(readWindowActive());
    window.addEventListener("focus", sync);
    window.addEventListener("blur", sync);
    document.addEventListener("visibilitychange", sync);
    return () => {
      window.removeEventListener("focus", sync);
      window.removeEventListener("blur", sync);
      document.removeEventListener("visibilitychange", sync);
    };
  }, []);
  const [deskFont, setDeskFont] = useState(readDeskFont);
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
    sessionsRef.current = liveSessions;
  }, [liveSessions]);
  const [layout, setLayoutState] = useState<WbLayout>(emptyLayout());
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  /* A preview that could not open (spec 61 REQ-WP-011): a notice with a close
     button, not the load error's Retry; it also goes away on its own. */
  const [previewNotice, setPreviewNotice] = useState("");
  useEffect(() => {
    if (!previewNotice) return;
    const timer = window.setTimeout(() => setPreviewNotice(""), 6000);
    return () => window.clearTimeout(timer);
  }, [previewNotice]);
  /* Spec 61 REQ-WP-012: one previewed file at a time; a new activation
     replaces it. The panel itself, not the shared layout document. */
  /* Spec 66 REQ-WG-063: a resolved ⌘+click goes to the git panel's Files tab
     (set once the git hook below exists). */
  const showPreviewRef = useRef<(sessionId: string, data: WorkbenchPreview) => void>(() => {});
  const [phoneView, setPhoneView] = useState<"home" | "space">("home");
  const layoutRef = useRef(layout);
  const savedRef = useRef("");
  const saveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  /* Spec 61 REQ-WP-022: the pane focus history of each tab, kept in memory, so
     closing the focused pane gives the focus back to the pane the user used
     before it. Spec 61 REQ-WP-023: a pane fades in when it is made and out
     when it is closed. */
  const focusHistory = useRef<Map<string, string[]>>(new Map());
  const lastFocus = useRef<Map<string, string>>(new Map());
  const [closing, setClosing] = useState<ReadonlySet<string>>(() => new Set<string>());
  const [entering, setEntering] = useState<ReadonlySet<string>>(() => new Set<string>());
  const closingRef = useRef(closing);
  useEffect(() => {
    closingRef.current = closing;
  }, [closing]);
  const fadeTimers = useRef<Set<ReturnType<typeof setTimeout>>>(new Set());
  useEffect(
    () => () => {
      for (const timer of fadeTimers.current) clearTimeout(timer);
    },
    []
  );
  const forgetSession = useCallback((sessionId: string) => {
    for (const [tabId, ids] of focusHistory.current) {
      const kept = forgetFocus(ids, sessionId);
      if (kept.length) focusHistory.current.set(tabId, kept);
      else focusHistory.current.delete(tabId);
    }
  }, []);
  const markEntering = useCallback((id: string) => {
    setEntering((prev) => (prev.has(id) ? prev : new Set(prev).add(id)));
    const timer = setTimeout(() => {
      fadeTimers.current.delete(timer);
      setEntering((prev) => {
        if (!prev.has(id)) return prev;
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    }, ENTER_FADE_MS);
    fadeTimers.current.add(timer);
  }, []);

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

  /* Spec 61 REQ-WP-022: record the focused pane of every tab, so closing one
     can give the focus back to the pane the user used before it. */
  useEffect(() => {
    for (const sp of layout.spaces) {
      for (const t of sp.tabs) {
        const f = t.focus;
        if (!f || lastFocus.current.get(t.id) === f) continue;
        lastFocus.current.set(t.id, f);
        focusHistory.current.set(t.id, noteFocus(focusHistory.current.get(t.id) ?? [], f));
      }
    }
  }, [layout]);

  /* Spec 61 REQ-WP-011: ask the daemon to read a path a terminal pointed at.
     The space's folder rides along as the relative-path fallback. A 404/403
     shows a short notice and opens nothing. */
  const openPreview = useCallback((sessionId: string, link: string, fallback?: string) => {
    const where = locate(layoutRef.current, sessionId);
    const dir = where ? spaceCwd(where.space, sessionsRef.current) || where.space.dir : "";
    /* A path joined from wrapped lines falls back to the piece that was
       clicked; when both are missing, the notice names the joined path. */
    previewWorkbenchFile(sessionId, link, dir)
      .catch((err) => {
        if (!fallback || !(err instanceof ApiError) || err.status !== 404) throw err;
        return previewWorkbenchFile(sessionId, fallback, dir).catch(() => {
          throw err;
        });
      })
      .then((data) => {
        setPreviewNotice("");
        showPreviewRef.current(sessionId, data);
      })
      .catch((err) => {
        if (err instanceof ApiError && err.status === 404) {
          /* The daemon's message names the path it tried. */
          setPreviewNotice(err.message ? capitalize(err.message) : "No such file.");
        } else if (err instanceof ApiError && err.status === 403) {
          setPreviewNotice("That file is not allowed.");
        } else {
          setPreviewNotice(errorMessage(err));
        }
      });
  }, []);

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
          /* Spec 61 REQ-WP-022: a session that goes away gives its tab's focus
             to the pane the user used before it, the same as a close. */
          const gone = event.session.id;
          const where = locate(layoutRef.current, gone);
          if (where) {
            const next = focusAfterClose(
              focusHistory.current.get(where.tab.id) ?? [],
              tabSessions(where.tab),
              where.tab.focus,
              gone,
              closingRef.current
            );
            update((l) => {
              const moved =
                where.tab.focus === gone && next && next !== gone
                  ? focusPane(l, where.space.id, where.tab.id, next)
                  : l;
              return removeSession(moved, gone);
            });
          }
          forgetSession(gone);
          setSessions((prev) => {
            const next = { ...prev };
            delete next[event.session.id];
            return next;
          });
          setDoneIds((prev) => dropDone(prev, event.session.id));
          return;
        }
        const before = sessionsRef.current[event.session.id];
        const after = event.session;
        if (after.state === "working" || after.state === "exited" || after.needs_you) {
          setDoneIds((prev) => dropDone(prev, after.id));
        } else if (before?.state === "working" && after.state === "idle" && isAgent(after) && !inView(layoutRef.current, after.id)) {
          setDoneIds((prev) => (prev.has(after.id) ? prev : new Set(prev).add(after.id)));
        }
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
  }, [load, update, forgetSession]);

  const space = pickSpace(layout);
  const tab = pickTab(space);
  const viewFocus = tab?.focus;
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- dropDone returns the same set when nothing changes.
    if (windowActive && viewFocus) setDoneIds((prev) => dropDone(prev, viewFocus));
  }, [windowActive, viewFocus]);

  const placedIds = useMemo(() => new Set(layout.spaces.flatMap((s) => s.tabs.flatMap(tabSessions))), [layout]);
  const orphans = useMemo(() => Object.values(sessions).filter((s) => !placedIds.has(s.id)), [sessions, placedIds]);
  /* Spec 55: keep a terminal alive only while its session is in the layout. A
     session that leaves the layout (closed, killed, removed) drops its cached
     terminal; switching tabs or spaces leaves this set unchanged, so those panes
     stay alive. */
  useEffect(() => {
    pruneTermCache(placedIds);
  }, [placedIds]);

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
      /* REQ-WP-021: the new terminal takes the focus and the keyboard at once;
         REQ-WP-023: it fades in. */
      markEntering(s.id);
      update((l) => addTab(l, sp.id, s.id));
      setPhoneView("space");
    } catch (err) {
      setError(errorMessage(err));
    }
  }, [create, update, markEntering]);

  /* "+" in Spaces: a new space with one terminal in the home folder, named
     after its folder as the terminal moves. */
  const newSpace = useCallback(async () => {
    try {
      const s = await create("");
      markEntering(s.id);
      update((l) => {
        const next = addSpace(l, "~", "");
        return addTab(next, next.activeSpace || "", s.id);
      });
      setPhoneView("space");
    } catch (err) {
      setError(errorMessage(err));
    }
  }, [create, update, markEntering]);

  /* Spec 66: Files, Changes and Worktrees. A session started from the panel
     opens as a new tab of the active space (model A). */
  const gitOpenTab = (dir: string, program: "claude" | "shell") => {
    const sp = pickSpace(layoutRef.current);
    if (!sp) return;
    create(dir, program === "claude" ? ["claude"] : undefined, program === "claude" ? "Claude Code" : undefined)
      .then((s) => {
        markEntering(s.id);
        update((l) => addTab(l, sp.id, s.id));
        setPhoneView("space");
      })
      .catch((err) => setError(errorMessage(err)));
  };
  const git = useWorkbenchGit({ layout, sessions, space, tab, phone, openTab: gitOpenTab });
  useEffect(() => {
    showPreviewRef.current = git.showPreview;
  });
  /* REQ-WG-073: the tab under the pointer, after a short delay. */
  const [tabHover, setTabHover] = useState<{ id: string; rect: DOMRect } | null>(null);
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(hoverTimer.current), []);
  /* REQ-WG-076: the "+" menu. */
  const plusRef = useRef<HTMLButtonElement | null>(null);
  const [plusMenu, setPlusMenu] = useState(false);

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
        /* REQ-WP-021: the new pane takes the focus and the keyboard at once
           (splitPane makes it the tab's focus); REQ-WP-023: it fades in. */
        markEntering(s.id);
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
      /* Spec 61 REQ-WP-022: move the focus off the closing pane at once, so it
         goes to the pane the user used before it (not the tab's first pane),
         before the fade. REQ-WP-023: the card fades out, then the kill goes. */
      const where = locate(layoutRef.current, id);
      if (where) {
        const next = focusAfterClose(
          focusHistory.current.get(where.tab.id) ?? [],
          tabSessions(where.tab),
          where.tab.focus,
          id,
          closingRef.current
        );
        if (where.tab.focus === id && next && next !== id) {
          update((l) => focusPane(l, where.space.id, where.tab.id, next));
        }
      }
      setClosing((prev) => (prev.has(id) ? prev : new Set(prev).add(id)));
      const timer = setTimeout(() => {
        fadeTimers.current.delete(timer);
        setClosing((prev) => {
          if (!prev.has(id)) return prev;
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
        forgetSession(id);
        update((l) => removeSession(l, id));
        /* A session that already went away has nothing to kill. */
        if (sessionsRef.current[id]) killWorkbenchSession(id).catch(() => {});
      }, CLOSE_FADE_MS);
      fadeTimers.current.add(timer);
    },
    onRestart: (id) => {
      restartWorkbenchSession(id)
        .then((s) => setSessions((prev) => ({ ...prev, [s.id]: s })))
        .catch((err) => setError(errorMessage(err)));
    },
    onOpenFile: openPreview,
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
          markEntering(s.id);
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
        .then((s) => {
          markEntering(s.id);
          update((l) => splitPane(l, sp.id, t.id, target, s.id, "row"));
        })
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
    focusHistory.current.delete(tabId);
    lastFocus.current.delete(tabId);
    update((l) => closeTab(l, spaceId, tabId));
    for (const id of ids) {
      forgetSession(id);
      killWorkbenchSession(id).catch(() => {});
    }
  };
  const closeSpaceNow = (spaceId: string) => {
    const sp = findSpace(spaceId);
    if (!sp) return;
    const ids = spaceSessions(sp);
    for (const t of sp.tabs) {
      focusHistory.current.delete(t.id);
      lastFocus.current.delete(t.id);
    }
    update((l) => {
      const next = removeSpace(l, spaceId);
      return next.spaces.length ? next : addSpace(next, "~", "");
    });
    for (const id of ids) {
      forgetSession(id);
      killWorkbenchSession(id).catch(() => {});
    }
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
          extra={phone && menu.kind === "space" ? <PhoneMenuExtra onFiles={() => git.toggle("files")} onDone={() => setMenu(null)} /> : undefined}
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

  /* GoRex-style shortcuts (workbenchShortcut). */
  const runShortcut = (sc: WbShortcut) => {
    const sp = pickSpace(layoutRef.current);
    const t = pickTab(sp);
    const ids = t ? tabSessions(t) : [];
    const focus = t?.focus && ids.includes(t.focus) ? t.focus : ids[0];
    switch (sc.kind) {
      case "new-tab":
        void newTerminal();
        break;
      case "new-space":
        void newSpace();
        break;
      case "split":
        if (focus) void actions.onSplit(focus, sc.dir);
        else void newTerminal();
        break;
      case "close-pane":
        if (focus) actions.onClose(focus);
        break;
      case "close-tab":
        if (sp && t) closeTabNow(sp.id, t.id);
        break;
      case "zoom":
        if (focus) actions.onZoom(focus);
        break;
      case "focus": {
        const next = focus ? paneInDirection(paneRects(), focus, sc.dir) : null;
        if (next) actions.onFocus(next);
        break;
      }
      case "tab": {
        const i = sp ? tabAtShortcut(sp.tabs.length, sc.index) : -1;
        const target = sp && i >= 0 ? sp.tabs[i] : undefined;
        if (sp && target) update((l) => selectTab(l, sp.id, target.id));
        break;
      }
      case "tab-step": {
        if (!sp || !t || sp.tabs.length < 2) break;
        const i = sp.tabs.findIndex((x) => x.id === t.id);
        const target = sp.tabs[(i + sc.step + sp.tabs.length) % sp.tabs.length];
        if (target) update((l) => selectTab(l, sp.id, target.id));
        break;
      }
      case "rename-tab":
        if (sp && t) openRenameTab(sp.id, t.id);
        break;
      case "clear":
        if (focus) window.dispatchEvent(new CustomEvent("lana:wb-clear", { detail: focus }));
        break;
      case "font":
        setDeskFont((n) => {
          const next = sc.step === 0 ? DESK_FONT : Math.min(DESK_FONT_MAX, Math.max(DESK_FONT_MIN, n + sc.step));
          saveDeskFont(next);
          return next;
        });
        break;
    }
  };
  const shortcutRef = useRef(runShortcut);
  const modalRef = useRef(false);
  useEffect(() => {
    shortcutRef.current = runShortcut;
    modalRef.current = menu !== null || dialog !== null || accountsAnchor !== null || accountsSheet;
  });
  useEffect(() => {
    if (phone) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || modalRef.current) return;
      const target = e.target instanceof HTMLElement ? e.target : null;
      if (
        target &&
        !target.classList.contains("xterm-helper-textarea") &&
        target.closest("input, textarea, select, [contenteditable='true'], [role='dialog']")
      ) {
        return;
      }
      const sc = workbenchShortcut(e);
      if (!sc) return;
      /* Before xterm.js and before the app menu (⌘W would close the window). */
      e.preventDefault();
      e.stopPropagation();
      if (e.repeat && sc.kind !== "focus" && sc.kind !== "tab-step" && sc.kind !== "font") return;
      shortcutRef.current(sc);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [phone]);

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
            motion: panelMotion,
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
  ) : previewNotice ? (
    <div className="wb-error" role="alert">
      {previewNotice}
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => setPreviewNotice("")}>
        Close
      </button>
    </div>
  ) : null;

  if (phone) {
    return (
      <PaneAccessContext.Provider value={paneAccess}>
      <WbGitContext.Provider value={git.context}>
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
          onOpenFile={openPreview}
          onChanges={() => git.toggle("changes")}
          changeCount={git.changeCount}
        />
        <RemoteControlBar control={remoteControl} />
        {git.panel}
      </WbGitContext.Provider>
      </PaneAccessContext.Provider>
    );
  }

  const zoomed = tab?.zoom && tab && paneSessions(tab.root).includes(tab.zoom) ? tab.zoom : undefined;
  const attached = tab ? tabSessions(tab).reduce((n, id) => Math.max(n, sessions[id]?.attached || 0), 0) : 0;

  return (
    <PaneAccessContext.Provider value={paneAccess}>
    <WbGitContext.Provider value={git.context}>
    <main id="main" className={"wb-page" + (git.open ? " wbg-open" : "") + (git.maximized ? " wbg-maxed" : "")}>
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
                onMouseEnter={(e) => {
                  const rect = e.currentTarget.getBoundingClientRect();
                  clearTimeout(hoverTimer.current);
                  hoverTimer.current = setTimeout(() => setTabHover({ id: t.id, rect }), 350);
                }}
                onMouseLeave={() => {
                  clearTimeout(hoverTimer.current);
                  setTabHover(null);
                }}
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
                  <TabTiles ids={tabSessions(t)} focus={t.focus} sessions={sessions} />
                  <span className="wb-tab-label">{tabLabel(t, sessions)}</span>
                  {s === "working" || s === "blocked" || s === "done" ? <StatusDot status={s} /> : null}
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
          <button
            ref={plusRef}
            type="button"
            className={"wb-tab-new wbg-plus" + (plusMenu ? " is-open" : "")}
            aria-label="New tab"
            aria-haspopup="menu"
            data-tip="New tab ⌘T"
            onClick={() => setPlusMenu((m) => !m)}
          >
            <Ic name="plus" size={15} />
            <Ic name="chevron-down" size={11} />
          </button>
          {plusMenu ? (
            <Menu anchorRef={plusRef} open onClose={() => setPlusMenu(false)} className="wbg-plus-menu">
              <MenuItem icon="square-terminal" kbd="⌘T" onClick={() => (setPlusMenu(false), void newTerminal())}>
                New terminal
              </MenuItem>
              {git.focused ? (
                <MenuItem
                  icon="sparkles"
                  right={
                    <span className="wbg-pane-chip">
                      <i className="wbg-wt-dot" style={{ background: git.focused.color }} />
                      {git.focused.short}
                    </span>
                  }
                  onClick={() => {
                    setPlusMenu(false);
                    const f = git.focused;
                    if (f) gitOpenTab(f.worktree, "claude");
                  }}
                >
                  Claude Code here
                </MenuItem>
              ) : null}
              {git.inSpace.length ? (
                <>
                  <MenuSeparator />
                  <div className="wbg-menu-label">Claude Code in worktree</div>
                  {git.inSpace.map((w) => (
                    <MenuItem
                      key={w.info.worktree}
                      onClick={() => {
                        setPlusMenu(false);
                        gitOpenTab(w.info.worktree, "claude");
                      }}
                      right={<i className="wbg-wt-dot" style={{ background: w.info.color }} />}
                    >
                      <span className="mono">{w.info.branch}</span>
                    </MenuItem>
                  ))}
                  <MenuItem icon="folder-git-2" onClick={() => (setPlusMenu(false), git.openWorktrees())}>
                    All worktrees…
                  </MenuItem>
                </>
              ) : null}
              <MenuSeparator />
              <MenuItem icon="folder-plus" kbd="⇧⌘N" onClick={() => (setPlusMenu(false), git.openNewWorktree())}>
                New worktree…
              </MenuItem>
            </Menu>
          ) : null}
          {tabHover ? <TabWorktreeCard tabId={tabHover.id} rect={tabHover.rect} tab={space?.tabs.find((x) => x.id === tabHover.id)} sessions={sessions} /> : null}
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
          <span className="wbg-toggle-sep" />
          {git.toggleButton}
        </div>
        <div className="wb-workspace">
          {!loaded ? (
            <p className="wb-empty-note">Opening Workbench…</p>
          ) : tab ? (
            zoomed ? (
              <PaneView
                id={zoomed}
                session={sessions[zoomed]}
                focused
                zoomed
                closing={closing.has(zoomed)}
                entering={entering.has(zoomed)}
                actions={actions}
                fontSize={deskFont}
              />
            ) : (
              <PaneTree
                node={tab.root}
                tab={tab}
                sessions={sessions}
                actions={actions}
                closing={closing}
                entering={entering}
                fontSize={deskFont}
              />
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
        <RemoteControlBar control={remoteControl} />
      </section>
      {git.panel}
    </main>
    </WbGitContext.Provider>
    </PaneAccessContext.Provider>
  );
}
