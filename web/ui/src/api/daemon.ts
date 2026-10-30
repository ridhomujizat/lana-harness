import { request } from "./client";

/* Daemon calls of API-WEB-022 and API-WEB-023. */

export interface DaemonTurn {
  session_id: string;
  title: string;
  agent: string;
}

export interface DaemonInfo {
  pid: number;
  started_at: number;
  uptime_ms: number;
  addr: string;
  home: string;
  version: string;
  go_version: string;
  os: string;
  arch: string;
  restart_supported: boolean;
  running_turns: DaemonTurn[];
  browsers_signed_in: number;
}

export interface RestartDaemonResponse {
  restart: string;
  running_turns: number;
}

export function daemonInfo(): Promise<DaemonInfo> {
  return request<DaemonInfo>("GET", "/v1/daemon");
}

export function restartDaemon(): Promise<RestartDaemonResponse> {
  return request<RestartDaemonResponse>("POST", "/v1/daemon/restart", {
    confirm: true,
  });
}
