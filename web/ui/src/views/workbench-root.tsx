import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  getGitRoot,
  getGitStatus,
  getGitTree,
  listFolder,
  onGitRootEvent,
  subscribeGitRoot,
  unsubscribeGitRoot,
  type FolderEntry,
  type GitFileChange,
  type GitRootInfo,
  type GitRootRepo,
  type GitRootSummary,
  type GitTreeEntry,
} from "../api/workbench-git";
import { Ic } from "../ui/icons";
import { Counts, DiffView, StatusLetter } from "./workbench-changes";
import { FileView } from "./workbench-files";
import { baseName, defaultOpenGroups, dirName, isUnder, orderRootRepos, rootRows, ROOT_ROW_H, type RootRow } from "./workbench-git-model";
import { useVirtualRows } from "./workbench-virtual";

/* Spec 70: the panel for a pane whose folder holds several repos (a root):
   Changes grouped by repo, Files over the whole root, the root picker. */

/* One id per page, so the daemon can tell this tab's subscription apart. */
const CLIENT_ID = "c-" + Math.random().toString(36).slice(2, 10);
const RENEW_MS = 30_000;
const FALLBACK_MS = 10_000;

/* REQ-WM-034, 080, 083: the root summary, refreshed when the daemon pushes
   a new ETag; a slow poll only when no event came for a while (the stream
   dropped). active keeps the daemon's watcher alive. */
export function useRootSummary(root: string | null, active: boolean, panelOpen: boolean): {
  summary: GitRootSummary | null;
  repoTags: Record<string, string>;
  refresh: () => void;
} {
  const [summary, setSummary] = useState<GitRootSummary | null>(null);
  const [repoTags, setRepoTags] = useState<Record<string, string>>({});
  const lastEvent = useRef(0);
  const lastTag = useRef("");

  const fetchNow = useCallback(() => {
    if (!root) return;
    getGitRoot(root)
      .then((s) => setSummary((old) => (old && JSON.stringify(old) === JSON.stringify(s) ? old : s)))
      .catch(() => {});
  }, [root]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- another root starts empty
    setSummary(null);
    setRepoTags({});
    lastTag.current = "";
    if (!root || !active) return;
    fetchNow();
    const sub = () => void subscribeGitRoot(root, CLIENT_ID).catch(() => {});
    sub();
    const renew = setInterval(sub, RENEW_MS);
    const off = onGitRootEvent((ev) => {
      if (ev.root !== root) return;
      lastEvent.current = Date.now();
      setRepoTags((t) => (JSON.stringify(t) === JSON.stringify(ev.repos) ? t : ev.repos));
      if (ev.etag !== lastTag.current) {
        lastTag.current = ev.etag;
        fetchNow();
      }
    });
    return () => {
      clearInterval(renew);
      off();
      void unsubscribeGitRoot(root, CLIENT_ID).catch(() => {});
    };
  }, [root, active, fetchNow]);

  useEffect(() => {
    if (!root || !active || !panelOpen) return;
    const t = setInterval(() => {
      if (document.visibilityState === "visible" && Date.now() - lastEvent.current > FALLBACK_MS * 2.5) fetchNow();
    }, FALLBACK_MS);
    return () => clearInterval(t);
  }, [root, active, panelOpen, fetchNow]);

  return { summary, repoTags, refresh: fetchNow };
}

type Row = RootRow<GitRootRepo, GitFileChange>;

const RepoDot = ({ color }: { color?: string }) => <i className="wbg-wt-dot" style={{ background: color || "var(--text-4)" }} />;

