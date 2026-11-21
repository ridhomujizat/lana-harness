import { request } from "./client";
import type { WorkbenchPreview } from "./workbench";

/* Spec 66: git data for the Workbench panel (Files, Changes, Worktrees).
   The daemon runs git; see docs/specs/66-workbench-git/api-contract.md. */

export interface GitInfo {
  repo: string;
  worktree: string;
  branch: string;
  short: string;
  head: string;
  color: string;
  detached: boolean;
  main: boolean;
}

/* A dir outside any repo maps to {repo: null}; a refused one to {error}. */
export type GitResolved = GitInfo | { repo: null } | { error: string };

export function isGitInfo(value: GitResolved | undefined): value is GitInfo {
  return !!value && typeof (value as GitInfo).repo === "string" && !!(value as GitInfo).worktree;
}

export function resolveGitDirs(dirs: string[]): Promise<{ dirs: Record<string, GitResolved> }> {
  return request("POST", "/v1/workbench/git/resolve", { dirs });
}

export type GitScope = "uncommitted" | "branch" | "last_commit" | `commit:${string}`;

export type GitFileStatus = "M" | "A" | "D" | "R" | "U" | "C";

export interface GitFileChange {
  path: string;
  old_path?: string;
  status: GitFileStatus;
  add: number;
  del: number;
  binary: boolean;
}

export interface GitCommit {
  sha: string;
  subject: string;
  time: string;
}

export interface GitStatus {
  worktree: string;
  branch: string;
  upstream?: string;
  ahead: number;
  behind: number;
  scope: string;
  base?: string;
  merge_base?: string;
  commits?: GitCommit[];
  files: GitFileChange[];
  totals: { files: number; add: number; del: number };
}

/* The status keeps its ETag so a poll that changed nothing costs a 304. */
const etags = new Map<string, { etag: string; body: GitStatus }>();

export async function getGitStatus(worktree: string, scope: GitScope): Promise<GitStatus> {
  const path = "/v1/workbench/git/status?" + new URLSearchParams({ worktree, scope }).toString();
  const cached = etags.get(path);
  const res = await fetch(path, {
    credentials: "same-origin",
    headers: cached ? { "If-None-Match": cached.etag } : undefined,
  });
  if (res.status === 304 && cached) return cached.body;
  if (!res.ok) {
    /* Let the shared client build the ApiError from the envelope. */
    return request("GET", path);
  }
  const body = (await res.json()) as GitStatus;
  const etag = res.headers.get("ETag");
  if (etag) etags.set(path, { etag, body });
  return body;
}

export interface GitDiffLine {
  kind: "ctx" | "add" | "del";
  old?: number;
  new?: number;
  text: string;
  no_newline?: boolean;
}

export interface GitHunk {
  header: string;
  old_start: number;
  new_start: number;
  lines: GitDiffLine[];
}

export interface GitDiff {
  path: string;
  status: GitFileStatus;
  binary: boolean;
  truncated: boolean;
  hunks: GitHunk[];
}

export function getGitDiff(worktree: string, path: string, scope: GitScope): Promise<GitDiff> {
  return request("GET", "/v1/workbench/git/diff?" + new URLSearchParams({ worktree, path, scope }).toString());
}

export interface GitTreeEntry {
  name: string;
  kind: "dir" | "file";
  status?: GitFileStatus;
  changed?: boolean;
  ignored: boolean;
  size?: number;
}

export function getGitTree(worktree: string, dir: string): Promise<{ dir: string; truncated: boolean; entries: GitTreeEntry[] }> {
  return request("GET", "/v1/workbench/git/tree?" + new URLSearchParams({ worktree, dir }).toString());
}

export function searchGitFiles(worktree: string, q: string): Promise<{ files: string[] }> {
  return request("GET", "/v1/workbench/git/search?" + new URLSearchParams({ worktree, q }).toString());
}

