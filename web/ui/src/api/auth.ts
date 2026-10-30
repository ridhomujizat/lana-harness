import { request } from "./client";

/* Browser session calls of API-WEB-002 and API-WEB-024. */

export interface LoginRequest {
  token: string;
}

export interface AuthSession {
  csrf: string;
  expires_at: number;
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

export function login(token: string): Promise<AuthSession | null> {
  return request<AuthSession | null>("POST", "/v1/auth/login", { token });
}

export function authSession(): Promise<AuthSession> {
  return request<AuthSession>("GET", "/v1/auth/session");
}

export function logout(): Promise<null> {
  return request<null>("POST", "/v1/auth/logout");
}

export function getAuthSession(): Promise<AuthSession> {
  return authSession();
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