const RootRowView = memo(function RootRowView({
  row,
  selected,
  onToggle,
  onSelect,
  onMore,
  onCleanToggle,
  onNarrow,
  phone,
}: {
  row: Row;
  selected: boolean;
  onToggle: (w: string) => void;
  onSelect: (repo: GitRootRepo, file: GitFileChange) => void;
  onMore: (w: string) => void;
  onCleanToggle: () => void;
  onNarrow: (w: string) => void;
  phone?: boolean;
}) {
  switch (row.kind) {
    case "group":
      return (
        <button type="button" className={"wbr-group" + (row.here ? " is-here" : "")} onClick={() => onToggle(row.repo.worktree)} aria-expanded={row.open} title={row.repo.worktree}>
          <Ic name={row.open ? "chevron-down" : "chevron-right"} size={14} className="wbr-chev" />
          <RepoDot color={row.repo.color} />
          <b className="wbr-name">{row.repo.display}</b>
          {row.here ? (
            <span className="wbr-here">
              <i />
              agent here
            </span>
          ) : (
            <span className="wbr-branch mono">{row.repo.short || row.repo.branch}</span>
          )}
          {row.repo.slow ? <Ic name="clock" size={12} className="wbr-slow" /> : null}
          <span className="wbr-count mono">{row.repo.changed}</span>
        </button>
      );
    case "file":
      return (
        <button
          type="button"
          className={"wbg-change wbr-file" + (selected ? " is-on" : "")}
          onClick={() => onSelect(row.repo, row.file)}
          title={row.repo.display + "/" + row.file.path}
        >
          <StatusLetter status={row.file.status} />
          <span className="wbg-change-text">
            <span className={"wbg-change-name" + (row.file.status === "D" ? " is-deleted" : "")}>{baseName(row.file.path)}</span>
            <small>{dirName(row.file.path)}</small>
          </span>
          {row.file.binary ? <span className="wbg-bin">bin</span> : <Counts add={row.file.add} del={row.file.del} />}
          {phone ? <Ic name="chevron-right" size={14} className="wbg-row-chev" /> : null}
        </button>
      );
    case "more":
      return (
        <button type="button" className="wbr-more" onClick={() => onMore(row.repo.worktree)}>
          … {row.hidden} more
        </button>
      );
    case "loading":
      return <div className="wbr-more">Loading…</div>;
    case "clean":
      return (
        <button type="button" className="wbr-clean" onClick={onCleanToggle} aria-expanded={row.open}>
          <Ic name={row.open ? "chevron-down" : "chevron-right"} size={14} className="wbr-chev" />
          {row.count} clean {row.count === 1 ? "repo" : "repos"}
        </button>
      );
    case "clean-repo":
      return (
        <button type="button" className="wbr-clean-repo" onClick={() => onNarrow(row.repo.worktree)} title={row.repo.worktree}>
          <RepoDot color={row.repo.color} />
          <span className="wbr-name">{row.repo.display}</span>
          <span className="wbr-branch mono">{row.repo.short || row.repo.branch}</span>
        </button>
      );
  }
});

