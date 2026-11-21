import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import type { WorkbenchPreview, WorkbenchSession } from "../api/workbench";
import { isGitInfo, type GitInfo } from "../api/workbench-git";
import { Ic } from "../ui/icons";
import { LAYOUT_MOTION_MS, holdLayout, reducedMotion } from "../app/layout-motion";
import { GitPanel, GIT_TABS, type GitTab, type SpaceWorktree } from "./workbench-git-panel";
import { readPref, savePref, useGitResolve, useGitStatus } from "./workbench-git-hooks";
import { groupByWorktree, panelWorktree, relativeTo, sessionDir, sharedAgents } from "./workbench-git-model";
import { WorktreesTab } from "./workbench-worktrees";
import { programName, tabSessions, type WbLayout, type WbSpace, type WbTab } from "./workbench-model";

/* Spec 66: the Workbench's git state in one hook, so workbench.tsx only wires
   it in: the worktree of every session (model A, REQ-WG-070), the panel that
   follows the focused pane (REQ-WG-062, 072), the Changes button
   (REQ-WG-060) and ⌘+click into Files (REQ-WG-063). */

const OPEN_KEY = "lana.workbench.gitPanel";
const TAB_KEY = "lana.workbench.gitTab";

type Sessions = Record<string, WorkbenchSession>;

export interface WbGitContextValue {
  gitOf: (sessionId: string) => GitInfo | undefined;
  shared: Map<string, string[]>;
  describe: (sessionId: string) => string;
  onShared: (sessionId: string) => void;
}

export const WbGitContext = createContext<WbGitContextValue | null>(null);

export function useWbGit(): WbGitContextValue | null {
  return useContext(WbGitContext);
}

function hasAgent(s: WorkbenchSession | undefined): boolean {
  return !!s?.agent && s.state !== "exited";
}

/* "Tab 2 · Claude Code + zsh" for the picker (REQ-WG-072). */
function usesOf(space: WbSpace, ids: string[], sessions: Sessions): string {
  const parts: string[] = [];
  space.tabs.forEach((t, i) => {
    const mine = tabSessions(t).filter((id) => ids.includes(id));
    if (!mine.length) return;
    parts.push("Tab " + (i + 1) + " · " + mine.map((id) => programName(sessions[id])).join(" + "));
  });
  return parts.join(", ");
}

export interface UseWorkbenchGit {
  open: boolean;
  maximized: boolean;
  panel: ReactNode;
  toggleButton: ReactNode;
  context: WbGitContextValue;
  /* A spec 61 preview resolved from a ⌘+click (REQ-WG-063). */
  showPreview: (sessionId: string, data: WorkbenchPreview) => void;
  toggle: (tab?: GitTab) => void;
  /* The worktrees used in the active space, with their tabs (REQ-WG-072). */
  inSpace: SpaceWorktree[];
  /* The focused pane's worktree. */
  focused: GitInfo | undefined;
  openNewWorktree: () => void;
  openWorktrees: () => void;
  close: () => void;
  changeCount: number;
}

