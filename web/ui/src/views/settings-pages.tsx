/* eslint-disable react-hooks/set-state-in-effect -- the old Preact views loaded
   data on mount and synced form state in effects; this port keeps that shape. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
  FormEvent,
  KeyboardEvent,
  ReactNode,
  UIEvent,
} from "react";
import { ApiError, errorMessage } from "../api/client";
import * as settingsApi from "../api/settings";
import type {
  ConfigIssue,
  ConfigValidation,
  GetConfigResponse,
} from "../api/settings";
import type { ListSecretsResponse, SecretRow } from "../api/secrets";
import type { DaemonInfo } from "../api/daemon";
import type { AuditEntry } from "../api/audit";
import { ErrorDetails } from "../ui/ui";
import { agoLabel } from "./agent-common";
import { Ic } from "../ui/icons";
import {
  Badge,
  Button,
  EmptyState,
  Field,
  IconButton,
  Input,
  Menu,
  MenuItem,
  Modal,
  SearchInput,
  Skeleton,
  Textarea,
} from "../ui/gr8r";
import { RestartDaemonDialog, useDaemonRestart } from "./restart-dialog";

/* Settings rev 2 (T-034): the shared shell plus Providers, Secrets, Config file,
   and Audit log. General lives in settings-general.js and imports the shell from
   here, so this file must not import settings-general.js. */

const SETTINGS_TABS = [
  { key: "general", label: "General", icon: "sliders-horizontal" },
  { key: "secrets", label: "Secrets", icon: "key-round" },
  { key: "config", label: "Config file", icon: "file-code" },
  { key: "audit", label: "Audit log", icon: "scroll-text" },
];

const SECRET_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const SAVED_AT_KEY = "lana.settings.savedAt";

function readSavedAt(): number {
  try {
    const value = Number(window.localStorage.getItem(SAVED_AT_KEY));
    return Number.isFinite(value) && value > 0 ? value : 0;
  } catch {
    return 0;
  }
}

export function markSettingsSaved(): void {
  try {
    window.localStorage.setItem(SAVED_AT_KEY, String(Date.now()));
  } catch {
    /* The banner still appears; only the "N min ago" hint is lost. */
  }
}

function clearSettingsSaved(): void {
  try {
    window.localStorage.removeItem(SAVED_AT_KEY);
  } catch {
    /* Nothing else depends on the hint. */
  }
}

function savedAgoLabel(): string {
  const savedAt = readSavedAt();
  if (!savedAt) {
    return "";
  }
  return agoLabel(savedAt);
}

function failureInfo(failure: unknown): {
  message: string;
  details: unknown[];
  code: string;
} {
  return {
    message: errorMessage(failure),
    details: failure instanceof ApiError ? failure.details : [],
    code: failure instanceof ApiError ? failure.code : "",
  };
}

export function StatusLine({ text }: { text: string }) {
  return (
    <p
      className="settings-status"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {text}
    </p>
  );
}

/* Gr8r `.alert` in place of the legacy error box: one message plus the detail
   list, with the tone and icon matching Gr8r's state colours. */
export function Alert({
  tone,
  icon = "circle-alert",
  message,
  details,
}: {
  tone: "danger" | "warn" | "ok" | "info";
  icon?: string;
  message?: string;
  details?: unknown[];
}) {
  const hasDetails = Array.isArray(details) && details.length > 0;
  if (!message && !hasDetails) {
    return null;
  }
  return (
    <div
      className={"alert " + tone}
      role={tone === "danger" ? "alert" : undefined}
      aria-live={tone === "danger" ? undefined : "polite"}
    >
      <Ic name={icon} size={15} />
      <div className="grow">
        {message ? <p className="settings-alert-msg">{message}</p> : null}
        <ErrorDetails details={details} />
      </div>
    </div>
  );
}

