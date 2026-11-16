import { useCallback, useEffect, useMemo, useState } from "react";

import { errorMessage } from "../api/client";
import { runCommand } from "../api/toolbox";
import { Popover } from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { AgentLogo } from "./agent-logo";

/* Spec 56: the "Claude accounts" plugin. It is the herdr mod
   ~/.config/herdr-app/mods/claude-accounts ported to the Workbench: every
   account claude-swap (`cswap`) manages, with its usage, and four actions.
   - Default: `cswap switch N` makes N the login plain `claude` uses.
   - New tab: `cswap run N` in a new tab, in the focused terminal's folder.
   - Beside: `cswap run N -- --continue` beside the focused pane, so the
     folder's latest conversation carries on under N.
   - Add / Re-login: `claude auth login`, then `cswap add`, in a new tab. */

interface UsageWindow {
  pct?: number | null;
  resetsAt?: string;
  name?: string;
}

interface Usage {
  fiveHour?: UsageWindow;
  sevenDay?: UsageWindow;
  scoped?: UsageWindow[];
}

export interface ClaudeAccount {
  number: number;
  email?: string;
  organizationName?: string;
  active?: boolean;
  disabled?: boolean;
  usageStatus?: string;
  usage?: Usage | null;
  lastGoodUsage?: Usage | null;
  lastGoodAgeSeconds?: number;
  loginExpiresAt?: string;
  alias?: string;
}

interface AccountList {
  activeAccountNumber?: number;
  accounts?: ClaudeAccount[];
}

export interface AccountActions {
  /* Opens `argv` in a new tab of the current space, in the focused folder. */
  newTab: (argv: string[], title: string) => void;
  /* Opens `argv` beside the focused pane (a new tab when there is none). */
  beside: (argv: string[]) => void;
}

const REFRESH_MS = 120_000;

function until(iso?: string): string {
  if (!iso) return "";
  const ms = Date.parse(iso) - Date.now();
  if (!(ms > 0)) return "reset";
  const m = Math.max(1, Math.round(ms / 60000));
  const d = Math.floor(m / 1440);
  const h = Math.floor((m % 1440) / 60);
  const mm = m % 60;
  return d ? d + "d " + h + "h" : h ? h + "h " + mm + "m" : mm + "m";
}

function ago(seconds?: number): string {
  if (seconds === undefined) return "";
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.round(s / 60) + "m ago";
  if (s < 86400) return Math.round(s / 3600) + "h ago";
  return Math.round(s / 86400) + "d ago";
}

/* A window past its reset is empty again, whatever was last read. */
function used(w?: UsageWindow): number | null {
  if (!w) return null;
  if (w.resetsAt && Date.parse(w.resetsAt) <= Date.now()) return 0;
  return typeof w.pct === "number" ? w.pct : null;
}

function windows(a: ClaudeAccount): { label: string; w: UsageWindow }[] {
  const u = a.usage ?? a.lastGoodUsage;
  if (!u) return [];
  const rows: { label: string; w: UsageWindow }[] = [];
  if (u.fiveHour) rows.push({ label: "5h", w: u.fiveHour });
  if (u.sevenDay) rows.push({ label: "7d", w: u.sevenDay });
  for (const s of u.scoped ?? []) rows.push({ label: s.name ?? "model", w: s });
  return rows;
}

/* Most headroom: the usable account whose tightest window is the least used. */
function bestOf(accounts: ClaudeAccount[]): number | null {
  let best: number | null = null;
  let score = Infinity;
  for (const a of accounts) {
    if (a.disabled || !(a.usage ?? a.lastGoodUsage)) continue;
    const tight = Math.max(
      0,
      ...windows(a)
        .filter((r) => r.label === "5h" || r.label === "7d")
        .map((r) => used(r.w) ?? 0)
    );
    if (tight < score) {
      score = tight;
      best = a.number;
    }
  }
  return best;
}

/* The tool runs `cswap list --json`; the output may carry a note before the
   JSON, so read from the first brace. */
