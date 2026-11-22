/* eslint-disable react-hooks/set-state-in-effect -- loads on mount like the
   other settings pages. */
import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import { getAuthSession, listBrowserSessions } from "../api/auth";
import type { AuthSession, BrowserSessionInfo } from "../api/auth";
import {
  TAILSCALE_ADMIN_DNS_URL,
  browserName,
  disableRemoteAccess,
  enableRemoteAccess,
  getRemoteAccess,
  openTailscaleApp,
  revokeBrowserSession,
  revokeRemoteSessions,
  saveRemoteSettings,
} from "../api/remote";
import type {
  RemoteAccess,
  RemoteConfig,
  RemoteSettingsUpdate,
  RemoteTerminal,
  TailscaleStatus,
} from "../api/remote";
import { encodeQr, qrPath } from "../lib/qr";
import { Ic } from "../ui/icons";
import {
  Badge,
  Button,
  Checkbox,
  IconButton,
  Input,
  Radio,
  Select,
  Skeleton,
  Toggle,
} from "../ui/gr8r";
import {
  Alert,
  SettingsPanel,
  SettingsTabs,
  StatusLine,
  markSettingsSaved,
} from "./settings-pages";

/* Settings > Remote access (spec 57, REQ-RA-080 through REQ-RA-083): open
   Lana from the phone through Tailscale Serve. A remote browser sees the page
   read-only (REQ-RA-040, REQ-RA-082). */

const CONTROL_OPTIONS = [5, 15, 30, 60, 120].map((m) => ({
  value: String(m),
  label: m < 60 ? m + " minutes" : m / 60 + (m === 60 ? " hour" : " hours"),
}));
const IDLE_OPTIONS = [15, 30, 60, 240, 720, 1440].map((m) => ({
  value: String(m),
  label:
    m < 60
      ? m + " minutes"
      : m === 60
        ? "1 hour"
        : m === 1440
          ? "1 day"
          : m / 60 + " hours",
}));
const MAX_AGE_OPTIONS = [24, 72, 168, 720].map((h) => ({
  value: String(h),
  label: h === 24 ? "1 day" : h / 24 + " days",
}));
const CONFIRM_OPTIONS = [
  { value: "password_or_telegram", label: "Password or Telegram code" },
  { value: "password", label: "Password only" },
];

function withValue(
  options: { value: string; label: string }[],
  value: number,
  unit: string
) {
  const key = String(value);
  return options.some((option) => option.value === key)
    ? options
    : [...options, { value: key, label: value + " " + unit }];
}

function failureText(failure: unknown): string {
  if (failure instanceof ApiError) {
    if (failure.code === "stale_version") {
      return "config.toml changed since the page loaded. Reload and try again.";
    }
    if (failure.code === "remote_forbidden") {
      return "Remote access settings can only be changed on the Mac.";
    }
  }
  return errorMessage(failure);
}

function agoText(ms: number, now: number): string {
  const diff = Math.max(0, now - ms);
  const min = Math.floor(diff / 60000);
  if (min < 1) {
    return "Active now";
  }
  if (min < 60) {
    return min + " min ago";
  }
  const hours = Math.floor(min / 60);
  if (hours < 24) {
    return hours + " h ago";
  }
  const days = Math.floor(hours / 24);
  return days === 1 ? "1 day ago" : days + " days ago";
}

type StepState = "ok" | "fail" | "warn" | "todo";

interface SetupStep {
  key: string;
  state: StepState;
  title: string;
  detail: string;
  mono?: boolean;
}

