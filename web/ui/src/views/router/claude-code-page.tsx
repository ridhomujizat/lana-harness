import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import * as routerApi from "../../api/router";
import type {
  ClaudeAccount,
  ListProvidersResponse,
  ProviderModels,
  ProviderRow,
  ProviderWriteRequest,
  UsageProviderRow,
} from "../../api/router";
import { Badge, Button, EmptyState, Field, IconButton, Input, Modal, Panel, Select, Toggle } from "../../ui/gr8r";
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
   revision 2 (REQ-CCT-009..011): the Claude Code accounts as connections,
   each with its email, plan, windows, and usage; then the Claude Code models
   with custom IDs. lana never logs an account in: a logged-out account shows
   the command to run once in a terminal. */

const NAME_RE = /^[a-z0-9-]+$/;

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

/* ------------------------------------------------------------------ */
/* Account row                                                         */
/* ------------------------------------------------------------------ */

function AccountRow({
  account,
  row,
  windows,
  usage,
  busy,
  confirming,
  onToggle,
  onScheduled,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  account: ClaudeAccount;
  row: ProviderRow | undefined;
  windows: routerApi.PlanWindow[];
  usage: UsageProviderRow | undefined;
  busy: boolean;
  confirming: boolean;
  onToggle: (enabled: boolean) => void;
  onScheduled: (allowed: boolean) => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const enabled = !(row && row.disabled);
  const users = account.agents.map((name) => "agents." + name).concat(account.combos.map((name) => "combos." + name));
  const folder = account.config_dir || (row ? row.config_dir || "" : "");
  return (
    <li className={"rt-acct" + (enabled ? "" : " is-off")} aria-label={"Account " + account.name}>
      <div className="rt-acct-head">
        <ProviderLogo logo="claude" name="Claude Code" size={32} />
        <div className="rt-acct-id">
          <span className="rt-acct-name">
            <span className="mono">{account.name}</span>
            {account.builtin ? <Badge>Built in</Badge> : null}
          </span>
          <span className="rt-acct-email trunc">
            {account.email || (account.logged_in ? "Email not reported" : "No account yet")}
          </span>
        </div>
        <div className="rt-acct-state">
          {account.plan ? <Badge tone="violet">{planLabel(account.plan)}</Badge> : null}
          {account.logged_in ? (
            <Badge tone="green" dot>
              Logged in
            </Badge>
          ) : (
            <Badge tone="amber" dot>
              Not logged in
            </Badge>
          )}
        </div>
        <div className="rt-acct-acts">
          {confirming ? (
            <>
              <span className="hint">Delete {account.name}? Its folder stays.</span>
              <Button size="sm" variant="ghost" disabled={busy} onClick={onCancelDelete}>
                Keep
              </Button>
              <Button size="sm" variant="danger" loading={busy} disabled={busy} onClick={onDelete}>
                Delete
              </Button>
            </>
          ) : (
            <>
              <label className="rt-conn-toggle">
                <Toggle
                  aria-label={"Use " + account.name}
                  checked={enabled}
                  disabled={busy || !row}
                  onChange={onToggle}
                />
                <span className="hint">{enabled ? "On" : "Off"}</span>
              </label>
              {account.builtin ? null : (
                <IconButton
                  icon="trash-2"
                  size="sm"
                  label={"Delete " + account.name}
                  disabled={busy}
                  onClick={onAskDelete}
                />
              )}
            </>
          )}
        </div>
      </div>

      {!account.logged_in ? <LoginCommand command={account.login_command} /> : null}

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
/* Add account                                                         */
/* ------------------------------------------------------------------ */

function AddAccountDialog({
  version,
  takenNames,
  onClose,
  onSaved,
  onCheck,
}: {
  version: string;
  takenNames: string[];
  onClose: () => void;
  onSaved: (name: string) => void;
  onCheck: (name: string) => Promise<ClaudeAccount | null>;
}) {
  const [name, setName] = useState("");
  const [folder, setFolder] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [saved, setSaved] = useState<{ name: string; command: string } | null>(null);
  const [checking, setChecking] = useState(false);
  const [checked, setChecked] = useState<{ account: ClaudeAccount | null } | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const value = name.trim();
    if (!NAME_RE.test(value)) {
      setError("Name the account with lowercase letters, digits, and dashes, for example claude-work.");
      setDetails([]);
      return;
    }
    if (takenNames.includes(value)) {
      setError("A provider named " + value + " already exists. Pick another name.");
      setDetails([]);
      return;
    }
    if (!version) {
      setError("config.toml is missing. Run lana init before adding accounts.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      const body: ProviderWriteRequest = { kind: CLAUDE_CODE_KIND, expected_version: version };
      if (folder.trim()) {
        body.config_dir = folder.trim();
      }
      const result = await routerApi.saveProvider(value, body);
      markSettingsSaved();
      const command =
        (result && result.login_command) ||
        (folder.trim() ? "CLAUDE_CONFIG_DIR='" + folder.trim() + "' claude" : "claude");
      setSaved({ name: value, command });
      onSaved(value);
    } catch (failure) {
      if (isStale(failure)) {
        setError("The config changed since this page loaded. Close the dialog and reload the page before saving.");
        setDetails([]);
      } else {
        const info = failureOf(failure);
        setError(info.message);
        setDetails(info.details);
      }
    } finally {
      setBusy(false);
    }
  }

  async function check() {
    if (!saved) {
      return;
    }
    setChecking(true);
    try {
      setChecked({ account: await onCheck(saved.name) });
    } finally {
      setChecking(false);
    }
  }

  const loggedIn = Boolean(checked && checked.account && checked.account.logged_in);

  return (
    <Modal
      title={
        <span className="rt-dialog-title">
          <ProviderLogo logo="claude" name="Claude Code" size={24} />
          <span>{saved ? "Log in to " + saved.name : "Add Claude Code account"}</span>
        </span>
      }
      subtitle={saved ? undefined : "Each account has its own folder and its own Claude login."}
      dismissible={!busy}
      onClose={onClose}
      footer={
        saved ? (
          <>
            <span className="sp" />
            {loggedIn ? null : (
              <Button variant="secondary" icon="refresh-cw" loading={checking} disabled={checking} onClick={() => void check()}>
                Check again
              </Button>
            )}
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </>
        ) : (
          <>
            <span className="sp" />
            <Button variant="secondary" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" form="rt-cc-account-form" variant="primary" disabled={busy} loading={busy}>
              {busy ? "Adding…" : "Add account"}
            </Button>
          </>
        )
      }
    >
      {saved ? (
        <div className="rt-cc-login-step">
          <p>
            Account <b className="mono">{saved.name}</b> is added. Log in once with the Claude account you want it to
            use.
          </p>
          <LoginCommand command={saved.command} />
          {checked ? (
            loggedIn ? (
              <p className="rt-test ok" role="status">
                <Ic name="circle-check" size={12} /> Logged in
                {checked.account && checked.account.email ? " as " + checked.account.email : ""}
                {checked.account && checked.account.plan ? " · " + planLabel(checked.account.plan) : ""}
              </p>
            ) : (
              <p className="rt-test bad" role="status">
                <Ic name="circle-alert" size={12} /> Not logged in yet. Run the command, type /login, then check again.
              </p>
            )
          ) : null}
        </div>
      ) : (
        <form id="rt-cc-account-form" className="settings-dialog-form" onSubmit={submit}>
          <Field label="Name" hint="Agents and combos call its models as name/model, for example claude-work/opus.">
            <Input
              data-autofocus
              name="account-name"
              className="mono"
              value={name}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              placeholder="claude-work"
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
          <Field label="Folder (optional)" hint="Where this account keeps its Claude Code login. Leave empty for a new folder in lana's home.">
            <Input
              name="account-folder"
              className="mono"
              value={folder}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              placeholder="~/.lana/claude/claude-work"
              onChange={(event) => setFolder(event.target.value)}
            />
          </Field>
          <Alert tone="danger" message={error} details={details} />
        </form>
      )}
      <p className="hint rt-cc-terms">{ACCOUNTS_TERMS_NOTE}</p>
    </Modal>
  );
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
  const [confirm, setConfirm] = useState("");
  const [adding, setAdding] = useState(false);
  const [creating, setCreating] = useState(false);
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

  const rows = useMemo(() => (data ? data.providers.filter((row) => row.kind === CLAUDE_CODE_KIND) : []), [data]);
  const rowOf = (name: string) => rows.find((row) => row.name === name);
  const state = claudeStatus(status, failed);
  const installed = Boolean(status && (status.binary || accounts.some((account) => account.binary)));
  const firstAccount = (accounts.find((account) => account.logged_in) || accounts[0] || { name: routerApi.CLAUDE_CODE_PROVIDER }).name;

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

  async function save(account: ClaudeAccount, change: Partial<ProviderWriteRequest>, done: string) {
    setBusyRow(account.name);
    setError("");
    setMessage("");
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

  async function remove(account: ClaudeAccount) {
    setBusyRow(account.name);
    setError("");
    setMessage("");
    try {
      const result = await routerApi.deleteProvider(account.name, version);
      markSettingsSaved();
      const folder = (result && result.config_dir) || account.config_dir;
      setMessage(
        "Account " + account.name + " deleted." + (folder ? " Its folder stays at " + folder + "." : "")
      );
      setConfirm("");
      reload();
      void reloadStatus();
    } catch (failure) {
      setConfirm("");
      fail(failure, account.name);
    } finally {
      setBusyRow("");
    }
  }

  async function checkAccount(name: string): Promise<ClaudeAccount | null> {
    const value = await reloadStatus();
    return value ? value.accounts.find((account) => account.name === name) || null : null;
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
          <Button variant="primary" size="sm" icon="plus" disabled={!version} onClick={() => setAdding(true)}>
            Add account
          </Button>
        </div>
      </header>

      <SettingsRestartBanner restartRequired={Boolean(data && data.restart_required)} onRestarted={reload} />
      <StatusLine text={message} />
      <Alert tone="danger" message={error} details={details} />
      {status && !installed ? (
        <p className="rt-claude-login">
          Claude Code is not on this machine&apos;s PATH. Install it, then add or log in to an account.
        </p>
      ) : null}

      <Panel title="Accounts" className="rt-conns-panel rt-accts-panel">
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
                confirming={confirm === account.name}
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
                onAskDelete={() => setConfirm(account.name)}
                onCancelDelete={() => setConfirm("")}
                onDelete={() => void remove(account)}
              />
            ))}
          </ul>
        ) : null}
        {accounts.length > 1 ? <p className="hint rt-conn-note">{ACCOUNTS_TERMS_NOTE}</p> : null}
      </Panel>

      <ClaudeModelsPanel
        accounts={accounts}
        version={version}
        onVersion={(next, restartRequired) => {
          setVersion(next);
          setMessage(restartRequired ? "Models saved. Restart the daemon for it to take effect." : "Models saved.");
        }}
      />

      {adding ? (
        <AddAccountDialog
          version={version}
          takenNames={data ? data.providers.map((row) => row.name) : []}
          onClose={() => setAdding(false)}
          onSaved={(name) => {
            setMessage("Account " + name + " added. Log in once to use it.");
            reload();
            void reloadStatus();
          }}
          onCheck={checkAccount}
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
