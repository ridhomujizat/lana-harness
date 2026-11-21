import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { errorMessage } from "../api/client";
import type { WorkbenchPreview } from "../api/workbench";
import {
  getGitDiff,
  getGitFile,
  getGitTree,
  isTextFile,
  searchGitFiles,
  type GitFileChange,
  type GitFileContent,
  type GitTreeEntry,
} from "../api/workbench-git";
import { Ic } from "../ui/icons";
import { CHAR_W, Code, Counts, DiffView, LINE_H, StatusLetter } from "./workbench-changes";
import { baseName, blockCommentStates, dirName, gutterMarks, type GutterMark } from "./workbench-git-model";
import { humanSize } from "./workbench-preview";
import { useVirtualRows } from "./workbench-virtual";

/* Spec 66 REQ-WG-064, 065: the Files tab, a lazy tree of the worktree and a
   read-only viewer with a git gutter. */

function join(dir: string, name: string): string {
  return dir ? dir + "/" + name : name;
}

type Tree = Record<string, GitTreeEntry[] | "loading" | { error: string }>;

/* The name of a folder's only child when that child is a folder. */
function soleDir(kids: Tree[string] | undefined): string | null {
  if (!Array.isArray(kids) || kids.length !== 1) return null;
  const only = kids[0];
  return only && only.kind === "dir" ? only.name : null;
}

