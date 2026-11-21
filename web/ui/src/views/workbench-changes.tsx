import { Fragment, memo, useEffect, useMemo, useRef, useState } from "react";

import { errorMessage } from "../api/client";
import { getGitDiff, getGitStatus, type GitDiff, type GitFileChange, type GitScope, type GitStatus } from "../api/workbench-git";
import { Ic } from "../ui/icons";
import { readPref, savePref } from "./workbench-git-hooks";
import { baseName, dirName, highlightLine, splitRows, unifiedRows, type DiffRow, type SplitRow, type Token } from "./workbench-git-model";
import { useVirtualRows } from "./workbench-virtual";

/* Fixed row heights the virtualiser relies on (see workbench-git.css). */
export const LINE_H = 19;
/* Geist Mono at 12 px. */
export const CHAR_W = 7.25;
const HUNK_H = 24;
const HUNK_GAP = 6;

/* Spec 66 REQ-WG-066..068: the Changes tab and the diff viewer. */

const STATUS_CLASS: Record<string, string> = { M: "m", A: "a", U: "a", D: "d", R: "r", C: "d" };

export function StatusLetter({ status }: { status?: string }) {
  if (!status) return null;
  return <span className={"wbg-st wbg-st-" + (STATUS_CLASS[status] || "m")}>{status}</span>;
}

export function Counts({ add, del }: { add: number; del: number }) {
  return (
    <span className="wbg-counts">
      {add ? <span className="wbg-add">+{add}</span> : null}
      {del ? <span className="wbg-del">−{del}</span> : null}
    </span>
  );
}

export const Code = memo(function Code({ text, language, inBlock }: { text: string; language: string; inBlock?: boolean }) {
  const tokens: Token[] = useMemo(() => highlightLine(text, language, inBlock), [text, language, inBlock]);
  return (
    <>
      {tokens.map((t, i) => (t.kind === "plain" ? <Fragment key={i}>{t.text}</Fragment> : <span key={i} className={"wbg-tk-" + t.kind}>{t.text}</span>))}
    </>
  );
});

function languageOf(path: string): string {
  const ext = path.slice(path.lastIndexOf(".") + 1).toLowerCase();
  if (ext === "go") return "go";
  if (ext === "py") return "python";
  if (ext === "sh" || ext === "zsh" || ext === "bash") return "shell";
  if (ext === "yml" || ext === "yaml") return "yaml";
  if (ext === "toml") return "toml";
  return "typescript";
}

type DiffMode = "unified" | "split";
const MODE_KEY = "lana.workbench.diffMode";

