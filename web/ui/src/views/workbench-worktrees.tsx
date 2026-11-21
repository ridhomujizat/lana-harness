import { useEffect, useMemo, useRef, useState } from "react";

import { ApiError, errorMessage } from "../api/client";
import {
  cleanupGitWorktrees,
  createGitWorktree,
  getCopyCandidates,
  getGitSettings,
  listGitWorktrees,
  lockGitWorktree,
  moveGitWorktrees,
  pruneGitWorktrees,
  putGitSettings,
  removeGitWorktree,
  suggestWorktreePath,
  type GitWorktree,
  type GitWorktreeList,
  type WorktreeResult,
} from "../api/workbench-git";
import { Button, Checkbox, Input, Menu, MenuItem, MenuSeparator, Modal } from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { baseName, plural, shortAge, shortBranch } from "./workbench-git-model";
import type { SpaceWorktree } from "./workbench-git-panel";

/* Spec 66 REQ-WG-080..085: the Worktrees tab and its dialogs. */

export interface WorktreeActions {
  /* Starts a session in dir as a new tab of this space (model A). */
  openTab: (dir: string, program: "claude" | "shell") => void;
  /* Shows that worktree in the panel. */
  show: (dir: string) => void;
  /* Live sessions whose directory is inside dir (REQ-WG-033). */
  sessionsIn: (dir: string) => string[];
}

type Filter = "all" | "active" | "merged" | "prunable";

function tilde(path: string): string {
  return path.replace(/^\/(?:Users|home)\/[^/]+/, "~");
}

function chipList(w: GitWorktree, uses?: string) {
  const chips: [string, string][] = [];
  if (w.main) chips.push(["main checkout", "gray"]);
  if (w.agent) chips.push(["Claude agent", "purple"]);
  if (w.prunable) chips.push(["folder missing", "red"]);
  if (w.locked) chips.push(["locked", "gray"]);
  if (w.dirty) chips.push([w.dirty + " changed", "amber"]);
  if (w.ahead) chips.push(["↑" + w.ahead, "blue"]);
  if (w.merged && !w.main) chips.push(["merged", "green"]);
  if (w.error) chips.push(["error", "red"]);
  if (uses) chips.push([uses, "gray"]);
  return chips;
}