function LoadingRows({ label }: { label: string }) {
  return (
    <div className="panel settings-loading" aria-busy="true">
      <span className="sr">{label}</span>
      {Array.from({ length: 4 }).map((_, index) => (
        <div key={index} className="mini settings-loading-row">
          <Skeleton width={30} height={30} radius={8} />
          <Skeleton width="40%" height={12} />
          <Skeleton width="18%" height={12} />
        </div>
      ))}
    </div>
  );
}

function matchesSecret(secret: SecretRow, query: string): boolean {
  if (!query) {
    return true;
  }
  const fields: Array<string | null | undefined> = [
    secret.name,
    secret.state,
    secret.source,
  ].concat(Array.isArray(secret.used_by) ? secret.used_by : []);
  return fields
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

export interface SettingsTabsProps {
  active: string;
}

/* The Gr8r settings rail: Lana's sections as `.sitem` rows, the active one
   carrying `.on` and aria-current. Arrow keys move focus down the list. */
export function SettingsTabs({ active }: SettingsTabsProps) {
  function handleKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (
      event.key !== "ArrowDown" &&
      event.key !== "ArrowUp" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    const items = Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>(".sitem")
    );
    if (items.length === 0) {
      return;
    }
    const index = items.findIndex((item) => item === document.activeElement);
    let next = index < 0 ? 0 : index;
    if (event.key === "ArrowDown") {
      next = (next + 1) % items.length;
    } else if (event.key === "ArrowUp") {
      next = (next - 1 + items.length) % items.length;
    } else if (event.key === "Home") {
      next = 0;
    } else {
      next = items.length - 1;
    }
    event.preventDefault();
    const target = items[next];
    if (target) {
      target.focus();
    }
  }

  return (
    <nav className="set-nav" aria-label="Settings" onKeyDown={handleKeyDown}>
      {SETTINGS_TABS.map((tab) => (
        <a
          key={tab.key}
          id={"settings-tab-" + tab.key}
          className={"sitem" + (active === tab.key ? " on" : "")}
          href={"#/settings/" + tab.key}
          aria-current={active === tab.key ? "page" : undefined}
        >
          <Ic name={tab.icon} size={15} />
          <span>{tab.label}</span>
        </a>
      ))}
    </nav>
  );
}

export interface SettingsPanelProps {
  active: string;
  title?: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}

/* The Gr8r settings body: `.set-body > .set-in` with a `.ph` section header,
   then the section blocks. */
export function SettingsPanel({
  active,
  title,
  description,
  children,
}: SettingsPanelProps) {
  return (
    <div id={"settings-panel-" + active} className="set-body">
      <div className="set-in">
        {title ? (
          <header className="ph">
            <div>
              <h1>{title}</h1>
              {description ? <p>{description}</p> : null}
            </div>
          </header>
        ) : null}
        {children}
      </div>
    </div>
  );
}

export interface SettingsRestartDialogProps {
  onClose: () => void;
  onRestarted?: () => void;
}

/* The restart flow lives in one shared component so Settings > General and the
   Telegram connect step behave the same (REQ-TFB-003, API-TFB-002). */
export function SettingsRestartDialog({
  onClose,
  onRestarted,
}: SettingsRestartDialogProps) {
  const controller = useDaemonRestart({
    onRestarted: () => {
      clearSettingsSaved();
      if (onRestarted) {
        onRestarted();
      }
      onClose();
    },
  });

  return <RestartDaemonDialog controller={controller} onClose={onClose} />;
}

export interface SettingsRestartBannerProps {
  restartRequired: boolean;
  onRestarted?: () => void;
}

