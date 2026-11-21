import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";

import type { WorkbenchPreview } from "../api/workbench";
import { getGitStatus, listGitWorktrees, type GitInfo, type GitStatus, type GitWorktree } from "../api/workbench-git";
import { Ic } from "../ui/icons";
import { ChangesTab, readScopeKind, type ScopeKind } from "./workbench-changes";
import { FilesTab } from "./workbench-files";
import { useGitStatus } from "./workbench-git-hooks";
import { shortBranch } from "./workbench-git-model";

/* Spec 66 REQ-WG-062: the panel at the right of the panes, in the spec 61
   preview slot: Files, Changes and Worktrees for one worktree. */

export type GitTab = "files" | "changes" | "worktrees";
export const GIT_TABS = ["files", "changes", "worktrees"] as const;

const WIDTH_KEY = "lana.workbench.previewWidth";
const MIN_WIDTH = 360;
const MAX_FRACTION = 0.9;

function readWidth(): number {
  /* About the design's 760 px at 1680 wide, never more than 45%. */
  const fallback = Math.max(MIN_WIDTH, Math.min(760, Math.round((typeof window !== "undefined" ? window.innerWidth : 1280) * 0.45)));
  try {
    const v = Number(window.localStorage.getItem(WIDTH_KEY));
    return Number.isFinite(v) && v >= MIN_WIDTH ? v : fallback;
  } catch {
    return fallback;
  }
}

/* One worktree used in this space, for the picker (REQ-WG-072). */
export interface SpaceWorktree {
  info: GitInfo;
  uses: string;
}

export interface GitPanelProps {
  phone?: boolean;
  motion?: "in" | "out" | null;
  tab: GitTab;
  onTab: (tab: GitTab) => void;
  /* The worktree shown, null when the focused pane is not in a repo. */
  worktree: GitInfo | null;
  /* What the panel says when there is no worktree. */
  noRepoDir?: string;
  inSpace: SpaceWorktree[];
  pinned: boolean;
  onPin: (pinned: boolean) => void;
  onPick: (worktree: string) => void;
  maximized: boolean;
  onMaximize: () => void;
  onClose: () => void;
  /* The uncommitted status, polled by the page for the badge too. */
  status: GitStatus | null;
  onRefresh: () => void;
  file: { path: string | null; line?: number };
  onFile: (path: string | null, line?: number) => void;
  external: WorkbenchPreview | null;
  worktreesTab: ReactNode;
  focusMode?: boolean;
  onFocusMode?: (on: boolean) => void;
}