export function FilesTab({
  worktree,
  changes,
  selected,
  line,
  onSelect,
  external,
  phone,
}: {
  worktree: string;
  changes: GitFileChange[];
  selected: string | null;
  line?: number;
  onSelect: (path: string | null, line?: number) => void;
  /* A spec 61 preview of a path outside the worktree (REQ-WG-063). */
  external?: WorkbenchPreview | null;
  phone?: boolean;
}) {
  const [tree, setTree] = useState<Tree>({});
  const [open, setOpen] = useState<Set<string>>(() => new Set());
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<string[] | null>(null);
  const filterRef = useRef<HTMLInputElement>(null);

  const load = useCallback(
    (dir: string) => {
      setTree((t) => ({ ...t, [dir]: "loading" }));
      getGitTree(worktree, dir)
        .then((r) => setTree((t) => ({ ...t, [dir]: r.entries })))
        .catch((err) => setTree((t) => ({ ...t, [dir]: { error: errorMessage(err) } })));
    },
    [worktree]
  );

  /* A new worktree starts from its root. */
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a new worktree starts from its root
    setTree({});
    setOpen(new Set());
    setQuery("");
    setHits(null);
    load("");
  }, [worktree, load]);

  /* Badges come from the status the page already polls, so a change on disk
     updates the tree without asking the daemon for every open folder. */
  const statusOf = useMemo(() => new Map(changes.map((c) => [c.path, c.status])), [changes]);
  const changedDirs = useMemo(() => {
    const dirs = new Set<string>();
    for (const c of changes) {
      let d = dirName(c.path);
      while (d) {
        dirs.add(d);
        d = dirName(d);
      }
    }
    return dirs;
  }, [changes]);

  /* Open the folders above a selected file (a ⌘+click, REQ-WG-063). */
  useEffect(() => {
    if (!selected) return;
    const parts = selected.split("/");
    const next = new Set(open);
    let dir = "";
    let added = false;
    for (let i = 0; i < parts.length - 1; i++) {
      dir = join(dir, parts[i] ?? "");
      if (!next.has(dir)) {
        next.add(dir);
        added = true;
      }
      // eslint-disable-next-line react-hooks/set-state-in-effect -- loads the folders above a file picked from outside the tree
      if (!tree[dir]) load(dir);
    }
    if (added) setOpen(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected]);

  /* ⌘P focuses the filter (REQ-WG-064). */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "p" && !e.shiftKey) {
        e.preventDefault();
        filterRef.current?.focus();
        filterRef.current?.select();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    const q = query.trim();
    if (!q) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- an empty query shows the tree again
      setHits(null);
      return;
    }
    let cancelled = false;
    const timer = window.setTimeout(() => {
      searchGitFiles(worktree, q)
        .then((r) => !cancelled && setHits(r.files))
        .catch(() => !cancelled && setHits([]));
    }, 150);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [query, worktree]);

  const toggle = (dir: string) => {
    const next = new Set(open);
    if (next.has(dir)) next.delete(dir);
    else {
      next.add(dir);
      if (!tree[dir]) load(dir);
    }
    setOpen(next);
  };

  /* A folder whose only child is a folder shows as one row ("web/ui/src"),
     loading the chain as it goes (REQ-WG-064). */
  const chainOf = (dir: string, name: string): { label: string; path: string } => {
    let label = name;
    let path = join(dir, name);
    for (let guard = 0; guard < 12; guard++) {
      const only = soleDir(tree[path]);
      if (!only) break;
      label += "/" + only;
      path = join(path, only);
    }
    return { label, path };
  };

  /* Keep loading the single-child chains below open folders. */
  useEffect(() => {
    for (const dir of open) {
      let p = dir;
      for (let guard = 0; guard < 12; guard++) {
        const only = soleDir(tree[p]);
        if (!only) break;
        const child = join(p, only);
        if (!tree[child]) {
          // eslint-disable-next-line react-hooks/set-state-in-effect -- keeps loading a single-child folder chain
          load(child);
          break;
        }
        p = child;
      }
    }
  }, [tree, open, load]);

  const rows: ReactNode[] = [];
  const walk = (dir: string, depth: number) => {
    const kids = tree[dir];
    if (kids === "loading" || kids === undefined) {
      rows.push(
        <div key={dir + ":loading"} className="wbg-tree-note" style={{ paddingLeft: 12 + depth * 14 }}>
          Loading…
        </div>
      );
      return;
    }
    if (!Array.isArray(kids)) {
      rows.push(
        <div key={dir + ":err"} className="wbg-tree-note" style={{ paddingLeft: 12 + depth * 14 }}>
          {kids.error}
        </div>
      );
      return;
    }
    for (const e of kids) {
      if (e.kind === "dir") {
        const { label, path } = chainOf(dir, e.name);
        const isOpen = open.has(join(dir, e.name));
        rows.push(
          <button
            key={path}
            type="button"
            className={"wbg-tree-row" + (e.ignored ? " is-ignored" : "")}
            style={{ paddingLeft: 8 + depth * 14 }}
            onClick={() => toggle(join(dir, e.name))}
          >
            <Ic name={isOpen ? "chevron-down" : "chevron-right"} size={12} className="wbg-chev" />
            <Ic name={isOpen ? "folder-open" : "folder"} size={14} />
            <span className="wbg-tree-name">{label}</span>
            {changedDirs.has(join(dir, e.name)) ? <span className="wbg-dot" /> : null}
          </button>
        );
        if (isOpen) walk(path, depth + 1);
      } else {
        const path = join(dir, e.name);
        const st = statusOf.get(path);
        rows.push(
          <button
            key={path}
            type="button"
            className={"wbg-tree-row" + (path === selected ? " is-on" : "") + (e.ignored ? " is-ignored" : "") + (st ? " wbg-tone-" + st : "")}
            style={{ paddingLeft: 22 + depth * 14 }}
            onClick={() => onSelect(path)}
          >
            <Ic name="file" size={14} />
            <span className="wbg-tree-name">{e.name}</span>
            <StatusLetter status={st} />
          </button>
        );
      }
    }
  };
  walk("", 0);

  const list = (
    <div className="wbg-list">
      <label className="wbg-filter">
        <Ic name="search" size={14} />
        <input
          ref={filterRef}
          value={query}
          placeholder="Filter files"
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.stopPropagation();
              setQuery("");
            }
            if (e.key === "Enter" && hits?.[0]) onSelect(hits[0]);
          }}
        />
        {!phone ? <kbd>⌘P</kbd> : null}
      </label>
      <div className="wbg-rows wbg-tree">
        {hits
          ? hits.map((p) => (
              <button key={p} type="button" className={"wbg-tree-row wbg-hit" + (p === selected ? " is-on" : "")} onClick={() => onSelect(p)}>
                <Ic name="file" size={14} />
                <span className="wbg-tree-name">{baseName(p)}</span>
                <small>{dirName(p)}</small>
              </button>
            ))
          : rows}
        {hits && !hits.length ? <div className="wbg-tree-note">No match.</div> : null}
      </div>
    </div>
  );

  const change = selected ? changes.find((c) => c.path === selected) : undefined;
  const viewer = external ? (
    <ExternalView data={external} />
  ) : selected ? (
    <FileView worktree={worktree} path={selected} line={line} change={change} />
  ) : (
    <div className="wbg-viewer wbg-viewer-empty">
      <p className="wbg-note">Pick a file to view it.</p>
    </div>
  );

  if (phone) {
    return selected || external ? (
      <div className="wbg-phone-diff">
        <div className="wbg-phone-diff-head">
          <button type="button" className="wbg-back" onClick={() => onSelect(null)}>
            <Ic name="chevron-left" size={20} /> Files
          </button>
        </div>
        {viewer}
      </div>
    ) : (
      list
    );
  }
  return (
    <div className="wbg-split">
      {list}
      {viewer}
    </div>
  );
}

