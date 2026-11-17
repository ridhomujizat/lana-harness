import { request } from "./client";

/* Remote access over Tailscale (spec 57, REQ-RA-060 through REQ-RA-064, and
   the control calls of REQ-RA-032 through REQ-RA-034). */

export type RemoteTerminal = "view" | "confirm" | "full";
export type RemoteConfirmWith = "password" | "password_or_telegram";

export interface RemoteConfig {
  version: string;
  enabled: boolean;
  host: string;
  require_identity: boolean;
  allowed_logins: string[];
  terminal: RemoteTerminal;
  confirm_with: RemoteConfirmWith;
  control_minutes: number;
  idle_minutes: number;
  max_age_hours: number;
  notify_sign_in: boolean;
}

export interface TailscaleStatus {
  installed: boolean;
  path: string;
  version: string;
  running: boolean;
  dns_name: string;
  login: string;
  tailnet: string;
  magic_dns: boolean;
  https_certs: boolean;
  serving: boolean;
  serve_other: boolean;
}

export interface RemoteAccess {
  config: RemoteConfig;
  status: TailscaleStatus;
  url: string;
  checked_at: number;
  warning?: string;
}

export interface RemoteSettingsUpdate {
  expected_version: string;
  require_identity: boolean;
  allowed_logins: string[];
  terminal: RemoteTerminal;
  confirm_with: RemoteConfirmWith;
  control_minutes: number;
  idle_minutes: number;
  max_age_hours: number;
  notify_sign_in: boolean;
}

export function getRemoteAccess(): Promise<RemoteAccess> {
  return request<RemoteAccess>("GET", "/v1/web/remote");
}

export function saveRemoteSettings(
  update: RemoteSettingsUpdate
): Promise<RemoteAccess> {
  return request<RemoteAccess>("PUT", "/v1/web/remote", update);
}

export function enableRemoteAccess(
  expectedVersion: string
): Promise<RemoteAccess> {
  return request<RemoteAccess>("POST", "/v1/web/remote/enable", {
    expected_version: expectedVersion,
  });
}

export function disableRemoteAccess(
  expectedVersion: string
): Promise<RemoteAccess> {
  return request<RemoteAccess>("POST", "/v1/web/remote/disable", {
    expected_version: expectedVersion,
  });
}

export function openTailscaleApp(): Promise<null> {
  return request<null>("POST", "/v1/web/remote/open-app", {});
}

export interface ControlUnlockResponse {
  control_until: number;
}

/* REQ-RA-032: exactly one of password or code. */
export function unlockControl(
  credentials: { password: string } | { code: string }
): Promise<ControlUnlockResponse> {
  return request<ControlUnlockResponse>(
    "POST",
    "/v1/auth/control/unlock",
    credentials
  );
}

export function sendControlCode(): Promise<{ sent_to: number }> {
  return request<{ sent_to: number }>("POST", "/v1/auth/control/telegram", {});
}

export function lockControl(): Promise<null> {
  return request<null>("POST", "/v1/auth/control/lock", {});
}

export function revokeBrowserSession(id: string): Promise<null> {
  return request<null>(
    "POST",
    "/v1/auth/sessions/" + encodeURIComponent(id) + "/revoke",
    {}
  );
}

export function revokeRemoteSessions(): Promise<{ revoked: number }> {
  return request<{ revoked: number }>(
    "POST",
    "/v1/auth/sessions/revoke-remote",
    {}
  );
}

/* TAILSCALE_ADMIN_DNS_URL is where MagicDNS and HTTPS certificates are
   turned on (REQ-RA-081). */
export const TAILSCALE_ADMIN_DNS_URL = "https://login.tailscale.com/admin/dns";

/* controlUnlocked reports whether a remote session may type now. */
export function controlUnlocked(
  terminal: RemoteTerminal | undefined,
  controlUntil: number | undefined,
  now: number
): boolean {
  if (terminal === "full") {
    return true;
  }
  if (terminal === "confirm") {
    return Boolean(controlUntil && controlUntil > now);
  }
  return false;
}

/* browserName turns a User-Agent into "Safari · iPhone" style text. */
export function browserName(userAgent: string): string {
  const ua = userAgent || "";
  let browser = "Browser";
  if (/Edg\//.test(ua)) {
    browser = "Edge";
  } else if (/Firefox\/|FxiOS\//.test(ua)) {
    browser = "Firefox";
  } else if (/Chrome\/|CriOS\//.test(ua)) {
    browser = "Chrome";
  } else if (/Safari\//.test(ua)) {
    browser = "Safari";
  } else if (/LanaMenu|Lana\//.test(ua)) {
    browser = "Lana app";
  }
  let os = "";
  if (/iPhone/.test(ua)) {
    os = "iPhone";
  } else if (/iPad/.test(ua)) {
    os = "iPad";
  } else if (/Android/.test(ua)) {
    os = "Android";
  } else if (/Mac OS X|Macintosh/.test(ua)) {
    os = "macOS";
  } else if (/Windows/.test(ua)) {
    os = "Windows";
  } else if (/Linux/.test(ua)) {
    os = "Linux";
  }
  return os ? browser + " · " + os : browser;
}
