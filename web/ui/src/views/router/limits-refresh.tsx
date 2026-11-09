import { useEffect, useState } from "react";
import { CLAUDE_AGENT_PROVIDER, CLAUDE_CODE_PROVIDER } from "../../api/router";
import type { LimitSnapshot } from "../../api/router";
import { Button } from "../../ui/gr8r";
import { agoText } from "./router-common";

/* Spec 39 Revision 4: the plan windows of a Claude Code account move with
   its turns and with a usage probe (`claude -p /usage`, no model call). The
   Quota, Usage, and Claude Code pages show a Refresh button and refresh on
   mount when a Claude Code snapshot is older than five minutes; the daemon
   probes an account at most once a minute, so a reload loop cannot run the
   probe more often. */

export const CLAUDE_STALE_MS = 5 * 60 * 1000;

const TICK_MS = 30000;

/* Re-renders on an interval so "Updated 3 min ago" stays current. */
export function useTick(interval: number = TICK_MS): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setTick((value) => value + 1), interval);
    return () => window.clearInterval(timer);
  }, [interval]);
  return tick;
}

/* Whether a snapshot belongs to a Claude Code account. */
export function isClaudeLimit(limit: LimitSnapshot, names: string[] = []): boolean {
  return (
    limit.kind === "claude-code" ||
    limit.provider === CLAUDE_CODE_PROVIDER ||
    limit.provider === CLAUDE_AGENT_PROVIDER ||
    names.includes(limit.provider)
  );
}

function timeOf(value: string | undefined | null): number {
  const at = value ? Date.parse(value) : NaN;
  return Number.isFinite(at) ? at : 0;
}

/* Whether a refresh is due: a Claude Code snapshot older than
   CLAUDE_STALE_MS or without a time, or a signed-in account (names) with no
   snapshot yet. */
export function claudeLimitsStale(limits: LimitSnapshot[], names: string[] = [], now: number = Date.now()): boolean {
  const claude = limits.filter((limit) => isClaudeLimit(limit, names));
  if (claude.some((limit) => now - timeOf(limit.updated_at) > CLAUDE_STALE_MS)) {
    return true;
  }
  return names.some(
    (name) =>
      !limits.some(
        (limit) =>
          limit.provider === name || (name === CLAUDE_CODE_PROVIDER && limit.provider === CLAUDE_AGENT_PROVIDER)
      )
  );
}

/* The newest time of the snapshots pick accepts, or null. */
export function newestUpdatedAt(
  limits: LimitSnapshot[],
  pick: (limit: LimitSnapshot) => boolean = () => true
): string | null {
  let best: string | null = null;
  for (const limit of limits) {
    if (pick(limit) && limit.updated_at && timeOf(limit.updated_at) > timeOf(best)) {
      best = limit.updated_at;
    }
  }
  return best;
}

/* The newest time of the Claude Code snapshots, or null. */
export function claudeUpdatedAt(limits: LimitSnapshot[], names: string[] = []): string | null {
  return newestUpdatedAt(limits, (limit) => isClaudeLimit(limit, names));
}

/* "Updated 3 min ago" and a Refresh button with a spinner. */
export function RefreshControl({
  refreshing,
  updatedAt,
  onRefresh,
  label = "Refresh",
}: {
  refreshing: boolean;
  updatedAt: string | null;
  onRefresh: () => void;
  label?: string;
}) {
  useTick();
  return (
    <span className="rt-refresh">
      <span className="rt-refresh-at" aria-live="polite">
        {refreshing ? "Refreshing…" : updatedAt ? "Updated " + agoText(updatedAt) : "Not updated yet"}
      </span>
      <Button
        variant="secondary"
        size="sm"
        icon="refresh-cw"
        loading={refreshing}
        disabled={refreshing}
        onClick={onRefresh}
      >
        {label}
      </Button>
    </span>
  );
}