function parseList(output: string): AccountList {
  const start = output.indexOf("{");
  if (start < 0) throw new Error(output.trim().split("\n").pop() || "cswap returned no list");
  return JSON.parse(output.slice(start)) as AccountList;
}

/* "x's Organization" is a personal account; show that instead. */
function orgLabel(a: ClaudeAccount): string {
  const org = a.organizationName || "";
  if (!org) return "";
  if (a.email && org === a.email + "'s Organization") return "Personal";
  return org;
}

function Bar({ label, w }: { label: string; w: UsageWindow }) {
  const pct = used(w);
  const tone = pct === null ? "" : pct >= 90 ? " is-red" : pct >= 70 ? " is-amber" : "";
  return (
    <div className="wb-acc-bar">
      <span className="wb-acc-bar-label">{label}</span>
      <span className="wb-acc-track">
        <span className={"wb-acc-fill" + tone} style={{ width: Math.min(100, Math.max(0, pct ?? 0)) + "%" }} />
      </span>
      <span className="wb-acc-pct">{pct === null ? "–" : Math.round(pct) + "%"}</span>
      <span className="wb-acc-reset">{until(w.resetsAt)}</span>
    </div>
  );
}

export function ClaudeAccountsPanel({
  binary,
  actions,
  onClose,
}: {
  binary: string;
  actions: AccountActions;
  onClose: () => void;
}) {
  const [list, setList] = useState<AccountList | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState<number | null>(null);
  /* Remove asks twice: the first click arms it for a few seconds. */
  const [arming, setArming] = useState<number | null>(null);
  useEffect(() => {
    if (arming === null) return;
    const timer = setTimeout(() => setArming(null), 4000);
    return () => clearTimeout(timer);
  }, [arming]);

  const refresh = useCallback(() => {
    setLoading(true);
    runCommand("claude-swap", "list", {})
      .then((r) => {
        if (r.exit_code !== 0) throw new Error(r.output.trim().split("\n").pop() || "cswap exited " + r.exit_code);
        setList(parseList(r.output));
        setError("");
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch is async; state lands after the await.
    refresh();
    const timer = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  const accounts = useMemo(() => list?.accounts ?? [], [list]);
  const best = useMemo(() => bestOf(accounts), [accounts]);

  const makeDefault = (n: number) => {
    setBusy(n);
    runCommand("claude-swap", "switch", { account: String(n) })
      .then((r) => {
        if (r.exit_code !== 0) throw new Error(r.output.trim().split("\n").pop() || "cswap exited " + r.exit_code);
        refresh();
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setBusy(null));
  };

  const addAccount = (relogin?: ClaudeAccount) => {
    const what = relogin ? "reconnect" : "add";
    const script =
      "printf '\\n  Sign in with the Claude account to " + what + " in the browser.\\n  It becomes the default login; cswap keeps the others.\\n\\n'" +
      " && claude auth login && printf '\\n  Saving it in cswap...\\n' && '" + binary.replace(/'/g, "'\\''") + "' add";
    actions.newTab(["sh", "-c", script], relogin ? "Re-login Claude" : "Add Claude account");
    onClose();
  };

  /* `cswap remove` confirms on stdin and has no --yes flag, so it runs in a
     terminal tab with the answer piped in; the tab shows what happened. */
  const removeAccount = (a: ClaudeAccount) => {
    const bin = "'" + binary.replace(/'/g, "'\\''") + "'";
    const script =
      "printf 'y\\n' | " + bin + " remove " + a.number +
      "; printf '\\n  Done. Close this tab when you are ready.\\n'";
    actions.newTab(["sh", "-c", script], "Remove Claude account " + a.number);
    setArming(null);
    setTimeout(refresh, 4000);
  };

  return (
    <div className="wb-acc">
      <div className="wb-acc-head">
        <AgentLogo agent="claude" size={20} />
        <b>Claude accounts</b>
        <span className="wb-acc-sub">{list ? accounts.length + (accounts.length === 1 ? " account" : " accounts") : ""}</span>
        <span className="wb-spacer" />
        <button type="button" className="wb-icon-btn" aria-label="Refresh" data-tip="Refresh" onClick={refresh} disabled={loading}>
          <Ic name={loading ? "loader-circle" : "refresh-cw"} size={15} className={loading ? "spin" : ""} />
        </button>
        <button type="button" className="wb-icon-btn" aria-label="Add account" data-tip="Add account" onClick={() => addAccount()}>
          <Ic name="user-plus" size={15} />
        </button>
      </div>
      {error ? <p className="wb-acc-error">{error}</p> : null}
      {!list && !error ? <p className="wb-acc-note">Reading accounts…</p> : null}
      <div className="wb-acc-list">
        {accounts.map((a) => {
          const isActive = a.active || a.number === list?.activeAccountNumber;
          const rows = windows(a);
          const stale = a.usageStatus && a.usageStatus !== "ok";
          return (
            <div key={a.number} className={"wb-acc-item" + (isActive ? " is-active" : "")}>
              <div className="wb-acc-top">
                <span className="wb-acc-num">{a.number}</span>
                <span className="wb-acc-name" title={a.email || undefined}>
                  <b>{a.alias || a.email || "Account " + a.number}</b>
                  <span>{orgLabel(a)}</span>
                </span>
                {isActive ? <span className="wb-acc-tag">default</span> : null}
                {best === a.number && !isActive ? <span className="wb-acc-tag is-best">most room</span> : null}
                <button
                  type="button"
                  className={"wb-acc-x" + (arming === a.number ? " is-armed" : "")}
                  aria-label={arming === a.number ? "Click again to remove account " + a.number : "Remove account " + a.number}
                  data-tip={arming === a.number ? "Click again to remove" : "Remove account"}
                  onClick={() => (arming === a.number ? removeAccount(a) : setArming(a.number))}
                >
                  <Ic name={arming === a.number ? "trash-2" : "x"} size={14} />
                </button>
              </div>
              {rows.length ? (
                <div className="wb-acc-bars">
                  {rows.map((r) => (
                    <Bar key={r.label} label={r.label} w={r.w} />
                  ))}
                </div>
              ) : (
                <p className="wb-acc-note">No usage read yet.</p>
              )}
              {stale ? (
                <p className="wb-acc-note">
                  {a.usageStatus === "auth_error" || a.usageStatus === "expired" ? "Login expired." : "Usage not current"}
                  {a.lastGoodAgeSeconds !== undefined ? " · last read " + ago(a.lastGoodAgeSeconds) : ""}
                </p>
              ) : null}
              <div className="wb-acc-actions">
                {!isActive ? (
                  <button type="button" className="wb-acc-act is-main" disabled={busy === a.number} onClick={() => makeDefault(a.number)}>
                    <Ic name="check" size={13} /> Make default
                  </button>
                ) : null}
                <button
                  type="button"
                  className="wb-acc-act"
                  onClick={() => {
                    actions.newTab([binary, "run", String(a.number)], "claude · " + a.number);
                    onClose();
                  }}
                >
                  <Ic name="square-terminal" size={13} /> New tab
                </button>
                <button
                  type="button"
                  className="wb-acc-act"
                  onClick={() => {
                    actions.beside([binary, "run", String(a.number), "--", "--continue"]);
                    onClose();
                  }}
                >
                  <Ic name="columns-2" size={13} /> Beside
                </button>
                {stale ? (
                  <button type="button" className="wb-acc-act" onClick={() => addAccount(a)}>
                    <Ic name="log-in" size={13} /> Re-login
                  </button>
                ) : null}

              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/* Desktop: a popover under the tab-bar button. */
export function ClaudeAccountsPopover({
  anchor,
  binary,
  actions,
  onClose,
}: {
  anchor: HTMLElement;
  binary: string;
  actions: AccountActions;
  onClose: () => void;
}) {
  const anchorRef = useMemo(() => ({ current: anchor as HTMLElement | null }), [anchor]);
  return (
    <Popover anchorRef={anchorRef} open onClose={onClose} className="wb-acc-pop" width={460}>
      <ClaudeAccountsPanel binary={binary} actions={actions} onClose={onClose} />
    </Popover>
  );
}
