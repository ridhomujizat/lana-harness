/* eslint-disable react-hooks/set-state-in-effect -- the page loads its status on mount, like the Claude Code page. */
import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import * as routerApi from "../../api/router";
import type { AntigravityAccount, AntigravitySignIn, AntigravityStatus, PlanWindow } from "../../api/router";
import { Badge, Button, Checkbox, EmptyState, IconButton, Input, Modal, Panel } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { Alert, SettingsRestartBanner, StatusLine, markSettingsSaved } from "../settings-pages";
import { AccountOrderList, POOL_TERMS_NOTE } from "./account-order";
import type { OrderItem } from "./account-order";
import { PlanWindows } from "./limits";
import { RefreshControl, newestUpdatedAt } from "./limits-refresh";
import { rememberAntigravityAccounts } from "./presets";
import { ProviderLogo } from "./provider-logo";
import { UsedBy, antigravityStatus } from "./providers";
import { compact, count, failureOf, inUseText, isStale } from "./router-common";

/* Router → Providers → Antigravity (#/router/providers/antigravity), spec 74
   API-AGY-030..032 and spec 75 REQ-PAF-006: the Antigravity accounts with
   their tier, sign-in state, quota windows per model group, and 24-hour
   usage; "Add account"; the Google sign-in panel; and the account order.
   Adding an account starts `agy acp --login` on the daemon. The page shows
   Google's link; after the owner allows access, Google shows a code on
   antigravity.google, which the owner pastes here. The code goes to agy once
   and is never kept, here or on the daemon. */

/* How often a sign-in in progress is polled (API-AGY-031). */
export const AGY_SIGN_IN_POLL_MS = 1500;

export const AGY_CODE_HINT = "After you allow access, Google shows a code on antigravity.google. Paste it here.";

export const AGY_TERMS_NOTE =
  "Each account runs with its own Google sign-in. Using several personal accounts to get around plan limits can break Google's terms.";

const LABEL_MAX = 60;

const ACTIVE_STATES = ["starting", "waiting_code", "running"];

function BackLink() {
  return (
    <a className="rt-back" href="#/router/providers">
      <Ic name="arrow-left" size={14} />
      Providers
    </a>
  );
}

/* The quota windows of an account as plan windows: "Gemini · 5 hours",
   used = 1 − remaining (API-AGY-027). */
export function antigravityWindows(account: AntigravityAccount): PlanWindow[] {
  return account.windows.map((window) => {
    const span = window.type === "5h" ? "5 hours" : window.type === "weekly" ? "weekly" : window.type;
    const remaining = window.remaining;
    const used = remaining === null ? null : Math.max(0, Math.min(1, 1 - remaining));
    return {
      type: (window.group || "all") + "/" + window.type,
      status: remaining !== null && remaining <= 0 ? "rejected" : "allowed",
      utilization: used,
      resets_at: window.resets_at || undefined,
      label: (window.group ? window.group + " · " : "") + span,
      source: window.source,
    };
  });
}

function usageText(account: AntigravityAccount): string {
  const usage = account.usage_24h;
  if (!usage.calls) {
    return "No calls";
  }
  return (
    count(usage.calls) +
    (usage.calls === 1 ? " call" : " calls") +
    " · " +
    compact(usage.input_tokens) +
    " in / " +
    compact(usage.output_tokens) +
    " out"
  );
}

/* ------------------------------------------------------------------ */
/* Sign-in panel (API-AGY-031)                                         */
/* ------------------------------------------------------------------ */

export interface AgyFlow {
  name: string;
  phase: Exclude<AntigravitySignIn["state"], "idle">;
  url: string;
  error: string;
  /* A refused code (409 not_waiting, 422 bad_code); the panel stays open. */
  codeError: string;
  sending: boolean;
}

function isActive(flow: AgyFlow | undefined): boolean {
  return Boolean(flow && ACTIVE_STATES.includes(flow.phase));
}

