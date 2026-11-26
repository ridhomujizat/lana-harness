import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { ApiError, errorMessage } from "../api/client";
import {
  getAntigravity,
  getClaudeAgent,
  refreshRouterLimits,
  type AntigravityAccount,
  type AntigravityStatus,
  type AntigravityWindow,
  type ClaudeAccount,
  type PlanWindow,
} from "../api/router";
import {
  createWorkbenchSession,
  getAgentHook,
  getWorkbenchClaudeAccount,
  setAgentHook,
  setWorkbenchClaudeAccount,
  switchWorkbenchAccount,
  type AgentHookState,
  type WorkbenchClaudeAccount,
  type WorkbenchSession,
} from "../api/workbench";
import { Popover } from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { AGENT_HOOK_SETTINGS_HREF, AGENT_HOOK_TITLE, agentHookSummary } from "./agent-hook-settings";
import { AgentLogo } from "./agent-logo";
import { planLabel } from "./router/limits";

/* Spec 77: the Workbench "Accounts" popup. It lists the Router's accounts
   grouped by provider, each account in its own folder, so no login is ever
   copied in or out of the Keychain. Adding, removing, renaming and signing in
   live in the Router.
   - Claude Code (GET /v1/router/claude-agent): New tab, Switch here (spec 82:
     the focused pane's conversation continues on the account, in place),
     Sign in (login_command, signed out only), Use for new terminals (the
     Workbench default account, REQ-WRA-005).
   - Antigravity (GET /v1/router/antigravity): New tab runs `agy` with the
     account's HOME, and Switch here moves the focused pane's conversation to
     the account (spec 83); sign-in is a link + code flow, so a signed-out
     account links to the Router's Antigravity page. There is no default:
     HOME for a whole shell would break it.
   A search box filters both groups; each group collapses (remembered). */

/* Kept here for the callers that import it with the popup. */
export { agentHookCount } from "./agent-hook-settings";

export interface AccountActions {
  /* Opens a shell in a new tab of the current space, in the focused folder,
     and types `line` into it. The pane stays the shell, so exiting the agent
     returns to the prompt instead of ending the pane. Without a title the tab
     and its session follow the terminal's window title, so a Claude Code tab
     shows its task or /rename name. claudeAccount names the Claude Code
     account the line launches, so the daemon mirrors the built-in account's
     MCP servers into it first (spec 81). agyAccount names the Antigravity
     account, so the daemon relinks its mcp_config.json first (spec 83). */
  newTab: (line: string, title?: string, claudeAccount?: string, agyAccount?: string) => void;
  /* The focused pane's session, which Switch here acts on; null or absent
     when the space has no pane. */
  focused?: WorkbenchSession | null;
}

/* Spec 82 REQ-WAS-001 and spec 83 REQ-WAG-003: why Switch here is off. */
export const SWITCH_NO_CONVERSATION = "No Claude conversation in this pane";
export const SWITCH_NO_AGY_CONVERSATION = "No Antigravity conversation in this pane";
export const SWITCH_NOT_SWITCHABLE = "This pane was opened before account switching; use New tab";
export const SWITCH_SAME_ACCOUNT = "This pane already runs on this account";

export type SwitchAgent = "claude" | "agy";

/* Why Switch here is off for an account of `agent` (its folder: the Claude
   config dir or the agy HOME, "" for none) on the focused pane, "" when it is
   on. An older core reports nothing about switching: a pane running the
   agent is then let through, and the daemon answers core_outdated, after
   which the popup offers New tab. */
export function paneSwitchBlocked(
  focused: WorkbenchSession | null | undefined,
  agent: SwitchAgent,
  builtin: boolean,
  folder: string,
): string {
  const none = agent === "agy" ? SWITCH_NO_AGY_CONVERSATION : SWITCH_NO_CONVERSATION;
  if (!focused) return none;
  if (focused.account_switch === undefined) {
    return focused.agent === agent || focused.resumed_agent === agent ? "" : none;
  }
  if (focused.account_switch === "no_conversation") return none;
  if ((focused.conversation_agent || "claude") !== agent) return none;
  if (focused.account_switch === "not_switchable") return SWITCH_NOT_SWITCHABLE;
  const dir = focused.agent_config_dir || "";
  const same = builtin ? dir === "" || dir === folder : dir === folder;
  return same ? SWITCH_SAME_ACCOUNT : "";
}