export interface GitTextFile {
  path: string;
  kind: "text";
  size: number;
  lines: number;
  language: string;
  text: string;
}

export type GitFileContent = GitTextFile | WorkbenchPreview;

/* A text file carries its text; every other kind is a spec 61 preview. */
export function isTextFile(f: GitFileContent): f is GitTextFile {
  return typeof (f as GitTextFile).text === "string";
}

export function getGitFile(worktree: string, path: string): Promise<GitFileContent> {
  return request("GET", "/v1/workbench/git/file?" + new URLSearchParams({ worktree, path }).toString());
}

export interface GitWorktree {
  path: string;
  name: string;
  branch: string;
  head: string;
  head_time: string;
  main: boolean;
  locked: boolean;
  prunable: boolean;
  dirty: number;
  ahead: number;
  merged: boolean;
  agent: boolean;
  location: "main" | "container" | "sibling" | "other";
  color: string;
  error: string;
}

export interface GitWorktreeList {
  repo: string;
  base: string;
  root: string;
  worktrees: GitWorktree[];
}

export function listGitWorktrees(repo: string): Promise<GitWorktreeList> {
  return request("GET", "/v1/workbench/git/worktrees?" + new URLSearchParams({ repo }).toString());
}

export function getCopyCandidates(repo: string): Promise<{ files: { path: string; size: number; default: boolean }[] }> {
  return request("GET", "/v1/workbench/git/worktrees/copy-candidates?" + new URLSearchParams({ repo }).toString());
}

export function suggestWorktreePath(repo: string, branch: string): Promise<{ path: string }> {
  return request("GET", "/v1/workbench/git/worktrees/name?" + new URLSearchParams({ repo, branch }).toString());
}

export interface CreateWorktreeInput {
  repo: string;
  branch: string;
  new_branch: boolean;
  base: string;
  fetch: boolean;
  path: string;
  copy: string[];
}

export function createGitWorktree(input: CreateWorktreeInput): Promise<GitWorktree> {
  return request("POST", "/v1/workbench/git/worktrees", input);
}

export interface RemoveWorktreeInput {
  path: string;
  force: boolean;
  confirm?: string;
  delete_branch: boolean;
}

export function removeGitWorktree(input: RemoveWorktreeInput): Promise<{ removed: boolean; branch_deleted: boolean }> {
  return request("POST", "/v1/workbench/git/worktrees/remove", input);
}

export interface WorktreeResult {
  path: string;
  result: "removed" | "moved" | "skipped" | "failed";
  reason?: string;
}

export function cleanupGitWorktrees(paths: string[], deleteBranch: boolean, deleteRemote: boolean): Promise<{ results: WorktreeResult[] }> {
  return request("POST", "/v1/workbench/git/worktrees/cleanup", { paths, delete_branch: deleteBranch, delete_remote: deleteRemote });
}

export function moveGitWorktrees(moves: { path: string; to: string }[]): Promise<{ results: WorktreeResult[] }> {
  return request("POST", "/v1/workbench/git/worktrees/move", { moves });
}

export function pruneGitWorktrees(repo: string): Promise<{ pruned: string[] }> {
  return request("POST", "/v1/workbench/git/worktrees/prune", { repo });
}

export function lockGitWorktree(path: string, locked: boolean): Promise<GitWorktree> {
  return request("POST", "/v1/workbench/git/worktrees/lock", { path, locked });
}

export interface GitSettings {
  base: string;
  worktree_root: string;
  warn_shared: boolean;
}

export function getGitSettings(repo: string): Promise<GitSettings> {
  return request("GET", "/v1/workbench/git/settings?" + new URLSearchParams({ repo }).toString());
}

export function putGitSettings(repo: string, settings: Partial<GitSettings>): Promise<GitSettings> {
  return request("PUT", "/v1/workbench/git/settings?" + new URLSearchParams({ repo }).toString(), settings);
}