export function AgySignInPanel({
  flow,
  onSend,
  onCancel,
  onRetry,
  onDismiss,
}: {
  flow: AgyFlow;
  onSend: (code: string) => void;
  onCancel: () => void;
  onRetry: () => void;
  onDismiss: () => void;
}) {
  /* The code lives only in this field and leaves it on Send. */
  const [code, setCode] = useState("");
  const name = "Sign-in of " + flow.name;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = code.trim();
    setCode("");
    if (value) {
      onSend(value);
    }
  }

  if (flow.phase === "starting" || flow.phase === "running") {
    return (
      <div className="rt-signin is-running" role="status" aria-label={name}>
        <span className="rt-signin-spin" aria-hidden="true">
          <Ic name="loader-circle" size={16} />
        </span>
        <div className="rt-signin-t">
          <b>{flow.phase === "starting" ? "Starting Antigravity sign-in…" : "Signing in…"}</b>
        </div>
        <Button size="sm" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    );
  }
  if (flow.phase === "waiting_code") {
    return (
      <div className="rt-signin is-running rt-agy-code" role="group" aria-label={name}>
        <Ic name="log-in" size={16} />
        <div className="rt-signin-t">
          <b>Sign in with Google</b>
          {flow.url ? (
            <a className="btn btn-primary btn-sm rt-agy-open" href={flow.url} target="_blank" rel="noopener noreferrer">
              <Ic name="external-link" size={13} />
              Open Google sign-in
            </a>
          ) : null}
          <span className="hint">{AGY_CODE_HINT}</span>
          <form className="rt-agy-code-form" onSubmit={submit}>
            <Input
              aria-label={"Code from Google for " + flow.name}
              className="mono"
              type="text"
              value={code}
              disabled={flow.sending}
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
              spellCheck={false}
              maxLength={2048}
              placeholder="Paste the code"
              onChange={(event) => setCode(event.target.value.replace(/[\r\n]+/g, ""))}
            />
            <Button
              type="submit"
              size="sm"
              variant="primary"
              loading={flow.sending}
              disabled={flow.sending || !code.trim()}
            >
              Send
            </Button>
            <Button size="sm" variant="ghost" disabled={flow.sending} onClick={onCancel}>
              Cancel
            </Button>
          </form>
          {flow.codeError ? (
            <span className="rt-signin-err" role="alert">
              {flow.codeError}
            </span>
          ) : null}
        </div>
      </div>
    );
  }
  if (flow.phase === "succeeded") {
    return null;
  }
  const failed = flow.phase === "failed";
  return (
    <div
      className={"rt-signin " + (failed ? "is-failed" : "is-cancelled")}
      role={failed ? "alert" : "status"}
      aria-label={name}
    >
      <Ic name={failed ? "circle-alert" : "circle-slash"} size={16} />
      <div className="rt-signin-t">
        <b>{failed ? "Sign-in did not finish." : "Sign-in cancelled."}</b>
        {failed && flow.error ? <span className="rt-signin-err">{flow.error}</span> : null}
      </div>
      <span className="rt-signin-acts">
        <Button size="sm" variant="ghost" onClick={onDismiss}>
          Dismiss
        </Button>
        <Button size="sm" variant="primary" icon="rotate-ccw" onClick={onRetry}>
          Try again
        </Button>
      </span>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Account row                                                         */
/* ------------------------------------------------------------------ */

function LabelForm({
  account,
  busy,
  onSave,
  onCancel,
}: {
  account: AntigravityAccount;
  busy: boolean;
  onSave: (label: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(account.label);
  const tooLong = value.trim().length > LABEL_MAX;
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!tooLong) {
      onSave(value.trim());
    }
  }
  return (
    <form className="rt-acct-rename" onSubmit={submit}>
      <Input
        aria-label={"Label of " + account.name}
        value={value}
        disabled={busy}
        autoFocus
        maxLength={LABEL_MAX + 20}
        placeholder={account.name}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onCancel();
          }
        }}
      />
      <Button type="submit" size="sm" variant="primary" loading={busy} disabled={busy || tooLong}>
        Save
      </Button>
      <Button size="sm" variant="ghost" disabled={busy} onClick={onCancel}>
        Cancel
      </Button>
      {tooLong ? <span className="rt-warn">At most {LABEL_MAX} characters.</span> : null}
    </form>
  );
}