/* Switch here for a Claude Code account (spec 82). */
export function switchBlocked(focused: WorkbenchSession | null | undefined, a: ClaudeAccount): string {
  return paneSwitchBlocked(focused, "claude", a.builtin, a.config_dir);
}

/* Switch here for an Antigravity account (spec 83). */
export function agySwitchBlocked(focused: WorkbenchSession | null | undefined, a: AntigravityAccount): string {
  return paneSwitchBlocked(focused, "agy", a.builtin, a.home);
}

/* One Switch here target: the account and the line New tab would run. */
interface SwitchTarget {
  agent: SwitchAgent;
  name: string;
  label: string;
  line: string;
}

/* Starts the pane an account action opens: the owner's shell in dir (no
   command), with `line` typed into it once it has printed. The pane stays the
   shell, so exiting the agent returns to the prompt instead of showing
   "Process exited" (spec 77 revision 7). A Claude Code account's name goes
   along as claude_account (spec 81 REQ-SMC-005), an Antigravity account's as
   agy_account (spec 83 REQ-WAG-004). */
export function createAccountSession(
  dir: string,
  line: string,
  title?: string,
  claudeAccount?: string,
  agyAccount?: string,
): Promise<WorkbenchSession> {
  return createWorkbenchSession({
    dir: dir || undefined,
    title,
    input: line,
    claude_account: claudeAccount || undefined,
    agy_account: agyAccount || undefined,
  });
}

const REFRESH_MS = 120_000;

export const CLAUDE_MANAGE_HREF = "#/router/providers/claude-code";
export const AGY_MANAGE_HREF = "#/router/providers/antigravity";

export type AccountGroup = "claude" | "agy";

export const COLLAPSE_STORAGE_KEY = "lana.workbench.accounts.collapsed";

function readCollapsed(): Partial<Record<AccountGroup, boolean>> {
  try {
    const raw = window.localStorage.getItem(COLLAPSE_STORAGE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const value = parsed as Record<string, unknown>;
      return { claude: value.claude === true, agy: value.agy === true };
    }
  } catch {
    /* storage blocked or bad JSON: every group starts expanded */
  }
  return {};
}

function writeCollapsed(state: Partial<Record<AccountGroup, boolean>>) {
  try {
    window.localStorage.setItem(COLLAPSE_STORAGE_KEY, JSON.stringify(state));
  } catch {
    /* storage blocked: the state lasts until the popup closes */
  }
}

/* True when any field holds the query, ignoring case. */
export function matchesQuery(fields: (string | undefined | null)[], query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return fields.some((f) => Boolean(f) && String(f).toLowerCase().includes(q));
}

function claudeMatches(a: ClaudeAccount, query: string): boolean {
  return matchesQuery([a.display_name, a.name, a.email, a.plan, a.plan ? planLabel(a.plan) : "", a.label], query);
}

function agyMatches(a: AntigravityAccount, query: string): boolean {
  return matchesQuery([a.display_name, a.name, a.email, a.tier, a.label], query);
}

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

function past(iso?: string | null): boolean {
  return Boolean(iso && Date.parse(iso) <= Date.now());
}

/* Percent used; a window past its reset is empty again, whatever was last
   read. utilization is a fraction (0.42 is 42 %). */
function used(w: PlanWindow): number | null {
  if (past(w.resets_at)) return 0;
  return typeof w.utilization === "number" ? w.utilization * 100 : null;
}

/* Percent used of an Antigravity window: remaining is a fraction LEFT. */
export function agyUsed(w: AntigravityWindow): number | null {
  if (past(w.resets_at)) return 0;
  if (w.remaining === null) return null;
  return Math.max(0, Math.min(100, (1 - w.remaining) * 100));
}

export function windowShortLabel(w: PlanWindow): string {
  if (w.type === "five_hour") return "5h";
  if (w.type === "seven_day") return "7d";
  return w.label || w.type;
}

/* "Gemini 5h", "Claude wk": the model group and a short span. */
export function agyWindowLabel(w: AntigravityWindow): string {
  const span = w.type === "5h" ? "5h" : w.type === "weekly" ? "wk" : w.type;
  return [w.group, span].filter(Boolean).join(" ");
}

