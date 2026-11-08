import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import * as routerApi from "../../api/router";
import type {
  ClaudeAccount,
  ClaudeSignIn,
  ListProvidersResponse,
  ProviderModels,
  ProviderRow,
  ProviderWriteRequest,
  UsageProviderRow,
} from "../../api/router";
import { Badge, Button, EmptyState, IconButton, Input, Modal, Panel, Select, Toggle } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { CreateAgentDialog } from "../agents";
import { Alert, SettingsRestartBanner, StatusLine, markSettingsSaved } from "../settings-pages";
import { ACCOUNTS_TERMS_NOTE, useClaudeAccounts } from "./claude-code";
import { LoginCommand, PlanWindows, accountWindows, planLabel } from "./limits";
import { TestResult, usedByText } from "./models";
import type { TestState } from "./models";
import { CLAUDE_AGENT_PROVIDER_NAME, CLAUDE_CODE_KIND } from "./presets";
import { ProviderLogo } from "./provider-logo";
import { UsedBy, claudeStatus } from "./providers";
import { DASH, compact, count, failureOf, inUseText, isStale, statsCostText } from "./router-common";

/* Router → Providers → Claude Code (#/router/providers/claude-code), spec 39
   revision 3 (REQ-CCT-009..012): the Claude Code accounts as connections,
   each with its email, plan, windows, and usage; then the Claude Code models
   with custom IDs. Adding or signing in an account is one click: the daemon
   starts the official `claude auth login`, which opens Claude Code's own
   sign-in page in the browser; the page polls the sign-in until it ends. */

/* How often a running sign-in is polled. */
export const SIGN_IN_POLL_MS = 1500;

export const SIGN_IN_BUTTON = "Sign in with Claude Code";

export const SIGN_IN_HELP = "Claude Code opens its own sign-in page. lana never sees your password or tokens.";

const LABEL_MAX = 60;

function BackLink() {
  return (
    <a className="rt-back" href="#/router/providers">
      <Ic name="arrow-left" size={14} />
      Providers
    </a>
  );
}

/* The PUT body of a kind "claude-code" account (API-CCT-010): the stored
   keys with one change. */
function accountBody(row: ProviderRow | undefined, version: string, change: Partial<ProviderWriteRequest>): ProviderWriteRequest {
  const body: ProviderWriteRequest = { kind: CLAUDE_CODE_KIND, expected_version: version };
  if (row && !row.builtin && row.config_dir) {
    body.config_dir = row.config_dir;
  }
  body.allow_scheduled = Boolean(row && row.allow_scheduled);
  body.disabled = Boolean(row && row.disabled);
  return { ...body, ...change };
}

function usageText(usage: UsageProviderRow | undefined): string {
  if (!usage || !usage.calls) {
    return "No calls";
  }
  return (
    count(usage.calls) +
    (usage.calls === 1 ? " call" : " calls") +
    " · " +
    compact(usage.input_tokens) +
    " in / " +
    compact(usage.output_tokens) +
    " out · " +
    statsCostText(usage)
  );
}

/* The name an account is shown with: its label, else its email, else its
   name (REQ-CCT-012). */
export function accountDisplayName(account: ClaudeAccount): string {
  return account.display_name || account.label || account.email || account.name;
}

/* "Signed in as me@work.dev · Pro". */
function signedInText(account: ClaudeAccount | undefined): string {
  if (!account) {
    return "Signed in.";
  }
  return (
    "Signed in" +
    (account.email ? " as " + account.email : "") +
    (account.plan ? " · " + planLabel(account.plan) : "")
  );
}

/* ------------------------------------------------------------------ */
/* Sign-in flow                                                        */
/* ------------------------------------------------------------------ */

/* One sign-in the page follows. command is the terminal fallback. */
interface SignInFlow {
  name: string;
  phase: "running" | "succeeded" | "failed" | "cancelled";
  error: string;
  command: string;
}