function setupSteps(status: TailscaleStatus, addr: string): SetupStep[] {
  const steps: SetupStep[] = [];
  steps.push(
    status.installed
      ? {
          key: "installed",
          state: "ok",
          title: "Tailscale app is installed",
          detail:
            "Tailscale " + (status.version || "") + " · " + (status.path || ""),
        }
      : {
          key: "installed",
          state: "fail",
          title: "Tailscale is not installed",
          detail: "Install Tailscale on this Mac and sign in to your tailnet.",
        }
  );
  steps.push(
    status.running
      ? {
          key: "running",
          state: "ok",
          title: "Tailscale is running",
          detail:
            "Signed in as " +
            (status.login || "unknown") +
            (status.tailnet ? " · " + status.tailnet : ""),
        }
      : {
          key: "running",
          state: status.installed ? "fail" : "todo",
          title: "Tailscale is stopped",
          detail: "Open the app and turn it on. This Mac then joins your tailnet.",
        }
  );
  steps.push(
    status.running && status.https_certs
      ? {
          key: "certs",
          state: "ok",
          title: "HTTPS certificates are on",
          detail: status.dns_name || "MagicDNS name ready",
        }
      : {
          key: "certs",
          state: status.running ? "warn" : "todo",
          title: "HTTPS certificates are off for your tailnet",
          detail:
            "Turn on MagicDNS and HTTPS Certificates in the Tailscale admin console, under DNS.",
        }
  );
  steps.push(
    status.serve_other
      ? {
          key: "serve",
          state: "warn",
          title: "Port 443 already serves something else",
          detail:
            "Run tailscale serve --https=443 off, or free port 443, then check again.",
        }
      : {
          key: "serve",
          state: status.serving ? "ok" : "todo",
          title: "Serve Lana over HTTPS",
          detail: "Lana runs: tailscale serve --bg --https=443 http://" + addr,
          mono: true,
        }
  );
  return steps;
}

function tailscaleReady(status: TailscaleStatus): boolean {
  return (
    status.installed && status.running && status.https_certs && !status.serve_other
  );
}

function QrCode({ text }: { text: string }) {
  const svg = useMemo(() => {
    try {
      const grid = encodeQr(text);
      return { path: qrPath(grid, 2), size: grid.length + 4 };
    } catch {
      return null;
    }
  }, [text]);
  if (!svg) {
    return null;
  }
  return (
    <svg
      className="remote-qr-svg"
      viewBox={"0 0 " + svg.size + " " + svg.size}
      role="img"
      aria-label={"QR code for " + text}
      shapeRendering="crispEdges"
    >
      <rect width={svg.size} height={svg.size} fill="#fff" />
      <path d={svg.path} fill="#000" />
    </svg>
  );
}

function StepIcon({ state }: { state: StepState }) {
  const icon =
    state === "ok"
      ? "check"
      : state === "fail"
        ? "x"
        : state === "warn"
          ? "triangle-alert"
          : "circle-dashed";
  return (
    <span className={"remote-step-ic " + state} aria-hidden="true">
      <Ic name={icon} size={13} />
    </span>
  );
}

