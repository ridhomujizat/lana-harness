import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import * as routerApi from "../../api/router";
import type {
  LimitSnapshot,
  ListProvidersResponse,
  ProviderRow,
  ProviderTestRequest,
  UsageProviderRow,
} from "../../api/router";
import { Badge, Button, EmptyState, ErrorState, IconButton, Input, Panel, Toggle } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { Alert, SettingsRestartBanner, StatusLine, markSettingsSaved } from "../settings-pages";
import { ClaudeCodeDetail } from "./claude-code-page";
import { AntigravityDetail } from "./antigravity-page";
import { ConnectionDialog } from "./connection-dialog";
import { AccountOrderList, POOL_TERMS_NOTE } from "./account-order";
import { LimitCard } from "./limits";
import { ProviderModelsPanel } from "./models";
import { ANTIGRAVITY_ID, CLAUDE_CODE_ID, cardIdFor, presetById } from "./presets";
import { ProviderLogo } from "./provider-logo";
import { StatusPills, UsedBy, cardStatus, keyMissing, keyState } from "./providers";
import { compact, count, failureOf, inUseText, isStale, ms, percent, statsCostText } from "./router-common";

/* One provider's page (#/router/providers/<preset or name>): its connections
   — each one [providers.<name>] entry with its own key — then its models, a
   24-hour usage strip, and its quota. Claude Code and Antigravity have their
   own pages. */

export function ProviderDetail({ id }: { id: string }) {
  if (id === CLAUDE_CODE_ID) {
    return <ClaudeCodeDetail />;
  }
  if (id === ANTIGRAVITY_ID) {
    return <AntigravityDetail />;
  }
  return <ApiProviderDetail id={id} />;
}

function BackLink() {
  return (
    <a className="rt-back" href="#/router/providers">
      <Ic name="arrow-left" size={14} />
      Providers
    </a>
  );
}

/* ------------------------------------------------------------------ */
/* Usage strip                                                         */
/* ------------------------------------------------------------------ */

function sumRows(rows: UsageProviderRow[]) {
  const total = {
    calls: 0,
    errors: 0,
    input_tokens: 0,
    output_tokens: 0,
    cost_usd: 0 as number | null,
    cost_partial: false,
    known: false,
  };
  for (const row of rows) {
    total.calls += Number(row.calls) || 0;
    total.errors += Number(row.errors) || 0;
    total.input_tokens += Number(row.input_tokens) || 0;
    total.output_tokens += Number(row.output_tokens) || 0;
    if (row.cost_usd !== null && row.cost_usd !== undefined) {
      total.cost_usd = (total.cost_usd || 0) + Number(row.cost_usd);
      total.known = true;
    }
    total.cost_partial = total.cost_partial || Boolean(row.cost_partial);
  }
  if (!total.known && rows.length > 0) {
    total.cost_usd = null;
  }
  return total;
}

export function UsageStrip({ names }: { names: string[] }) {
  const [rows, setRows] = useState<UsageProviderRow[] | null>(null);
  const [error, setError] = useState("");
  const key = names.join(",");

  useEffect(() => {
    let alive = true;
    const wanted = key ? key.split(",") : [];
    routerApi
      .getRouterUsage("24h")
      .then((usage) => {
        if (alive) {
          setRows(usage.by_provider.filter((row) => wanted.includes(row.provider)));
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          setError(failureOf(failure).message);
        }
      });
    return () => {
      alive = false;
    };
  }, [key]);

  const total = sumRows(rows || []);
  return (
    <Panel
      title="Usage (24h)"
      className="rt-strip-panel"
      actions={
        <a className="hint" href="#/router/usage">
          All usage
        </a>
      }
    >
      {error ? <p className="rt-limit-err">{error}</p> : null}
      {rows === null && !error ? <div className="home-empty">Loading usage…</div> : null}
      {rows !== null ? (
        <dl className="rt-strip" aria-label="Usage in the last 24 hours">
          <div>
            <dt>Calls</dt>
            <dd className="num">{count(total.calls)}</dd>
          </div>
          <div>
            <dt>Errors</dt>
            <dd className={"num" + (total.errors ? " rt-bad" : "")}>
              {count(total.errors)}
              <span className="rt-strip-sub"> {percent(total.errors, total.calls)}</span>
            </dd>
          </div>
          <div>
            <dt>Tokens in / out</dt>
            <dd className="num">
              {compact(total.input_tokens)} / {compact(total.output_tokens)}
            </dd>
          </div>
          <div>
            <dt>Cost</dt>
            <dd className="num">{statsCostText(total)}</dd>
          </div>
        </dl>
      ) : null}
    </Panel>
  );
}