/* The inline card of a running, failed, or cancelled sign-in. */
function SignInCard({
  flow,
  onCancel,
  onRetry,
  onDismiss,
}: {
  flow: SignInFlow;
  onCancel: () => void;
  onRetry: () => void;
  onDismiss: () => void;
}) {
  if (flow.phase === "running") {
    return (
      <div className="rt-signin is-running" role="status" aria-label={"Sign-in of " + flow.name}>
        <span className="rt-signin-spin" aria-hidden="true">
          <Ic name="loader-circle" size={16} />
        </span>
        <div className="rt-signin-t">
          <b>Opening Claude Code sign-in in your browser… Finish signing in there.</b>
          <span className="hint">{SIGN_IN_HELP}</span>
        </div>
        <Button size="sm" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    );
  }
  if (flow.phase === "succeeded") {
    return null;
  }
  const failed = flow.phase === "failed";
  return (
    <div className={"rt-signin " + (failed ? "is-failed" : "is-cancelled")} role={failed ? "alert" : "status"} aria-label={"Sign-in of " + flow.name}>
      <Ic name={failed ? "circle-alert" : "circle-slash"} size={16} />
      <div className="rt-signin-t">
        <b>{failed ? "Sign-in did not finish." : "Sign-in cancelled."}</b>
        {failed && flow.error ? <span className="rt-signin-err">{flow.error}</span> : null}
        <details className="rt-signin-alt">
          <summary>Sign in from a terminal instead</summary>
          <p className="hint">For a lana daemon on another machine: run this where the daemon runs.</p>
          <LoginCommand command={flow.command} />
        </details>
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

function LabelEditor({
  account,
  busy,
  onSave,
  onCancel,
}: {
  account: ClaudeAccount;
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
        data-autofocus
        aria-label={"Label of " + account.name}
        value={value}
        disabled={busy}
        autoFocus
        maxLength={LABEL_MAX + 20}
        placeholder={account.email || account.name}
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

function AccountRow({
  account,
  row,
  windows,
  usage,
  busy,
  flow,
  highlighted,
  renaming,
  onToggle,
  onScheduled,
  onSignIn,
  onCancelSignIn,
  onDismissFlow,
  onAskSignOut,
  onAskRemove,
  onRename,
  onSaveLabel,
  onCancelRename,
}: {
  account: ClaudeAccount;
  row: ProviderRow | undefined;
  windows: routerApi.PlanWindow[];
  usage: UsageProviderRow | undefined;
  busy: boolean;
  flow: SignInFlow | undefined;
  highlighted: boolean;
  renaming: boolean;
  onToggle: (enabled: boolean) => void;
  onScheduled: (allowed: boolean) => void;
  onSignIn: () => void;
  onCancelSignIn: () => void;
  onDismissFlow: () => void;
  onAskSignOut: () => void;
  onAskRemove: () => void;
  onRename: () => void;
  onSaveLabel: (label: string) => void;
  onCancelRename: () => void;
}) {
  const enabled = !(row && row.disabled);
  const users = account.agents.map((name) => "agents." + name).concat(account.combos.map((name) => "combos." + name));
  const folder = account.config_dir || (row ? row.config_dir || "" : "");
  const display = accountDisplayName(account);
  const running = Boolean(flow && flow.phase === "running");
  return (
    <li
      className={"rt-acct" + (enabled ? "" : " is-off") + (highlighted ? " is-new" : "")}
      aria-label={"Account " + account.name}
    >
      <div className="rt-acct-head">
        <ProviderLogo logo="claude" name="Claude Code" size={32} />
        <div className="rt-acct-id">
          {renaming ? (
            <LabelEditor account={account} busy={busy} onSave={onSaveLabel} onCancel={onCancelRename} />
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
            {account.email && display !== account.email ? <span className="trunc"> · {account.email}</span> : null}
            {!account.email && !account.logged_in ? <span> · Not signed in yet</span> : null}
            {!account.email && account.logged_in ? <span> · Email not reported</span> : null}
          </span>
        </div>
        <div className="rt-acct-state">
          {account.plan ? <Badge tone="violet">{planLabel(account.plan)}</Badge> : null}
          {account.logged_in ? (
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
          {!account.logged_in && !running ? (
            <Button size="sm" variant="primary" icon="log-in" disabled={busy} onClick={onSignIn}>
              {SIGN_IN_BUTTON}
            </Button>
          ) : null}
          {account.logged_in ? (
            <Button size="sm" variant="ghost" icon="log-out" disabled={busy || running} onClick={onAskSignOut}>
              Sign out
            </Button>
          ) : null}
          <label className="rt-conn-toggle">
            <Toggle aria-label={"Use " + account.name} checked={enabled} disabled={busy || !row} onChange={onToggle} />
            <span className="hint">{enabled ? "On" : "Off"}</span>
          </label>
          {account.builtin ? null : (
            <IconButton icon="trash-2" size="sm" label={"Remove " + account.name} disabled={busy} onClick={onAskRemove} />
          )}
        </div>
      </div>

      {flow && flow.phase === "succeeded" ? (
        <p className="rt-signin-ok" role="status">
          <Ic name="circle-check" size={14} /> {signedInText(account)}
        </p>
      ) : null}
      {flow ? (
        <SignInCard flow={flow} onCancel={onCancelSignIn} onRetry={onSignIn} onDismiss={onDismissFlow} />
      ) : null}
      {!account.logged_in && !flow ? (
        <details className="rt-signin-alt rt-acct-alt">
          <summary>Sign in from a terminal instead</summary>
          <LoginCommand command={account.login_command} />
        </details>
      ) : null}

      <div className="rt-acct-body">
        <PlanWindows windows={windows} />
        <dl className="rt-facts rt-acct-facts">
          <dt>Last 24 h</dt>
          <dd className="num">{usageText(usage)}</dd>
          <dt>Runs</dt>
          <dd className="num">
            {count(account.running)} running · {count(account.queued)} queued
          </dd>
          <dt>Version</dt>
          <dd className="num">
            {account.version || "Unknown"}
            {account.version && !account.version_ok ? (
              <span className="rt-warn">
                <Ic name="triangle-alert" size={12} /> Too old. Update Claude Code.
              </span>
            ) : null}
          </dd>
          <dt>Used by</dt>
          <dd>
            <UsedBy usedBy={users} />
          </dd>
          {folder ? (
            <>
              <dt>Folder</dt>
              <dd className="mono trunc" title={folder}>
                {folder}
              </dd>
            </>
          ) : null}
        </dl>
      </div>

      <div className="rt-acct-foot">
        <label className="rt-acct-sched">
          <Toggle
            aria-label={"Allow scheduled runs for " + account.name}
            checked={Boolean(row && row.allow_scheduled)}
            disabled={busy || !row}
            onChange={onScheduled}
          />
          <span>
            <b>Allow scheduled runs</b>
            <span className="hint">Workflows and the calendar may start turns on this account.</span>
          </span>
        </label>
        {account.error ? (
          <p className="rt-limit-err" role="status">
            <Ic name="circle-alert" size={12} />
            <span>{account.error}</span>
          </p>
        ) : null}
      </div>
    </li>
  );
}

/* ------------------------------------------------------------------ */
/* Confirm dialogs                                                     */
/* ------------------------------------------------------------------ */

function ConfirmDialog({
  title,
  body,
  action,
  busy,
  onCancel,
  onConfirm,
}: {
  title: string;
  body: string;
  action: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal
      title={title}
      size="sm"
      dismissible={!busy}
      onClose={onCancel}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="danger" loading={busy} disabled={busy} onClick={onConfirm}>
            {action}
          </Button>
        </>
      }
    >
      <p className="rt-confirm-body">{body}</p>
    </Modal>
  );
}

/* The sign-out warning: stronger for the built-in account, which is the
   owner's own Claude Code login. */
export function signOutText(account: ClaudeAccount): string {
  if (account.builtin) {
    return (
      "This signs you out of Claude Code on this machine, including the claude you use in a terminal. " +
      "Agents on this account stop until you sign in again."
    );
  }
  return "Agents that use " + account.name + " stop until you sign in again. The account and its folder stay.";
}

/* ------------------------------------------------------------------ */
/* Models                                                              */
/* ------------------------------------------------------------------ */

const SOURCE_LABELS: Record<string, string> = {
  alias: "Alias",
  claude_code: "Claude Code",
  catalog: "Catalog",
  custom: "Custom",
};

function sourceTone(source: string): "accent" | "violet" | "gray" {
  if (source === "claude_code") {
    return "accent";
  }
  if (source === "custom") {
    return "violet";
  }
  return "gray";
}

function ClaudeModelsPanel({
  accounts,
  version,
  onVersion,
}: {
  accounts: ClaudeAccount[];
  version: string;
  onVersion: (version: string, restartRequired: boolean) => void;
}) {
  const [list, setList] = useState<ProviderModels | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [tests, setTests] = useState<Record<string, TestState>>({});
  const [picked, setPicked] = useState("");
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");

  const load = useCallback(async (refresh: boolean) => {
    setLoading(true);
    try {
      const result = await routerApi.listRouterModels(routerApi.CLAUDE_CODE_PROVIDER, refresh);
      setList(result.find((entry) => entry.provider === routerApi.CLAUDE_CODE_PROVIDER) || result[0] || null);
      setError("");
    } catch (failure) {
      setError(failureOf(failure).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(false);
  }, [load]);

  const fallbackAccount =
    (accounts.find((account) => account.logged_in) || accounts[0] || { name: routerApi.CLAUDE_CODE_PROVIDER }).name;
  const account = accounts.some((entry) => entry.name === picked) ? picked : fallbackAccount;
  const models = list ? list.models : [];
  const custom = models.filter((model) => model.source === "custom").map((model) => model.id);

  async function runTest(id: string) {
    const address = account + "/" + id;
    setTests((current) => ({ ...current, [id]: { busy: true } }));
    try {
      const result = await routerApi.testRouterModel(address);
      setTests((current) => ({ ...current, [id]: { busy: false, result } }));
    } catch (failure) {
      setTests((current) => ({ ...current, [id]: { busy: false, error: failureOf(failure).message } }));
    }
  }

  async function saveCustom(next: string[]) {
    if (!version) {
      setSaveError("config.toml is missing. Run lana init first.");
      return false;
    }
    setSaving(true);
    setSaveError("");
    try {
      const result = await routerApi.saveClaudeCodeModels(next, version);
      markSettingsSaved();
      onVersion(result && result.version ? result.version : version, Boolean(result && result.restart_required));
      await load(false);
      return true;
    } catch (failure) {
      setSaveError(
        isStale(failure)
          ? "The config changed since this page loaded. Reload the page and try again."
          : failureOf(failure).message
      );
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function addModel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const id = draft.trim();
    if (!id) {
      return;
    }
    if (/\s/.test(id) || id.startsWith("-")) {
      setSaveError("A model ID is one word and does not start with a dash, for example claude-opus-5-5.");
      return;
    }
    if (models.some((model) => model.id === id)) {
      setSaveError(id + " is already in the list.");
      return;
    }
    if (await saveCustom(custom.concat([id]))) {
      setDraft("");
    }
  }

  return (
    <section className="panel rt-model-list" aria-label="Claude Code models">
      <div className="panel-h">
        <h2>Models</h2>
        <div className="acts">
          {accounts.length > 1 ? (
            <span className="rt-filter">
              <label className="rt-filter-k" htmlFor="rt-cc-test-account">
                Test with
              </label>
              <Select
                id="rt-cc-test-account"
                size="sm"
                value={account}
                options={accounts.map((entry) => ({ value: entry.name, label: entry.name }))}
                onChange={setPicked}
              />
            </span>
          ) : null}
          <Button variant="ghost" size="sm" icon="refresh-cw" loading={loading} disabled={loading} onClick={() => void load(true)}>
            Refresh
          </Button>
        </div>
      </div>
      <div className="panel-b">
        <p className="hint rt-cc-models-lead">
          The same models work with every account as <span className="mono">account/model</span>. Test sends a short
          prompt through Claude Code and uses your plan.
        </p>
        {error ? (
          <p className="rt-limit-err" role="alert">
            <Ic name="circle-alert" size={12} />
            <span>Could not load models: {error}</span>
          </p>
        ) : null}
        {list === null && !error ? <div className="home-empty">Loading models…</div> : null}
        {list && models.length === 0 ? (
          <EmptyState size="sm" icon="boxes" text="No Claude Code models yet. Install Claude Code, or add a model ID below." />
        ) : null}
        {models.length > 0 ? (
          <div className="home-table-wrap">
            <table className="perm-t home-table rt-models-table">
              <thead>
                <tr>
                  <th scope="col">Model</th>
                  <th scope="col" className="is-num">Context</th>
                  <th scope="col">Vision</th>
                  <th scope="col">Thinking</th>
                  <th scope="col">Source</th>
                  <th scope="col">Used by</th>
                  <th scope="col">
                    <span className="sr">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {models.map((model) => (
                  <tr key={model.id}>
                    <td className="rt-model-cell">
                      <span className="rt-model-name trunc">{model.name || model.id}</span>
                      <span className="rt-model-addr mono trunc">{account + "/" + model.id}</span>
                    </td>
                    <td className="is-num num">{model.context_window ? compact(model.context_window) : DASH}</td>
                    <td>{model.vision ? "Yes" : "No"}</td>
                    <td>{model.thinking ? "Yes" : "No"}</td>
                    <td>
                      <Badge tone={sourceTone(model.source)}>{SOURCE_LABELS[model.source] || model.source || DASH}</Badge>
                    </td>
                    <td className="rt-used-cell trunc" title={usedByText(model)}>
                      {usedByText(model) || <span className="faint">{DASH}</span>}
                    </td>
                    <td className="rt-test-cell">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={tests[model.id]?.busy}
                        aria-label={"Test " + account + "/" + model.id}
                        onClick={() => void runTest(model.id)}
                      >
                        Test
                      </Button>
                      {model.source === "custom" ? (
                        <IconButton
                          icon="x"
                          size="sm"
                          label={"Remove " + model.id}
                          disabled={saving}
                          onClick={() => void saveCustom(custom.filter((id) => id !== model.id))}
                        />
                      ) : null}
                      <TestResult state={tests[model.id]} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        <form className="rt-cc-add-model" onSubmit={(event) => void addModel(event)}>
          <Input
            className="mono"
            aria-label="Custom model ID"
            placeholder="claude-opus-5-5"
            value={draft}
            disabled={saving}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => setDraft(event.target.value)}
          />
          <Button type="submit" variant="secondary" size="sm" icon="plus" loading={saving} disabled={saving || !draft.trim()}>
            Add model
          </Button>
          <span className="hint">For a model Claude Code does not list yet.</span>
        </form>
        {saveError ? (
          <p className="rt-limit-err" role="alert">
            <Ic name="circle-alert" size={12} />
            <span>{saveError}</span>
          </p>
        ) : null}
      </div>
    </section>
  );
}


/* ------------------------------------------------------------------ */
/* Page                                                                */
/* ------------------------------------------------------------------ */

export function ClaudeCodeDetail() {
  const { status, accounts, failed, reload: reloadStatus } = useClaudeAccounts();
  const [data, setData] = useState<ListProvidersResponse | null>(null);
  const [version, setVersion] = useState("");
  const [usage, setUsage] = useState<UsageProviderRow[]>([]);
  const [limits, setLimits] = useState<routerApi.LimitSnapshot[]>([]);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [busyRow, setBusyRow] = useState("");
  const [adding, setAdding] = useState(false);
  const [creating, setCreating] = useState(false);
  const [flows, setFlows] = useState<Record<string, SignInFlow>>({});
  const [highlight, setHighlight] = useState("");
  const [renaming, setRenaming] = useState("");
  const [confirm, setConfirm] = useState<{ kind: "sign-out" | "remove"; account: ClaudeAccount } | null>(null);
  const [pollTick, setPollTick] = useState(0);
  const [reloadToken, setReloadToken] = useState(0);
  const reload = useCallback(() => setReloadToken((current) => current + 1), []);

  useEffect(() => {
    let alive = true;
    routerApi
      .listProviders()
      .then((value) => {
        if (alive) {
          setData(value);
          setVersion(value.version);
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          setError(failureOf(failure).message);
        }
      });
    Promise.resolve()
      .then(() => routerApi.getRouterUsage("24h"))
      .then((value) => {
        if (alive && value) {
          setUsage(value.by_provider);
        }
      })
      .catch(() => {});
    Promise.resolve()
      .then(() => routerApi.getRouterLimits())
      .then((value) => {
        if (alive && Array.isArray(value)) {
          setLimits(value);
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  const setFlow = useCallback((name: string, change: Partial<SignInFlow> | null) => {
    setFlows((current) => {
      const next = { ...current };
      if (change === null) {
        delete next[name];
      } else {
        const base: SignInFlow = current[name] || { name, phase: "running", error: "", command: "" };
        next[name] = { ...base, ...change, name };
      }
      return next;
    });
  }, []);

  /* A sign-in ended: on success the accounts reload and the row is
     highlighted with "Signed in as …". */
  const finish = useCallback(
    (name: string, state: ClaudeSignIn) => {
      if (state.state === "succeeded") {
        setFlow(name, { phase: "succeeded", error: "" });
        setHighlight(name);
        reload();
        void reloadStatus();
        return;
      }
      if (state.state === "cancelled") {
        setFlow(name, { phase: "cancelled", error: "" });
        return;
      }
      setFlow(name, {
        phase: "failed",
        error: state.error || (state.state === "idle" ? "The sign-in stopped. Try again." : ""),
      });
    },
    [reload, reloadStatus, setFlow]
  );

  /* Poll every running sign-in each SIGN_IN_POLL_MS. */
  const running = Object.values(flows)
    .filter((flow) => flow.phase === "running")
    .map((flow) => flow.name)
    .sort()
    .join(",");
  useEffect(() => {
    if (!running) {
      return undefined;
    }
    let alive = true;
    const timer = window.setTimeout(() => {
      void (async () => {
        for (const name of running.split(",")) {
          try {
            const state = await routerApi.getClaudeSignIn(name);
            if (!alive) {
              return;
            }
            if (state.state !== "running") {
              finish(name, state);
            }
          } catch (failure) {
            if (!alive) {
              return;
            }
            finish(name, { state: "failed", error: failureOf(failure).message, started_at: null, finished_at: null });
          }
        }
        if (alive) {
          setPollTick((tick) => tick + 1);
        }
      })();
    }, SIGN_IN_POLL_MS);
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [running, pollTick, finish]);

  const rows = useMemo(() => (data ? data.providers.filter((row) => row.kind === CLAUDE_CODE_KIND) : []), [data]);
  const rowOf = (name: string) => rows.find((row) => row.name === name);
  const state = claudeStatus(status, failed);
  const installed = Boolean(status && (status.binary || accounts.some((account) => account.binary)));
  const firstAccount = (accounts.find((account) => account.logged_in) || accounts[0] || { name: routerApi.CLAUDE_CODE_PROVIDER }).name;
  /* Sign-ins of accounts the status does not list yet (just added). */
  const pending = Object.values(flows).filter(
    (flow) => flow.phase !== "succeeded" && !accounts.some((account) => account.name === flow.name)
  );

  function limitOf(name: string): routerApi.LimitSnapshot | null {
    return (
      limits.find((limit) => limit.provider === name) ||
      (name === routerApi.CLAUDE_CODE_PROVIDER
        ? limits.find((limit) => limit.provider === CLAUDE_AGENT_PROVIDER_NAME) || null
        : null)
    );
  }

  function fail(failure: unknown, what: string) {
    if (isStale(failure)) {
      setError("The config changed since this page loaded. Reload the page and try again.");
      setDetails([]);
      reload();
      return;
    }
    const inUse = inUseText(failure, what);
    if (inUse) {
      setError(inUse);
      setDetails([]);
      return;
    }
    const info = failureOf(failure);
    setError(info.message);
    setDetails(info.details);
  }

  function clearNotes() {
    setError("");
    setDetails([]);
    setMessage("");
  }

  async function save(account: ClaudeAccount, change: Partial<ProviderWriteRequest>, done: string) {
    setBusyRow(account.name);
    clearNotes();
    try {
      const result = await routerApi.saveProvider(account.name, accountBody(rowOf(account.name), version, change));
      markSettingsSaved();
      setMessage(done + (result && result.restart_required ? " Restart the daemon for it to take effect." : ""));
      reload();
      void reloadStatus();
    } catch (failure) {
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  /* One click: add the account and start its sign-in. */
  async function addAccount() {
    if (adding) {
      return;
    }
    setAdding(true);
    clearNotes();
    try {
      const result = await routerApi.createClaudeAccount();
      markSettingsSaved();
      if (result.version) {
        setVersion(result.version);
      }
      setHighlight("");
      setFlow(result.name, { phase: "running", error: "", command: result.login_command });
      if (result.sign_in.state !== "running") {
        finish(result.name, result.sign_in);
      }
      if (result.restart_required) {
        setMessage("Account " + result.name + " added. Restart the daemon for agents to use it.");
      }
      reload();
      void reloadStatus();
    } catch (failure) {
      fail(failure, "the account");
    } finally {
      setAdding(false);
    }
  }

  async function signIn(account: { name: string; login_command: string }) {
    clearNotes();
    setHighlight("");
    setFlow(account.name, { phase: "running", error: "", command: account.login_command });
    try {
      const result = await routerApi.startClaudeSignIn(account.name);
      if (result.state !== "running") {
        finish(account.name, result);
      }
    } catch (failure) {
      const info = failureOf(failure);
      if (info.code === "sign_in_running") {
        return;
      }
      setFlow(account.name, { phase: "failed", error: info.message });
    }
  }

  async function cancelSignIn(name: string) {
    try {
      const result = await routerApi.cancelClaudeSignIn(name);
      if (result.state === "succeeded") {
        finish(name, result);
        return;
      }
      setFlow(name, { phase: "cancelled", error: "" });
    } catch (failure) {
      setFlow(name, { phase: "failed", error: failureOf(failure).message });
    }
  }

  async function signOut(account: ClaudeAccount) {
    setBusyRow(account.name);
    clearNotes();
    try {
      await routerApi.signOutClaudeAccount(account.name, account.builtin);
      setFlow(account.name, null);
      setConfirm(null);
      setMessage(accountDisplayName(account) + " is signed out.");
      void reloadStatus();
    } catch (failure) {
      setConfirm(null);
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  async function remove(account: ClaudeAccount) {
    setBusyRow(account.name);
    clearNotes();
    try {
      const result = await routerApi.deleteProvider(account.name, version, { removeFolder: true });
      markSettingsSaved();
      setFlow(account.name, null);
      setConfirm(null);
      setMessage(
        (result && result.message) || "Account " + account.name + " removed. It was signed out and its folder deleted."
      );
      reload();
      void reloadStatus();
    } catch (failure) {
      setConfirm(null);
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  async function saveLabel(account: ClaudeAccount, label: string) {
    setBusyRow(account.name);
    clearNotes();
    try {
      const result = await routerApi.setClaudeAccountLabel(account.name, label, version);
      markSettingsSaved();
      if (result && result.version) {
        setVersion(result.version);
      }
      setRenaming("");
      setMessage(label ? account.name + " is now shown as " + label + "." : account.name + " has no label now.");
      reload();
      void reloadStatus();
    } catch (failure) {
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  return (
    <div className="rt-detail-page">
      <BackLink />
      <header className="rt-dh">
        <ProviderLogo logo="claude" name="Claude Code" size={52} />
        <div className="rt-dh-t">
          <h1>Claude Code</h1>
          <p className="rt-dh-sub">
            Your Claude plan, through the claude program on this machine. Agents pick its models like any other model;
            the turn runs in Claude Code with the agent&apos;s persona, memory, and lana tools.
          </p>
          <div className="rt-dh-status">
            <Badge tone={state.tone} dot={state.tone !== "gray"}>
              {state.label}
            </Badge>
          </div>
        </div>
        <div className="rt-dh-acts">
          <Button variant="secondary" size="sm" icon="bot" onClick={() => setCreating(true)}>
            Create agent
          </Button>
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            loading={adding}
            disabled={!version || adding}
            onClick={() => void addAccount()}
          >
            Add account
          </Button>
        </div>
      </header>

      <SettingsRestartBanner restartRequired={Boolean(data && data.restart_required)} onRestarted={reload} />
      <StatusLine text={message} />
      <Alert tone="danger" message={error} details={details} />
      {status && !installed ? (
        <p className="rt-claude-login">
          Claude Code is not on this machine&apos;s PATH. Install it, then add or sign in to an account.
        </p>
      ) : null}

      <Panel title="Accounts" className="rt-conns-panel rt-accts-panel">
        <p className="hint rt-signin-help">
          <Ic name="shield-check" size={12} /> {SIGN_IN_HELP}
        </p>
        {pending.map((flow) => (
          <SignInCard
            key={flow.name}
            flow={flow}
            onCancel={() => void cancelSignIn(flow.name)}
            onRetry={() => void signIn({ name: flow.name, login_command: flow.command })}
            onDismiss={() => setFlow(flow.name, null)}
          />
        ))}
        {failed && !status ? <p className="rt-limit-err">Could not read the Claude Code status.</p> : null}
        {!status && !failed ? <div className="home-empty">Checking Claude Code…</div> : null}
        {status && accounts.length === 0 ? (
          <EmptyState size="sm" icon="user-plus" text="No Claude Code account yet. Add one to use your Claude plan." />
        ) : null}
        {accounts.length > 0 ? (
          <ul className="rt-accts">
            {accounts.map((account) => (
              <AccountRow
                key={account.name}
                account={account}
                row={rowOf(account.name)}
                windows={accountWindows(account, limitOf(account.name))}
                usage={usage.find((entry) => entry.provider === account.name)}
                busy={busyRow === account.name}
                flow={flows[account.name]}
                highlighted={highlight === account.name}
                renaming={renaming === account.name}
                onToggle={(enabled) =>
                  void save(
                    account,
                    { disabled: !enabled },
                    enabled ? account.name + " is on." : account.name + " is off. The router skips it."
                  )
                }
                onScheduled={(allowed) =>
                  void save(
                    account,
                    { allow_scheduled: allowed },
                    allowed
                      ? "Scheduled runs may use " + account.name + "."
                      : "Scheduled runs no longer use " + account.name + "."
                  )
                }
                onSignIn={() => void signIn(account)}
                onCancelSignIn={() => void cancelSignIn(account.name)}
                onDismissFlow={() => setFlow(account.name, null)}
                onAskSignOut={() => setConfirm({ kind: "sign-out", account })}
                onAskRemove={() => setConfirm({ kind: "remove", account })}
                onRename={() => setRenaming(account.name)}
                onSaveLabel={(label) => void saveLabel(account, label)}
                onCancelRename={() => setRenaming("")}
              />
            ))}
          </ul>
        ) : null}
        <p className="hint rt-conn-note">{ACCOUNTS_TERMS_NOTE}</p>
      </Panel>

      <ClaudeModelsPanel
        accounts={accounts}
        version={version}
        onVersion={(next, restartRequired) => {
          setVersion(next);
          setMessage(restartRequired ? "Models saved. Restart the daemon for it to take effect." : "Models saved.");
        }}
      />

      {confirm && confirm.kind === "sign-out" ? (
        <ConfirmDialog
          title={
            confirm.account.builtin
              ? "Sign out of your own Claude Code?"
              : "Sign out " + accountDisplayName(confirm.account) + "?"
          }
          body={signOutText(confirm.account)}
          action="Sign out"
          busy={busyRow === confirm.account.name}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void signOut(confirm.account)}
        />
      ) : null}
      {confirm && confirm.kind === "remove" ? (
        <ConfirmDialog
          title={"Remove " + accountDisplayName(confirm.account) + "?"}
          body={
            "Signs this account out and deletes its folder" +
            (confirm.account.config_dir ? " " + confirm.account.config_dir : "") +
            ". Agents and combos that use " +
            confirm.account.name +
            " must be changed first."
          }
          action="Remove"
          busy={busyRow === confirm.account.name}
          onCancel={() => setConfirm(null)}
          onConfirm={() => void remove(confirm.account)}
        />
      ) : null}

      {creating ? (
        <CreateAgentDialog
          initialModel={firstAccount + "/opus"}
          onClose={() => setCreating(false)}
          onCreated={(name) => {
            window.location.hash = "#/agents/" + encodeURIComponent(name);
          }}
        />
      ) : null}
    </div>
  );
}