/* REQ-WM-030…036: Changes on a root, one virtualised list of groups. */
export function RootChangesTab({
  root,
  summary,
  repoTags,
  reported,
  onNarrow,
  onOpenFile,
  phone,
}: {
  root: GitRootInfo;
  summary: GitRootSummary | null;
  repoTags: Record<string, string>;
  reported?: string;
  onNarrow: (worktree: string, tab?: "files") => void;
  onOpenFile: (worktree: string, path: string) => void;
  phone?: boolean;
}) {
  const repos = useMemo(() => summary?.repos ?? [], [summary]);
  const changed = useMemo(() => orderRootRepos(repos, reported), [repos, reported]);
  const clean = useMemo(() => repos.filter((r) => r.changed === 0 && r.worktree !== reported), [repos, reported]);
  const [open, setOpen] = useState<Set<string> | null>(null);
  const [showAll, setShowAll] = useState<Set<string>>(() => new Set());
  const [cleanOpen, setCleanOpen] = useState(false);
  const [files, setFiles] = useState<Record<string, GitFileChange[] | undefined>>({});
  const [sel, setSel] = useState<{ worktree: string; path: string } | null>(null);
  const bodyRef = useRef<HTMLDivElement>(null);

  /* Open by default when at most 3 repos changed; the user's toggles hold
     until the root changes. */
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- another root starts from the default
    setOpen(null);
    setFiles({});
    setSel(null);
  }, [root.path]);
  const openSet = useMemo(() => open ?? defaultOpenGroups(changed), [open, changed]);

  /* An open group loads its files, again when its ETag changes. */
  const wanted = useMemo(
    () => changed.filter((r) => openSet.has(r.worktree) || r.worktree === reported).map((r) => r.worktree + "\u0000" + (repoTags[r.name] ?? r.etag ?? "")),
    [changed, openSet, reported, repoTags]
  );
  useEffect(() => {
    let cancelled = false;
    for (const key of wanted) {
      const w = key.split("\u0000")[0] ?? "";
      getGitStatus(w, "uncommitted")
        .then((s) => !cancelled && setFiles((f) => (f[w] && JSON.stringify(f[w]) === JSON.stringify(s.files) ? f : { ...f, [w]: s.files })))
        .catch(() => !cancelled && setFiles((f) => ({ ...f, [w]: f[w] ?? [] })));
    }
    return () => {
      cancelled = true;
    };
  }, [wanted]);

  const rows = useMemo(() => rootRows({ changed, clean, open: openSet, files, showAll, reported, cleanOpen }), [changed, clean, openSet, files, showAll, reported, cleanOpen]);
  const heights = useMemo(() => rows.map((r) => ROOT_ROW_H[r.kind]), [rows]);
  const win = useVirtualRows(bodyRef, heights, 12);

  const fileRows = useMemo(() => rows.filter((r): r is Extract<Row, { kind: "file" }> => r.kind === "file"), [rows]);
  /* Keep a selection on desktop: the first file when none or when it left. */
  useEffect(() => {
    if (phone) return;
    if (sel && fileRows.some((r) => r.repo.worktree === sel.worktree && r.file.path === sel.path)) return;
    const first = fileRows[0];
    // eslint-disable-next-line react-hooks/set-state-in-effect -- follows the list
    setSel(first ? { worktree: first.repo.worktree, path: first.file.path } : null);
  }, [fileRows, sel, phone]);

  const current = sel ? fileRows.find((r) => r.repo.worktree === sel.worktree && r.file.path === sel.path) : undefined;
  const idx = current ? fileRows.indexOf(current) : -1;

  const toggle = useCallback(
    (w: string) =>
      setOpen((o) => {
        const next = new Set(o ?? defaultOpenGroups(changed));
        if (next.has(w)) next.delete(w);
        else next.add(w);
        return next;
      }),
    [changed]
  );
  const more = useCallback((w: string) => setShowAll((s) => new Set(s).add(w)), []);
  const select = useCallback((repo: GitRootRepo, file: GitFileChange) => setSel({ worktree: repo.worktree, path: file.path }), []);
  const cleanToggle = useCallback(() => setCleanOpen((c) => !c), []);
  const narrow = useCallback((w: string) => onNarrow(w, "files"), [onNarrow]);

  /* ↑/↓ step through the files of all groups (REQ-WM-031). */
  useEffect(() => {
    if (phone) return;
    const el = bodyRef.current;
    if (!el) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      const next = fileRows[idx + (e.key === "ArrowDown" ? 1 : -1)];
      if (!next) return;
      e.preventDefault();
      setSel({ worktree: next.repo.worktree, path: next.file.path });
    };
    el.addEventListener("keydown", onKey);
    return () => el.removeEventListener("keydown", onKey);
  }, [fileRows, idx, phone]);

  const totals = useMemo(() => changed.reduce((t, r) => ({ add: t.add + r.additions, del: t.del + r.deletions }), { add: 0, del: 0 }), [changed]);

  const list = (
    <div className="wbg-list wbr-list">
      <div className="wbg-summary">
        <span>
          {summary
            ? changed.length
              ? changed.length + (changed.length === 1 ? " repo" : " repos") + " · " + summary.changed_files + (summary.changed_files === 1 ? " file" : " files")
              : "No changes in " + summary.repos.length + " repos"
            : "Loading…"}
        </span>
        {summary ? <Counts add={totals.add} del={totals.del} /> : null}
      </div>
      <div className="wbg-rows wbr-rows" ref={bodyRef} tabIndex={0}>
        <div style={{ height: win.top }} />
        {rows.slice(win.start, win.end).map((row, k) => {
          const i = win.start + k;
          const key = row.kind === "file" ? row.repo.worktree + "/" + row.file.path : row.kind === "clean" ? "clean" : row.kind + ":" + row.repo.worktree;
          return (
            <div key={key} style={{ height: heights[i] }} className="wbr-slot">
              <RootRowView
                row={row}
                selected={row.kind === "file" && !!sel && sel.worktree === row.repo.worktree && sel.path === row.file.path}
                onToggle={toggle}
                onSelect={select}
                onMore={more}
                onCleanToggle={cleanToggle}
                onNarrow={narrow}
                phone={phone}
              />
            </div>
          );
        })}
        <div style={{ height: win.bottom }} />
      </div>
    </div>
  );

  if (phone) {
    return current ? (
      <div className="wbg-phone-diff">
        <div className="wbg-phone-diff-head">
          <button type="button" className="wbg-back" onClick={() => setSel(null)}>
            <Ic name="chevron-left" size={20} /> Changes
          </button>
          <span className="wb-spacer" />
          <span className="wbg-pos">
            {idx + 1}/{fileRows.length}
          </span>
          <button type="button" className="wbg-icon-btn" aria-label="Previous file" disabled={idx <= 0} onClick={() => select(fileRows[idx - 1]!.repo, fileRows[idx - 1]!.file)}>
            <Ic name="chevron-up" size={18} />
          </button>
          <button type="button" className="wbg-icon-btn" aria-label="Next file" disabled={idx >= fileRows.length - 1} onClick={() => select(fileRows[idx + 1]!.repo, fileRows[idx + 1]!.file)}>
            <Ic name="chevron-down" size={18} />
          </button>
        </div>
        <DiffView worktree={current.repo.worktree} path={current.file.path} scope="uncommitted" change={current.file} onOpenFile={(p) => onOpenFile(current.repo.worktree, p)} />
      </div>
    ) : (
      list
    );
  }

  return (
    <div className="wbg-split">
      {list}
      {current ? (
        <DiffView
          worktree={current.repo.worktree}
          path={current.file.path}
          scope="uncommitted"
          change={current.file}
          onOpenFile={(p) => onOpenFile(current.repo.worktree, p)}
          footer={current.repo.display + " · working tree vs HEAD"}
        />
      ) : summary && !changed.length ? (
        <div className="wbg-viewer wbg-viewer-empty">
          <div className="wbg-empty wbg-empty-full">
            <Ic name="circle-check" size={20} />
            <b>Nothing to review</b>
            <span>No repo in {root.name} has uncommitted changes.</span>
          </div>
        </div>
      ) : (
        <div className="wbg-viewer wbg-viewer-empty" />
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Files on a root (REQ-WM-040…042)                                    */
/* ------------------------------------------------------------------ */

interface TreeNode {
  key: string; // absolute path
  name: string;
  kind: "dir" | "file";
  /* Set inside a repo: its worktree and the path relative to it. */
  worktree?: string;
  rel?: string;
  repo?: { short?: string; color?: string; changed?: number };
  status?: GitTreeEntry["status"];
  changed?: boolean;
  ignored?: boolean;
}

type Kids = Record<string, TreeNode[] | "loading" | { error: string }>;

function join(dir: string, name: string): string {
  return dir.replace(/\/+$/, "") + "/" + name;
}

const TREE_ROW_H = 24;

export function RootFilesTab({
  root,
  summary,
  selected,
  onSelect,
  phone,
}: {
  root: GitRootInfo;
  summary: GitRootSummary | null;
  /* The open file: an absolute path. */
  selected: string | null;
  onSelect: (path: string | null) => void;
  phone?: boolean;
}) {
  const [kids, setKids] = useState<Kids>({});
  const [open, setOpen] = useState<Set<string>>(() => new Set());
  const bodyRef = useRef<HTMLDivElement>(null);

  const repoByPath = useMemo(() => new Map((summary?.repos ?? []).map((r) => [r.worktree, r])), [summary]);

  const load = useCallback(
    (node: TreeNode | null) => {
      const key = node ? node.key : root.path;
      setKids((k) => ({ ...k, [key]: "loading" }));
      const done = (list: TreeNode[]) => {
        setKids((k) => ({ ...k, [key]: list }));
      };
      const fail = (err: unknown) => setKids((k) => ({ ...k, [key]: { error: String((err as Error)?.message ?? err) } }));
      if (node?.worktree !== undefined) {
        /* Inside a repo: the spec 66 tree with git status. */
        const worktree = node.worktree;
        getGitTree(worktree, node.rel ?? "")
          .then((t) =>
            done(
              t.entries.map((e) => ({
                key: join(node.key, e.name),
                name: e.name,
                kind: e.kind,
                worktree,
                rel: node.rel ? node.rel + "/" + e.name : e.name,
                status: e.status,
                changed: e.changed,
                ignored: e.ignored,
              }))
            )
          )
          .catch(fail);
        return;
      }
      listFolder(key)
        .then((l) =>
          done(
            l.entries.map((e: FolderEntry) => {
              const abs = join(key, e.name);
              return e.repo
                ? { key: abs, name: e.name, kind: "dir" as const, worktree: abs, rel: "", repo: { short: e.short, color: e.color, changed: e.changed } }
                : { key: abs, name: e.name, kind: e.kind };
            })
          )
        )
        .catch(fail);
    },
    [root.path]
  );

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- another root starts with a fresh tree
    setKids({});
    setOpen(new Set());
    load(null);
  }, [root.path, load]);

  /* Repo marks follow the watcher's summary, without git runs here. */
  useEffect(() => {
    if (!summary) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- refresh the repo marks from the summary
    setKids((k) => {
      const top = k[root.path];
      if (!Array.isArray(top)) return k;
      let touched = false;
      const next = top.map((n) => {
        const r = n.repo ? repoByPath.get(n.key) : undefined;
        if (!r || (n.repo?.changed === r.changed && n.repo?.short === r.short)) return n;
        touched = true;
        return { ...n, repo: { short: r.short, color: r.color, changed: r.changed } };
      });
      return touched ? { ...k, [root.path]: next } : k;
    });
  }, [summary, repoByPath, root.path]);

  const toggle = (n: TreeNode) => {
    setOpen((o) => {
      const next = new Set(o);
      if (next.has(n.key)) next.delete(n.key);
      else {
        next.add(n.key);
        if (!kids[n.key] || typeof kids[n.key] === "object" && !Array.isArray(kids[n.key])) load(n);
      }
      return next;
    });
  };

  const flat = useMemo(() => {
    const out: { node: TreeNode; depth: number }[] = [];
    const walk = (key: string, depth: number) => {
      const list = kids[key];
      if (!Array.isArray(list)) return;
      for (const n of list) {
        out.push({ node: n, depth });
        if (n.kind === "dir" && open.has(n.key)) walk(n.key, depth + 1);
      }
    };
    walk(root.path, 0);
    return out;
  }, [kids, open, root.path]);
  const heights = useMemo(() => flat.map(() => TREE_ROW_H), [flat]);
  const win = useVirtualRows(bodyRef, heights, 20);

  /* The open file: a tree node, or a path opened from Changes that the tree
     has not loaded yet (its repo comes from the summary). */
  const sel = useMemo((): TreeNode | undefined => {
    if (!selected) return undefined;
    for (const v of Object.values(kids)) {
      if (!Array.isArray(v)) continue;
      const n = v.find((x) => x.key === selected);
      if (n) return n;
    }
    const repo = (summary?.repos ?? []).find((r) => isUnder(selected, r.worktree));
    const name = selected.slice(selected.lastIndexOf("/") + 1);
    return repo ? { key: selected, name, kind: "file", worktree: repo.worktree, rel: selected.slice(repo.worktree.length + 1) } : { key: selected, name, kind: "file" };
  }, [selected, kids, summary]);
  const top = kids[root.path];

  const tree = (
    <div className="wbg-list">
      <div className="wbg-summary">
        <span>
          {root.name} · {root.repos} {root.repos === 1 ? "repo" : "repos"}
        </span>
      </div>
      <div className="wbg-rows" ref={bodyRef}>
        {top === "loading" || top === undefined ? <div className="wbg-tree-note">Loading…</div> : null}
        {top && !Array.isArray(top) && top !== "loading" ? <div className="wbg-tree-note">{top.error}</div> : null}
        <div style={{ height: win.top }} />
        {flat.slice(win.start, win.end).map(({ node: n, depth }) => {
          const isOpen = open.has(n.key);
          const tone = n.status ? " wbg-tone-" + n.status : "";
          return (
            <button
              key={n.key}
              type="button"
              className={"wbg-tree-row" + tone + (n.ignored ? " is-ignored" : "") + (selected === n.key ? " is-on" : "")}
              style={{ paddingLeft: 6 + depth * 14 }}
              onClick={() => (n.kind === "dir" ? toggle(n) : onSelect(n.key))}
              title={n.key}
            >
              {n.kind === "dir" ? <Ic name={isOpen ? "chevron-down" : "chevron-right"} size={13} /> : <span className="wbr-indent" />}
              {n.repo ? <RepoDot color={n.repo.color} /> : <Ic name={n.kind === "dir" ? (isOpen ? "folder-open" : "folder") : "file"} size={14} />}
              <span className="wbg-tree-name">{n.name}</span>
              {n.repo?.short ? <span className="wbr-tree-branch mono">{n.repo.short}</span> : null}
              {n.repo?.changed || n.changed ? <span className="wbg-dot" /> : n.status ? <span className="wbr-tree-st">{n.status}</span> : null}
            </button>
          );
        })}
        <div style={{ height: win.bottom }} />
      </div>
    </div>
  );

  const viewer = sel ? (
    sel.worktree !== undefined && sel.rel ? (
      <FileView worktree={sel.worktree} path={sel.rel} />
    ) : (
      <FileView worktree={root.path} path={sel.key} plain />
    )
  ) : (
    <div className="wbg-viewer wbg-viewer-empty">
      <div className="wbg-empty wbg-empty-full">
        <Ic name="folders" size={20} />
        <b>{root.name}</b>
        <span>Pick a file. Folders outside git open as plain files, without a diff.</span>
      </div>
    </div>
  );

  if (phone) {
    return sel ? (
      <div className="wbg-phone-diff">
        <div className="wbg-phone-diff-head">
          <button type="button" className="wbg-back" onClick={() => onSelect(null)}>
            <Ic name="chevron-left" size={20} /> Files
          </button>
        </div>
        {viewer}
      </div>
    ) : (
      tree
    );
  }
  return (
    <div className="wbg-split">
      {tree}
      {viewer}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Picker and Worktrees on a root (REQ-WM-020, 050)                    */
/* ------------------------------------------------------------------ */

export function RootPicker({
  root,
  summary,
  current,
  onPick,
  onClose,
  onClearReport,
}: {
  root: GitRootInfo;
  summary: GitRootSummary | null;
  /* The narrowed repo's worktree, null on "All repos". */
  current: string | null;
  onPick: (worktree: string | null) => void;
  onClose: () => void;
  onClearReport?: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [cleanOpen, setCleanOpen] = useState(false);
  useEffect(() => {
    const away = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node) && !(e.target as Element).closest?.(".wbg-picker")) onClose();
    };
    window.addEventListener("mousedown", away);
    return () => window.removeEventListener("mousedown", away);
  }, [onClose]);
  const q = query.trim().toLowerCase();
  const repos = (summary?.repos ?? []).filter((r) => !q || (r.name + " " + (r.branch ?? "")).toLowerCase().includes(q));
  const changed = orderRootRepos(repos);
  const clean = repos.filter((r) => r.changed === 0);
  const item = (r: GitRootRepo) => (
    <button key={r.worktree} type="button" role="menuitem" className={"wbg-menu-item" + (current === r.worktree ? " is-on" : "")} onClick={() => onPick(r.worktree)} title={r.worktree}>
      <RepoDot color={r.color} />
      <span className="wbg-menu-text">
        <b className="mono">{r.display}</b>
        <small>{r.short || r.branch || r.error || ""}</small>
      </span>
      <span className="wbg-menu-count wbg-amber">{r.changed || ""}</span>
      {current === r.worktree ? <Ic name="check" size={13} /> : null}
    </button>
  );
  return (
    <div className="wbg-menu wbg-picker-menu" ref={ref} role="menu">
      <label className="wbg-filter">
        <Ic name="search" size={14} />
        <input autoFocus value={query} placeholder="Find repo or branch" onChange={(e) => setQuery(e.target.value)} />
      </label>
      <div className="wbg-section">{root.name} · root folder</div>
      <button type="button" role="menuitem" className={"wbg-menu-item" + (current === null ? " is-on" : "")} onClick={() => onPick(null)}>
        <Ic name="folders" size={14} />
        <span className="wbg-menu-text">
          <b>All repos</b>
          <small>{summary ? summary.changed_repos + " changed · " + summary.changed_files + " files" : "…"}</small>
        </span>
        {current === null ? <Ic name="check" size={13} /> : null}
      </button>
      <div className="wbg-menu-scroll">
        {changed.length ? <div className="wbg-section">Changed · {changed.length}</div> : null}
        {changed.map(item)}
        {clean.length ? (
          <button type="button" className="wbg-menu-item" onClick={() => setCleanOpen((c) => !c)} aria-expanded={cleanOpen}>
            <Ic name={cleanOpen ? "chevron-down" : "chevron-right"} size={14} />
            <span className="wbg-menu-text">
              <b>Clean · {clean.length} repos</b>
            </span>
          </button>
        ) : null}
        {cleanOpen || q ? clean.map(item) : null}
      </div>
      {onClearReport ? (
        <>
          <div className="wbg-menu-sep" />
          <button type="button" role="menuitem" className="wbg-menu-item" onClick={onClearReport}>
            <Ic name="undo-2" size={14} />
            <span className="wbg-menu-text">
              <b>Clear report</b>
              <small>Follow the pane's own folder again</small>
            </span>
          </button>
        </>
      ) : null}
    </div>
  );
}

export function RootRepoChooser({ root, summary, onPick }: { root: GitRootInfo; summary: GitRootSummary | null; onPick: (worktree: string) => void }) {
  const repos = summary?.repos ?? [];
  const changed = orderRootRepos(repos);
  const clean = repos.filter((r) => r.changed === 0);
  return (
    <div className="wbr-chooser">
      <div className="wbg-empty">
        <Ic name="folder-git-2" size={20} />
        <b>Choose a repo in {root.name}</b>
        <span>Worktrees belong to one repo. Pick it to manage its worktrees.</span>
      </div>
      <div className="wbr-chooser-list">
        {[...changed, ...clean].map((r) => (
          <button key={r.worktree} type="button" className="wbr-clean-repo" onClick={() => onPick(r.worktree)} title={r.worktree}>
            <RepoDot color={r.color} />
            <span className="wbr-name">{r.display}</span>
            <span className="wbr-branch mono">{r.short || r.branch}</span>
            <span className="wbr-count mono">{r.changed || ""}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

/* The repo of the reported worktree, when it lies in the root. */
export function reportedIn(root: GitRootInfo | undefined, workDir: string | undefined, worktree: string | undefined): string | undefined {
  if (!root || !workDir || !worktree) return undefined;
  return isUnder(worktree, root.path) ? worktree : undefined;
}