export function GitPanel(props: GitPanelProps) {
  const { phone, tab, onTab, worktree, status } = props;
  const [width, setWidth] = useState(readWidth);
  const asideRef = useRef<HTMLElement>(null);

  /* In Lana.app the tab strip is the window's title row: it runs on over the
     panel. The page learns how far through --wbg-panel-space (0 when the
     panel floats over the terminals or is maximized). */
  useEffect(() => {
    const aside = asideRef.current;
    const page = aside?.closest<HTMLElement>(".wb-page");
    if (!aside || !page || phone) return;
    const measure = () => {
      const floating = getComputedStyle(aside).position === "absolute" || props.maximized;
      const space = floating ? 0 : Math.round(page.getBoundingClientRect().right - aside.getBoundingClientRect().left);
      page.style.setProperty("--wbg-panel-space", Math.max(0, space) + "px");
    };
    measure();
    const ro = typeof ResizeObserver !== "undefined" ? new ResizeObserver(measure) : null;
    ro?.observe(aside);
    ro?.observe(page);
    return () => {
      ro?.disconnect();
      page.style.removeProperty("--wbg-panel-space");
    };
  }, [phone, props.maximized]);
  const [scopeKind, setScopeKind] = useState<ScopeKind>(readScopeKind);
  const [commit, setCommit] = useState<string | null>(null);
  const [changeSel, setChangeSel] = useState<string | null>(null);
  const [picker, setPicker] = useState(false);
  const root = worktree?.worktree ?? null;

  /* The Changes tab has its own scope; uncommitted reuses the page's poll. */
  const scoped = useGitStatus(
    tab === "changes" && (scopeKind !== "uncommitted" || commit) ? root : null,
    commit ? `commit:${commit}` : scopeKind,
    3000
  );
  const changesStatus = scopeKind === "uncommitted" && !commit ? status : scoped.data;

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- another worktree drops the commit filter and the selection
    setCommit(null);
  }, [root]);

  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = width;
    const max = Math.round(window.innerWidth * MAX_FRACTION);
    let latest = startWidth;
    const move = (e: PointerEvent) => {
      latest = Math.min(max, Math.max(MIN_WIDTH, startWidth + (startX - e.clientX)));
      setWidth(latest);
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      document.body.classList.remove("wb-resizing");
      try {
        window.localStorage.setItem(WIDTH_KEY, String(Math.round(latest)));
      } catch {
        /* Storage is optional. */
      }
    };
    document.body.classList.add("wb-resizing");
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  const count = status?.totals.files ?? 0;
  const motion = props.motion === "in" ? " is-entering" : props.motion === "out" ? " is-leaving" : "";
  const style = phone || props.maximized ? undefined : ({ width, flexBasis: width, ["--wbg-w" as string]: width + "px" } as React.CSSProperties);

  let body: ReactNode;
  if (tab === "worktrees") body = props.worktreesTab;
  else if (!root)
    body = (
      <div className="wbg-empty wbg-empty-full">
        <Ic name="git-branch" size={20} />
        <b>{props.noRepoDir ? props.noRepoDir.replace(/^\/(?:Users|home)\/[^/]+/, "~") + " is not a git repository" : "No terminal in focus"}</b>
        <span>Files and Changes follow the focused pane. Focus a terminal inside a git repo.</span>
      </div>
    );
  else if (tab === "files")
    body = (
      <FilesTab
        worktree={root}
        changes={status?.files ?? []}
        selected={props.file.path}
        line={props.file.line}
        onSelect={props.onFile}
        external={props.external}
        phone={phone}
      />
    );
  else
    body = (
      <ChangesTab
        worktree={root}
        status={changesStatus}
        scopeKind={scopeKind}
        onScopeKind={setScopeKind}
        commit={commit}
        onCommit={setCommit}
        selected={changeSel}
        onSelect={setChangeSel}
        onOpenFile={(p) => {
          props.onFile(p);
          onTab("files");
        }}
        phone={phone}
      />
    );

  return (
    <>
    {phone ? <div className="wbg-scrim" onClick={props.onClose} aria-hidden="true" /> : null}
    <aside
      ref={asideRef}
      className={"wbg-panel" + (phone ? " wbg-sheet" : "") + (props.maximized ? " is-max" : "") + motion}
      style={style}
      aria-label="Files and changes"
    >
      {phone ? (
        <span className="wbg-grabber" />
      ) : props.maximized ? null : (
        <div className="wb-preview-resizer" role="separator" aria-orientation="vertical" aria-label="Resize panel" onPointerDown={startResize} />
      )}
      <header className="wbg-head">
        <span className="wbg-seg" role="tablist" aria-label="Panel">
          {(
            [
              ["files", "Files", "files"],
              ["changes", "Changes", "git-compare"],
              ["worktrees", "Worktrees", "folder-git-2"],
            ] as const
          ).map(([k, label, icon]) => (
            <button key={k} type="button" role="tab" aria-selected={tab === k} className={tab === k ? "is-on" : ""} onClick={() => onTab(k)}>
              <Ic name={icon} size={15} />
              <span className="wbg-seg-label">{label}</span>
              {k === "changes" && count ? <span className="wbg-badge">{count}</span> : null}
            </button>
          ))}
        </span>
        <span className="wb-spacer" />
        {worktree ? (
          <div className="wbg-picker-wrap">
            <button type="button" className={"wbg-picker" + (picker ? " is-open" : "")} onClick={() => setPicker((p) => !p)} title={worktree.worktree}>
              <Ic name="git-branch" size={14} />
              <span className="wbg-picker-label">{worktree.branch}</span>
              {props.pinned ? <Ic name="pin" size={13} /> : null}
              <Ic name="chevrons-up-down" size={13} />
            </button>
            {picker ? (
              <WorktreePicker
                current={worktree}
                inSpace={props.inSpace}
                pinned={props.pinned}
                onPin={props.onPin}
                onPick={(w) => {
                  props.onPick(w);
                  setPicker(false);
                }}
                onManage={() => {
                  onTab("worktrees");
                  setPicker(false);
                }}
                onClose={() => setPicker(false)}
              />
            ) : null}
          </div>
        ) : null}
        {!phone && props.onFocusMode ? (
          <button
            type="button"
            className={"wbg-icon-btn" + (props.focusMode ? " is-on" : "")}
            aria-pressed={!!props.focusMode}
            aria-label="Focus mode"
            data-tip={props.focusMode ? "Showing the focused pane only" : "Show only the focused pane"}
            onClick={() => props.onFocusMode?.(!props.focusMode)}
          >
            <Ic name="focus" size={15} />
          </button>
        ) : null}
        <button type="button" className="wbg-icon-btn" aria-label="Refresh" data-tip="Refresh" onClick={props.onRefresh}>
          <Ic name="rotate-cw" size={15} />
        </button>
        {!phone ? (
          <button
            type="button"
            className="wbg-icon-btn"
            aria-label={props.maximized ? "Restore panel" : "Maximize panel"}
            data-tip={props.maximized ? "Restore" : "Maximize"}
            onClick={props.onMaximize}
          >
            <Ic name={props.maximized ? "minimize-2" : "maximize-2"} size={15} />
          </button>
        ) : null}
        <button type="button" className="wbg-icon-btn" aria-label="Close panel" data-tip="Close Esc" onClick={props.onClose}>
          <Ic name="x" size={15} />
        </button>
      </header>
      <div className="wbg-body">{body}</div>
    </aside>
    </>
  );
}

