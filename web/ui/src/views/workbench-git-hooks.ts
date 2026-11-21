import { useCallback, useEffect, useRef, useState } from "react";

import { errorMessage } from "../api/client";
import { getGitStatus, resolveGitDirs, type GitResolved, type GitScope, type GitStatus } from "../api/workbench-git";

/* Spec 66 REQ-WG-070: the git worktree of each session directory, resolved in
   batches. A module cache keeps answers across remounts; directories are
   asked again after 30 s so a branch switch shows up. */

const RESOLVE_TTL_MS = 30_000;
const resolved = new Map<string, { at: number; value: GitResolved }>();

export function useGitResolve(dirs: string[]): { map: Record<string, GitResolved>; refresh: () => void } {
  const [, setTick] = useState(0);
  const key = Array.from(new Set(dirs.filter(Boolean))).sort().join("\n");
  const [nonce, setNonce] = useState(0);
  useEffect(() => {
    const list = key ? key.split("\n") : [];
    const now = Date.now();
    const stale = list.filter((d) => {
      const hit = resolved.get(d);
      return !hit || now - hit.at > RESOLVE_TTL_MS || nonce > 0;
    });
    if (!stale.length) return;
    let cancelled = false;
    /* REQ-WG-070: debounced 300 ms; the contract allows 64 dirs per call. */
    const timer = window.setTimeout(() => {
      const batches: string[][] = [];
      for (let i = 0; i < stale.length; i += 64) batches.push(stale.slice(i, i + 64));
      Promise.all(batches.map((b) => resolveGitDirs(b)))
        .then((answers) => {
          if (cancelled) return;
          const at = Date.now();
          for (const a of answers) for (const [d, v] of Object.entries(a.dirs || {})) resolved.set(d, { at, value: v });
          setTick((t) => t + 1);
        })
        .catch(() => {
          /* The panel shows its own errors; pane chips just stay empty. */
        });
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [key, nonce]);
  /* Re-resolve on a timer so branch switches inside a session show up. */
  useEffect(() => {
    const timer = window.setInterval(() => setNonce((n) => n + 1), RESOLVE_TTL_MS);
    return () => window.clearInterval(timer);
  }, []);
  const map: Record<string, GitResolved> = {};
  for (const d of key ? key.split("\n") : []) {
    const hit = resolved.get(d);
    if (hit) map[d] = hit.value;
  }
  const refresh = useCallback(() => setNonce((n) => n + 1), []);
  return { map, refresh };
}

function pageVisible(): boolean {
  return typeof document === "undefined" || document.visibilityState !== "hidden";
}

/* REQ-WG-021: status polling with the ETag. intervalMs 0 fetches once. */
export function useGitStatus(
  worktree: string | null,
  scope: GitScope,
  intervalMs: number
): { data: GitStatus | null; error: string; loading: boolean; refresh: () => void } {
  const [data, setData] = useState<GitStatus | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [nonce, setNonce] = useState(0);
  const keyRef = useRef("");
  useEffect(() => {
    const key = (worktree || "") + "\n" + scope;
    if (keyRef.current !== key) {
      keyRef.current = key;
      setData(null);
      setError("");
    }
    if (!worktree) return;
    let cancelled = false;
    let timer: number | undefined;
    const tick = (first: boolean) => {
      if (!first && !pageVisible()) {
        timer = window.setTimeout(() => tick(false), intervalMs);
        return;
      }
      if (first) setLoading(true);
      getGitStatus(worktree, scope)
        .then((s) => {
          if (cancelled) return;
          setData(s);
          setError("");
        })
        .catch((err) => {
          if (!cancelled) setError(errorMessage(err));
        })
        .finally(() => {
          if (cancelled) return;
          setLoading(false);
          if (intervalMs > 0) timer = window.setTimeout(() => tick(false), intervalMs);
        });
    };
    tick(true);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [worktree, scope, intervalMs, nonce]);
  const refresh = useCallback(() => setNonce((n) => n + 1), []);
  return { data, error, loading, refresh };
}

/* Per-browser preferences of the panel (REQ-WG-062), never the shared
   layout document. */
export function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const v = window.localStorage.getItem(key);
    return v && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
  } catch {
    return fallback;
  }
}

export function savePref(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* Storage is optional. */
  }
}
