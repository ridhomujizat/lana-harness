import { useCallback, useEffect, useMemo, useState } from "react";
import * as routerApi from "../../api/router";
import type {
  LimitSnapshot,
  ListProvidersResponse,
  ProviderRow,
  ProviderTestRequest,
  UsageProviderRow,
} from "../../api/router";
import { Badge, Button, EmptyState, ErrorState, IconButton, Panel, Toggle } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { Alert, SettingsRestartBanner, StatusLine, markSettingsSaved } from "../settings-pages";
import { ClaudeCodeDetail } from "./claude-code-page";
import { ConnectionDialog } from "./connection-dialog";
import { LimitCard } from "./limits";
import { ProviderModelsPanel } from "./models";
import { CLAUDE_CODE_ID, cardIdFor, presetById } from "./presets";
import { ProviderLogo } from "./provider-logo";
import { StatusPills, UsedBy, cardStatus, keyState } from "./providers";
import { compact, count, failureOf, inUseText, isStale, ms, percent, statsCostText } from "./router-common";

/* One provider's page (#/router/providers/<preset or name>): its connections
   — each one [providers.<name>] entry with its own key — then its models, a
   24-hour usage strip, and its quota. Claude Code has its own page. */

export function ProviderDetail({ id }: { id: string }) {
  if (id === CLAUDE_CODE_ID) {
    return <ClaudeCodeDetail />;
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
/* API key provider                                                    */
/* ------------------------------------------------------------------ */

type RowTest = { busy: boolean; ok?: boolean; text?: string };

function ConnectionRow({
  row,
  defaultBase,
  busy,
  test,
  confirming,
  onToggle,
  onTest,
  onEdit,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  row: ProviderRow;
  defaultBase: string;
  busy: boolean;
  test: RowTest | undefined;
  confirming: boolean;
  onToggle: (enabled: boolean) => void;
  onTest: () => void;
  onEdit: () => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const key = keyState(row);
  const base = row.base_url && row.base_url !== defaultBase ? row.base_url : "";
  const enabled = !row.disabled;
  return (
    <li className={"rt-conn" + (enabled ? "" : " is-off")} aria-label={"Connection " + row.name}>
      <div className="rt-conn-main">
        <span className="rt-conn-name mono">{row.name}</span>
        <span className="rt-conn-meta">
          {row.api_key_env ? <code className="mono">{row.api_key_env}</code> : null}
          <Badge tone={key.tone} dot>
            {key.label}
          </Badge>
          {key.note ? <span className="hint">{key.note}</span> : null}
          {base ? <span className="mono trunc rt-conn-base">{base}</span> : null}
        </span>
        <span className="rt-conn-users">
          <UsedBy usedBy={row.used_by} />
        </span>
        {test && !test.busy && test.text ? (
          <span className={"rt-test " + (test.ok ? "ok" : "bad")} role="status">
            <Ic name={test.ok ? "circle-check" : "circle-alert"} size={12} /> {test.text}
          </span>
        ) : null}
      </div>
      <div className="rt-conn-acts">
        {confirming ? (
          <>
            <span className="hint">Delete {row.name}?</span>
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
                aria-label={"Use " + row.name}
                checked={enabled}
                disabled={busy}
                onChange={onToggle}
              />
              <span className="hint">{enabled ? "On" : "Off"}</span>
            </label>
            <Button
              size="sm"
              variant="ghost"
              icon="plug"
              loading={Boolean(test && test.busy)}
              disabled={busy || Boolean(test && test.busy)}
              aria-label={"Test " + row.name}
              onClick={onTest}
            >
              Test
            </Button>
            <IconButton icon="pencil" size="sm" label={"Edit " + row.name} disabled={busy} onClick={onEdit} />
            <IconButton icon="trash-2" size="sm" label={"Delete " + row.name} disabled={busy} onClick={onAskDelete} />
          </>
        )}
      </div>
    </li>
  );
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
        className="rt-conns-panel"
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
        ) : (
          <ul className="rt-conns">
            {connections.map((row) => (
              <ConnectionRow
                key={row.name}
                row={row}
                defaultBase={defaultBase(row)}
                busy={busyRow === row.name}
                test={tests[row.name]}
                confirming={confirm === row.name}
                onToggle={(enabled) => void toggle(row, enabled)}
                onTest={() => void testRow(row)}
                onEdit={() => setDialog({ connection: row })}
                onAskDelete={() => setConfirm(row.name)}
                onCancelDelete={() => setConfirm("")}
                onDelete={() => void remove(row)}
              />
            ))}
          </ul>
        )}
        {connections.length > 1 ? (
          <p className="hint rt-conn-note">
            Each connection is its own provider name with its own key, for example two accounts. Put
            them in a combo to fall back from one to the other.
          </p>
        ) : null}
      </Panel>

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