/* REQ-WG-072: switch the panel to another worktree, or pin it. */
function WorktreePicker({
  current,
  inSpace,
  pinned,
  onPin,
  onPick,
  onManage,
  onClose,
}: {
  current: GitInfo;
  inSpace: SpaceWorktree[];
  pinned: boolean;
  onPin: (p: boolean) => void;
  onPick: (worktree: string) => void;
  onManage: () => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [others, setOthers] = useState<GitWorktree[] | null>(null);
  const [counts, setCounts] = useState<Record<string, number>>({});

  useEffect(() => {
    let cancelled = false;
    listGitWorktrees(current.repo)
      .then((l) => !cancelled && setOthers(l.worktrees))
      .catch(() => !cancelled && setOthers([]));
    for (const w of inSpace) {
      getGitStatus(w.info.worktree, "uncommitted")
        .then((s) => !cancelled && setCounts((c) => ({ ...c, [w.info.worktree]: s.totals.files })))
        .catch(() => {});
    }
    const away = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node) && !(e.target as Element).closest?.(".wbg-picker")) onClose();
    };
    window.addEventListener("mousedown", away);
    return () => {
      cancelled = true;
      window.removeEventListener("mousedown", away);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current.repo]);

  const q = query.trim().toLowerCase();
  const inSpacePaths = new Set(inSpace.map((w) => w.info.worktree));
  const rest = (others ?? []).filter((w) => !inSpacePaths.has(w.path) && !w.prunable && (!q || (w.branch + " " + w.name).toLowerCase().includes(q)));

  return (
    <div className="wbg-menu wbg-picker-menu" ref={ref} role="menu">
      <label className="wbg-filter">
        <Ic name="search" size={14} />
        <input autoFocus value={query} placeholder="Find worktree or branch" onChange={(e) => setQuery(e.target.value)} />
      </label>
      {inSpace.length ? <div className="wbg-section">In this space</div> : null}
      {inSpace
        .filter((w) => !q || w.info.branch.toLowerCase().includes(q))
        .map((w) => (
          <button
            key={w.info.worktree}
            type="button"
            role="menuitem"
            className={"wbg-menu-item" + (w.info.worktree === current.worktree ? " is-on" : "")}
            onClick={() => onPick(w.info.worktree)}
          >
            <i className="wbg-wt-dot" style={{ background: w.info.color }} />
            <span className="wbg-menu-text">
              <b className="mono">{w.info.branch}</b>
              <small>{w.uses}</small>
            </span>
            <span className="wbg-menu-count wbg-amber">{counts[w.info.worktree] || ""}</span>
            {w.info.worktree === current.worktree ? <Ic name="check" size={13} /> : null}
          </button>
        ))}
      <div className="wbg-section">Other worktrees{others ? " · " + rest.length : ""}</div>
      <div className="wbg-menu-scroll">
        {others === null ? <div className="wbg-tree-note">Loading…</div> : null}
        {rest.slice(0, 40).map((w) => (
          <button key={w.path} type="button" role="menuitem" className="wbg-menu-item" onClick={() => onPick(w.path)} title={w.path}>
            <Ic name="folder-git-2" size={14} />
            <span className="wbg-menu-text">
              <b className="mono">{w.branch || shortBranch(w.head)}</b>
            </span>
            <span className="wbg-menu-count wbg-amber">{w.dirty || ""}</span>
          </button>
        ))}
      </div>
      <div className="wbg-menu-sep" />
      <button type="button" role="menuitemcheckbox" aria-checked={pinned} className="wbg-menu-item" onClick={() => onPin(!pinned)}>
        <Ic name="pin" size={14} />
        <span className="wbg-menu-text">
          <b>Pin panel to this worktree</b>
        </span>
        <span className={"wbg-switch" + (pinned ? " is-on" : "")} />
      </button>
      <button type="button" role="menuitem" className="wbg-menu-item" onClick={onManage}>
        <Ic name="folder-git-2" size={14} />
        <span className="wbg-menu-text">
          <b>Manage worktrees…</b>
        </span>
      </button>
    </div>
  );
}