/* REQ-WG-065: one file, read-only, with line numbers and the git gutter. A
   changed file can switch to its diff. */
function FileView({ worktree, path, line, change }: { worktree: string; path: string; line?: number; change?: GitFileChange }) {
  const [file, setFile] = useState<GitFileContent | null>(null);
  const [error, setError] = useState("");
  const [marks, setMarks] = useState<Map<number, GutterMark>>(new Map());
  const [mode, setMode] = useState<"file" | "diff">("file");
  const bodyRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a new path starts from an empty viewer
    setFile(null);
    setError("");
    setMode("file");
    getGitFile(worktree, path)
      .then((f) => !cancelled && setFile(f))
      .catch((err) => !cancelled && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [worktree, path]);

  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the gutter of the previous file must not linger
    setMarks(new Map());
    if (!change || change.status === "U" || change.status === "A" || change.binary) return;
    getGitDiff(worktree, path, "uncommitted")
      .then((d) => !cancelled && setMarks(gutterMarks(d.hunks)))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [worktree, path, change?.add, change?.del, change?.status, change?.binary, change]);

  const text = file && isTextFile(file) ? file : null;
  const lines = useMemo(() => {
    const all = text ? text.text.split("\n") : [];
    if (all.length && all[all.length - 1] === "") all.pop();
    return all;
  }, [text]);
  /* Only the lines on screen are rendered, so a big file stays light. */
  const heights = useMemo(() => lines.map(() => LINE_H), [lines]);
  const blocks = useMemo(() => blockCommentStates(lines, text?.language ?? ""), [lines, text?.language]);
  const longest = useMemo(() => lines.reduce((m, l) => Math.max(m, l.length), 0), [lines]);
  const win = useVirtualRows(bodyRef, heights);

  /* A ⌘+click with :line lands on that line, centred (REQ-WG-063). */
  const { offsetOf } = win;
  useEffect(() => {
    const el = bodyRef.current;
    if (!el || !line || !lines.length) return;
    el.scrollTop = Math.max(0, offsetOf(line - 1) - el.clientHeight / 2);
  }, [lines, line, offsetOf]);

  if (mode === "diff" && change) {
    return (
      <div className="wbg-viewer-stack">
        <DiffView worktree={worktree} path={path} scope="uncommitted" change={change} onOpenFile={() => setMode("file")} />
      </div>
    );
  }

  const isNew = change && (change.status === "U" || change.status === "A");
  return (
    <div className="wbg-viewer">
      <header className="wbg-viewer-head">
        <Ic name="file" size={14} />
        <b className="wbg-viewer-name" title={path}>
          {baseName(path)}
        </b>
        <span className="wbg-viewer-dir">{dirName(path)}</span>
        <StatusLetter status={change?.status} />
        <span className="wb-spacer" />
        {change && change.status !== "D" ? (
          <span className="wbg-seg wbg-seg-sm" role="group" aria-label="View">
            <button type="button" className="is-on">
              File
            </button>
            <button type="button" onClick={() => setMode("diff")}>
              Diff
            </button>
          </span>
        ) : null}
        <button
          type="button"
          className="wbg-icon-btn"
          aria-label="Copy path"
          data-tip="Copy path"
          onClick={() => void navigator.clipboard?.writeText(worktree.replace(/\/+$/, "") + "/" + path)}
        >
          <Ic name="copy" size={14} />
        </button>
      </header>
      <div className={"wbg-viewer-body" + (text ? " wbg-code-view" : " wbg-viewer-plain")} ref={bodyRef}>
        {error ? (
          <p className="wbg-note">{error}</p>
        ) : !file ? (
          <p className="wbg-note">Loading…</p>
        ) : !text ? (
          <ExternalBody data={file as WorkbenchPreview} />
        ) : (
          <table className="wbg-diff-table" style={{ minWidth: 46 + 3 + 10 + Math.ceil(longest * CHAR_W) + 24 }}>
            <colgroup>
              <col className="wbg-col-ln" />
              <col className="wbg-col-gutter" />
              <col />
            </colgroup>
            <tbody>
              {win.top ? <tr className="wbg-spacer" style={{ height: win.top }} /> : null}
              {lines.slice(win.start, win.end).map((t, k) => {
                const n = win.start + k + 1;
                const mark = isNew ? "add" : marks.get(n);
                return (
                  <tr key={n} className={"wbg-dl" + (n === line ? " is-target" : "")}>
                    <td className="wbg-ln">{n}</td>
                    <td className={"wbg-gutter" + (mark ? " wbg-g-" + mark : "")} />
                    <td className="wbg-code">
                      <Code text={t} language={text.language} inBlock={blocks[n - 1]} />
                    </td>
                  </tr>
                );
              })}
              {win.bottom ? <tr className="wbg-spacer" style={{ height: win.bottom }} /> : null}
            </tbody>
          </table>
        )}
      </div>
      {text ? (
        <footer className="wbg-viewer-foot">
          <span>
            {text.language} · {text.lines.toLocaleString()} lines · {humanSize(text.size)} · read-only
          </span>
          <span className="wb-spacer" />
          {change ? <Counts add={change.add} del={change.del} /> : null}
          {change ? <span className="wbg-foot-dim">since HEAD</span> : null}
        </footer>
      ) : null}
    </div>
  );
}