export function useWorkbenchGit({
  layout,
  sessions,
  space,
  tab,
  phone,
  openTab,
  onOpenChange,
}: {
  layout: WbLayout;
  sessions: Sessions;
  space: WbSpace | undefined;
  tab: WbTab | undefined;
  phone: boolean;
  /* Starts a session in dir as a new tab of the active space. */
  openTab: (dir: string, program: "claude" | "shell") => void;
  /* Told when the panel starts to open or close, so the page can make room. */
  onOpenChange?: (open: boolean) => void;
}): UseWorkbenchGit {
  const [open, setOpen] = useState(() => readPref(OPEN_KEY, ["open", "closed"] as const, "closed") === "open");
  const [motion, setMotion] = useState<"in" | "out" | null>(null);
  const [gitTab, setGitTab] = useState<GitTab>(() => readPref(TAB_KEY, GIT_TABS, "changes"));
  const [maximized, setMaximized] = useState(false);
  const [pinned, setPinned] = useState<string | null>(null);
  const [chosen, setChosen] = useState<string | null>(null);
  const [file, setFile] = useState<{ path: string | null; line?: number }>({ path: null });
  const [external, setExternal] = useState<WorkbenchPreview | null>(null);
  /* Bumped to open the New worktree dialog from the "+" menu (REQ-WG-076). */
  const [newNonce, setNewNonce] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  /* REQ-WG-070: every session's directory, plus a pinned or chosen one. */
  const allIds = useMemo(() => Object.keys(sessions), [sessions]);
  const dirs = useMemo(() => {
    const list = allIds.map((id) => sessionDir(sessions[id]));
    if (pinned) list.push(pinned);
    if (chosen) list.push(chosen);
    return list;
  }, [allIds, sessions, pinned, chosen]);
  const { map, refresh: refreshResolve } = useGitResolve(dirs);
  const gitOf = useCallback(
    (id: string): GitInfo | undefined => {
      const v = map[sessionDir(sessions[id])];
      return isGitInfo(v) ? v : undefined;
    },
    [map, sessions]
  );
  const infoOfDir = (dir: string | null): GitInfo | undefined => {
    if (!dir) return undefined;
    const v = map[dir];
    return isGitInfo(v) ? v : undefined;
  };

  const spaceIds = useMemo(() => (space ? space.tabs.flatMap(tabSessions) : []), [space]);
  const inSpace: SpaceWorktree[] = useMemo(() => {
    if (!space) return [];
    return groupByWorktree(spaceIds, gitOf).map((g) => ({ info: g.info, uses: usesOf(space, g.sessions, sessions) }));
  }, [space, spaceIds, gitOf, sessions]);
  const shared = useMemo(() => sharedAgents(spaceIds, gitOf, (id) => hasAgent(sessions[id])), [spaceIds, gitOf, sessions]);

  /* REQ-WG-072: the panel follows the focused pane unless pinned. A worktree
     picked by hand holds until the focus moves to another pane. */
  const focused = tab?.focus ?? (tab ? tabSessions(tab)[0] : undefined);
  const focusedInfo = focused ? gitOf(focused) : undefined;
  const lastFocus = useRef(focused);
  useEffect(() => {
    if (lastFocus.current !== focused) {
      lastFocus.current = focused;
      setChosen(null);
    }
  }, [focused]);
  const shownPath = panelWorktree(pinned, chosen ?? focusedInfo?.worktree, null);
  const shown = infoOfDir(shownPath) ?? (focusedInfo && focusedInfo.worktree === shownPath ? focusedInfo : undefined) ?? inSpace.find((w) => w.info.worktree === shownPath)?.info ?? null;

  /* REQ-WG-021, 060: the badge needs the count even with the panel closed,
     so the page polls slower then. */
  const status = useGitStatus(shown?.worktree ?? null, "uncommitted", open ? 3000 : 15000);

  /* A new worktree in the panel starts with no file picked. */
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- another worktree starts with no file picked
    setFile({ path: null });
    setExternal(null);
  }, [shown?.worktree]);

  const setOpenSaved = (next: boolean) => {
    savePref(OPEN_KEY, next ? "open" : "closed");
    onOpenChange?.(next);
    clearTimeout(timer.current);
    if (phone || reducedMotion()) {
      setMotion(null);
      setOpen(next);
      return;
    }
    /* REQ-WG-062: slide like the sidebar, terminals refit at the end. */
    holdLayout();
    setOpen(true);
    setMotion(next ? "in" : "out");
    timer.current = setTimeout(() => {
      setMotion(null);
      if (!next) setOpen(false);
    }, LAYOUT_MOTION_MS);
  };

  const pickTab = (t: GitTab) => {
    setGitTab(t);
    savePref(TAB_KEY, t);
  };

  const toggle = (t?: GitTab) => {
    if (open && motion !== "out" && (!t || t === gitTab)) {
      setOpenSaved(false);
      setMaximized(false);
      return;
    }
    if (t) pickTab(t);
    if (!open || motion === "out") setOpenSaved(true);
  };
  const close = () => {
    if (!open) return;
    setOpenSaved(false);
    setMaximized(false);
  };

  /* REQ-WG-063: a ⌘+click lands in Files: inside the pane's worktree as a
     file of the tree, else as an outside preview. */
  const showPreview = (sessionId: string, data: WorkbenchPreview) => {
    const info = gitOf(sessionId);
    if (!pinned) setChosen(info?.worktree ?? null);
    const rel = info ? relativeTo(info.worktree, data.path) : null;
    pickTab("files");
    if (rel) {
      setExternal(null);
      setFile({ path: rel, line: data.line || undefined });
    } else {
      setFile({ path: null });
      setExternal(data);
    }
    if (!open || motion === "out") setOpenSaved(true);
  };

  /* Esc closes the panel when the focus is not in a terminal or a field. */
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      const t = e.target instanceof HTMLElement ? e.target : null;
      if (t && (t.classList.contains("xterm-helper-textarea") || t.closest("input, textarea, [role='dialog'], .wbg-menu, .pop"))) return;
      close();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  /* ⇧⌘E Files, ⇧⌘G Changes (REQ-WG-060), ⇧⌘N New worktree (REQ-WG-076). */
  const toggleRef = useRef(toggle);
  const newRef = useRef(() => {});
  useEffect(() => {
    toggleRef.current = toggle;
    newRef.current = () => {
      pickTab("worktrees");
      if (!open || motion === "out") setOpenSaved(true);
      setNewNonce((n) => n + 1);
    };
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || !e.shiftKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (k !== "e" && k !== "g" && k !== "n") return;
      e.preventDefault();
      e.stopPropagation();
      if (k === "n") newRef.current();
      else toggleRef.current(k === "e" ? "files" : "changes");
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);

  const count = status.data?.totals.files ?? 0;
  const noRepoDir = !shown && focused ? sessionDir(sessions[focused]) : undefined;

  const worktreesTab = shown ? (
    <WorktreesTab
      repo={shown.repo}
      inSpace={inSpace}
      phone={phone}
      newNonce={newNonce}
      spaceName={space?.name}
      actions={{
        openTab,
        show: (dir) => {
          setChosen(dir);
          if (pinned) setPinned(dir);
          pickTab("changes");
        },
        sessionsIn: (dir) =>
          allIds.filter((id) => {
            const s = sessions[id];
            if (!s || s.state === "exited") return false;
            const d = sessionDir(s);
            return d === dir || d.startsWith(dir.replace(/\/+$/, "") + "/");
          }),
      }}
    />
  ) : (
    <div className="wbg-empty wbg-empty-full">
      <Ic name="folder-git-2" size={20} />
      <b>No repository in focus</b>
      <span>Focus a terminal inside a git repo to manage its worktrees.</span>
    </div>
  );

  const panel = open ? (
    <GitPanel
      phone={phone}
      motion={motion}
      tab={gitTab}
      onTab={pickTab}
      worktree={shown}
      noRepoDir={noRepoDir}
      inSpace={inSpace}
      pinned={!!pinned}
      onPin={(p) => setPinned(p ? (shown?.worktree ?? null) : null)}
      onPick={(w) => {
        setChosen(w);
        if (pinned) setPinned(w);
      }}
      maximized={maximized}
      onMaximize={() => {
        holdLayout();
        setMaximized((m) => !m);
      }}
      onClose={close}
      status={status.data}
      onRefresh={() => {
        status.refresh();
        refreshResolve();
      }}
      file={file}
      onFile={(path, line) => {
        setExternal(null);
        setFile({ path, line });
      }}
      external={external}
      worktreesTab={worktreesTab}
    />
  ) : null;

  const tip = count ? count + " changed" + (shown ? " · " + shown.branch : "") : "Files & changes";
  const toggleButton = (
    <button
      type="button"
      className={"wbg-toggle" + (open && motion !== "out" ? " is-on" : "")}
      aria-label="Files and changes"
      aria-pressed={open && motion !== "out"}
      data-tip={"Files & changes ⇧⌘E · " + tip}
      onClick={() => toggle()}
    >
      <Ic name="git-compare" size={16} />
      {count ? <span className="wbg-badge">{count}</span> : null}
    </button>
  );

  const describe = (id: string) => {
    if (!space) return "";
    const ti = space.tabs.findIndex((t) => tabSessions(t).includes(id));
    return (ti >= 0 ? "Tab " + (ti + 1) + " · " : "") + programName(sessions[id]);
  };

  /* Memoised so panes re-render only when git data changes. */
  const onSharedRef = useRef(() => {});
  useEffect(() => {
    onSharedRef.current = () => {
      pickTab("worktrees");
      if (!open) setOpenSaved(true);
    };
  });
  const context: WbGitContextValue = useMemo(
    () => ({ gitOf, shared, describe, onShared: () => onSharedRef.current() }),
    // describe reads the space and sessions that gitOf and shared already follow.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [gitOf, shared, space]
  );

  void layout;
  const openWorktrees = () => {
    pickTab("worktrees");
    if (!open || motion === "out") setOpenSaved(true);
  };
  return {
    open,
    maximized: open && maximized,
    panel,
    toggleButton,
    context,
    showPreview,
    toggle,
    close,
    changeCount: count,
    inSpace,
    focused: focusedInfo,
    openNewWorktree: () => {
      openWorktrees();
      setNewNonce((n) => n + 1);
    },
    openWorktrees,
  };
}