function AgyAccountRow({
  account,
  busy,
  flow,
  highlighted,
  renaming,
  canSignIn,
  onSignIn,
  onSendCode,
  onCancelSignIn,
  onDismissFlow,
  onAskRemove,
  onRename,
  onSaveLabel,
  onCancelRename,
}: {
  account: AntigravityAccount;
  busy: boolean;
  flow: AgyFlow | undefined;
  highlighted: boolean;
  renaming: boolean;
  canSignIn: boolean;
  onSignIn: () => void;
  onSendCode: (code: string) => void;
  onCancelSignIn: () => void;
  onDismissFlow: () => void;
  onAskRemove: () => void;
  onRename: () => void;
  onSaveLabel: (label: string) => void;
  onCancelRename: () => void;
}) {
  const users = account.agents.map((name) => "agents." + name).concat(account.combos.map((name) => "combos." + name));
  const display = account.display_name || account.label || account.name;
  const windows = antigravityWindows(account);
  const active = isActive(flow);
  return (
    <li
      className={"rt-acct" + (account.disabled ? " is-off" : "") + (highlighted ? " is-new" : "")}
      aria-label={"Account " + account.name}
    >
      <div className="rt-acct-head">
        <ProviderLogo logo="antigravity" name="Antigravity" size={32} />
        <div className="rt-acct-id">
          {renaming ? (
            <LabelForm account={account} busy={busy} onSave={onSaveLabel} onCancel={onCancelRename} />
          ) : (
            <span className="rt-acct-name">
              <span className="trunc" title={display}>
                {display}
              </span>
              {account.builtin ? <Badge>Built in</Badge> : null}
              {account.builtin ? null : (
                <IconButton icon="pencil" size="xs" label={"Rename " + account.name} disabled={busy} onClick={onRename} />
              )}
            </span>
          )}
          <span className="rt-acct-sub">
            <span className="mono">{account.name}</span>
            {account.tier ? <span> · {account.tier}</span> : null}
          </span>
        </div>
        <div className="rt-acct-state">
          {account.tier ? <Badge tone="violet">{account.tier}</Badge> : null}
          {account.disabled ? <Badge tone="gray">Off</Badge> : null}
          {account.signed_in ? (
            <Badge tone="green" dot>
              Signed in
            </Badge>
          ) : (
            <Badge tone="amber" dot>
              Not signed in
            </Badge>
          )}
        </div>
        <div className="rt-acct-acts">
          {!account.builtin && !account.signed_in && !active ? (
            <Button size="sm" variant="primary" icon="log-in" disabled={busy || !canSignIn} onClick={onSignIn}>
              Sign in
            </Button>
          ) : null}
          {account.builtin ? null : (
            <IconButton icon="trash-2" size="sm" label={"Remove " + account.name} disabled={busy} onClick={onAskRemove} />
          )}
        </div>
      </div>

      {account.builtin ? (
        <p className="hint rt-agy-builtin">
          {account.signed_in ? (
            "Signed in with your own agy login"
          ) : (
            <>
              Run <code className="mono">agy</code> in a terminal to sign in
            </>
          )}
        </p>
      ) : null}
      {flow && flow.phase === "succeeded" ? (
        <p className="rt-signin-ok" role="status">
          <Ic name="circle-check" size={14} /> Signed in{account.tier ? " · " + account.tier : ""}
        </p>
      ) : null}
      {flow ? (
        <AgySignInPanel
          flow={flow}
          onSend={onSendCode}
          onCancel={onCancelSignIn}
          onRetry={onSignIn}
          onDismiss={onDismissFlow}
        />
      ) : null}

      <div className="rt-acct-body">
        <div className="rt-acct-windows">
          {account.signed_in || windows.length > 0 ? <PlanWindows windows={windows} /> : null}
          {account.windows_error ? (
            <p className="rt-limit-err" role="status">
              <Ic name="circle-alert" size={12} />
              <span>{account.windows_error}</span>
            </p>
          ) : null}
        </div>
        <dl className="rt-facts rt-acct-facts">
          <dt>Last 24 h</dt>
          <dd className="num">{usageText(account)}</dd>
          <dt>Runs</dt>
          <dd className="num">
            {count(account.running)} running · {count(account.queued)} queued
          </dd>
          <dt>Models</dt>
          <dd className="num">{account.models_count ? count(account.models_count) : "None listed yet"}</dd>
          <dt>Used by</dt>
          <dd>
            <UsedBy usedBy={users} />
          </dd>
          {account.home ? (
            <>
              <dt>Folder</dt>
              <dd className="mono trunc" title={account.home}>
                {account.home}
              </dd>
            </>
          ) : null}
        </dl>
      </div>
      {account.error ? (
        <p className="rt-limit-err rt-acct-foot" role="status">
          <Ic name="circle-alert" size={12} />
          <span>{account.error}</span>
        </p>
      ) : null}
    </li>
  );
}