/* ------------------------------------------------------------------ */
/* Quota                                                               */
/* ------------------------------------------------------------------ */

function useLimits(names: string[]): { limits: LimitSnapshot[] | null; error: string } {
  const [limits, setLimits] = useState<LimitSnapshot[] | null>(null);
  const [error, setError] = useState("");
  const key = names.join(",");
  useEffect(() => {
    let alive = true;
    const wanted = key ? key.split(",") : [];
    routerApi
      .getRouterLimits()
      .then((all) => {
        if (alive) {
          setLimits(all.filter((limit) => wanted.includes(limit.provider)));
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          setError(failureOf(failure).message);
        }
      });
    return () => {
      alive = false;
    };
  }, [key]);
  return { limits, error };
}

/* ------------------------------------------------------------------ */
/* Keys (spec 75 REQ-PAF-005/006)                                      */
/* ------------------------------------------------------------------ */

const SECRET_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

/* A connection's keys in use order. An older daemon sends no keys; the
   connection then has its one api_key_env. */
export function connectionKeys(row: ProviderRow): routerApi.ProviderKey[] {
  if (Array.isArray(row.keys) && row.keys.length > 0) {
    return row.keys;
  }
  if (!row.api_key_env) {
    return [];
  }
  return [{ name: row.api_key_env, present: !keyMissing(row), cooling_until: null }];
}

/* The keys of one connection: drag to change which key is used first, add a
   backup key by its secret name. Secret values never show here. */
export function KeysPanel({
  row,
  title,
  version,
  onSaved,
}: {
  row: ProviderRow;
  title: string;
  version: string;
  onSaved: () => void;
}) {
  const keys = connectionKeys(row);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function save(names: string[]) {
    await routerApi.saveProviderKeys(row.name, names, version || undefined);
    markSettingsSaved();
    onSaved();
  }

  async function write(names: string[], done: () => void) {
    setBusy(true);
    setError("");
    try {
      await save(names);
      done();
    } catch (failure) {
      setError(
        isStale(failure)
          ? "The config changed since this page loaded. Reload the page and try again."
          : failureOf(failure).message
      );
    } finally {
      setBusy(false);
    }
  }

  function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = draft.trim();
    if (!name) {
      return;
    }
    if (!SECRET_NAME.test(name)) {
      setError("A secret name has letters, digits, and _ only, for example " + (row.api_key_env || "OPENAI_API_KEY") + "_2.");
      return;
    }
    if (keys.some((key) => key.name === name)) {
      setError(name + " is already a key of " + row.name + ".");
      return;
    }
    void write(
      keys.map((key) => key.name).concat([name]),
      () => setDraft("")
    );
  }

  return (
    <Panel title={title} className="rt-keys-panel">
      <AccountOrderList
        variant="key"
        label={"Keys of " + row.name}
        disabled={busy}
        items={keys.map((key) => ({
          id: key.name,
          label: key.name,
          mono: true,
          coolingUntil: key.cooling_until,
          badges: key.present ? (
            <Badge tone="green" dot>
              Present
            </Badge>
          ) : (
            <Badge tone="amber" dot>
              Missing
            </Badge>
          ),
        }))}
        onSave={(order) => save(order)}
        actions={(item) =>
          keys.length > 1 ? (
            <IconButton
              icon="x"
              size="sm"
              label={"Remove key " + item.id}
              disabled={busy}
              onClick={() => void write(keys.map((key) => key.name).filter((name) => name !== item.id), () => {})}
            />
          ) : null
        }
      />
      <form className="rt-keys-add" onSubmit={add}>
        <Input
          className="mono"
          aria-label={"Backup key secret name for " + row.name}
          placeholder={(row.api_key_env || "MY_PROVIDER_API_KEY") + "_2"}
          value={draft}
          disabled={busy}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setDraft(event.target.value)}
        />
        <Button type="submit" variant="secondary" size="sm" icon="plus" loading={busy} disabled={busy || !draft.trim()}>
          Add backup key
        </Button>
        <span className="hint">
          The name of a secret in <a href="#/settings/secrets">Secrets</a>. The router moves to the next key when one is
          out of credit or rate limited.
        </span>
      </form>
      {error ? (
        <p className="rt-limit-err" role="alert">
          <Ic name="circle-alert" size={12} />
          <span>{error}</span>
        </p>
      ) : null}
    </Panel>
  );
}