/* POSIX single-quotes a value for a shell line: 'it'\''s' for it's. */
export function shellQuote(value: string): string {
  return "'" + value.replace(/'/g, "'\\''") + "'";
}

/* A program word: left bare when it is plain, quoted otherwise. */
function shellWord(value: string): string {
  return /^[A-Za-z0-9_./-]+$/.test(value) ? value : shellQuote(value);
}

/* KEY='value' for `env`. */
function envAssign(kv: string): string {
  const i = kv.indexOf("=");
  return kv.slice(0, i) + "=" + shellQuote(kv.slice(i + 1));
}

/* The line a pane's shell runs for an account (REQ-WRA-004, revision 7): the
   account's own config folder, or neither variable for the built-in account. */
export function accountLine(a: ClaudeAccount): string {
  const binary = shellWord(a.binary || "claude");
  if (a.builtin) return "env -u CLAUDE_CONFIG_DIR -u CLAUDE_SECURESTORAGE_CONFIG_DIR " + binary;
  const dir = shellQuote(a.config_dir);
  return "env CLAUDE_CONFIG_DIR=" + dir + " CLAUDE_SECURESTORAGE_CONFIG_DIR=" + dir + " " + binary;
}

/* Spec 77 revision 4: set for every agy of an added account, like the daemon
   does. On macOS agy then keeps its login in its file token storage under
   HOME instead of the Keychain, so no "Keychain Not Found" dialog pops up for
   an account HOME that has no login keychain. */
export const AGY_FILE_TOKEN_ENV = "SSH_CONNECTION=127.0.0.1 0 127.0.0.1 0";

/* The line a pane's shell runs for an Antigravity account: the account's own
   HOME (where agy keeps its login), or plain agy for the built-in account. */
export function agyLine(a: AntigravityAccount, statusBinary = ""): string {
  const binary = shellWord(a.binary || statusBinary || "agy");
  if (a.builtin) return binary;
  /* Without its own HOME an added account would run on the owner's own
     login, so it gets no command (its buttons are off). */
  if (!a.home) return "";
  return "env HOME=" + shellQuote(a.home) + " " + envAssign(AGY_FILE_TOKEN_ENV) + " " + binary;
}

function cooling(a: { cooling_until?: string | null }, now = Date.now()): boolean {
  return Boolean(a.cooling_until && Date.parse(a.cooling_until) > now);
}

/* The built-in account first, then pool order (spec 75), then name. */
export function sortAccounts<T extends { builtin: boolean; priority?: number; display_name: string }>(accounts: T[]): T[] {
  return [...accounts].sort(
    (x, y) =>
      Number(y.builtin) - Number(x.builtin) ||
      (x.priority ?? 0) - (y.priority ?? 0) ||
      x.display_name.localeCompare(y.display_name),
  );
}

/* Most room: the signed-in account whose tightest 5-hour/weekly window is
   the least used. A cooling account is skipped. */
export function mostRoom(accounts: ClaudeAccount[], now = Date.now()): string | null {
  let best: string | null = null;
  let score = Infinity;
  for (const a of accounts) {
    if (!a.logged_in || cooling(a, now)) continue;
    const levels = a.windows
      .filter((w) => w.type === "five_hour" || w.type === "seven_day")
      .map(used)
      .filter((v): v is number => v !== null);
    if (levels.length === 0) continue;
    const tight = Math.max(...levels);
    if (tight < score) {
      score = tight;
      best = a.name;
    }
  }
  return best;
}

/* Most room among Antigravity accounts: the signed-in, enabled, not cooling
   account with the highest minimum remaining across its windows. */
export function agyMostRoom(accounts: AntigravityAccount[], now = Date.now()): string | null {
  let best: string | null = null;
  let score = -Infinity;
  for (const a of accounts) {
    if (!a.signed_in || a.disabled || cooling(a, now)) continue;
    const left = a.windows
      .map(agyUsed)
      .filter((v): v is number => v !== null)
      .map((v) => 100 - v);
    if (left.length === 0) continue;
    const tight = Math.min(...left);
    if (tight > score) {
      score = tight;
      best = a.name;
    }
  }
  return best;
}

function Bar({ label, title, pct, resetsAt }: { label: string; title?: string; pct: number | null; resetsAt?: string }) {
  const tone = pct === null ? "" : pct >= 90 ? " is-red" : pct >= 70 ? " is-amber" : "";
  return (
    <div className="wb-acc-bar">
      <span className="wb-acc-bar-label" title={title || undefined}>
        {label}
      </span>
      <span className="wb-acc-track">
        <span className={"wb-acc-fill" + tone} style={{ width: Math.min(100, Math.max(0, pct ?? 0)) + "%" }} />
      </span>
      <span className="wb-acc-pct">{pct === null ? "–" : Math.round(pct) + "%"}</span>
      <span className="wb-acc-reset">{until(resetsAt)}</span>
    </div>
  );
}

function countText(n: number): string {
  return n + (n === 1 ? " account" : " accounts");
}

/* One provider group: a sticky heading that collapses it (chevron, logo,
   name, count) with a link to the provider's Router page at its right, then
   the group's rows as a flat list. */
function Group({
  id,
  agent,
  title,
  count,
  expanded,
  manageHref,
  onToggle,
  onClose,
  children,
}: {
  id: AccountGroup;
  agent: string;
  title: string;
  count: number | null;
  expanded: boolean;
  manageHref: string;
  onToggle: () => void;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <section className={"wb-acc-group" + (expanded ? " is-open" : "")} data-group={id} role="group" aria-label={title}>
      <div className="wb-acc-gh">
        <button
          type="button"
          className="wb-acc-gh-toggle"
          aria-expanded={expanded}
          aria-label={title + (count === null ? "" : ", " + countText(count))}
          onClick={onToggle}
        >
          <Ic name="chevron-right" size={14} className="wb-acc-gh-chev" />
          <AgentLogo agent={agent} size={16} />
          <span className="wb-acc-gh-name">{title}</span>
          {count === null ? null : <span className="wb-acc-gh-count">{count}</span>}
        </button>
        <a className="wb-acc-manage" href={manageHref} onClick={onClose} aria-label={"Manage " + title + " in Router"}>
          Manage <Ic name="arrow-up-right" size={13} />
        </a>
      </div>
      {expanded ? <div className="wb-acc-gbody">{children}</div> : null}
    </section>
  );
}

export function AccountsPanel({
  actions,
  onClose,
  autoFocusSearch = false,
}: {
  actions: AccountActions;
  onClose: () => void;
  /* Desktop popover only: the phone keyboard would cover the sheet. */
  autoFocusSearch?: boolean;
}) {
  const [accounts, setAccounts] = useState<ClaudeAccount[] | null>(null);
  const [error, setError] = useState("");
  const [agy, setAgy] = useState<AntigravityStatus | null>(null);
  const [agyError, setAgyError] = useState("");
  const [current, setCurrent] = useState<WorkbenchClaudeAccount | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState(readCollapsed);
  /* Spec 82: the account being switched to, the one whose "Claude is
     working" question is open, and a refused switch's message. */
  /* Keyed "<agent>:<account>". */
  const [switching, setSwitching] = useState<string | null>(null);
  const [confirmSwitch, setConfirmSwitch] = useState<string | null>(null);
  const [switchNote, setSwitchNote] = useState<{ key: string; message: string; outdated: boolean } | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const focused = actions.focused ?? null;

  /* Both providers in parallel; a failure stays inside its own group. */
  const load = useCallback(() => {
    return Promise.all([
      getClaudeAgent()
        .then((status) => {
          setAccounts(sortAccounts(status.accounts));
          setError("");
        })
        .catch((err) => setError(errorMessage(err))),
      getAntigravity()
        .then((status) => {
          setAgy({ ...status, accounts: sortAccounts(status.accounts) });
          setAgyError("");
        })
        .catch((err) => setAgyError(errorMessage(err))),
    ]);
  }, []);

  /* Refresh also probes usage (claude /usage and the Antigravity quota; the
     daemon probes an account at most once a minute), then rereads both. */
  const refresh = () => {
    setLoading(true);
    refreshRouterLimits()
      .catch(() => {})
      .then(load)
      .finally(() => setLoading(false));
  };

  /* Opening the popup shows the accounts at once, as the Router pages do,
     then probes usage once in the background (a daemon restart leaves the
     windows empty until something asks) and rereads; the timer only
     rereads. */
  useEffect(() => {
    let alive = true;
    void load().then(() => {
      if (!alive) return;
      setLoading(true);
      refreshRouterLimits()
        .catch(() => {})
        .then(() => (alive ? load() : undefined))
        .finally(() => alive && setLoading(false));
    });
    const timer = setInterval(load, REFRESH_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [load]);

  useEffect(() => {
    let alive = true;
    getWorkbenchClaudeAccount()
      .then((value) => alive && setCurrent(value))
      .catch(() => alive && setCurrent({ account: "claude-code", config_dir: "", supported: false }));
    return () => {
      alive = false;
    };
  }, []);

  /* After the popover is shown (a passive effect runs after its layout
     effect), so the focus lands. */
  useEffect(() => {
    if (autoFocusSearch) searchRef.current?.focus({ preventScroll: true });
  }, [autoFocusSearch]);

  const list = useMemo(() => accounts ?? [], [accounts]);
  const agyList = useMemo(() => agy?.accounts ?? [], [agy]);
  const best = useMemo(() => mostRoom(list), [list]);
  const agyBest = useMemo(() => agyMostRoom(agyList), [agyList]);
  /* The Workbench default; a name no account has falls back to built-in. */
  const defaultName = useMemo(() => {
    const name = current?.account || "claude-code";
    if (list.some((a) => a.name === name)) return name;
    return list.find((a) => a.builtin)?.name ?? name;
  }, [current, list]);
  const canSetDefault = Boolean(current?.supported);

  const searching = query.trim() !== "";
  const claudeShown = useMemo(() => list.filter((a) => claudeMatches(a, query)), [list, query]);
  const agyShown = useMemo(() => agyList.filter((a) => agyMatches(a, query)), [agyList, query]);
  /* Antigravity hides while it is not installed and has no accounts; until
     it is read, only an error shows it. */
  const agyAvailable = agy ? agy.installed || agy.accounts.length > 0 : Boolean(agyError);
  const showClaude = !searching || claudeShown.length > 0;
  const showAgy = searching ? agyShown.length > 0 : agyAvailable;
  const noMatch = searching && claudeShown.length === 0 && agyShown.length === 0;
  const isExpanded = (g: AccountGroup) => searching || !collapsed[g];

  const toggle = (g: AccountGroup) => {
    if (searching) return;
    const next = { ...collapsed, [g]: !collapsed[g] };
    setCollapsed(next);
    writeCollapsed(next);
  };

  const makeDefault = (a: ClaudeAccount) => {
    setBusy(a.name);
    setWorkbenchClaudeAccount(a.name)
      .then((value) => {
        setCurrent(value);
        setError("");
      })
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setBusy(null));
  };

  /* Spec 82 REQ-WAS-002, spec 83 REQ-WAG-003: the daemon stops the pane's
     agent and resumes its conversation on the account; a refusal stays in the
     row, and an older core's "restart the Workbench" comes with New tab. */
  const keyOf = (t: SwitchTarget) => t.agent + ":" + t.name;
  const switchTo = (t: SwitchTarget) => {
    if (!focused) return;
    setConfirmSwitch(null);
    setSwitchNote(null);
    setSwitching(keyOf(t));
    switchWorkbenchAccount(focused.id, t.name, t.agent)
      .then(() => onClose())
      .catch((err) =>
        setSwitchNote({
          key: keyOf(t),
          message: errorMessage(err),
          outdated: err instanceof ApiError && err.code === "core_outdated",
        }),
      )
      .finally(() => setSwitching(null));
  };
  /* A running turn is stopped only after the owner says so. */
  const askSwitch = (t: SwitchTarget) => {
    setSwitchNote(null);
    if (focused?.state === "working") setConfirmSwitch(keyOf(t));
    else switchTo(t);
  };
  const openNewTab = (t: SwitchTarget) => {
    if (t.agent === "agy") actions.newTab(t.line, undefined, undefined, t.name);
    else actions.newTab(t.line, undefined, t.name);
    onClose();
  };
  const switchButton = (t: SwitchTarget, blocked: string) => (
    <button
      type="button"
      className="wb-acc-act"
      disabled={Boolean(blocked) || switching !== null}
      title={blocked || "Continue this pane's conversation on " + t.label}
      onClick={() => askSwitch(t)}
    >
      <Ic name="arrow-left-right" size={13} /> {switching === keyOf(t) ? "Switching…" : "Switch here"}
    </button>
  );
  const switchExtras = (t: SwitchTarget) => (
    <>
      {confirmSwitch === keyOf(t) ? (
        <div className="wb-acc-confirm" role="group" aria-label={"Switch to " + t.label}>
          <p>
            {t.agent === "agy" ? "Antigravity" : "Claude"} is working in this pane. Stop it and switch to {t.label}?
          </p>
          <div className="wb-acc-actions">
            <button type="button" className="wb-acc-act is-main" onClick={() => switchTo(t)}>
              Switch
            </button>
            <button type="button" className="wb-acc-act" onClick={() => setConfirmSwitch(null)}>
              Cancel
            </button>
          </div>
        </div>
      ) : null}
      {switchNote?.key === keyOf(t) ? (
        <div className="wb-acc-switch-note" role="alert">
          <p className="wb-acc-error">{switchNote.message}</p>
          {switchNote.outdated && t.line ? (
            <div className="wb-acc-actions">
              <button type="button" className="wb-acc-act" onClick={() => openNewTab(t)}>
                <Ic name="square-terminal" size={13} /> Open in a new tab
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
    </>
  );

  return (
    <div className="wb-acc">
      <div className="wb-acc-top-bar">
        <div className="wb-acc-head">
          <Ic name="users" size={16} />
          <b>Accounts</b>
          <span className="wb-spacer" />
          <button type="button" className="wb-icon-btn" aria-label="Refresh" data-tip="Refresh" onClick={refresh} disabled={loading}>
            <Ic name={loading ? "loader-circle" : "refresh-cw"} size={15} className={loading ? "spin" : ""} />
          </button>
        </div>
        <div className="wb-acc-search">
          <Ic name="search" size={14} />
          <input
            ref={searchRef}
            type="search"
            aria-label="Search accounts"
            placeholder="Search name, email, plan"
            autoComplete="off"
            spellCheck={false}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              /* Escape clears the search first; the next one closes the popup. */
              if (e.key === "Escape" && query) {
                e.preventDefault();
                e.stopPropagation();
                setQuery("");
              }
            }}
          />
          {query ? (
            <button
              type="button"
              className="wb-acc-clear"
              aria-label="Clear search"
              onClick={() => {
                setQuery("");
                searchRef.current?.focus({ preventScroll: true });
              }}
            >
              <Ic name="x" size={13} />
            </button>
          ) : null}
        </div>
      </div>

      <div className="wb-acc-scroll">
        {noMatch ? (
          <p className="wb-acc-note wb-acc-empty" role="status">
            No accounts match
          </p>
        ) : null}

        {showClaude ? (
          <Group
            id="claude"
            agent="claude"
            title="Claude Code"
            count={accounts ? (searching ? claudeShown.length : list.length) : null}
            expanded={isExpanded("claude")}
            manageHref={CLAUDE_MANAGE_HREF}
            onToggle={() => toggle("claude")}
            onClose={onClose}
          >
            {error ? <p className="wb-acc-error">{error}</p> : null}
            {!accounts && !error ? <p className="wb-acc-note">Reading accounts…</p> : null}
            {accounts && list.length === 0 ? <p className="wb-acc-note">No Claude Code accounts in the Router yet.</p> : null}
            {claudeShown.length ? (
              <ul className="wb-acc-list" aria-label="Claude Code accounts">
                {claudeShown.map((a) => {
                  const isDefault = a.name === defaultName;
                  const target: SwitchTarget = { agent: "claude", name: a.name, label: a.display_name, line: accountLine(a) };
                  const sub = [a.email, a.plan ? planLabel(a.plan) : ""].filter(Boolean).join(" · ");
                  return (
                    <li key={a.name} className={"wb-acc-item" + (isDefault ? " is-active" : "")}>
                      <div className="wb-acc-top">
                        <span className="wb-acc-num">{list.indexOf(a) + 1}</span>
                        <span className="wb-acc-name" title={a.config_dir || "~/.claude"}>
                          <b>{a.display_name}</b>
                          <span>{sub || a.name}</span>
                        </span>
                        <span className="wb-acc-tags">
                          {isDefault ? <span className="wb-acc-tag is-default">default</span> : null}
                          {best === a.name && !isDefault ? <span className="wb-acc-tag is-best">most room</span> : null}
                          {!a.logged_in ? <span className="wb-acc-tag is-warn">signed out</span> : null}
                          {cooling(a) ? (
                            <span className="wb-acc-tag is-warn" title={"Until " + new Date(a.cooling_until as string).toLocaleTimeString()}>
                              cooling
                            </span>
                          ) : null}
                        </span>
                      </div>
                      {a.windows.length ? (
                        <div className="wb-acc-bars">
                          {a.windows.map((w, j) => (
                            <Bar key={w.type + ":" + j} label={windowShortLabel(w)} title={w.label} pct={used(w)} resetsAt={w.resets_at} />
                          ))}
                        </div>
                      ) : (
                        <p className="wb-acc-note wb-acc-nouse">No usage read yet.</p>
                      )}
                      <div className="wb-acc-actions">
                        {!a.logged_in && a.login_command ? (
                          <button
                            type="button"
                            className="wb-acc-act is-main"
                            onClick={() => {
                              actions.newTab(a.login_command, "Sign in " + a.display_name, a.name);
                              onClose();
                            }}
                          >
                            <Ic name="log-in" size={13} /> Sign in
                          </button>
                        ) : null}
                        <button
                          type="button"
                          className="wb-acc-act"
                          onClick={() => {
                            actions.newTab(accountLine(a), undefined, a.name);
                            onClose();
                          }}
                        >
                          <Ic name="square-terminal" size={13} /> New tab
                        </button>
                        {switchButton(target, switchBlocked(focused, a))}
                        {canSetDefault && !isDefault ? (
                          <button type="button" className="wb-acc-act is-quiet" disabled={busy === a.name} onClick={() => makeDefault(a)}>
                            <Ic name="check" size={13} /> Use for new terminals
                          </button>
                        ) : null}
                      </div>
                      {switchExtras(target)}
                    </li>
                  );
                })}
              </ul>
            ) : null}
            {canSetDefault ? (
              <p className="wb-acc-note wb-acc-foot">
                New Workbench terminals run <code>claude</code> on the default account. Plain <code>claude</code> outside the
                Workbench is unchanged.
              </p>
            ) : null}
          </Group>
        ) : null}

        {showAgy ? (
          <Group
            id="agy"
            agent="agy"
            title="Antigravity"
            count={agy ? (searching ? agyShown.length : agyList.length) : null}
            expanded={isExpanded("agy")}
            manageHref={AGY_MANAGE_HREF}
            onToggle={() => toggle("agy")}
            onClose={onClose}
          >
            {agyError ? <p className="wb-acc-error">{agyError}</p> : null}
            {agy && agyList.length === 0 ? <p className="wb-acc-note">No Antigravity accounts in the Router yet.</p> : null}
            {agyShown.length ? (
              <ul className="wb-acc-list" aria-label="Antigravity accounts">
                {agyShown.map((a) => {
                  const line = agyLine(a, agy?.binary);
                  const target: SwitchTarget = { agent: "agy", name: a.name, label: a.display_name, line };
                  /* The display name, then email · tier; the email is left
                     out when it already is the display name. */
                  const sub = [a.email && a.email !== a.display_name ? a.email : "", a.tier].filter(Boolean).join(" · ");
                  return (
                    <li key={a.name} className="wb-acc-item">
                      <div className="wb-acc-top">
                        <span className="wb-acc-num">{agyList.indexOf(a) + 1}</span>
                        <span className="wb-acc-name" title={a.home || "~"}>
                          <b>{a.display_name}</b>
                          <span>{sub || a.name}</span>
                        </span>
                        <span className="wb-acc-tags">
                          {agyBest === a.name ? <span className="wb-acc-tag is-best">most room</span> : null}
                          {!a.signed_in ? <span className="wb-acc-tag is-warn">signed out</span> : null}
                          {cooling(a) ? (
                            <span className="wb-acc-tag is-warn" title={"Until " + new Date(a.cooling_until as string).toLocaleTimeString()}>
                              cooling
                            </span>
                          ) : null}
                          {a.disabled ? <span className="wb-acc-tag">disabled</span> : null}
                        </span>
                      </div>
                      {a.windows.length ? (
                        <div className="wb-acc-bars is-agy">
                          {a.windows.map((w, j) => (
                            <Bar
                              key={w.group + "/" + w.type + ":" + j}
                              label={agyWindowLabel(w)}
                              title={[w.group, w.type].filter(Boolean).join(" · ")}
                              pct={agyUsed(w)}
                              resetsAt={w.resets_at}
                            />
                          ))}
                        </div>
                      ) : (
                        <p className="wb-acc-note wb-acc-nouse" title={a.windows_error || undefined}>
                          No usage read yet.
                        </p>
                      )}
                      <div className="wb-acc-actions">
                        {!a.signed_in ? (
                          <a className="wb-acc-act is-main" href={AGY_MANAGE_HREF} onClick={onClose}>
                            <Ic name="log-in" size={13} /> Sign in in Router
                          </a>
                        ) : null}
                        <button
                          type="button"
                          className="wb-acc-act"
                          disabled={!line}
                          onClick={() => openNewTab(target)}
                        >
                          <Ic name="square-terminal" size={13} /> New tab
                        </button>
                        {switchButton(target, line ? agySwitchBlocked(focused, a) : SWITCH_NO_AGY_CONVERSATION)}
                      </div>
                      {switchExtras(target)}
                    </li>
                  );
                })}
              </ul>
            ) : null}
          </Group>
        ) : null}
      </div>
      <AgentHookLink onClose={onClose} />
    </div>
  );
}

/* Spec 69: the notice the Workbench shows when it opens while the Claude Code
   hooks are not installed and the owner was never asked (state "unset").
   Lana writes settings.json only on Install. "Not now" hides it until the
   Workbench opens again; "Don't ask again" stores off, so it never returns
   (the switch in Accounts can still turn the hooks on). */
export function AgentHookNotice() {
  const [state, setState] = useState<AgentHookState | null>(null);
  const [hidden, setHidden] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let cancelled = false;
    getAgentHook()
      .then((s) => !cancelled && setState(s))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);
  if (!state || hidden) return null;
  const broken = state.folders.filter((f) => f.error);
  if (state.state !== "unset" || !state.needs_install) {
    // After Install: stay only to show a folder that failed.
    if (state.state === "on" && broken.length > 0) {
      return (
        <div className="wb-hook-notice" role="status">
          <div className="wb-hook-notice-body">
            <b>Some agent folders could not be updated</b>
            {broken.map((f) => (
              <small key={f.dir} className="wb-hook-notice-error">
                {f.dir}: {f.error}
              </small>
            ))}
          </div>
          <div className="wb-hook-notice-actions">
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setHidden(true)}>
              Close
            </button>
          </div>
        </div>
      );
    }
    return null;
  }
  const choose = (enabled: boolean) => {
    setBusy(true);
    setError("");
    setAgentHook(enabled)
      .then(setState)
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setBusy(false));
  };
  return (
    <div className="wb-hook-notice" role="status">
      <Ic name="git-branch" size={16} className="wb-hook-notice-icon" />
      <div className="wb-hook-notice-body">
        <b>{AGENT_HOOK_TITLE}</b>
        <small>Lana adds its hooks to Claude Code and Antigravity, so the Workbench can follow what they work on.</small>
        {error ? <small className="wb-hook-notice-error">{error}</small> : null}
      </div>
      <div className="wb-hook-notice-actions">
        <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={() => choose(true)}>
          {busy ? "Installing…" : "Install"}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setHidden(true)}>
          Not now
        </button>
        <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => choose(false)}>
          Don't ask again
        </button>
      </div>
    </div>
  );
}