export function SettingsRestartBanner({
  restartRequired,
  onRestarted,
}: SettingsRestartBannerProps) {
  const [open, setOpen] = useState(false);
  if (!restartRequired) {
    return null;
  }
  const saved = savedAgoLabel();
  return (
    <div className="alert warn settings-restart-banner">
      <Ic name="triangle-alert" size={15} />
      <p className="settings-restart-text">
        {saved ? "Saved " + saved + ". " : "Saved. "}Restart the daemon for the
        saved changes to take effect.
      </p>
      <Button
        variant="secondary"
        size="sm"
        icon="rotate-cw"
        onClick={() => setOpen(true)}
      >
        Restart daemon
      </Button>
      {open ? (
        <SettingsRestartDialog
          onClose={() => setOpen(false)}
          onRestarted={() => {
            setOpen(false);
            if (onRestarted) {
              onRestarted();
            }
          }}
        />
      ) : null}
    </div>
  );
}

/* Providers moved to the Router page (spec 37): views/router/providers.tsx. */

interface SecretDialogProps {
  secret: SecretRow | null;
  version: string;
  onClose: () => void;
  onSaved?: (text: string) => void;
}

function SecretDialog({ secret, version, onClose, onSaved }: SecretDialogProps) {
  const editing = Boolean(secret);
  const [name, setName] = useState(secret ? secret.name : "");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    const secretName = secret ? secret.name : name.trim();
    if (!SECRET_NAME_RE.test(secretName)) {
      setError(
        "The name must match [A-Za-z_][A-Za-z0-9_]*, for example OPENAI_API_KEY."
      );
      setDetails([]);
      return;
    }
    if (!value) {
      setError("Enter a value. The value is never shown again after saving.");
      setDetails([]);
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await settingsApi.setSecret(secretName, value, version);
      setValue("");
      markSettingsSaved();
      if (onSaved) {
        onSaved(
          "Secret " +
            secretName +
            " saved. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure: unknown) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "The secrets file changed since this list loaded. Close the dialog and reload the list before saving."
        );
        setDetails([]);
      } else {
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title={editing ? "Replace secret" : "New secret"}
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="settings-secret-form"
            variant="primary"
            disabled={busy}
            loading={busy}
          >
            {busy ? "Saving…" : editing ? "Replace secret" : "Save secret"}
          </Button>
        </>
      }
    >
      <form id="settings-secret-form" className="settings-dialog-form" onSubmit={submit}>
        {editing ? (
          <p className="settings-secret-target">
            Replacing <code className="mono">{secret ? secret.name : ""}</code>.
            The old value is removed.
          </p>
        ) : (
          <Field
            label="Name"
            hint="Letters, digits, and underscores; must not start with a digit."
          >
            <Input
              data-autofocus={true}
              type="text"
              name="secret-name"
              value={name}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              pattern="[A-Za-z_][A-Za-z0-9_]*"
              required
              onChange={(event) => setName(event.target.value)}
            />
          </Field>
        )}

        <Field
          label={editing ? "New value" : "Value"}
          hint="Values are write-only. The value is never shown again after saving."
        >
          <Input
            data-autofocus={editing ? true : undefined}
            type="password"
            name="secret-value"
            value={value}
            disabled={busy}
            autoComplete="new-password"
            spellCheck={false}
            required
            onChange={(event) => setValue(event.target.value)}
          />
        </Field>

        <Alert tone="danger" message={error} details={details} />
      </form>
    </Modal>
  );
}

interface RemoveSecretDialogProps {
  secret: SecretRow;
  version: string;
  onClose: () => void;
  onRemoved?: (text: string) => void;
}