/* ------------------------------------------------------------------ */
/* Remove dialog                                                       */
/* ------------------------------------------------------------------ */

function RemoveDialog({
  account,
  busy,
  onCancel,
  onConfirm,
}: {
  account: AntigravityAccount;
  busy: boolean;
  onCancel: () => void;
  onConfirm: (removeFolder: boolean) => void;
}) {
  const [removeFolder, setRemoveFolder] = useState(true);
  const display = account.display_name || account.name;
  return (
    <Modal
      title={"Remove " + display + "?"}
      size="sm"
      dismissible={!busy}
      onClose={onCancel}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="danger" loading={busy} disabled={busy} onClick={() => onConfirm(removeFolder)}>
            Remove
          </Button>
        </>
      }
    >
      <p className="rt-confirm-body">
        Agents and combos that use {account.name} must be changed first. A sign-in that is still running stops.
      </p>
      <label className="rt-agy-folder">
        <Checkbox checked={removeFolder} disabled={busy} onChange={setRemoveFolder} />
        <span>
          <b>Also delete the account folder</b>
          {account.home ? <span className="hint mono">{account.home}</span> : null}
        </span>
      </label>
    </Modal>
  );
}

/* ------------------------------------------------------------------ */
/* Page                                                                */
/* ------------------------------------------------------------------ */

/* The rows of the account order list. */
export function antigravityOrderItems(accounts: AntigravityAccount[]): OrderItem[] {
  return accounts.map((account) => ({
    id: account.name,
    label: account.display_name || account.name,
    detail: account.tier || (account.signed_in ? "" : "Not signed in"),
    windows: antigravityWindows(account),
    coolingUntil: account.cooling_until,
    pool: account.pool,
  }));
}