/* REQ-WG-068: one file's diff for a scope. */
export function DiffView({
  worktree,
  path,
  scope,
  change,
  footer,
  onOpenFile,
}: {
  worktree: string;
  path: string;
  scope: GitScope;
  change?: GitFileChange;
  footer?: string;
  onOpenFile?: (path: string) => void;
}) {
  const [diff, setDiff] = useState<GitDiff | null>(null);
  const [error, setError] = useState("");
  const [mode, setMode] = useState<DiffMode>(() => readPref(MODE_KEY, ["unified", "split"] as const, "unified"));
  const bodyRef = useRef<HTMLDivElement>(null);
  const [hunk, setHunk] = useState(0);

  useEffect(() => {
    let cancelled = false;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a new file or scope starts from an empty viewer
    setDiff(null);
    setError("");
    setHunk(0);
    getGitDiff(worktree, path, scope)
      .then((d) => !cancelled && setDiff(d))
      .catch((err) => !cancelled && setError(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [worktree, path, scope, change?.add, change?.del]);

  const language = languageOf(path);
  const hunks = useMemo(() => diff?.hunks ?? [], [diff]);
  /* REQ-WG-091: a phone shows the unified diff only. */
  const narrow = typeof window.matchMedia === "function" && window.matchMedia("(max-width: 640px)").matches;
  const layout: DiffMode = narrow ? "unified" : mode;

  /* Only the rows on screen are rendered, so a long diff stays light. */
  const rows = useMemo<(DiffRow | SplitRow)[]>(() => (layout === "unified" ? unifiedRows(hunks) : splitRows(hunks)), [hunks, layout]);
  const heights = useMemo(() => rows.map((r, i) => (r.type === "hunk" ? (i === 0 ? HUNK_H : HUNK_H + HUNK_GAP) : LINE_H)), [rows]);
  const hunkRows = useMemo(() => rows.flatMap((r, i) => (r.type === "hunk" ? [i] : [])), [rows]);
  const win = useVirtualRows(bodyRef, heights);
  /* The widest line sets the table width once, so it does not jump while
     scrolling and the row colours reach the end of every line. */
  const longest = useMemo(() => hunks.reduce((m, h) => h.lines.reduce((n, l) => Math.max(n, l.text.length), m), 0), [hunks]);
  const tableWidth = layout === "unified" ? 46 * 2 + 16 + Math.ceil(longest * CHAR_W) + 24 : undefined;

  const goHunk = (next: number) => {
    if (!hunks.length) return;
    const i = Math.max(0, Math.min(hunks.length - 1, next));
    setHunk(i);
    bodyRef.current?.scrollTo({ top: win.offsetOf(hunkRows[i] ?? 0), behavior: "smooth" });
  };

  /* ] and [ step through the hunks while the viewer has focus. */
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "]") goHunk(hunk + 1);
    else if (e.key === "[") goHunk(hunk - 1);
  };

  const setModeSaved = (m: DiffMode) => {
    setMode(m);
    savePref(MODE_KEY, m);
  };

  return (
    <div className="wbg-viewer" tabIndex={-1} onKeyDown={onKey}>
      <header className="wbg-viewer-head">
        <Ic name="file" size={14} />
        <b className="wbg-viewer-name" title={path}>
          {baseName(path)}
        </b>
        <span className="wbg-viewer-dir">{dirName(path)}</span>
        <StatusLetter status={change?.status ?? diff?.status} />
        {change ? <Counts add={change.add} del={change.del} /> : null}
        <span className="wb-spacer" />
        <span className="wbg-seg wbg-seg-sm wbg-layout-seg" role="group" aria-label="Diff layout">
          <button type="button" className={mode === "unified" ? "is-on" : ""} onClick={() => setModeSaved("unified")}>
            Unified
          </button>
          <button type="button" className={mode === "split" ? "is-on" : ""} onClick={() => setModeSaved("split")}>
            Split
          </button>
        </span>
        {onOpenFile && change?.status !== "D" ? (
          <button type="button" className="wbg-icon-btn" aria-label="Open file" data-tip="Open file" onClick={() => onOpenFile(path)}>
            <Ic name="file-text" size={15} />
          </button>
        ) : null}
      </header>
      <div className="wbg-viewer-body wbg-diff" ref={bodyRef}>
        {error ? (
          <p className="wbg-note">{error}</p>
        ) : !diff ? (
          <p className="wbg-note">Loading diff…</p>
        ) : diff.binary ? (
          <p className="wbg-note">Binary file. No text diff.</p>
        ) : !hunks.length ? (
          <p className="wbg-note">{change?.status === "U" || change?.status === "A" ? "Empty file." : "No changes in this file for this scope."}</p>
        ) : (
          <table className={"wbg-diff-table" + (layout === "split" ? " wbg-diff-split" : "")} style={tableWidth ? { minWidth: tableWidth } : undefined}>
            {layout === "unified" ? (
              <colgroup>
                <col className="wbg-col-ln" />
                <col className="wbg-col-ln" />
                <col className="wbg-col-sign" />
                <col />
              </colgroup>
            ) : (
              <colgroup>
                <col className="wbg-col-ln" />
                <col />
                <col className="wbg-col-ln" />
                <col />
              </colgroup>
            )}
            <tbody>
              {win.top ? <tr className="wbg-spacer" style={{ height: win.top }} /> : null}
              {rows.slice(win.start, win.end).map((row, k) => {
                const i = win.start + k;
                if (row.type === "hunk")
                  return (
                    <tr key={i} className={"wbg-hunk" + (i > 0 ? " wbg-hunk-gap" : "")}>
                      <td colSpan={4}>
                        <span className="wbg-hunk-text">{row.header}</span>
                      </td>
                    </tr>
                  );
                if (row.type === "line")
                  return (
                    <tr key={i} className={"wbg-dl wbg-dl-" + row.line.kind}>
                      <td className="wbg-ln">{row.line.old ?? ""}</td>
                      <td className="wbg-ln">{row.line.new ?? ""}</td>
                      <td className="wbg-sign">{row.line.kind === "add" ? "+" : row.line.kind === "del" ? "−" : ""}</td>
                      <td className="wbg-code">
                        <Code text={row.line.text} language={language} />
                      </td>
                    </tr>
                  );
                return (
                  <tr key={i} className="wbg-dl">
                    <td className="wbg-ln">{row.left?.old ?? ""}</td>
                    <td className={"wbg-code wbg-half" + (row.left && row.left.kind === "del" ? " wbg-dl-del" : "") + (!row.left ? " wbg-blank" : "")}>
                      {row.left ? <Code text={row.left.text} language={language} /> : null}
                    </td>
                    <td className="wbg-ln">{row.right?.new ?? ""}</td>
                    <td className={"wbg-code wbg-half" + (row.right && row.right.kind === "add" ? " wbg-dl-add" : "") + (!row.right ? " wbg-blank" : "")}>
                      {row.right ? <Code text={row.right.text} language={language} /> : null}
                    </td>
                  </tr>
                );
              })}
              {win.bottom ? <tr className="wbg-spacer" style={{ height: win.bottom }} /> : null}
            </tbody>
          </table>
        )}
        {diff?.truncated ? <p className="wbg-note">The diff is cut at 5,000 lines.</p> : null}
      </div>
      <footer className="wbg-viewer-foot">
        <span>{footer || (hunks.length ? hunks.length + (hunks.length === 1 ? " hunk" : " hunks") : "")}</span>
        <span className="wb-spacer" />
        <button type="button" className="wbg-icon-btn" aria-label="Previous hunk" data-tip="Previous hunk [" disabled={hunk <= 0} onClick={() => goHunk(hunk - 1)}>
          <Ic name="chevron-up" size={14} />
        </button>
        <button
          type="button"
          className="wbg-icon-btn"
          aria-label="Next hunk"
          data-tip="Next hunk ]"
          disabled={hunk >= hunks.length - 1}
          onClick={() => goHunk(hunk + 1)}
        >
          <Ic name="chevron-down" size={14} />
        </button>
      </footer>
    </div>
  );
}

type ScopeKind = "uncommitted" | "branch" | "last_commit";
const SCOPE_KEY = "lana.workbench.changesScope";

function scopeLabel(kind: ScopeKind, base?: string): string {
  if (kind === "branch") return "vs " + (base ? base.replace(/^origin\//, "") : "base");
  if (kind === "last_commit") return "Last commit";
  return "Uncommitted";
}

/* REQ-WG-066, 067: the Changes tab. */
export function ChangesTab({
  worktree,
  status,
  scopeKind,
  onScopeKind,
  commit,
  onCommit,
  selected,
  onSelect,
  onOpenFile,
  phone,
}: {
  worktree: string;
  status: GitStatus | null;
  scopeKind: ScopeKind;
  onScopeKind: (k: ScopeKind) => void;
  commit: string | null;
  onCommit: (sha: string | null) => void;
  selected: string | null;
  onSelect: (path: string | null) => void;
  onOpenFile: (path: string) => void;
  phone?: boolean;
}) {
  const [menu, setMenu] = useState(false);
  const [counts, setCounts] = useState<Partial<Record<ScopeKind, number>>>({});
  const menuRef = useRef<HTMLDivElement>(null);

  /* The scope menu shows each scope's file count; ask when it opens. */
  useEffect(() => {
    if (!menu) return;
    let cancelled = false;
    (["uncommitted", "branch", "last_commit"] as const).forEach((k) => {
      getGitStatus(worktree, k)
        .then((s) => !cancelled && setCounts((c) => ({ ...c, [k]: s.totals.files })))
        .catch(() => {});
    });
    const close = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenu(false);
    };
    window.addEventListener("mousedown", close);
    return () => {
      cancelled = true;
      window.removeEventListener("mousedown", close);
    };
  }, [menu, worktree]);

  const files = useMemo(() => status?.files ?? [], [status]);
  const scope: GitScope = commit ? `commit:${commit}` : scopeKind;
  const current = files.find((f) => f.path === selected) ?? null;

  /* Keep a selection: the first file when none, or when the old one left. */
  useEffect(() => {
    if (phone) return;
    if (!files.length) {
      if (selected) onSelect(null);
      return;
    }
    if (!selected || !files.some((f) => f.path === selected)) onSelect(files[0]?.path ?? null);
  }, [files, selected, onSelect, phone]);

  const list = (
    <div className="wbg-list">
      <div className="wbg-scope-wrap" ref={menuRef}>
        <button type="button" className="wbg-scope" onClick={() => setMenu((m) => !m)} aria-expanded={menu}>
          <Ic name="git-compare" size={14} />
          <b>{scopeLabel(scopeKind, status?.base)}</b>
          <span>{status ? status.totals.files + (status.totals.files === 1 ? " file" : " files") : ""}</span>
          <Ic name="chevrons-up-down" size={14} className="wbg-scope-chev" />
        </button>
        {menu ? (
          <div className="wbg-menu wbg-scope-menu" role="menu">
            {(
              [
                ["uncommitted", "Uncommitted changes", "Working tree + staged vs HEAD"],
                ["branch", "Branch vs " + (status?.base?.replace(/^origin\//, "") || "base"), "Everything this branch adds"],
                ["last_commit", "Last commit", "HEAD~1..HEAD"],
              ] as const
            ).map(([k, label, sub]) => (
              <button
                key={k}
                type="button"
                role="menuitem"
                className={"wbg-menu-item" + (k === scopeKind ? " is-on" : "")}
                onClick={() => {
                  onScopeKind(k);
                  savePref(SCOPE_KEY, k);
                  onCommit(null);
                  setMenu(false);
                }}
              >
                <span className="wbg-menu-check">{k === scopeKind ? <Ic name="check" size={14} /> : null}</span>
                <span className="wbg-menu-text">
                  <b>{label}</b>
                  <small>{sub}</small>
                </span>
                <span className="wbg-menu-count">{counts[k] ?? ""}</span>
              </button>
            ))}
          </div>
        ) : null}
      </div>
      {scopeKind === "branch" && status?.commits?.length ? (
        <>
          <div className="wbg-section">Commits · {status.commits.length}</div>
          {status.commits.map((c) => (
            <button
              key={c.sha}
              type="button"
              className={"wbg-commit" + (commit === c.sha ? " is-on" : "")}
              onClick={() => onCommit(commit === c.sha ? null : c.sha)}
            >
              <Ic name="git-commit-horizontal" size={14} />
              <code>{c.sha}</code>
              <span className="wbg-commit-msg">{c.subject}</span>
            </button>
          ))}
          <div className="wbg-section">Files · {files.length}</div>
        </>
      ) : (
        <div className="wbg-summary">
          <span>{status ? (files.length === 0 ? "No changes" : files.length === 1 ? "1 changed file" : files.length + " changed files") : "Loading…"}</span>
          {status ? <Counts add={status.totals.add} del={status.totals.del} /> : null}
        </div>
      )}
      <div className="wbg-rows">
        {files.map((f) => (
          <button
            key={f.path}
            type="button"
            className={"wbg-change" + (f.path === selected ? " is-on" : "")}
            onClick={() => onSelect(f.path)}
            title={f.old_path ? f.old_path + " → " + f.path : f.path}
          >
            <StatusLetter status={f.status} />
            <span className="wbg-change-text">
              <span className={"wbg-change-name" + (f.status === "D" ? " is-deleted" : "")}>{baseName(f.path)}</span>
              <small>{dirName(f.path)}</small>
            </span>
            {f.binary ? <span className="wbg-bin">bin</span> : <Counts add={f.add} del={f.del} />}
            {phone ? <Ic name="chevron-right" size={14} className="wbg-row-chev" /> : null}
          </button>
        ))}

      </div>
    </div>
  );

  if (phone) {
    return current ? (
      <div className="wbg-phone-diff">
        <div className="wbg-phone-diff-head">
          <button type="button" className="wbg-back" onClick={() => onSelect(null)}>
            <Ic name="chevron-left" size={20} /> Changes
          </button>
          <span className="wb-spacer" />
          <span className="wbg-pos">
            {files.indexOf(current) + 1}/{files.length}
          </span>
          <button
            type="button"
            className="wbg-icon-btn"
            aria-label="Previous file"
            disabled={files.indexOf(current) <= 0}
            onClick={() => onSelect(files[files.indexOf(current) - 1]?.path ?? current.path)}
          >
            <Ic name="chevron-up" size={18} />
          </button>
          <button
            type="button"
            className="wbg-icon-btn"
            aria-label="Next file"
            disabled={files.indexOf(current) >= files.length - 1}
            onClick={() => onSelect(files[files.indexOf(current) + 1]?.path ?? current.path)}
          >
            <Ic name="chevron-down" size={18} />
          </button>
        </div>
        <DiffView worktree={worktree} path={current.path} scope={scope} change={current} onOpenFile={onOpenFile} />
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
          worktree={worktree}
          path={current.path}
          scope={scope}
          change={current}
          onOpenFile={onOpenFile}
          footer={status?.merge_base && scopeKind === "branch" ? "vs merge-base " + status.merge_base : undefined}
        />
      ) : status && !files.length ? (
        <div className="wbg-viewer wbg-viewer-empty">
          <div className="wbg-empty wbg-empty-full">
            <Ic name="circle-check" size={20} />
            <b>Nothing to review</b>
            <span>
              {scopeKind === "uncommitted"
                ? "The working tree matches HEAD. Switch the scope to compare with the base branch or the last commit."
                : "No changes in this scope."}
            </span>
          </div>
        </div>
      ) : (
        <div className="wbg-viewer wbg-viewer-empty" />
      )}
    </div>
  );
}

export function readScopeKind(): ScopeKind {
  return readPref(SCOPE_KEY, ["uncommitted", "branch", "last_commit"] as const, "uncommitted");
}

export type { ScopeKind };