/* ------------------------------------------------------------------ */
/* API key provider                                                    */
/* ------------------------------------------------------------------ */

type RowTest = { busy: boolean; ok?: boolean; text?: string };

interface ConnectionHandlers {
  busy: boolean;
  test: RowTest | undefined;
  confirming: boolean;
  onToggle: (enabled: boolean) => void;
  onTest: () => void;
  onEdit: () => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}

/* The key and base URL line of a connection. */
function ConnectionMeta({ row, defaultBase }: { row: ProviderRow; defaultBase: string }) {
  const key = keyState(row);
  const base = row.base_url && row.base_url !== defaultBase ? row.base_url : "";
  return (
    <span className="rt-conn-meta">
      {row.api_key_env ? <code className="mono">{row.api_key_env}</code> : null}
      <Badge tone={key.tone} dot>
        {key.label}
      </Badge>
      {key.note ? <span className="hint">{key.note}</span> : null}
      {base ? <span className="mono trunc rt-conn-base">{base}</span> : null}
    </span>
  );
}

/* The last connection test of a row. */
function ConnectionTest({ test }: { test: RowTest | undefined }) {
  if (!test || test.busy || !test.text) {
    return null;
  }
  return (
    <span className={"rt-test " + (test.ok ? "ok" : "bad")} role="status">
      <Ic name={test.ok ? "circle-check" : "circle-alert"} size={12} /> {test.text}
    </span>
  );
}

/* The buttons of a connection: on/off, Test, Edit, Delete, or the delete
   confirm. */
function ConnectionActions({ row, handlers }: { row: ProviderRow; handlers: ConnectionHandlers }) {
  const { busy, test, confirming } = handlers;
  const enabled = !row.disabled;
  if (confirming) {
    return (
      <>
        <span className="hint">Delete {row.name}?</span>
        <Button size="sm" variant="ghost" disabled={busy} onClick={handlers.onCancelDelete}>
          Keep
        </Button>
        <Button size="sm" variant="danger" loading={busy} disabled={busy} onClick={handlers.onDelete}>
          Delete
        </Button>
      </>
    );
  }
  return (
    <>
      <label className="rt-conn-toggle">
        <Toggle aria-label={"Use " + row.name} checked={enabled} disabled={busy} onChange={handlers.onToggle} />
        <span className="hint">{enabled ? "On" : "Off"}</span>
      </label>
      <Button
        size="sm"
        variant="ghost"
        icon="plug"
        loading={Boolean(test && test.busy)}
        disabled={busy || Boolean(test && test.busy)}
        aria-label={"Test " + row.name}
        onClick={handlers.onTest}
      >
        Test
      </Button>
      <IconButton icon="pencil" size="sm" label={"Edit " + row.name} disabled={busy} onClick={handlers.onEdit} />
      <IconButton icon="trash-2" size="sm" label={"Delete " + row.name} disabled={busy} onClick={handlers.onAskDelete} />
    </>
  );
}