export function AntigravityDetail() {
  const [status, setStatus] = useState<AntigravityStatus | null>(null);
  const [failed, setFailed] = useState(false);
  const [version, setVersion] = useState("");
  const [restartRequired, setRestartRequired] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [busyRow, setBusyRow] = useState("");
  const [adding, setAdding] = useState(false);
  const [flows, setFlows] = useState<Record<string, AgyFlow>>({});
  const [highlight, setHighlight] = useState("");
  const [renaming, setRenaming] = useState("");
  const [removing, setRemoving] = useState<AntigravityAccount | null>(null);
  const [pollTick, setPollTick] = useState(0);
  const [configToken, setConfigToken] = useState(0);

  const reloadStatus = useCallback(async () => {
    try {
      const value = await Promise.resolve().then(() => routerApi.getAntigravity());
      rememberAntigravityAccounts(value.accounts.map((account) => account.name));
      setStatus(value);
      /* A sign-in that runs on the daemon (another tab, a reload) shows
         here too. */
      setFlows((current) => {
        let next = current;
        for (const account of value.accounts) {
          const signIn = account.sign_in;
          if (!account.builtin && ACTIVE_STATES.includes(signIn.state) && !current[account.name]) {
            next = next === current ? { ...current } : next;
            next[account.name] = {
              name: account.name,
              phase: signIn.state as AgyFlow["phase"],
              url: signIn.url,
              error: "",
              codeError: "",
              sending: false,
            };
          }
        }
        return next;
      });
      setFailed(false);
      return value;
    } catch {
      setFailed(true);
      return null;
    }
  }, []);

  useEffect(() => {
    void reloadStatus();
  }, [reloadStatus]);

  /* The config version guards the writes. */
  useEffect(() => {
    let alive = true;
    Promise.resolve()
      .then(() => routerApi.listProviders())
      .then((value) => {
        if (alive && value) {
          setVersion(value.version);
          setRestartRequired(Boolean(value.restart_required));
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [configToken]);
  const reloadConfig = useCallback(() => setConfigToken((current) => current + 1), []);

  const accounts = status ? status.accounts : [];
  const installed = Boolean(status && status.installed);
  const usable = installed && Boolean(status && status.version_ok);

  const setFlow = useCallback((name: string, change: Partial<AgyFlow> | null) => {
    setFlows((current) => {
      const next = { ...current };
      if (change === null) {
        delete next[name];
      } else {
        const base: AgyFlow = current[name] || {
          name,
          phase: "starting",
          url: "",
          error: "",
          codeError: "",
          sending: false,
        };
        next[name] = { ...base, ...change, name };
      }
      return next;
    });
  }, []);

  /* Applies a sign-in state from the daemon. */
  const apply = useCallback(
    (name: string, state: AntigravitySignIn) => {
      if (state.state === "succeeded") {
        setFlow(name, { phase: "succeeded", error: "", url: "", sending: false });
        setHighlight(name);
        void reloadStatus();
        return;
      }
      if (state.state === "cancelled") {
        setFlow(name, { phase: "cancelled", error: "", url: "", sending: false });
        return;
      }
      if (state.state === "failed" || state.state === "idle") {
        setFlow(name, {
          phase: "failed",
          url: "",
          sending: false,
          error: state.error || (state.state === "idle" ? "The sign-in stopped. Try again." : ""),
        });
        return;
      }
      setFlow(name, { phase: state.state, url: state.url });
    },
    [reloadStatus, setFlow]
  );

  /* Poll every sign-in in progress each AGY_SIGN_IN_POLL_MS. */
  const polling = Object.values(flows)
    .filter((flow) => isActive(flow))
    .map((flow) => flow.name)
    .sort()
    .join(",");
  useEffect(() => {
    if (!polling) {
      return undefined;
    }
    let alive = true;
    const timer = window.setTimeout(() => {
      void (async () => {
        for (const name of polling.split(",")) {
          try {
            const state = await routerApi.getAntigravitySignIn(name);
            if (!alive) {
              return;
            }
            apply(name, state);
          } catch (failure) {
            if (!alive) {
              return;
            }
            setFlow(name, { phase: "failed", error: failureOf(failure).message, url: "", sending: false });
          }
        }
        if (alive) {
          setPollTick((tick) => tick + 1);
        }
      })();
    }, AGY_SIGN_IN_POLL_MS);
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [polling, pollTick, apply, setFlow]);

  function fail(failure: unknown, what: string) {
    if (isStale(failure)) {
      setError("The config changed since this page loaded. Reload the page and try again.");
      setDetails([]);
      reloadConfig();
      return;
    }
    const inUse = inUseText(failure, what);
    if (inUse) {
      setError(inUse);
      setDetails([]);
      return;
    }
    const info = failureOf(failure);
    if (info.code === "folder_outside_home") {
      setError(
        "The folder of " + what + " is outside lana's antigravity folder, so lana did not delete anything. Remove it without the folder."
      );
      setDetails([]);
      return;
    }
    setError(info.message);
    setDetails(info.details);
  }

  function clearNotes() {
    setError("");
    setDetails([]);
    setMessage("");
  }

  async function refresh() {
    setRefreshing(true);
    try {
      await routerApi.refreshRouterLimits();
    } catch (failure) {
      setError(failureOf(failure).message);
    }
    await reloadStatus();
    setRefreshing(false);
  }

  /* One click: add the account and start its sign-in (API-AGY-021). */
  async function addAccount() {
    if (adding) {
      return;
    }
    setAdding(true);
    clearNotes();
    try {
      const result = await routerApi.createAntigravityAccount();
      markSettingsSaved();
      if (result.version) {
        setVersion(result.version);
      }
      setHighlight("");
      setFlow(result.name, { phase: "starting", url: "", error: "", codeError: "", sending: false });
      apply(result.name, result.sign_in.state === "idle" ? { ...result.sign_in, state: "starting" } : result.sign_in);
      if (result.restart_required) {
        setMessage("Account " + result.name + " added. Restart the daemon for agents to use it.");
        setRestartRequired(true);
      }
      void reloadStatus();
    } catch (failure) {
      fail(failure, "the account");
    } finally {
      setAdding(false);
    }
  }

  async function signIn(name: string) {
    clearNotes();
    setHighlight("");
    setFlow(name, { phase: "starting", url: "", error: "", codeError: "", sending: false });
    try {
      const result = await routerApi.startAntigravitySignIn(name);
      apply(name, result.state === "idle" ? { ...result, state: "starting" } : result);
    } catch (failure) {
      const info = failureOf(failure);
      if (info.code === "sign_in_running") {
        return;
      }
      setFlow(name, { phase: "failed", error: info.message });
    }
  }

  async function sendCode(name: string, code: string) {
    setFlow(name, { sending: true, codeError: "" });
    try {
      const result = await routerApi.sendAntigravitySignInCode(name, code);
      setFlow(name, { sending: false });
      apply(name, result);
    } catch (failure) {
      const info = failureOf(failure);
      const text =
        info.code === "bad_code"
          ? "That code was not accepted. Copy the whole code Google shows and paste it again."
          : info.code === "not_waiting"
            ? "The sign-in is no longer waiting for a code."
            : info.message;
      setFlow(name, { sending: false, codeError: text });
    }
  }

  async function cancelSignIn(name: string) {
    try {
      const result = await routerApi.cancelAntigravitySignIn(name);
      if (result.state === "succeeded") {
        apply(name, result);
        return;
      }
      setFlow(name, { phase: "cancelled", error: "", url: "", sending: false });
    } catch (failure) {
      setFlow(name, { phase: "failed", error: failureOf(failure).message, url: "", sending: false });
    }
  }

  async function remove(account: AntigravityAccount, removeFolder: boolean) {
    setBusyRow(account.name);
    clearNotes();
    try {
      const result = await routerApi.deleteProvider(account.name, version, { removeFolder });
      markSettingsSaved();
      setFlow(account.name, null);
      setRemoving(null);
      setMessage(
        "Account " +
          account.name +
          " removed." +
          (result && result.folder_removed ? " Its folder was deleted." : removeFolder ? "" : " Its folder stays.")
      );
      if (result && result.version) {
        setVersion(result.version);
      }
      reloadConfig();
      void reloadStatus();
    } catch (failure) {
      setRemoving(null);
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  async function saveLabel(account: AntigravityAccount, label: string) {
    setBusyRow(account.name);
    clearNotes();
    try {
      const result = await routerApi.setAntigravityAccountLabel(account.name, label, version);
      markSettingsSaved();
      if (result && result.version) {
        setVersion(result.version);
      }
      setRenaming("");
      setMessage(label ? account.name + " is now shown as " + label + "." : account.name + " has no label now.");
      void reloadStatus();
    } catch (failure) {
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  async function saveOrder(order: string[], unpooled: string[]) {
    const result = await routerApi.savePoolOrder("antigravity", order, unpooled, version || undefined);
    markSettingsSaved();
    if (result.version) {
      setVersion(result.version);
    }
    void reloadStatus();
  }

  const state = antigravityStatus(status, failed);
  const pending = Object.values(flows).filter(
    (flow) => flow.phase !== "succeeded" && !accounts.some((account) => account.name === flow.name)
  );
  const updatedAt = newestUpdatedAt(
    accounts.map((account) => ({ provider: account.name, updated_at: account.windows_updated_at || undefined })),
    () => true
  );
  const panelHandlers = (name: string) => ({
    onSend: (code: string) => void sendCode(name, code),
    onCancel: () => void cancelSignIn(name),
    onRetry: () => void signIn(name),
    onDismiss: () => setFlow(name, null),
  });

  return (
    <div className="rt-detail-page">
      <BackLink />
      <header className="rt-dh">
        <ProviderLogo logo="antigravity" name="Antigravity" size={52} />
        <div className="rt-dh-t">
          <h1>Antigravity</h1>
          <p className="rt-dh-sub">
            Your Google Antigravity plan, through the agy program on this machine. Agents pick its models like any other
            model; the turn runs in Antigravity with the agent&apos;s persona, memory, and lana tools.
          </p>
          <div className="rt-dh-status">
            <Badge tone={state.tone} dot={state.tone !== "gray"}>
              {state.label}
            </Badge>
          </div>
        </div>
        <div className="rt-dh-acts">
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            loading={adding}
            disabled={adding || !usable}
            title={status && !usable ? "Install the Antigravity CLI first." : undefined}
            onClick={() => void addAccount()}
          >
            Add account
          </Button>
        </div>
      </header>

      <SettingsRestartBanner restartRequired={restartRequired} onRestarted={reloadConfig} />
      <StatusLine text={message} />
      <Alert tone="danger" message={error} details={details} />

      {status && !usable ? (
        <div className="rt-agy-install" role="note" aria-label="Install the Antigravity CLI">
          <Ic name="download" size={18} />
          <div>
            <b>Install the Antigravity CLI</b>
            <p className="hint">
              {installed
                ? "agy " +
                  (status.version || "on this machine") +
                  " is too old. lana needs " +
                  status.min_version +
                  " or newer. Update it, then reload this page."
                : "lana runs Antigravity through the agy program, version " +
                  status.min_version +
                  " or newer. Install it on this machine, then reload this page."}{" "}
              <a href="https://antigravity.google" target="_blank" rel="noopener noreferrer">
                antigravity.google
              </a>
            </p>
          </div>
        </div>
      ) : null}

      <Panel
        title="Accounts"
        className="rt-conns-panel rt-accts-panel"
        actions={<RefreshControl refreshing={refreshing} updatedAt={updatedAt} onRefresh={() => void refresh()} />}
      >
        {pending.map((flow) => (
          <AgySignInPanel key={flow.name} flow={flow} {...panelHandlers(flow.name)} />
        ))}
        {failed && !status ? <p className="rt-limit-err">Could not read the Antigravity status.</p> : null}
        {!status && !failed ? <div className="home-empty">Checking Antigravity…</div> : null}
        {status && accounts.length === 0 ? (
          <EmptyState size="sm" icon="user-plus" text="No Antigravity account yet. Add one to use your Google plan." />
        ) : null}
        {accounts.length > 0 ? (
          <ul className="rt-accts">
            {accounts.map((account) => (
              <AgyAccountRow
                key={account.name}
                account={account}
                busy={busyRow === account.name}
                flow={flows[account.name]}
                highlighted={highlight === account.name}
                renaming={renaming === account.name}
                canSignIn={usable}
                onSignIn={() => void signIn(account.name)}
                onSendCode={(code) => void sendCode(account.name, code)}
                onCancelSignIn={() => void cancelSignIn(account.name)}
                onDismissFlow={() => setFlow(account.name, null)}
                onAskRemove={() => setRemoving(account)}
                onRename={() => setRenaming(account.name)}
                onSaveLabel={(label) => void saveLabel(account, label)}
                onCancelRename={() => setRenaming("")}
              />
            ))}
          </ul>
        ) : null}
        {accounts.length > 1 ? <p className="hint rt-conn-note">{AGY_TERMS_NOTE}</p> : null}
      </Panel>

      {accounts.length > 1 ? (
        <Panel title="Account order" className="rt-order-panel">
          <AccountOrderList
            label="Antigravity account order"
            items={antigravityOrderItems(accounts)}
            onSave={saveOrder}
          />
          <p className="hint rt-conn-note">{POOL_TERMS_NOTE}</p>
        </Panel>
      ) : null}

      {removing ? (
        <RemoveDialog
          account={removing}
          busy={busyRow === removing.name}
          onCancel={() => setRemoving(null)}
          onConfirm={(removeFolder) => void remove(removing, removeFolder)}
        />
      ) : null}
    </div>
  );
}