function RemoveSecretDialog({
  secret,
  version,
  onClose,
  onRemoved,
}: RemoveSecretDialogProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  async function remove() {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    try {
      await settingsApi.deleteSecret(secret.name, version);
      markSettingsSaved();
      if (onRemoved) {
        onRemoved(
          "Secret " +
            secret.name +
            " removed. Restart the daemon for the saved changes to take effect."
        );
      }
    } catch (failure: unknown) {
      if (failure instanceof ApiError && failure.code === "stale_version") {
        setError(
          "The secrets file changed since this list loaded. Close the dialog and reload the list before removing."
        );
        setDetails([]);
      } else {
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title={"Remove " + secret.name + "?"}
      dismissible={!busy}
      onClose={onClose}
      footer={
        <>
          <span className="sp" />
          <Button
            data-autofocus={true}
            variant="secondary"
            disabled={busy}
            onClick={onClose}
          >
            Cancel
          </Button>
          <Button
            variant="danger"
            disabled={busy}
            loading={busy}
            onClick={remove}
          >
            {busy ? "Removing…" : "Remove secret"}
          </Button>
        </>
      }
    >
      <div className="settings-dialog-form">
        <p>
          This removes the value from <code className="mono">secrets.env</code>.
          An agent or channel that uses this secret will fail until it is set
          again.
        </p>
        <Alert tone="danger" message={error} details={details} />
      </div>
    </Modal>
  );
}

/* The row action menu: an IconButton that opens the Gr8r popover menu with the
   Replace and Remove items (REQ-WEB-055). */
function RowMenu({
  label,
  onReplace,
  onRemove,
  removeDisabled,
  removeTitle,
}: {
  label: string;
  onReplace: () => void;
  onRemove: () => void;
  removeDisabled?: boolean;
  removeTitle?: string;
}) {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLSpanElement>(null);
  return (
    <span className="settings-rowmenu" ref={anchorRef}>
      <IconButton
        icon="ellipsis"
        size="sm"
        label={label}
        onClick={() => setOpen(true)}
      />
      <Menu anchorRef={anchorRef} open={open} onClose={() => setOpen(false)}>
        <MenuItem
          icon="refresh-cw"
          onClick={() => {
            setOpen(false);
            onReplace();
          }}
        >
          Replace
        </MenuItem>
        <MenuItem
          icon="trash-2"
          danger
          disabled={removeDisabled}
          title={removeTitle}
          onClick={() => {
            setOpen(false);
            onRemove();
          }}
        >
          Remove
        </MenuItem>
      </Menu>
    </span>
  );
}

export function SettingsSecretsPage() {
  const [data, setData] = useState<ListSecretsResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [dialog, setDialog] = useState<{ secret: SecretRow | null } | null>(null);
  const [removeTarget, setRemoveTarget] = useState<SecretRow | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  const reload = useCallback(() => {
    setReloadToken((current) => current + 1);
  }, []);

  useEffect(() => {
    let alive = true;
    setState("loading");
    setError("");
    setDetails([]);
    settingsApi
      .listSecrets()
      .then((value) => {
        if (!alive) {
          return;
        }
        setData(value);
        setState("ready");
      })
      .catch((failure: unknown) => {
        if (!alive) {
          return;
        }
        const failureState = failureInfo(failure);
        setError(failureState.message);
        setDetails(failureState.details);
        setState("error");
      });
    return () => {
      alive = false;
    };
  }, [reloadToken]);

  const secrets = useMemo(
    () => (data && Array.isArray(data.secrets) ? data.secrets : []),
    [data]
  );
  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => secrets.filter((secret) => matchesSecret(secret, search)),
    [secrets, search]
  );

  function handleSaved(text: string) {
    setDialog(null);
    setMessage(text);
    reload();
  }

  function handleRemoved(text: string) {
    setRemoveTarget(null);
    setMessage(text);
    reload();
  }

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="secrets" />
      <SettingsPanel
        active="secrets"
        title="Secrets"
        description="Write-only values providers, channels, and MCP servers read at start."
      >
        <SettingsRestartBanner
          restartRequired={Boolean(data && data.restart_required)}
          onRestarted={reload}
        />

        <div className="alert info settings-write-note">
          <Ic name="lock" size={15} />
          <p>
            Values are write-only. After you save one, nobody can read it here
            again. You can replace or remove it, not read it.
          </p>
        </div>

        <div className="settings-toolbar">
          <SearchInput
            wrapClassName="settings-search"
            aria-label="Search secrets"
            placeholder="Search secrets"
            autoComplete="off"
            spellCheck={false}
            kbd={null}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <span className="settings-toolbar-spacer" />
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            disabled={!data || state === "loading"}
            onClick={() => setDialog({ secret: null })}
          >
            New secret
          </Button>
        </div>

        <StatusLine text={message} />
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" onClick={reload}>
              Reload secrets
            </Button>
          </div>
        ) : null}
        {state === "loading" ? (
          <LoadingRows label="Loading secrets…" />
        ) : state === "ready" && visible.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon={secrets.length === 0 ? "key-round" : "search-x"}
              size={secrets.length === 0 ? undefined : "sm"}
              title={
                secrets.length === 0
                  ? "No secrets yet"
                  : "No secrets match “" + query.trim() + "”"
              }
              text={
                secrets.length === 0
                  ? "Add a secret for a provider, a channel, or an MCP server."
                  : "Try a different name or usage."
              }
            />
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <div className="tbl-wrap">
            <table className="tbl settings-secrets-table">
              <caption className="sr">Secrets</caption>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Used by</th>
                  <th scope="col">Updated</th>
                  <th scope="col" className="settings-col-actions">
                    <span className="sr">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {visible.map((secret) => {
                  const isSet = secret.state === "set";
                  const usedBy = Array.isArray(secret.used_by)
                    ? secret.used_by
                    : [];
                  return (
                    <tr key={secret.name}>
                      <td>
                        <span className="settings-secret-name">
                          <Ic name="lock" size={14} />
                          <span className="settings-secret-text">
                            <code className="mono">{secret.name}</code>
                            <span className="settings-secret-value">
                              {isSet ? (
                                <>
                                  <span
                                    className="settings-secret-dots"
                                    aria-hidden="true"
                                  >
                                    ••••••••••••
                                  </span>
                                  <Badge tone="green" dot>
                                    Set
                                  </Badge>
                                </>
                              ) : (
                                <Badge tone="gray" dot>
                                  Not set
                                </Badge>
                              )}
                              {secret.overridden_by_env ? (
                                <Badge tone="amber">From environment</Badge>
                              ) : null}
                            </span>
                          </span>
                        </span>
                      </td>
                      <td>
                        {usedBy.length > 0 ? (
                          <span className="settings-used-paths">
                            {usedBy.map((path, index) => (
                              <span key={path}>
                                {index > 0 ? ", " : ""}
                                <code className="mono">{path}</code>
                              </span>
                            ))}
                          </span>
                        ) : (
                          <span className="hint">Not used</span>
                        )}
                      </td>
                      <td className="settings-audit-time">
                        {agoLabel(secret.updated_at) || "—"}
                      </td>
                      <td className="settings-row-action">
                        <RowMenu
                          label={"Actions for " + secret.name}
                          onReplace={() => setDialog({ secret })}
                          onRemove={() => setRemoveTarget(secret)}
                          removeDisabled={!isSet}
                          removeTitle={
                            !isSet ? "This secret is not set." : undefined
                          }
                        />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}

        {data && data.version ? (
          <p className="settings-footer-note">
            Secrets are stored in <code className="mono">secrets.env</code>.
            Every write uses the version this list loaded.
          </p>
        ) : null}

        {dialog ? (
          <SecretDialog
            secret={dialog.secret}
            version={data ? data.version : ""}
            onClose={() => setDialog(null)}
            onSaved={handleSaved}
          />
        ) : null}
        {removeTarget ? (
          <RemoveSecretDialog
            secret={removeTarget}
            version={data ? data.version : ""}
            onClose={() => setRemoveTarget(null)}
            onRemoved={handleRemoved}
          />
        ) : null}
      </SettingsPanel>
    </main>
  );
}

function configLineNumbers(raw: string): number[] {
  const count = String(raw || "").split("\n").length;
  const lines: number[] = [];
  for (let number = 1; number <= count; number += 1) {
    lines.push(number);
  }
  return lines;
}

export function SettingsConfigPage() {
  const [config, setConfig] = useState<GetConfigResponse | null>(null);
  const [daemon, setDaemon] = useState<DaemonInfo | null>(null);
  const [raw, setRaw] = useState("");
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [errorCode, setErrorCode] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const gutterRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async (announceFailure: boolean) => {
    setState("loading");
    setError("");
    setDetails([]);
    setErrorCode("");
    const results = await Promise.allSettled([
      settingsApi.getConfig(),
      settingsApi.getDaemon(),
    ]);
    let failure: unknown = null;
    if (results[0].status === "fulfilled") {
      const value = results[0].value;
      setConfig(value);
      setRaw(value && typeof value.raw === "string" ? value.raw : "");
    } else {
      failure = results[0].reason;
    }
    if (results[1].status === "fulfilled") {
      setDaemon(results[1].value);
    }
    if (failure) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setState("error");
      if (announceFailure) {
        setMessage("Could not load the config. " + failureState.message);
      }
    } else {
      setState("ready");
    }
  }, []);

  useEffect(() => {
    void load(false);
  }, [load]);

  const lineNumbers = useMemo(() => configLineNumbers(raw), [raw]);
  const missing = Boolean(config && !config.version);
  const validation: ConfigValidation =
    config && config.validation ? config.validation : { ok: true, errors: [] };
  const validationErrors: ConfigIssue[] = Array.isArray(validation.errors)
    ? validation.errors
    : [];

  function syncGutter(event: UIEvent<HTMLTextAreaElement>) {
    const gutter = gutterRef.current;
    if (gutter) {
      gutter.scrollTop = event.currentTarget.scrollTop;
    }
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) {
      return;
    }
    if (!config) {
      return;
    }
    if (!config.version) {
      setError(
        "config.toml is missing. Run lana init to create it; the web view does not create the file."
      );
      setDetails([]);
      setMessage("Config not saved: config.toml is missing.");
      return;
    }
    if (!raw.trim()) {
      setError("The config is empty. Nothing was saved.");
      setDetails([]);
      setMessage("Config not saved: the editor is empty.");
      return;
    }
    setBusy(true);
    setError("");
    setDetails([]);
    setErrorCode("");
    setMessage("Saving config.toml…");
    try {
      const result = await settingsApi.saveConfig(raw, config.version);
      markSettingsSaved();
      setConfig({
        ...config,
        version: result.version,
        restart_required: result.restart_required,
        validation: { ok: true, errors: [] },
      });
      setMessage(
        "Config saved. Restart the daemon for the saved changes to take effect."
      );
    } catch (failure: unknown) {
      const failureState = failureInfo(failure);
      if (failureState.code === "validation_failed") {
        setError("The config was not saved because validation failed.");
        setMessage("Config not saved. Validation failed.");
      } else if (failureState.code === "stale_version") {
        setError(
          "The file changed on disk since it was loaded. Reload before saving."
        );
        setMessage("Config not saved: the file changed on disk.");
      } else if (failureState.code === "config_missing") {
        setError("config.toml does not exist. Run lana init to create it.");
        setMessage("Config not saved: config.toml does not exist.");
      } else {
        setError(failureState.message);
        setMessage("Could not save the config.");
      }
      setDetails(failureState.details);
      setErrorCode(failureState.code);
    } finally {
      setBusy(false);
    }
  }

  const path =
    daemon && daemon.home ? daemon.home + "/config.toml" : "config.toml";

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="config" />
      <SettingsPanel
        active="config"
        title="Config file"
        description="The daemon's config.toml, edited in place."
      >
        <SettingsRestartBanner
          restartRequired={Boolean(config && config.restart_required)}
          onRestarted={() => void load(false)}
        />

        {state === "loading" && !config ? (
          <LoadingRows label="Loading config.toml…" />
        ) : null}
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" disabled={busy} onClick={() => void load(true)}>
              Reload config
            </Button>
          </div>
        ) : null}
        <StatusLine text={message} />
        {missing ? (
          <div className="alert warn">
            <Ic name="triangle-alert" size={15} />
            <span>
              config.toml is missing. Run <code className="mono">lana init</code>{" "}
              to create it; the web view does not create the file.
            </span>
          </div>
        ) : null}

        <form className="settings-config-form" onSubmit={save}>
          <div className="settings-config-head">
            <p className="settings-config-path">
              <code className="mono">{path}</code>{" "}
              <span className="settings-config-note">
                · validated on save · comments kept · secret values show as ***
              </span>
            </p>
            <Button
              type="submit"
              variant="primary"
              disabled={busy || missing || !config || !raw.trim()}
              loading={busy}
            >
              {busy ? "Saving…" : "Save config.toml"}
            </Button>
          </div>

          <div className="settings-editor">
            <div className="settings-gutter" aria-hidden="true" ref={gutterRef}>
              {lineNumbers.map((number) => (
                <span key={number} className="settings-gutter-line">
                  {number}
                </span>
              ))}
            </div>
            <Textarea
              id="settings-config-raw"
              className="settings-config-editor mono"
              aria-label="config.toml contents"
              wrap="off"
              spellCheck={false}
              value={raw}
              disabled={busy || missing || !config}
              onChange={(event) => setRaw(event.target.value)}
              onScroll={syncGutter}
            />
          </div>
        </form>

        {validationErrors.length > 0 ? (
          <Alert
            tone="danger"
            message="Validation errors"
            details={validationErrors}
          />
        ) : null}
        {errorCode === "stale_version" ? (
          <div className="settings-actions-bar">
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => void load(true)}
            >
              Reload from disk
            </Button>
          </div>
        ) : null}
        {config && config.version ? (
          <p className="settings-footer-note">
            Optimistic concurrency: saving sends this file's current version.
          </p>
        ) : null}
      </SettingsPanel>
    </main>
  );
}