function ConnectionRow({
  row,
  defaultBase,
  handlers,
}: {
  row: ProviderRow;
  defaultBase: string;
  handlers: ConnectionHandlers;
}) {
  const enabled = !row.disabled;
  return (
    <li className={"rt-conn" + (enabled ? "" : " is-off")} aria-label={"Connection " + row.name}>
      <div className="rt-conn-main">
        <span className="rt-conn-name mono">{row.name}</span>
        <ConnectionMeta row={row} defaultBase={defaultBase} />
        <span className="rt-conn-users">
          <UsedBy usedBy={row.used_by} />
        </span>
        <ConnectionTest test={handlers.test} />
      </div>
      <div className="rt-conn-acts">
        <ConnectionActions row={row} handlers={handlers} />
      </div>
    </li>
  );
}

/* Spec 80 (REQ-ACP-005): the note under the connections of one pool. */
export const CONNECTION_POOL_NOTE =
  "The top connection is used first. Lana moves to the next one when a connection runs out of credit, hits its rate limit, or its key is rejected. Turn off Use as backup to keep a connection for agents set to it.";

/* The connections form one ordered pool when there are two or more and all
   of them share one pool group (spec 80 REQ-ACP-005). A page whose
   connections are in different groups, or a daemon that sends no group,
   keeps the plain rows. */
export function connectionsPooled(connections: ProviderRow[]): boolean {
  if (connections.length < 2) {
    return false;
  }
  const group = connections[0] ? connections[0].pool_group : "";
  return Boolean(group) && connections.every((row) => row.pool_group === group);
}