export function WorktreesTab({
  repo,
  inSpace,
  actions,
  phone,
  newNonce = 0,
  spaceName,
}: {
  spaceName?: string;
  repo: string;
  inSpace: SpaceWorktree[];
  actions: WorktreeActions;
  phone?: boolean;
  /* A change opens the New worktree dialog (the "+" menu, REQ-WG-076). */
  newNonce?: number;
}) {
  const [data, setData] = useState<GitWorktreeList | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const [nonce, setNonce] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [showAllMerged, setShowAllMerged] = useState(false);
  const [dialog, setDialog] = useState<
    | { kind: "new" }
    | { kind: "cleanup"; paths: string[] }
    | { kind: "move" }
    | { kind: "remove"; w: GitWorktree }
    | null
  >(null);
  const [menu, setMenu] = useState<{ w: GitWorktree; anchor: HTMLElement } | null>(null);
  const anchorRef = useRef<HTMLElement | null>(null);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    let cancelled = false;
    listGitWorktrees(repo)
      .then((d) => {
        if (cancelled) return;
        setData(d);
        setError("");
        /* Merged, clean worktrees start checked for clean-up (REQ-WG-080). */
        setSelected(new Set(d.worktrees.filter((w) => w.merged && !w.main && !w.dirty && !w.prunable).map((w) => w.path)));
      })
      .catch((err) => !cancelled && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [repo, nonce]);
  const reload = () => setNonce((n) => n + 1);
  const seenNew = useRef(newNonce);
  useEffect(() => {
    if (newNonce === seenNew.current) return;
    seenNew.current = newNonce;
    setDialog({ kind: "new" });
  }, [newNonce]);

  const usesOf = useMemo(() => new Map(inSpace.map((w) => [w.info.worktree, w.uses])), [inSpace]);
  const all = data?.worktrees ?? [];
  const q = query.trim().toLowerCase();
  const match = (w: GitWorktree) => !q || (w.name + " " + w.branch + " " + w.path).toLowerCase().includes(q);
  const inSpaceRows = all.filter((w) => usesOf.has(w.path) && match(w));
  const active = all.filter((w) => !usesOf.has(w.path) && !w.merged && !w.prunable && match(w));
  const merged = all.filter((w) => !usesOf.has(w.path) && w.merged && !w.prunable && !w.main && match(w));
  const prunable = all.filter((w) => w.prunable && match(w));
  const cleanable = all.filter((w) => w.merged && !w.main && !w.dirty && !w.prunable && !actions.sessionsIn(w.path).length);
  const siblings = all.filter((w) => w.location === "sibling" && !w.prunable);
  const counts = { all: all.length, active: all.filter((w) => !w.merged && !w.prunable).length, merged: all.filter((w) => w.merged && !w.main).length, prunable: all.filter((w) => w.prunable).length };

  const groups: [string, GitWorktree[], "space" | "active" | "merged" | "prunable"][] = [];
  if (filter === "all" || filter === "active") groups.push(["In this space", inSpaceRows, "space"], ["Active", active, "active"]);
  if (filter === "all" || filter === "merged") groups.push(["Merged into " + (data?.base.replace(/^origin\//, "") || "base"), merged, "merged"]);
  if (filter === "all" || filter === "prunable") groups.push(["Prunable", prunable, "prunable"]);

  const toggleSel = (path: string) => {
    const next = new Set(selected);
    if (next.has(path)) next.delete(path);
    else next.add(path);
    setSelected(next);
  };

  const prune = () => {
    pruneGitWorktrees(repo)
      .then((r) => {
        setNotice(r.pruned.length ? "Pruned " + plural(r.pruned.length, "worktree") + "." : "Nothing to prune.");
        reload();
      })
      .catch((err) => setNotice(errorMessage(err)));
  };

  const row = (w: GitWorktree, kind: "space" | "active" | "merged" | "prunable") => {
    const uses = usesOf.get(w.path);
    return (
      <div key={w.path} className={"wbg-wt-row" + (kind === "prunable" ? " is-dim" : "")} title={w.path}>
        {kind === "merged" ? (
          <Checkbox checked={selected.has(w.path)} onChange={() => toggleSel(w.path)} aria-label={"Select " + w.name} />
        ) : uses ? (
          <i className="wbg-wt-dot wbg-wt-dot-lg" style={{ background: w.color }} />
        ) : (
          <Ic name="folder-git-2" size={16} />
        )}
        <span className="wbg-wt-text">
          <span className="wbg-wt-line">
            <b>{w.name}</b>
            {chipList(w, uses).map(([label, tone]) => (
              <span key={label} className={"wbg-chip wbg-chip-" + tone}>
                {label}
              </span>
            ))}
          </span>
          <small className="mono">
            <Ic name="git-branch" size={11} /> {w.branch || "detached " + w.head}
          </small>
        </span>
        <span className="wbg-wt-age">{w.prunable ? "" : shortAge(w.head_time)}</span>
        {kind !== "prunable" ? (
          <span className="wbg-wt-actions">
            <button type="button" className="wbg-icon-btn" aria-label="Claude Code here" data-tip="Claude Code here" onClick={() => actions.openTab(w.path, "claude")}>
              <Ic name="square-terminal" size={15} />
            </button>
            <button type="button" className="wbg-icon-btn" aria-label="Show in panel" data-tip="Show in panel" onClick={() => actions.show(w.path)}>
              <Ic name="git-compare" size={15} />
            </button>
            <button
              type="button"
              className="wbg-icon-btn"
              aria-label="More"
              onClick={(e) => {
                anchorRef.current = e.currentTarget;
                setMenu({ w, anchor: e.currentTarget });
              }}
            >
              <Ic name="ellipsis" size={15} />
            </button>
          </span>
        ) : null}
      </div>
    );
  };

  return (
    <div className="wbg-wt">
      <div className="wbg-wt-bar">
        <label className="wbg-filter">
          <Ic name="search" size={14} />
          <input value={query} placeholder="Filter worktrees" onChange={(e) => setQuery(e.target.value)} />
        </label>
        {!phone ? (
          <span className="wbg-seg wbg-seg-sm" role="group" aria-label="Show">
            {(["all", "active", "merged", "prunable"] as const).map((f) => (
              <button key={f} type="button" className={filter === f ? "is-on" : ""} onClick={() => setFilter(f)}>
                {f.charAt(0).toUpperCase() + f.slice(1)} <small>{counts[f]}</small>
              </button>
            ))}
          </span>
        ) : null}
        <Button variant="primary" size="sm" icon="plus" onClick={() => setDialog({ kind: "new" })}>
          New worktree
        </Button>
      </div>
      {notice ? (
        <div className="wbg-banner">
          <span>{notice}</span>
          <button type="button" className="wbg-icon-btn" aria-label="Dismiss" onClick={() => setNotice("")}>
            <Ic name="x" size={14} />
          </button>
        </div>
      ) : null}
      {cleanable.length ? (
        <div className="wbg-banner wbg-banner-accent">
          <Ic name="eraser" size={16} />
          <span>
            {plural(cleanable.length, "worktree")} {cleanable.length === 1 ? "is" : "are"} merged into {data?.base.replace(/^origin\//, "")} and{" "}
            {cleanable.length === 1 ? "has" : "have"} no local changes.
          </span>
          <Button size="sm" onClick={() => setDialog({ kind: "cleanup", paths: cleanable.map((w) => w.path) })}>
            Clean up {cleanable.length}…
          </Button>
        </div>
      ) : null}
      {siblings.length ? (
        <div className="wbg-banner wbg-banner-blue">
          <Ic name="folder-input" size={16} />
          <span>
            {plural(siblings.length, "worktree")} {siblings.length === 1 ? "sits" : "sit"} next to the repo. Move them into {tilde(data?.root || "")}/
          </span>
          <Button size="sm" onClick={() => setDialog({ kind: "move" })}>
            Move {siblings.length}…
          </Button>
        </div>
      ) : null}
      <div className="wbg-rows wbg-wt-list">
        {error ? <p className="wbg-note">{error}</p> : null}
        {!data && !error ? <p className="wbg-note">Loading worktrees…</p> : null}
        {groups.map(([title, list, kind]) =>
          list.length ? (
            <section key={kind} className="wbg-wt-group">
              <div className="wbg-section">
                {title} <small>{list.length}</small>
                <span className="wb-spacer" />
                {kind === "merged" ? (
                  <button
                    type="button"
                    className="wbg-link"
                    onClick={() => setDialog({ kind: "cleanup", paths: list.filter((w) => selected.has(w.path)).map((w) => w.path) })}
                    disabled={!list.some((w) => selected.has(w.path))}
                  >
                    Clean up selected
                  </button>
                ) : null}
                {kind === "prunable" ? (
                  <button type="button" className="wbg-link" onClick={prune}>
                    Prune all
                  </button>
                ) : null}
              </div>
              {(kind === "merged" && !showAllMerged ? list.slice(0, 3) : list).map((w) => row(w, kind))}
              {kind === "merged" && !showAllMerged && list.length > 3 ? (
                <button type="button" className="wbg-link wbg-more" onClick={() => setShowAllMerged(true)}>
                  Show {list.length - 3} more
                </button>
              ) : null}
            </section>
          ) : null
        )}
      </div>
      {menu ? (
        <Menu anchorRef={anchorRef} open onClose={() => setMenu(null)}>
          <MenuItem icon="external-link" onClick={() => (actions.openTab(menu.w.path, "shell"), setMenu(null))}>
            Open in new tab
          </MenuItem>
          <MenuItem icon="square-terminal" onClick={() => (actions.openTab(menu.w.path, "claude"), setMenu(null))}>
            Claude Code here
          </MenuItem>
          <MenuItem icon="copy" onClick={() => (void navigator.clipboard?.writeText(menu.w.path), setMenu(null))}>
            Copy path
          </MenuItem>
          <MenuSeparator />
          <MenuItem
            icon="lock"
            onClick={() => {
              const w = menu.w;
              setMenu(null);
              lockGitWorktree(w.path, !w.locked)
                .then(reload)
                .catch((err) => setNotice(errorMessage(err)));
            }}
          >
            {menu.w.locked ? "Unlock" : "Lock"}
          </MenuItem>
          {!menu.w.main ? (
            <>
              <MenuSeparator />
              <MenuItem icon="trash" danger onClick={() => (setDialog({ kind: "remove", w: menu.w }), setMenu(null))}>
                Remove…
              </MenuItem>
            </>
          ) : null}
        </Menu>
      ) : null}
      {dialog?.kind === "new" && data ? (
        <NewWorktreeDialog
          list={data}
          spaceName={spaceName || "this space"}
          onClose={() => setDialog(null)}
          onCreated={(w, start, newSpace) => {
            setDialog(null);
            reload();
            if (start !== "none") actions.openTab(w.path, start);
            else if (!newSpace) actions.show(w.path);
          }}
        />
      ) : null}
      {dialog?.kind === "cleanup" && data ? (
        <CleanupDialog
          list={data}
          paths={dialog.paths}
          sessionsIn={actions.sessionsIn}
          onClose={() => setDialog(null)}
          onDone={(msg) => {
            setDialog(null);
            setNotice(msg);
            reload();
          }}
        />
      ) : null}
      {dialog?.kind === "move" && data ? (
        <MoveDialog
          list={data}
          sessionsIn={actions.sessionsIn}
          onClose={() => setDialog(null)}
          onDone={(msg) => {
            setDialog(null);
            setNotice(msg);
            reload();
          }}
        />
      ) : null}
      {dialog?.kind === "remove" ? (
        <RemoveDialog
          w={dialog.w}
          sessions={actions.sessionsIn(dialog.w.path).length}
          onReview={() => {
            actions.show(dialog.w.path);
            setDialog(null);
          }}
          onPush={() => {
            actions.openTab(dialog.w.path, "shell");
            setDialog(null);
          }}
          onClose={() => setDialog(null)}
          onDone={(msg) => {
            setDialog(null);
            setNotice(msg);
            reload();
          }}
        />
      ) : null}
    </div>
  );
}

/* REQ-WG-082 */
function NewWorktreeDialog({
  list,
  spaceName,
  onClose,
  onCreated,
}: {
  list: GitWorktreeList;
  spaceName: string;
  onClose: () => void;
  onCreated: (w: GitWorktree, start: "claude" | "shell" | "none", newSpace: boolean) => void;
}) {
  const [newBranch, setNewBranch] = useState(true);
  const [branch, setBranch] = useState("");
  const [base, setBase] = useState(list.base || "origin/development");
  const [fetchFirst, setFetchFirst] = useState(true);
  const [path, setPath] = useState("");
  const [pathEdited, setPathEdited] = useState(false);
  const [copy, setCopy] = useState<{ path: string; size: number; on: boolean }[]>([]);
  const [allCopy, setAllCopy] = useState(false);
  const [start, setStart] = useState<"claude" | "shell" | "none">("claude");
  const [newSpace, setNewSpace] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    getCopyCandidates(list.repo)
      .then((r) =>
        setCopy(
          r.files
            .filter((f) => baseName(f.path) !== ".DS_Store")
            .sort((a, b) => Number(b.default) - Number(a.default))
            .map((f) => ({ path: f.path, size: f.size, on: f.default }))
        )
      )
      .catch(() => {});
  }, [list.repo]);

  /* The folder follows the branch until the owner edits it (REQ-WG-030). */
  useEffect(() => {
    if (pathEdited || !branch.trim()) return;
    const timer = window.setTimeout(() => {
      suggestWorktreePath(list.repo, branch.trim())
        .then((r) => setPath(r.path))
        .catch(() => {});
    }, 200);
    return () => window.clearTimeout(timer);
  }, [branch, pathEdited, list.repo]);

  const rel = path ? "../" + path.split("/").slice(-2).join("/") : "…";
  const command = [
    fetchFirst ? "git fetch origin" : "",
    "git worktree add " + (newBranch ? "-b " + (branch || "<branch>") + " " : "") + rel + " " + (newBranch ? base : branch || "<branch>"),
  ].filter(Boolean);

  const create = () => {
    setBusy(true);
    setError("");
    createGitWorktree({
      repo: list.repo,
      branch: branch.trim(),
      new_branch: newBranch,
      base,
      fetch: fetchFirst,
      path,
      copy: copy.filter((c) => c.on).map((c) => c.path),
    })
      .then((w) => onCreated(w, start, newSpace))
      .catch((err) => {
        setBusy(false);
        if (err instanceof ApiError && err.code === "branch_exists") setError("That branch exists. Choose Existing branch, or another name.");
        else if (err instanceof ApiError && err.code === "path_exists") setError("That folder exists. Pick another folder.");
        else setError(errorMessage(err));
      });
  };

  return (
    <Modal
      title="New worktree"
      subtitle={tilde(list.repo.replace(/\/\.git$/, ""))}
      onClose={onClose}
      className="wbg-dialog"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={!branch.trim() || !path} onClick={create}>
            Create worktree
          </Button>
        </>
      }
    >
      <div className="wbg-form">
        <span className="wbg-seg" role="group" aria-label="Branch">
          <button type="button" className={newBranch ? "is-on" : ""} onClick={() => setNewBranch(true)}>
            New branch
          </button>
          <button type="button" className={!newBranch ? "is-on" : ""} onClick={() => setNewBranch(false)}>
            Existing branch
          </button>
        </span>
        <label className="wbg-field">
          <span>Branch name</span>
          <Input className="mono" value={branch} placeholder="feature/name" onChange={(e) => setBranch(e.target.value)} data-autofocus />
          <small>Lowercase, slash for the type: feature/, fix/, perf/, chore/</small>
        </label>
        {newBranch ? (
          <div className="wbg-row2">
            <label className="wbg-field">
              <span>Base</span>
              <Input className="mono" value={base} onChange={(e) => setBase(e.target.value)} />
            </label>
            <label className="wbg-check">
              <Checkbox checked={fetchFirst} onChange={setFetchFirst} />
              <span className="mono">git fetch origin</span>
            </label>
          </div>
        ) : null}
        <label className="wbg-field">
          <span>Folder</span>
          <Input
            className="mono"
            value={tilde(path)}
            onChange={(e) => {
              setPathEdited(true);
              setPath(e.target.value.replace(/^~/, list.root.replace(/^(\/(?:Users|home)\/[^/]+).*/, "$1")));
            }}
          />
          <small>Inside the repo's worktree folder {tilde(list.root)}/ (change it per repo in Settings).</small>
        </label>
        {copy.length ? (
          <div className="wbg-field">
            <span>Copy local files (git-ignored, not in the new checkout)</span>
            {copy.map((c, i) =>
              allCopy || c.on ? (
                <label key={c.path} className="wbg-check">
                  <Checkbox checked={c.on} onChange={(on) => setCopy(copy.map((x, j) => (j === i ? { ...x, on } : x)))} />
                  <span className="mono">{c.path}</span>
                  <small>{c.size < 1024 ? c.size + " B" : Math.round(c.size / 1024) + " KB"}</small>
                </label>
              ) : null
            )}
            {!allCopy && copy.some((c) => !c.on) ? (
              <button type="button" className="wbg-link wbg-more-inline" onClick={() => setAllCopy(true)}>
                Show {copy.filter((c) => !c.on).length} more ignored files
              </button>
            ) : null}
          </div>
        ) : null}
        <div className="wbg-field">
          <span>After create</span>
          <div className="wbg-row2">
            <span className="wbg-seg wbg-seg-sm" role="group" aria-label="Start">
              {(
                [
                  ["claude", "Claude Code"],
                  ["shell", "Terminal"],
                  ["none", "Nothing"],
                ] as const
              ).map(([k, label]) => (
                <button key={k} type="button" className={start === k ? "is-on" : ""} onClick={() => setStart(k)}>
                  {label}
                </button>
              ))}
            </span>
            <span className="muted">in</span>
            <span className="wbg-seg wbg-seg-sm" role="group" aria-label="Open in">
              <button type="button" className={!newSpace ? "is-on" : ""} onClick={() => setNewSpace(false)}>
                New tab in {spaceName}
              </button>
              <button type="button" className={newSpace ? "is-on" : ""} onClick={() => setNewSpace(true)}>
                New space
              </button>
            </span>
          </div>
        </div>
        <pre className="wbg-cmd">{command.map((c) => "$ " + c).join("\n")}</pre>
        {error ? <p className="wbg-error">{error}</p> : null}
      </div>
    </Modal>
  );
}

function resultsMessage(results: WorktreeResult[], verb: string): string {
  const done = results.filter((r) => r.result === "removed" || r.result === "moved").length;
  const skipped = results.length - done;
  return verb + " " + plural(done, "worktree") + (skipped ? "; " + skipped + " skipped (" + Array.from(new Set(results.filter((r) => r.reason).map((r) => r.reason))).join(", ") + ")" : "") + ".";
}

/* REQ-WG-083 */
function CleanupDialog({
  list,
  paths,
  sessionsIn,
  onClose,
  onDone,
}: {
  list: GitWorktreeList;
  paths: string[];
  sessionsIn: (dir: string) => string[];
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const rows = list.worktrees.filter((w) => paths.includes(w.path) || (w.merged && !w.main && !w.dirty && !w.prunable));
  const [on, setOn] = useState<Set<string>>(() => new Set(paths.filter((p) => !sessionsIn(p).length)));
  const [deleteBranch, setDeleteBranch] = useState(true);
  const [deleteRemote, setDeleteRemote] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [all, setAll] = useState(false);
  const shown = all ? rows : rows.slice(0, 8);

  const run = () => {
    setBusy(true);
    cleanupGitWorktrees(Array.from(on), deleteBranch, deleteRemote)
      .then((r) => onDone(resultsMessage(r.results, "Removed")))
      .catch((err) => {
        setBusy(false);
        setError(errorMessage(err));
      });
  };

  return (
    <Modal
      title={"Clean up " + plural(rows.length, "worktree")}
      subtitle={"Merged into " + list.base.replace(/^origin\//, "") + ", no local changes. Folders are deleted from disk."}
      onClose={onClose}
      className="wbg-dialog"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" icon="trash" loading={busy} disabled={!on.size} onClick={run}>
            Remove {on.size}
          </Button>
        </>
      }
    >
      <div className="wbg-form">
        <div className="wbg-table">
          <label className="wbg-table-head">
            <Checkbox checked={on.size === rows.length} onChange={(v) => setOn(new Set(v ? rows.map((w) => w.path) : []))} />
            <b>All {rows.length}</b>
            <span className="wb-spacer" />
            <small>last commit</small>
          </label>
          {shown.map((w) => {
            const busyIn = sessionsIn(w.path).length;
            const outside = w.location === "other";
            return (
              <label key={w.path} className="wbg-table-row">
                <Checkbox
                  checked={on.has(w.path)}
                  onChange={(v) => {
                    const next = new Set(on);
                    if (v) next.add(w.path);
                    else next.delete(w.path);
                    setOn(next);
                  }}
                />
                <span className="wbg-wt-text">
                  <b>{w.name}</b>
                  <small className="mono">
                    {w.branch}
                    {busyIn ? <em className="wbg-warn"> · space open, {plural(busyIn, "terminal")}</em> : null}
                    {outside ? <em className="wbg-warn"> · outside repo, {tilde(w.path)}</em> : null}
                  </small>
                </span>
                <small>{shortAge(w.head_time)}</small>
              </label>
            );
          })}
          {!all && rows.length > 8 ? (
            <button type="button" className="wbg-link wbg-more" onClick={() => setAll(true)}>
              Show {rows.length - 8} more
            </button>
          ) : null}
        </div>
        <div className="wbg-field">
          <span>Branches</span>
          <label className="wbg-check">
            <Checkbox checked={deleteBranch} onChange={setDeleteBranch} />
            Delete local branches <small className="mono">git branch -d</small>
          </label>
          <label className="wbg-check">
            <Checkbox checked={deleteRemote} onChange={setDeleteRemote} />
            Delete remote branches on origin
          </label>
        </div>
        {rows.some((w) => sessionsIn(w.path).length) ? <p className="wbg-warn">Worktrees with a running terminal are skipped.</p> : null}
        {error ? <p className="wbg-error">{error}</p> : null}
      </div>
    </Modal>
  );
}

/* REQ-WG-084 */
function RemoveDialog({
  w,
  sessions,
  onReview,
  onPush,
  onClose,
  onDone,
}: {
  w: GitWorktree;
  sessions: number;
  onReview: () => void;
  onPush: () => void;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [confirm, setConfirm] = useState("");
  const [deleteBranch, setDeleteBranch] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [work, setWork] = useState<{ dirty: number; unpushed: number } | null>(w.dirty ? { dirty: w.dirty, unpushed: 0 } : null);

  const attempt = (force: boolean) => {
    setBusy(true);
    setError("");
    removeGitWorktree({ path: w.path, force, confirm: force ? confirm : undefined, delete_branch: deleteBranch })
      .then(() => onDone("Removed " + w.name + "."))
      .catch((err) => {
        setBusy(false);
        if (err instanceof ApiError && err.code === "has_work") {
          const d = (err.details[0] ?? {}) as { dirty?: number; unpushed?: number };
          setWork({ dirty: d.dirty ?? w.dirty, unpushed: d.unpushed ?? 0 });
        } else if (err instanceof ApiError && err.code === "in_use") setError("A terminal is still running in it. Close it first.");
        else setError(errorMessage(err));
      });
  };

  /* A clean worktree is removed at once; one with work asks first. */
  const risky = !!work && (work.dirty > 0 || work.unpushed > 0);
  return (
    <Modal
      title={"Remove " + w.name + "?"}
      size="sm"
      onClose={onClose}
      className="wbg-dialog"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" loading={busy} disabled={sessions > 0 || (risky && confirm !== w.name)} onClick={() => attempt(risky)}>
            {risky ? "Remove anyway" : "Remove"}
          </Button>
        </>
      }
    >
      <div className="wbg-form">
        {sessions > 0 ? <p className="wbg-warn">{plural(sessions, "terminal")} still running in it. Close them first.</p> : null}
        {risky ? (
          <>
            <div className="wbg-danger-box">
              <Ic name="triangle-alert" size={18} />
              <div>
                <b>This throws away work</b>
                {work.dirty ? <span>{plural(work.dirty, "uncommitted change")}</span> : null}
                {work.unpushed ? <span>{plural(work.unpushed, "commit")} not on any remote</span> : null}
                <small className="mono">branch {w.branch}</small>
              </div>
            </div>
            <div className="wbg-row2">
              <Button size="sm" icon="git-compare" onClick={onReview}>
                Review changes
              </Button>
              <Button size="sm" icon="cloud-upload" onClick={onPush}>
                Push branch
              </Button>
            </div>
            <label className="wbg-field">
              <span>Type the folder name to remove anyway</span>
              <Input className="mono" value={confirm} placeholder={w.name} onChange={(e) => setConfirm(e.target.value)} />
            </label>
          </>
        ) : (
          <p>
            The folder {tilde(w.path)} is deleted. The branch <span className="mono">{shortBranch(w.branch) ? w.branch : w.head}</span> stays unless you tick the box.
          </p>
        )}
        <label className="wbg-check">
          <Checkbox checked={deleteBranch} onChange={setDeleteBranch} />
          Also delete branch <span className="mono">{w.branch}</span>
        </label>
        {error ? <p className="wbg-error">{error}</p> : null}
      </div>
    </Modal>
  );
}

/* REQ-WG-085 */
function MoveDialog({
  list,
  sessionsIn,
  onClose,
  onDone,
}: {
  list: GitWorktreeList;
  sessionsIn: (dir: string) => string[];
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const siblings = list.worktrees.filter((w) => w.location === "sibling" && !w.prunable);
  const [targets, setTargets] = useState<Record<string, string>>({});
  const [removeMerged, setRemoveMerged] = useState(true);
  const [remember, setRemember] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [all, setAll] = useState(false);

  useEffect(() => {
    let cancelled = false;
    for (const w of siblings) {
      if (!w.branch) continue;
      suggestWorktreePath(list.repo, w.branch)
        .then((r) => !cancelled && setTargets((t) => ({ ...t, [w.path]: r.path })))
        .catch(() => {});
    }
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.repo]);

  const blocked = (w: GitWorktree) => (sessionsIn(w.path).length ? plural(sessionsIn(w.path).length, "terminal") + " running" : w.locked ? "locked" : "");
  const toRemove = removeMerged ? siblings.filter((w) => w.merged && !w.dirty && !blocked(w)) : [];
  const toMove = siblings.flatMap((w) => {
    const to = targets[w.path];
    return !toRemove.includes(w) && !blocked(w) && to ? [{ w, to }] : [];
  });
  const shown = all ? siblings : siblings.slice(0, 6);

  const run = async () => {
    setBusy(true);
    setError("");
    try {
      const results: WorktreeResult[] = [];
      if (toRemove.length) results.push(...(await cleanupGitWorktrees(toRemove.map((w) => w.path), true, false)).results);
      if (toMove.length) results.push(...(await moveGitWorktrees(toMove.map((m) => ({ path: m.w.path, to: m.to })))).results);
      if (remember) await putGitSettings(list.repo, { worktree_root: list.root }).catch(() => undefined);
      onDone(resultsMessage(results, "Done:"));
    } catch (err) {
      setBusy(false);
      setError(errorMessage(err));
    }
  };

  /* Keep the settings call honest: read them once so a later save keeps
     the base the owner chose. */
  useEffect(() => {
    getGitSettings(list.repo).catch(() => undefined);
  }, [list.repo]);

  return (
    <Modal
      title={"Move " + plural(siblings.length, "worktree")}
      subtitle={"Into " + tilde(list.root) + "/ with git worktree move. Branches and changes stay."}
      onClose={onClose}
      className="wbg-dialog"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" icon="folder-input" loading={busy} disabled={!toMove.length && !toRemove.length} onClick={() => void run()}>
            {[toMove.length ? "Move " + toMove.length : "", toRemove.length ? "remove " + toRemove.length : ""].filter(Boolean).join(", ") || "Nothing to do"}
          </Button>
        </>
      }
    >
      <div className="wbg-form">
        <div className="wbg-table">
          <div className="wbg-table-head">
            <small>From</small>
            <span className="wb-spacer" />
            <small>To {tilde(list.root)}/</small>
          </div>
          {shown.map((w) => {
            const why = blocked(w);
            const removing = toRemove.includes(w);
            return (
              <div key={w.path} className={"wbg-table-row" + (why ? " is-dim" : "")}>
                <span className="wbg-wt-text">
                  <b className="mono">{w.name}</b>
                  <small className={why ? "wbg-warn" : removing ? "wbg-ok" : ""}>
                    {why ? "skipped · " + why : removing ? "merged · removed instead" : w.dirty ? w.dirty + " changed · moves with it" : ""}
                  </small>
                </span>
                <Ic name="arrow-right" size={14} />
                <span className="mono wbg-move-to">{baseName(targets[w.path] ?? "…")}</span>
              </div>
            );
          })}
          {!all && siblings.length > 6 ? (
            <button type="button" className="wbg-link wbg-more" onClick={() => setAll(true)}>
              Show {siblings.length - 6} more
            </button>
          ) : null}
        </div>
        <label className="wbg-check">
          <Checkbox checked={removeMerged} onChange={setRemoveMerged} />
          Remove the merged ones instead of moving them
        </label>
        <label className="wbg-check">
          <Checkbox checked={remember} onChange={setRemember} />
          New worktrees go into {baseName(list.root)}/ from now on
        </label>
        <small className="muted">Folder names: the branch without its type (feature/router → router); on a clash, the whole branch with - (fix-router).</small>
        {error ? <p className="wbg-error">{error}</p> : null}
      </div>
    </Modal>
  );
}