/* Kinds other than text reuse the spec 61 preview page (REQ-WG-009). */
function ExternalBody({ data }: { data: WorkbenchPreview }) {
  if (data.kind === "binary") {
    return (
      <div className="wbg-empty">
        <Ic name="file" size={22} />
        <b>
          {data.name} · {humanSize(data.size)} · binary
        </b>
        <span>Not shown as text.</span>
        <a className="btn btn-ghost btn-sm" href={data.raw_url} download={data.name}>
          <Ic name="download" size={14} /> Download
        </a>
      </div>
    );
  }
  /* Never allow-same-origin (spec 61 REQ-WP-013). */
  return <iframe className="wbg-frame" sandbox="allow-scripts allow-forms allow-popups" src={data.url} title={data.name} />;
}

function ExternalView({ data }: { data: WorkbenchPreview }) {
  return (
    <div className="wbg-viewer">
      <header className="wbg-viewer-head">
        <Ic name="file" size={14} />
        <b className="wbg-viewer-name" title={data.path}>
          {data.name}
        </b>
        <span className="wbg-viewer-dir">{data.dir}</span>
        <span className="wb-spacer" />
        <a className="wbg-icon-btn" href={data.raw_url} target="_blank" rel="noopener" aria-label="Open in new tab" data-tip="Open in new tab">
          <Ic name="external-link" size={14} />
        </a>
      </header>
      <ExternalBody data={data} />
    </div>
  );
}
