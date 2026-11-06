import { request } from "./client";

/* Auth calls of API-WLG-001 through API-WLG-007, plus the browser session
   calls of API-WEB-002 and API-WEB-024. */

/* GET /v1/auth/state (API-WLG-001): how the owner can reach the dashboard. */
export interface AuthState {
  password_set: boolean;
  telegram_reset: boolean;
  locked_until: number | null;
}

export interface AuthSession {
  csrf: string;
  expires_at: number;
}

/* The sign-in endpoints answer with the CSRF value of the new browser
   session; the response also sets the session cookie. */
export interface CsrfResponse {
  csrf: string;
}

/* API-WLG-003 takes exactly one of password (once a password is set) or token
   (only while no password is set). The dashboard posts the password. */
export type LoginCredentials = { password: string } | { token: string };

export interface ResetTelegramResponse {
  sent_to: number;
}

export interface BrowserSessionInfo {
  id: string;
  user_agent: string;
  created_at: number;
  last_used_at: number;
  current: boolean;
}

export interface ListBrowserSessionsResponse {
  sessions: BrowserSessionInfo[];
}

export interface RevokeOtherBrowsersResponse {
  revoked: number;
}

export function authState(): Promise<AuthState> {
  return request<AuthState>("GET", "/v1/auth/state");
}

/* API-WLG-002: a valid sign-in code plus a new password sets the password,
   consumes the code, and signs this browser in. */
export function claim(code: string, password: string): Promise<CsrfResponse> {
  return request<CsrfResponse>("POST", "/v1/auth/claim", { code, password });
}

export function login(credentials: LoginCredentials): Promise<CsrfResponse> {
  return request<CsrfResponse>("POST", "/v1/auth/login", credentials);
}

/* API-WLG-004: send the reset code to every owner private chat. */
export function resetTelegram(): Promise<ResetTelegramResponse> {
  return request<ResetTelegramResponse>("POST", "/v1/auth/reset/telegram", {});
}

/* API-WLG-005: the Telegram code plus a new password signs this browser in. */
export function resetVerify(
  code: string,
  password: string
): Promise<CsrfResponse> {
  return request<CsrfResponse>("POST", "/v1/auth/reset/verify", {
    code,
    password,
  });
}

/* API-WLG-006: mark reset_requested so the next daemon start opens a link. */
export function resetRestart(): Promise<null> {
  return request<null>("POST", "/v1/auth/reset/restart", {});
}

/* API-WLG-007: change the password of the signed-in owner. Setting the new
   password ends every other browser session. */
export function changePassword(
  current: string,
  password: string
): Promise<null> {
  return request<null>("POST", "/v1/auth/password", { current, password });
}

export function authSession(): Promise<AuthSession> {
  return request<AuthSession>("GET", "/v1/auth/session");
}

export function getAuthSession(): Promise<AuthSession> {
  return authSession();
}

export function logout(): Promise<null> {
  return request<null>("POST", "/v1/auth/logout");
}

export function listBrowserSessions(): Promise<ListBrowserSessionsResponse> {
  return request<ListBrowserSessionsResponse>("GET", "/v1/auth/sessions");
}

export function revokeOtherBrowsers(): Promise<RevokeOtherBrowsersResponse> {
  return request<RevokeOtherBrowsersResponse>(
    "POST",
    "/v1/auth/sessions/revoke-others"
  );
}