export function SettingsRemotePage() {
  const [data, setData] = useState<RemoteAccess | null>(null);
  const [sessions, setSessions] = useState<BrowserSessionInfo[] | null>(null);
  const [me, setMe] = useState<AuthSession | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [newLogin, setNewLogin] = useState("");
  const [now, setNow] = useState(() => Date.now());

  const load = useCallback(async () => {
    setError("");
    const results = await Promise.allSettled([
      getRemoteAccess(),
      listBrowserSessions(),
      getAuthSession(),
    ]);
    if (results[0].status === "fulfilled") {
      setData(results[0].value);
      setState("ready");
    } else {
      setError(failureText(results[0].reason));
      setState("error");
    }
    if (results[1].status === "fulfilled") {
      const value = results[1].value;
      setSessions(value && Array.isArray(value.sessions) ? value.sessions : []);
    }
    if (results[2].status === "fulfilled") {
      setMe(results[2].value);
    }
    setNow(Date.now());
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const remoteBrowser = Boolean(me && me.remote);
  const locked = busy || remoteBrowser;
  const config = data ? data.config : null;
  const status = data ? data.status : null;

  async function run(label: string, action: () => Promise<void>) {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    setMessage(label);
    try {
      await action();
    } catch (failure: unknown) {
      setError(failureText(failure));
      setMessage("");
    } finally {
      setBusy(false);
    }
  }

  function update(patch: Partial<RemoteSettingsUpdate>) {
    if (!config) {
      return;
    }
    const body: RemoteSettingsUpdate = {
      expected_version: config.version,
      require_identity: config.require_identity,
      allowed_logins: config.allowed_logins || [],
      terminal: config.terminal,
      confirm_with: config.confirm_with,
      control_minutes: config.control_minutes,
      idle_minutes: config.idle_minutes,
      max_age_hours: config.max_age_hours,
      notify_sign_in: config.notify_sign_in,
      ...patch,
    };
    void run("Saving…", async () => {
      const result = await saveRemoteSettings(body);
      setData(result);
      markSettingsSaved();
      setMessage("Saved.");
    });
  }

  function setEnabled(on: boolean) {
    if (!config) {
      return;
    }
    void run(on ? "Turning on remote access…" : "Turning off remote access…", async () => {
      const result = on
        ? await enableRemoteAccess(config.version)
        : await disableRemoteAccess(config.version);
      setData(result);
      markSettingsSaved();
      setMessage(
        result.warning
          ? result.warning
          : on
            ? "Remote access is on. Open the address on your phone."
            : "Remote access is off. Remote browsers were signed out."
      );
      const list = await listBrowserSessions();
      setSessions(list.sessions || []);
    });
  }

  function addLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = newLogin.trim().toLowerCase();
    if (!value || !config) {
      return;
    }
    const logins = config.allowed_logins || [];
    if (logins.includes(value)) {
      setNewLogin("");
      return;
    }
    setNewLogin("");
    update({ allowed_logins: [...logins, value] });
  }

  function removeLogin(login: string) {
    if (!config) {
      return;
    }
    update({
      allowed_logins: (config.allowed_logins || []).filter((l) => l !== login),
    });
  }

  function signOut(row: BrowserSessionInfo) {
    void run("Signing out " + browserName(row.user_agent) + "…", async () => {
      await revokeBrowserSession(row.id);
      if (row.current) {
        window.location.reload();
        return;
      }
      const list = await listBrowserSessions();
      setSessions(list.sessions || []);
      setMessage("Signed out.");
    });
  }

  function signOutRemote() {
    void run("Signing out remote devices…", async () => {
      const result = await revokeRemoteSessions();
      const list = await listBrowserSessions();
      setSessions(list.sessions || []);
      const n = Number(result && result.revoked) || 0;
      setMessage(
        n === 0
          ? "No remote devices were signed in."
          : "Signed out " + n + " remote " + (n === 1 ? "device." : "devices.")
      );
    });
  }

  function copyUrl(url: string) {
    void navigator.clipboard
      ?.writeText(url)
      .then(() => setMessage("Address copied."))
      .catch(() => setMessage("Copy failed. Select the address and copy it."));
  }

  const addr = typeof window !== "undefined" ? window.location.host : "";
  const ready = status ? tailscaleReady(status) : false;
  const enabled = Boolean(config && config.enabled);
  const remoteRows = (sessions || []).filter((row) => row.remote);

  return (
    <main id="main" className="page settings-page">
      <SettingsTabs active="remote" />
      <SettingsPanel
        active="remote"
        title="Remote access"
        description="Open Lana from your phone over your tailnet. The daemon stays on 127.0.0.1; Tailscale serves it over HTTPS."
      >
        {state === "loading" && !data ? (
          <div className="settings-loading-inline" aria-busy="true">
            <span className="sr">Loading remote access…</span>
            <Skeleton width={180} height={14} />
            <Skeleton width="70%" height={12} />
          </div>
        ) : null}
        {remoteBrowser ? (
          <Alert
            tone="info"
            icon="lock"
            message="You are signed in over Tailscale. Change these settings on the Mac."
          />
        ) : null}
        <Alert tone="danger" message={error} />
        {state === "error" ? (
          <div className="settings-actions-bar">
            <Button variant="secondary" disabled={busy} onClick={() => void load()}>
              Reload
            </Button>
          </div>
        ) : null}
        <StatusLine text={message} />

        {config && status ? (
          <>
            <section className="sblock" aria-labelledby="remote-state-title">
              <h2 id="remote-state-title">Remote access</h2>
              <div className="srow">
                <div>
                  <div className="t remote-state">
                    {enabled ? "On" : "Off"}
                    {enabled ? (
                      status.serving ? (
                        <Badge tone="green" dot>
                          Reachable
                        </Badge>
                      ) : (
                        <Badge tone="amber" dot>
                          Serve is not running
                        </Badge>
                      )
                    ) : ready ? (
                      <Badge tone="gray">Ready</Badge>
                    ) : (
                      <Badge tone="amber" dot>
                        Setup needed
                      </Badge>
                    )}
                  </div>
                  <div className={"d" + (enabled ? " mono" : "")}>
                    {enabled
                      ? "tailscale serve · https:443 → " + addr
                      : ready
                        ? "Turn it on to serve Lana at https://" + status.dns_name + "."
                        : "Finish the steps below, then turn it on. Nothing is reachable from other devices yet."}
                  </div>
                </div>
                <Toggle
                  aria-label="Remote access"
                  checked={enabled}
                  disabled={locked || (!enabled && !ready)}
                  onChange={setEnabled}
                />
              </div>
            </section>

            {!enabled && !ready ? (
              <section className="sblock" aria-labelledby="remote-setup-title">
                <h2 id="remote-setup-title">Setup</h2>
                <ol className="remote-steps">
                  {setupSteps(status, addr).map((step) => (
                    <li key={step.key} className="remote-step">
                      <StepIcon state={step.state} />
                      <div className="grow">
                        <div className="t">{step.title}</div>
                        <div className={"d" + (step.mono ? " mono" : "")}>
                          {step.detail}
                        </div>
                      </div>
                      {step.key === "running" && step.state !== "ok" && status.installed ? (
                        <Button
                          variant="secondary"
                          size="sm"
                          disabled={locked}
                          onClick={() =>
                            void run("Opening Tailscale…", async () => {
                              await openTailscaleApp();
                              setMessage("Tailscale opened. Turn it on, then check again.");
                            })
                          }
                        >
                          Open Tailscale
                        </Button>
                      ) : null}
                      {step.key === "certs" && step.state === "warn" ? (
                        <a
                          className="btn btn-secondary btn-sm"
                          href={TAILSCALE_ADMIN_DNS_URL}
                          target="_blank"
                          rel="noreferrer"
                        >
                          Open admin console
                        </a>
                      ) : null}
                      {step.key === "serve" && !status.serve_other ? (
                        <Button variant="primary" size="sm" disabled>
                          Enable
                        </Button>
                      ) : null}
                    </li>
                  ))}
                </ol>
                <div className="remote-footer">
                  <span className="d">
                    Last checked {agoText(data ? data.checked_at : now, now).toLowerCase()}
                  </span>
                  <Button
                    variant="secondary"
                    size="sm"
                    icon="rotate-cw"
                    disabled={busy}
                    onClick={() => void run("Checking…", async () => {
                      await load();
                      setMessage("Checked.");
                    })}
                  >
                    Check again
                  </Button>
                </div>
                <div className="remote-note">
                  <Ic name="lock" size={14} />
                  <div>
                    <div className="t">Private to your tailnet</div>
                    <div className="d">
                      Lana uses tailscale serve, never Funnel, so the address is
                      not on the public internet. The daemon keeps listening on
                      127.0.0.1 only.
                    </div>
                  </div>
                </div>
              </section>
            ) : null}

            {enabled && data && data.url ? (
              <section className="sblock" aria-labelledby="remote-address-title">
                <h2 id="remote-address-title">Address</h2>
                <div className="remote-address">
                  <div className="grow">
                    <p className="d">
                      Open this on your phone. Only devices in your tailnet can
                      reach it.
                    </p>
                    <div className="remote-url">
                      <Input
                        className="mono"
                        readOnly
                        aria-label="Remote address"
                        value={data.url}
                        onFocus={(event) => event.currentTarget.select()}
                      />
                      <Button variant="secondary" icon="copy" onClick={() => copyUrl(data.url)}>
                        Copy
                      </Button>
                      <a
                        className="btn btn-secondary"
                        href={data.url}
                        target="_blank"
                        rel="noreferrer"
                      >
                        Open
                      </a>
                    </div>
                    <ul className="remote-checks">
                      <li>
                        <span className={"dot " + (status.running ? "ok" : "bad")} />
                        {status.running
                          ? "Tailscale running · signed in as " + (status.login || "unknown")
                          : "Tailscale is stopped"}
                      </li>
                      <li>
                        <span className={"dot " + (status.https_certs ? "ok" : "bad")} />
                        {status.https_certs
                          ? "HTTPS certificate from Tailscale"
                          : "HTTPS certificates are off"}
                      </li>
                      <li>
                        <span className={"dot " + (status.serving ? "ok" : "bad")} />
                        {status.serving
                          ? "Daemon still listens on 127.0.0.1 only"
                          : "tailscale serve is not running. Turn remote access off and on again."}
                      </li>
                    </ul>
                    <p className="d remote-install">
                      Install Lana as a phone app: on iPhone, open the address in
                      Safari and tap Share, then Add to Home Screen. On Android,
                      open it in Chrome and tap Install app.
                    </p>
                  </div>
                  <figure className="remote-qr">
                    <QrCode text={data.url} />
                    <figcaption>Scan with your phone</figcaption>
                  </figure>
                </div>
              </section>
            ) : null}

            {enabled || ready ? (
              <RemoteSettings
                config={config}
                locked={locked}
                newLogin={newLogin}
                onNewLogin={setNewLogin}
                onAddLogin={addLogin}
                onRemoveLogin={removeLogin}
                onUpdate={update}
              />
            ) : null}

            <section className="sblock" aria-labelledby="remote-devices-title">
              <h2 id="remote-devices-title">Signed-in devices</h2>
              <p className="d remote-lead">
                Every browser with an owner session. Signing a device out ends
                its session at once.
              </p>
              <div className="remote-devices" role="table" aria-label="Signed-in devices">
                {(sessions || []).map((row) => {
                  const unlocked =
                    row.remote && row.control_until && row.control_until > now;
                  return (
                    <div className="remote-device" role="row" key={row.id}>
                      <span className="remote-device-ic" aria-hidden="true">
                        <Ic name={row.remote ? "smartphone" : "laptop"} size={15} />
                      </span>
                      <div className="grow" role="cell">
                        <div className="t">
                          {browserName(row.user_agent)}
                          <Badge tone={row.remote ? "accent" : "gray"}>
                            {row.remote ? "Remote" : "Local"}
                          </Badge>
                        </div>
                        <div className="d">
                          {row.remote
                            ? [row.device, row.login, row.ip].filter(Boolean).join(" · ")
                            : "This Mac"}
                          {row.current ? " · this browser" : ""}
                        </div>
                        {row.remote ? (
                          <div className={"d remote-control" + (unlocked ? " on" : "")}>
                            {unlocked
                              ? "Control unlocked · " +
                                Math.max(1, Math.ceil(((row.control_until || 0) - now) / 60000)) +
                                " min left"
                              : config.terminal === "full"
                                ? "Full control"
                                : "View only"}
                          </div>
                        ) : null}
                      </div>
                      <div className="remote-device-last" role="cell">
                        {agoText(row.last_used_at, now)}
                      </div>
                      <div role="cell">
                        {row.current && !row.remote ? (
                          <span className="d">This browser</span>
                        ) : (
                          <Button
                            variant="secondary"
                            size="sm"
                            disabled={busy}
                            onClick={() => signOut(row)}
                          >
                            Sign out
                          </Button>
                        )}
                      </div>
                    </div>
                  );
                })}
                {sessions && sessions.length === 0 ? (
                  <div className="remote-device d">No browsers are signed in.</div>
                ) : null}
              </div>
              <div className="remote-footer">
                <span className="d">
                  {remoteRows.length === 0
                    ? "No remote devices signed in"
                    : remoteRows.length +
                      " remote " +
                      (remoteRows.length === 1 ? "device" : "devices") +
                      " signed in"}
                </span>
                <Button
                  variant="danger-ghost"
                  disabled={busy || remoteRows.length === 0}
                  onClick={signOutRemote}
                >
                  Sign out all remote devices
                </Button>
              </div>
            </section>
          </>
        ) : null}
      </SettingsPanel>
    </main>
  );
}

interface RemoteSettingsProps {
  config: RemoteConfig;
  locked: boolean;
  newLogin: string;
  onNewLogin: (value: string) => void;
  onAddLogin: (event: FormEvent<HTMLFormElement>) => void;
  onRemoveLogin: (login: string) => void;
  onUpdate: (patch: Partial<RemoteSettingsUpdate>) => void;
}

const TERMINAL_CHOICES: {
  value: RemoteTerminal;
  title: string;
  detail: string;
  recommended?: boolean;
}[] = [
  {
    value: "view",
    title: "View only",
    detail: "Watch terminals and agents. Typing and resize are off.",
  },
  {
    value: "confirm",
    title: "Control after confirming",
    detail:
      "Type after you enter your password again or a Telegram code. Control locks again after the time below.",
    recommended: true,
  },
  {
    value: "full",
    title: "Full control",
    detail: "Type right away, like on this Mac. Not recommended for a phone.",
  },
];

function RemoteSettings({
  config,
  locked,
  newLogin,
  onNewLogin,
  onAddLogin,
  onRemoveLogin,
  onUpdate,
}: RemoteSettingsProps) {
  const logins = config.allowed_logins || [];
  return (
    <>
      <section className="sblock" aria-labelledby="remote-who-title">
        <h2 id="remote-who-title">Who can sign in</h2>
        <label className="remote-check">
          <Checkbox
            checked={config.require_identity}
            disabled={locked}
            onChange={(checked) => onUpdate({ require_identity: checked })}
          />
          <span>
            <span className="t">Require Tailscale identity</span>
            <span className="d">
              Lana reads Tailscale-User-Login from tailscale serve. Requests
              without an allowed login are refused before the password.
            </span>
          </span>
        </label>
        <div className="remote-logins">
          {logins.map((login) => (
            <div className="remote-login" key={login}>
              <span className="remote-login-av" aria-hidden="true">
                {login.slice(0, 1).toUpperCase()}
              </span>
              <span className="grow mono">{login}</span>
              <IconButton
                icon="x"
                size="sm"
                label={"Remove " + login}
                disabled={locked}
                onClick={() => onRemoveLogin(login)}
              />
            </div>
          ))}
          {logins.length === 0 ? (
            <div className="remote-login d">
              No accounts yet. Turning remote access on adds your Tailscale
              login.
            </div>
          ) : null}
          <form className="remote-login add" onSubmit={onAddLogin}>
            <Input
              className="mono"
              placeholder="name@github"
              aria-label="Tailscale login to allow"
              value={newLogin}
              disabled={locked}
              onChange={(event) => onNewLogin(event.target.value)}
            />
            <Button type="submit" variant="secondary" icon="plus" disabled={locked || !newLogin.trim()}>
              Add account
            </Button>
          </form>
        </div>
      </section>

      <section className="sblock" aria-labelledby="remote-terminal-title">
        <h2 id="remote-terminal-title">Terminal from remote</h2>
        <p className="d remote-lead">
          Workbench is a full shell on this Mac. Choose what a browser signed in
          over Tailscale can do.
        </p>
        <div className="remote-choices" role="radiogroup" aria-label="Terminal from remote">
          {TERMINAL_CHOICES.map((choice) => {
            const on = config.terminal === choice.value;
            return (
              <div key={choice.value} className={"remote-choice" + (on ? " on" : "")}>
                <label className="remote-choice-head">
                  <Radio
                    name="remote-terminal"
                    checked={on}
                    disabled={locked}
                    onChange={() => onUpdate({ terminal: choice.value })}
                  />
                  <span className="grow">
                    <span className="t">
                      {choice.title}
                      {choice.recommended ? <Badge tone="accent">Recommended</Badge> : null}
                    </span>
                    <span className="d">{choice.detail}</span>
                  </span>
                </label>
                {on && choice.value === "confirm" ? (
                  <div className="remote-choice-opts">
                    <label>
                      <span className="t">Confirm with</span>
                      <Select
                        options={CONFIRM_OPTIONS}
                        value={config.confirm_with}
                        disabled={locked}
                        onChange={(value) =>
                          onUpdate({ confirm_with: value as RemoteSettingsUpdate["confirm_with"] })
                        }
                      />
                    </label>
                    <label>
                      <span className="t">Control lasts</span>
                      <Select
                        options={withValue(CONTROL_OPTIONS, config.control_minutes, "minutes")}
                        value={String(config.control_minutes)}
                        disabled={locked}
                        onChange={(value) => onUpdate({ control_minutes: Number(value) })}
                      />
                    </label>
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      </section>

      <section className="sblock" aria-labelledby="remote-sessions-title">
        <h2 id="remote-sessions-title">Remote sessions</h2>
        <p className="d remote-lead">
          Limits for browsers signed in over Tailscale. Browsers on this Mac keep
          the 12 hour idle limit.
        </p>
        <div className="remote-limits">
          <label>
            <span className="t">Sign out after idle</span>
            <Select
              options={withValue(IDLE_OPTIONS, config.idle_minutes, "minutes")}
              value={String(config.idle_minutes)}
              disabled={locked}
              onChange={(value) => onUpdate({ idle_minutes: Number(value) })}
            />
            <span className="d">No request for this long ends the session.</span>
          </label>
          <label>
            <span className="t">Sign in again after</span>
            <Select
              options={withValue(MAX_AGE_OPTIONS, config.max_age_hours, "hours")}
              value={String(config.max_age_hours)}
              disabled={locked}
              onChange={(value) => onUpdate({ max_age_hours: Number(value) })}
            />
            <span className="d">Even an active session ends then.</span>
          </label>
        </div>
        <div className="srow">
          <div>
            <div className="t">Tell me about new remote sign-ins</div>
            <div className="d">
              A macOS notification and a Telegram message for every sign-in
              over Tailscale.
            </div>
          </div>
          <Toggle
            aria-label="Notify on remote sign-in"
            checked={config.notify_sign_in}
            disabled={locked}
            onChange={(checked) => onUpdate({ notify_sign_in: checked })}
          />
        </div>
      </section>
    </>
  );
}
