import { request } from "./client";

/* Spec 67: the read-only FigCraft viewer. The daemon reads a .figcraft file
   (or the FigCraft app that has it open, in live mode) with figcraft-cli and
   answers page lists, layer trees and PNG renders (api-contract.md). */

export interface FigRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface FigLayer {
  id: number;
  name: string;
  type: string;
  bounds: FigRect;
  child_count: number;
  visible: boolean;
}

export interface FigPage {
  id: number;
  name: string;
  divider: boolean;
  background?: string;
  origin: { x: number; y: number };
  size: { width: number; height: number };
  layers: FigLayer[];
}

export interface FigInfo {
  path: string;
  source: "file" | "live";
  live_port: number;
  live_error: string;
  fingerprint: string;
  unsaved: boolean;
  stale: boolean;
  cli: { path: string; version: string } | null;
  app: { version: string } | null;
  warnings: string[];
  pages: FigPage[];
}

export interface FigChildren {
  node: number;
  fingerprint: string;
  children: FigLayer[];
}

export interface FigRender {
  url: string;
  originX: number;
  originY: number;
  scale: number;
  width: number;
  height: number;
  fingerprint: string;
  stale: boolean;
  ms: number;
}

export interface FigSettings {
  cli_path: string;
  live: boolean;
  live_ports: number[];
}

function query(worktree: string, path: string, extra: Record<string, string> = {}): string {
  return new URLSearchParams({ worktree, path, ...extra }).toString();
}

/* Info keeps its ETag, so a 2 s poll that changed nothing costs a 304
   (REQ-FV-021, REQ-FV-067). */
const etags = new Map<string, { etag: string; body: FigInfo }>();

export async function getFigInfo(worktree: string, path: string, force = false): Promise<FigInfo> {
  const url = "/v1/workbench/figcraft/info?" + query(worktree, path);
  const cached = force ? undefined : etags.get(url);
  const res = await fetch(url, {
    credentials: "same-origin",
    headers: cached ? { "If-None-Match": cached.etag } : undefined,
  });
  if (res.status === 304 && cached) return cached.body;
  if (!res.ok) return request("GET", url);
  const body = (await res.json()) as FigInfo;
  const etag = res.headers.get("ETag");
  if (etag) etags.set(url, { etag, body });
  return body;
}

export function getFigLayers(worktree: string, path: string, node: number): Promise<FigChildren> {
  return request("GET", "/v1/workbench/figcraft/layers?" + query(worktree, path, { node: String(node) }));
}

/* A render comes back as a PNG with its placement in headers (REQ-FV-022).
   The caller owns the object URL and revokes it when the image is replaced. */
export async function getFigRender(
  worktree: string,
  path: string,
  target: { page: number } | { node: number },
  scale: number,
  fingerprint: string,
  signal?: AbortSignal
): Promise<FigRender> {
  const extra: Record<string, string> = { scale: scale.toFixed(2), fp: fingerprint };
  if ("page" in target) extra.page = String(target.page);
  else extra.node = String(target.node);
  const url = "/v1/workbench/figcraft/render?" + query(worktree, path, extra);
  const res = await fetch(url, { credentials: "same-origin", signal });
  if (!res.ok) return request("GET", url);
  const blob = await res.blob();
  const num = (h: string) => Number(res.headers.get(h) ?? 0) || 0;
  const bitmap = await imageSize(blob);
  return {
    url: URL.createObjectURL(blob),
    originX: num("X-Figcraft-Origin-X"),
    originY: num("X-Figcraft-Origin-Y"),
    scale: num("X-Figcraft-Scale") || scale,
    width: bitmap.width,
    height: bitmap.height,
    fingerprint: res.headers.get("X-Figcraft-Fingerprint") ?? fingerprint,
    stale: res.headers.get("X-Figcraft-Stale") === "1",
    ms: num("X-Figcraft-Render-Ms"),
  };
}

async function imageSize(blob: Blob): Promise<{ width: number; height: number }> {
  if (typeof createImageBitmap === "function") {
    const bmp = await createImageBitmap(blob);
    const size = { width: bmp.width, height: bmp.height };
    bmp.close();
    return size;
  }
  return { width: 0, height: 0 };
}

export function openInFigcraft(worktree: string, path: string): Promise<unknown> {
  return request("POST", "/v1/workbench/figcraft/open", { worktree, path });
}

export function getFigSettings(): Promise<FigSettings> {
  return request("GET", "/v1/workbench/figcraft/settings");
}

export function putFigSettings(settings: FigSettings): Promise<FigSettings> {
  return request("PUT", "/v1/workbench/figcraft/settings", settings);
}