/* Pool order: priority (default 100), then name, as the router sorts. */
function poolOrder(connections: ProviderRow[]): ProviderRow[] {
  const priority = (row: ProviderRow) => (typeof row.priority === "number" ? row.priority : 100);
  return connections.slice().sort((a, b) => priority(a) - priority(b) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

function ApiProviderDetail({ id }: { id: string }) {
  const preset = presetById(id);
  const [data, setData] = useState<ListProvidersResponse | null>(null);
  const [loadError, setLoadError] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [dialog, setDialog] = useState<{ connection: ProviderRow | null } | null>(null);
  const [busyRow, setBusyRow] = useState("");
  const [confirm, setConfirm] = useState("");
  const [tests, setTests] = useState<Record<string, RowTest>>({});
  const [reloadToken, setReloadToken] = useState(0);
  const reload = useCallback(() => setReloadToken((current) => current + 1), []);

  useEffect(() => {
    let alive = true;
    routerApi
      .listProviders()
      .then((value) => {
        if (alive) {
          setData(value);
          setLoadError("");
        }
      })
      .catch((failure: unknown) => {
        if (alive) {
          setLoadError(failureOf(failure).message);
        }
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  const providers = useMemo(() => (data ? data.providers : []), [data]);
  const connections = useMemo(() => providers.filter((row) => cardIdFor(row) === id), [providers, id]);
  const names = useMemo(() => connections.map((row) => row.name), [connections]);
  const { limits } = useLimits(names);
  const version = data ? data.version : "";
  const label = preset ? preset.label : id;
  const status = cardStatus(connections);
  const pooled = connectionsPooled(connections);

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

  async function toggle(row: ProviderRow, enabled: boolean) {
    setBusyRow(row.name);
    setError("");
    setMessage("");
    try {
      const body = {
        kind: row.kind,
        disabled: !enabled,
        expected_version: version,
      } as routerApi.ProviderWriteRequest;
      if (row.base_url) {
        body.base_url = row.base_url;
      }
      if (row.api_key_env) {
        body.api_key_env = row.api_key_env;
      }
      const result = await routerApi.saveProvider(row.name, body);
      markSettingsSaved();
      setMessage(
        (enabled ? row.name + " is on." : row.name + " is off. The router skips it.") +
          (result && result.restart_required ? " Restart the daemon for it to take effect." : "")
      );
      reload();
    } catch (failure) {
      fail(failure, row.name);
    } finally {
      setBusyRow("");
    }
  }

  async function remove(row: ProviderRow) {
    setBusyRow(row.name);
    setError("");
    setMessage("");
    try {
      await routerApi.deleteProvider(row.name, version);
      markSettingsSaved();
      setMessage("Connection " + row.name + " deleted.");
      setConfirm("");
      reload();
    } catch (failure) {
      setConfirm("");
      fail(failure, row.name);
    } finally {
      setBusyRow("");
    }
  }

  /* Spec 80 (REQ-ACP-003): a drop saves the pool order at once. */
  async function saveOrder(order: string[], unpooled: string[]) {
    await routerApi.savePoolOrder("api", order, unpooled, version || undefined);
    markSettingsSaved();
    reload();
  }

  function handlersFor(row: ProviderRow): ConnectionHandlers {
    return {
      busy: busyRow === row.name,
      test: tests[row.name],
      confirming: confirm === row.name,
      onToggle: (enabled) => void toggle(row, enabled),
      onTest: () => void testRow(row),
      onEdit: () => setDialog({ connection: row }),
      onAskDelete: () => setConfirm(row.name),
      onCancelDelete: () => setConfirm(""),
      onDelete: () => void remove(row),
    };
  }

  async function testRow(row: ProviderRow) {
    setTests((current) => ({ ...current, [row.name]: { busy: true } }));
    const body: ProviderTestRequest = { name: row.name, kind: row.kind };
    if (row.base_url) {
      body.base_url = row.base_url;
    }
    if (row.api_key_env) {
      body.api_key_env = row.api_key_env;
    }
    try {
      const result = await routerApi.testProvider(body);
      if (result && result.ok) {
        setTests((current) => ({ ...current, [row.name]: { busy: false, ok: true, text: "Connected · " + ms(result.latency_ms) } }));
      } else {
        let text = (result && result.message) || "The provider did not answer.";
        if (result && Number(result.status) > 0) {
          text += " (HTTP " + result.status + ")";
        }
        setTests((current) => ({ ...current, [row.name]: { busy: false, ok: false, text } }));
      }
    } catch (failure) {
      setTests((current) => ({ ...current, [row.name]: { busy: false, ok: false, text: failureOf(failure).message } }));
    }
  }

  if (!data && !loadError) {
    return (
      <>
        <BackLink />
        <div className="home-empty">Loading {label}…</div>
      </>
    );
  }
  if (loadError) {
    return (
      <>
        <BackLink />
        <div className="home-alert" role="alert">
          <ErrorState
            icon="cloud-alert"
            title={"Could not load " + label}
            text={loadError}
            action={
              <Button variant="secondary" icon="refresh-cw" onClick={reload}>
                Retry
              </Button>
            }
          />
        </div>
      </>
    );
  }
  if (!preset && connections.length === 0) {
    return (
      <>
        <BackLink />
        <div className="panel">
          <EmptyState
            icon="search-x"
            title={"No provider named " + id}
            text="It may have been deleted. Go back to Providers to pick another."
          />
        </div>
      </>
    );
  }

  const custom = !preset;
  const defaultBase = (row: ProviderRow) =>
    row.default_base_url || (preset && preset.kind === row.kind ? preset.baseUrl : "");

  return (
    <div className="rt-detail-page">
      <BackLink />
      <header className="rt-dh">
        <ProviderLogo logo={preset ? preset.logo : ""} name={label} size={52} />
        <div className="rt-dh-t">
          <h1>{label}</h1>
          <p className="rt-dh-sub">
            {preset ? preset.blurb : "An OpenAI-compatible server at " + ((connections[0] && connections[0].base_url) || "") + "."}
          </p>
          <div className="rt-dh-status">
            <StatusPills status={status} />
          </div>
        </div>
        <div className="rt-dh-acts">
          {preset ? (
            <a className="btn btn-ghost btn-sm" href={preset.keysUrl} target="_blank" rel="noreferrer">
              <Ic name="external-link" size={13} />
              Get an API key
            </a>
          ) : null}
        </div>
      </header>

      <SettingsRestartBanner restartRequired={Boolean(data && data.restart_required)} onRestarted={reload} />
      <StatusLine text={message} />
      <Alert tone="danger" message={error} details={details} />

      <Panel
        title="Connections"
        className={"rt-conns-panel" + (pooled ? " rt-conns-pooled" : "")}
        actions={
          custom || connections.length === 0 ? null : (
            <Button variant="primary" size="sm" icon="plus" disabled={!version} onClick={() => setDialog({ connection: null })}>
              Add connection
            </Button>
          )
        }
      >
        {connections.length === 0 ? (
          <div className="rt-conn-empty">
            <p>
              No connection yet. Paste a {label} API key to let agents use its models.
            </p>
            <Button variant="primary" icon="key-round" disabled={!version} onClick={() => setDialog({ connection: null })}>
              Connect {label}
            </Button>
          </div>
        ) : pooled ? (
          <AccountOrderList
            label={label + " connections"}
            noun="connection"
            disabled={Boolean(busyRow)}
            onSave={saveOrder}
            items={poolOrder(connections).map((row) => {
              const handlers = handlersFor(row);
              return {
                id: row.name,
                label: row.name,
                mono: true,
                rowLabel: "Connection " + row.name,
                rowClass: row.disabled ? "is-off" : undefined,
                pool: row.pool !== false,
                sub: (
                  <>
                    <ConnectionMeta row={row} defaultBase={defaultBase(row)} />
                    <span className="rt-conn-users">
                      <UsedBy usedBy={row.used_by} />
                    </span>
                  </>
                ),
                below: handlers.test && !handlers.test.busy && handlers.test.text ? <ConnectionTest test={handlers.test} /> : null,
                rowActions: <ConnectionActions row={row} handlers={handlers} />,
              };
            })}
          />
        ) : (
          <ul className="rt-conns">
            {connections.map((row) => (
              <ConnectionRow key={row.name} row={row} defaultBase={defaultBase(row)} handlers={handlersFor(row)} />
            ))}
          </ul>
        )}
        {pooled ? <p className="hint rt-conn-note">{CONNECTION_POOL_NOTE}</p> : null}
        {pooled && connections.filter((row) => row.pool !== false).length > 1 ? (
          <p className="hint rt-conn-note">{POOL_TERMS_NOTE}</p>
        ) : null}
      </Panel>

      {connections
        .filter((row) => connectionKeys(row).length > 0)
        .map((row) => (
          <KeysPanel
            key={"keys-" + row.name}
            row={row}
            title={connections.length > 1 ? "Keys · " + row.name : "Keys"}
            version={version}
            onSaved={reload}
          />
        ))}

      {connections
        .filter((row) => !row.disabled)
        .map((row) => (
          <ProviderModelsPanel
            key={row.name}
            provider={row.name}
            title={connections.length > 1 ? "Models · " + row.name : "Models"}
          />
        ))}

      {connections.length > 0 ? (
        <div className="rt-grid2 rt-detail-foot">
          <UsageStrip names={names} />
          <section className="rt-detail-quota" aria-label={"Quota of " + label}>
            {limits === null ? null : limits.length === 0 ? (
              <Panel title="Quota">
                <p className="rt-limit-none">No limits reported yet. They appear after the first call.</p>
              </Panel>
            ) : (
              limits.map((limit) => (
                <LimitCard
                  key={limit.provider}
                  limit={limit}
                  provider={connections.find((row) => row.name === limit.provider)}
                />
              ))
            )}
          </section>
        </div>
      ) : null}

      {dialog && data ? (
        <ConnectionDialog
          preset={preset}
          connection={dialog.connection}
          version={data.version}
          takenNames={providers.map((row) => row.name)}
          onClose={() => setDialog(null)}
          onSaved={(text, restartRequired) => {
            setDialog(null);
            setError("");
            setMessage(restartRequired ? text + " Restart the daemon for it to take effect." : text + " It applies now.");
            reload();
          }}
        />
      ) : null}
    </div>
  );
}