function auditTimeLabel(value: unknown): string {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return "—";
  }
  return new Date(number).toLocaleString();
}

function auditTimeISO(value: unknown): string | undefined {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) {
    return undefined;
  }
  return new Date(number).toISOString();
}

function matchesAudit(entry: AuditEntry, query: string): boolean {
  if (!query) {
    return true;
  }
  const keys = Array.isArray(entry.changed_keys) ? entry.changed_keys : [];
  return [entry.kind, entry.target]
    .concat(keys)
    .filter((value) => value !== null && value !== undefined)
    .join(" ")
    .toLowerCase()
    .includes(query);
}

export function SettingsAuditPage() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [message, setMessage] = useState("");
  const [query, setQuery] = useState("");
  const [restartRequired, setRestartRequired] = useState(false);

  const loadRestartState = useCallback(() => {
    settingsApi
      .getConfig()
      .then((value) => {
        setRestartRequired(Boolean(value && value.restart_required));
      })
      .catch(() => {
        /* The banner is optional; the audit list still works. */
      });
  }, []);

  const loadPage = useCallback(async (before: number | null, append: boolean) => {
    if (append) {
      setLoadingMore(true);
    } else {
      setState("loading");
    }
    setError("");
    setDetails([]);
    setMessage(append ? "Loading more entries…" : "Loading the audit log…");
    try {
      const page = await settingsApi.listAudit(before ?? undefined, 50);
      const rows = page && Array.isArray(page.entries) ? page.entries : [];
      setEntries((current) => (append ? current.concat(rows) : rows));
      const cursor = page ? Number(page.next_before) : 0;
      setNextBefore(Number.isFinite(cursor) && cursor > 0 ? cursor : null);
      setState("ready");
      setMessage(
        append
          ? "Loaded " +
              rows.length +
              " more " +
              (rows.length === 1 ? "entry." : "entries.")
          : "Loaded " +
              rows.length +
              " " +
              (rows.length === 1 ? "entry." : "entries.")
      );
    } catch (failure: unknown) {
      const failureState = failureInfo(failure);
      setError(failureState.message);
      setDetails(failureState.details);
      setState(append ? "ready" : "error");
      setMessage("Could not load the audit log.");
    } finally {
      setLoadingMore(false);
    }
  }, []);

  useEffect(() => {
    loadRestartState();
    void loadPage(null, false);
  }, [loadPage, loadRestartState]);

  const search = query.trim().toLowerCase();
  const visible = useMemo(
    () => entries.filter((entry) => matchesAudit(entry, search)),
    [entries, search]
  );

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="audit" />
      <SettingsPanel
        active="audit"
        title="Audit log"
        description="Every settings change the daemon recorded, newest first."
      >
        <SettingsRestartBanner
          restartRequired={restartRequired}
          onRestarted={() => {
            loadRestartState();
            void loadPage(null, false);
          }}
        />

        <div className="settings-toolbar">
          <SearchInput
            wrapClassName="settings-search"
            aria-label="Search audit log"
            placeholder="Search audit log"
            autoComplete="off"
            spellCheck={false}
            kbd={null}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>

        <StatusLine text={message} />
        <Alert tone="danger" message={error} details={details} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" onClick={() => void loadPage(null, false)}>
              Reload audit log
            </Button>
          </div>
        ) : null}

        {state === "loading" ? (
          <LoadingRows label="Loading the audit log…" />
        ) : state === "ready" && entries.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon="scroll-text"
              title="No settings changes yet"
              text="Saves, restarts, and sign-outs appear here after they happen."
            />
          </div>
        ) : state === "ready" && visible.length === 0 ? (
          <div className="panel">
            <EmptyState
              icon="search-x"
              size="sm"
              title="No entries match"
              text="No audit entries match the current search."
            />
          </div>
        ) : null}

        {state === "ready" && visible.length > 0 ? (
          <div className="tbl-wrap">
            <table className="tbl settings-audit-table">
              <caption className="sr">Settings audit log</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Kind</th>
                  <th scope="col">Target</th>
                  <th scope="col">Changed keys</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((entry, index) => {
                  const changed = Array.isArray(entry.changed_keys)
                    ? entry.changed_keys
                    : [];
                  const iso = auditTimeISO(entry.time);
                  return (
                    <tr key={(entry.time || 0) + ":" + index}>
                      <td className="settings-audit-time">
                        <time dateTime={iso || undefined}>
                          {auditTimeLabel(entry.time)}
                        </time>
                      </td>
                      <td>
                        <Badge>{entry.kind}</Badge>
                      </td>
                      <td>
                        <code className="mono">{entry.target || "—"}</code>
                      </td>
                      <td className="settings-audit-keys">
                        {changed.length > 0 ? (
                          changed.map((key, keyIndex) => (
                            <span key={key + ":" + keyIndex}>
                              {keyIndex > 0 ? ", " : ""}
                              <code className="mono">{key}</code>
                            </span>
                          ))
                        ) : (
                          <span className="hint">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}

        {state === "ready" && entries.length > 0 ? (
          <div className="settings-load-more">
            {nextBefore !== null ? (
              <Button
                variant="secondary"
                loading={loadingMore}
                disabled={loadingMore}
                onClick={() => void loadPage(nextBefore, true)}
              >
                {loadingMore ? "Loading…" : "Load more"}
              </Button>
            ) : (
              <p className="settings-footer-note">
                That is every entry in the audit file.
              </p>
            )}
          </div>
        ) : null}

        <p className="settings-footer-note">
          Entries are newest first. Each line names the target and the changed
          keys, never a value.
        </p>
      </SettingsPanel>
    </main>
  );
}