/* The popup's footer: one line with the hook state, linking to Settings >
   General, where the switch lives (spec 77 REQ-WRA-009). Nothing shows while
   the state is unknown. */
function AgentHookLink({ onClose }: { onClose: () => void }) {
  const [state, setState] = useState<AgentHookState | null>(null);
  useEffect(() => {
    let cancelled = false;
    getAgentHook()
      .then((s) => !cancelled && setState(s))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);
  if (!state) return null;
  return (
    <div className="wb-acc-hook">
      <a className="wb-acc-hook-link" href={AGENT_HOOK_SETTINGS_HREF} title={AGENT_HOOK_TITLE} onClick={onClose}>
        <Ic name="plug" size={13} />
        <span>{agentHookSummary(state)}</span>
        <Ic name="arrow-up-right" size={13} />
      </a>
    </div>
  );
}

/* Desktop: a popover under the tab-bar button. */
export function AccountsPopover({
  anchor,
  actions,
  onClose,
}: {
  anchor: HTMLElement;
  actions: AccountActions;
  onClose: () => void;
}) {
  const anchorRef = useMemo(() => ({ current: anchor as HTMLElement | null }), [anchor]);
  return (
    <Popover anchorRef={anchorRef} open onClose={onClose} className="wb-acc-pop" width={440}>
      <AccountsPanel actions={actions} onClose={onClose} autoFocusSearch />
    </Popover>
  );
}
