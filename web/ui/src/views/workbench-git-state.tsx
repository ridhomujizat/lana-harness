import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { clearWorktreeReport, type WorkbenchPreview, type WorkbenchSession } from "../api/workbench";
import { isGitInfo, rootOfResolved, type GitInfo, type GitRootInfo } from "../api/workbench-git";
import { Ic } from "../ui/icons";
import { flushSync } from "react-dom";

import { LAYOUT_MOTION_MS, canViewTransition, holdLayout, reducedMotion, withViewTransition } from "../app/layout-motion";
import { GitPanel, GIT_TABS, type GitTab, type SpaceWorktree } from "./workbench-git-panel";
import { readPref, savePref, useGitResolve, useGitStatus } from "./workbench-git-hooks";
import { groupByWorktree, isUnder, panelWorktree, relativeTo, sessionDir, sharedAgents } from "./workbench-git-model";
import { isFigcraftPath } from "./workbench-figcraft-model";
import { useRootSummary } from "./workbench-root";
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
  /* Spec 70: the root folder of several repos the pane works in. */
  rootOf: (sessionId: string) => GitRootInfo | undefined;
  /* The changed repo count of a root whose summary is loaded. */
  rootChanged: (rootPath: string) => number | undefined;
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
  focusMode: boolean;
  setFocusMode: (on: boolean) => void;
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
  /* Told when the panel starts to open or close, so the page can make room;
     instant inside a View Transition. */
  onOpenChange?: (open: boolean, instant?: boolean) => void;
}): UseWorkbenchGit {
  const [open, setOpen] = useState(() => readPref(OPEN_KEY, ["open", "closed"] as const, "closed") === "open");
  const [motion, setMotion] = useState<"in" | "out" | null>(null);
  const [gitTab, setGitTab] = useState<GitTab>(() => readPref(TAB_KEY, GIT_TABS, "changes"));
  const [maximized, setMaximized] = useState(false);
  const [pinned, setPinned] = useState<string | null>(null);
  const [chosen, setChosen] = useState<string | null>(null);
  const [file, setFile] = useState<{ path: string | null; line?: number }>({ path: null });
  const [external, setExternal] = useState<WorkbenchPreview | null>(null);
  /* A ⌘+clicked folder outside git and the file open in it. */
  const [outside, setOutside] = useState<{ dir: string; file: string | null } | null>(null);
  /* A folder a ⌘+click named: Files opens the tree down to it. */
  const [reveal, setReveal] = useState<{ worktree: string; dir: string; nonce: number } | null>(null);
  /* Focus mode: only the focused pane shows beside the open panel (owner,
     2026-11-21). Kept per browser. */
  const [focusMode, setFocusMode] = useState(() => readPref("lana.workbench.gitFocus", ["on", "off"] as const, "on") === "on");
  /* Bumped to open the New worktree dialog from the "+" menu (REQ-WG-076). */
  const [newNonce, setNewNonce] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  /* REQ-WG-070: every session's directory, plus a pinned or chosen one. */
  const allIds = useMemo(() => Object.keys(sessions), [sessions]);
  /* Spec 69/70: the process directory too, so a pane whose agent reported a
     repo inside a root still knows the root. */
  const procDir = (s: WorkbenchSession | undefined) => (s?.cwd || s?.dir || "").trim();
  const dirs = useMemo(() => {
    const list = allIds.map((id) => sessionDir(sessions[id]));
    for (const id of allIds) if (sessions[id]?.work_dir) list.push(procDir(sessions[id]));
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
  const rootOf = useCallback(
    (id: string): GitRootInfo | undefined => {
      const s = sessions[id];
      return rootOfResolved(map[procDir(s)]) ?? rootOfResolved(map[sessionDir(s)]);
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
  /* Spec 70: a pane in a root shows the whole root until a repo is chosen;
     a repo the agent reported keeps the root view (REQ-WM-035). */
  const focusedRoot = focused ? rootOf(focused) : undefined;
  const focusedSession = focused ? sessions[focused] : undefined;
  const rootMode = !!focusedRoot && !chosen && !pinned && (!focusedInfo || isUnder(focusedInfo.worktree, focusedRoot.path));
  const reported = focusedRoot && focusedSession?.work_dir && focusedInfo && isUnder(focusedInfo.worktree, focusedRoot.path) ? focusedInfo.worktree : undefined;
  const rootSummary = useRootSummary(focusedRoot?.path ?? null, !!focusedRoot, open);
  const [rootFile, setRootFile] = useState<string | null>(null);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- another root starts with no file
    setRootFile(null);
  }, [focusedRoot?.path]);
  const rootChanged = useCallback(
    (path: string) => (rootSummary.summary && rootSummary.summary.root === path ? rootSummary.summary.changed_repos : undefined),
    [rootSummary.summary]
  );

  /* Spec 69 REQ-WA-043: a short notice when a report moves the panel. */
  const [notice, setNotice] = useState("");
  const lastReport = useRef<{ id?: string; dir?: string }>({});
  useEffect(() => {
    const dir = focusedSession?.work_dir;
    const prev = lastReport.current;
    lastReport.current = { id: focused, dir };
    if (prev.id !== focused || !dir || dir === prev.dir || pinned) return;
     
    setNotice("Following " + (focusedInfo?.short ?? dir.slice(dir.lastIndexOf("/") + 1)));
  }, [focused, focusedSession?.work_dir, focusedInfo?.short, pinned]);
  /* The notice clears on its own timer: tied to the effect above, a late
     focusedInfo load cancelled it and left an invisible notice in the header. */
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(""), 2500);
    return () => clearTimeout(t);
  }, [notice]);

  const shownPath = panelWorktree(pinned, chosen ?? (rootMode ? undefined : focusedInfo?.worktree), null);
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
    clearTimeout(timer.current);
    if (phone || reducedMotion()) {
      onOpenChange?.(next);
      setMotion(null);
      setOpen(next);
      return;
    }
    /* One View Transition moves the panel, the sidebar and the panes together. */
    if (canViewTransition()) {
      withViewTransition(() => {
        flushSync(() => {
          onOpenChange?.(next, true);
          setMotion(null);
          setOpen(next);
        });
      });
      return;
    }
    onOpenChange?.(next);
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
    if (data.kind === "dir" && !data.root) {
      /* A folder outside any git repo (an agent's /tmp plan folder, say)
         opens as a plain tree; its files read without git. */
      setExternal(null);
      setOutside({ dir: data.path, file: null });
      pickTab("files");
      if (!open || motion === "out") setOpenSaved(true);
      return;
    }
    setOutside(null);
    if (data.kind === "dir") {
      /* A folder opens Files in its own repo, the tree down to the folder,
         so a file inside it can be picked. */
      const root = data.root;
      if (!root) return;
      setChosen(root);
      if (pinned) setPinned(root);
      setExternal(null);
      setFile({ path: null });
      setReveal((r) => ({ worktree: root, dir: relativeTo(root, data.path) ?? "", nonce: (r?.nonce ?? 0) + 1 }));
      pickTab("files");
      if (!open || motion === "out") setOpenSaved(true);
      return;
    }
    const info = gitOf(sessionId);
    /* A .figcraft file under a non-git root opens in the root's own Files
       tree, where the FigCraft viewer draws it. */
    const fileRoot = !info && !pinned ? rootOf(sessionId) : undefined;
    if (fileRoot && isFigcraftPath(data.path) && relativeTo(fileRoot.path, data.path)) {
      setChosen(null);
      setExternal(null);
      setRootFile(data.path);
      pickTab("files");
      if (!open || motion === "out") setOpenSaved(true);
      return;
    }
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

  const count = rootMode ? (rootSummary.summary?.changed_files ?? 0) : (status.data?.totals.files ?? 0);
  const noRepoDir = !shown && focused && !focusedRoot ? sessionDir(sessions[focused]) : undefined;

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

  const changeFocusMode = (on: boolean) => {
    savePref("lana.workbench.gitFocus", on ? "on" : "off");
    withViewTransition(() => flushSync(() => setFocusMode(on)));
  };

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
        /* The panel grows to the whole workspace (and back) in one
           transition; the terminals fade out and in. */
        if (canViewTransition()) withViewTransition(() => flushSync(() => setMaximized((m) => !m)));
        else {
          holdLayout();
          setMaximized((m) => !m);
        }
      }}
      onClose={close}
      status={status.data}
      onRefresh={() => {
        status.refresh();
        refreshResolve();
        rootSummary.refresh();
      }}
      rootView={
        rootMode && focusedRoot
          ? {
              root: focusedRoot,
              summary: rootSummary.summary,
              repoTags: rootSummary.repoTags,
              reported,
              file: rootFile,
              /* Picking a file of the root closes a ⌘+clicked outside one. */
              onFile: (p: string | null) => {
                setExternal(null);
                setRootFile(p);
              },
            }
          : null
      }
      root={
        focusedRoot
          ? {
              info: focusedRoot,
              summary: rootSummary.summary,
              narrowed: rootMode ? null : (chosen ?? null),
              onPick: (w, t) => {
                setChosen(w);
                if (pinned) setPinned(w);
                if (t) pickTab(t);
              },
            }
          : null
      }
      notice={notice}
      onClearReport={focused && focusedSession?.work_dir ? () => void clearWorktreeReport(focused).catch(() => {}) : undefined}
      file={file}
      onFile={(path, line) => {
        setExternal(null);
        setFile({ path, line });
      }}
      external={external}
      outside={
        outside
          ? {
              dir: outside.dir,
              file: outside.file,
              onFile: (p) => setOutside((o) => (o ? { ...o, file: p } : o)),
              onClose: () => setOutside(null),
            }
          : null
      }
      reveal={reveal && reveal.worktree === shown?.worktree ? reveal : null}
      worktreesTab={worktreesTab}
      focusMode={focusMode}
      onFocusMode={(on) => changeFocusMode(on)}
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
    () => ({ gitOf, rootOf, rootChanged, shared, describe, onShared: () => onSharedRef.current() }),
    // describe reads the space and sessions that gitOf and shared already follow.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [gitOf, rootOf, rootChanged, shared, space]
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
    focusMode,
    setFocusMode: changeFocusMode,
  };
}
