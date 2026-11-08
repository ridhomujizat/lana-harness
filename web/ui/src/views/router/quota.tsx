/* eslint-disable react-hooks/set-state-in-effect -- the page loads its data on mount, like the other pages. */
import { useCallback, useEffect, useMemo, useState } from "react";
import { CLAUDE_AGENT_PROVIDER, CLAUDE_CODE_PROVIDER, getClaudeAgent, getRouter, getRouterLimits } from "../../api/router";
import type { ClaudeAgentStatus, LimitSnapshot, RouterProvider } from "../../api/router";
import { Button, EmptyState } from "../../ui/gr8r";
import { ClaudeAccountCard, LimitCard } from "./limits";
import { rememberClaudeAccounts } from "./presets";
import { providerIndex } from "./provider-logo";
import { failureOf } from "./router-common";

/* Router → Quota (REQ-RTR-012, REQ-CCR-008, REQ-CCT-011), like 9router's
   Quota Tracker: one Claude Code card per account with its email, plan, and
   windows first, then one card per provider with its rate limits, credit, or
   balance. Reset countdowns tick every 30 seconds. */

const TICK_MS = 30000;

/* Re-renders the page on an interval so "resets in 3m" stays current. */
function useTick(interval: number): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => setTick((value) => value + 1), interval);
    return () => window.clearInterval(timer);
  }, [interval]);
  return tick;
}

export function RouterQuota() {
  const [limits, setLimits] = useState<LimitSnapshot[] | null>(null);
  const [error, setError] = useState("");
  const [claude, setClaude] = useState<ClaudeAgentStatus | null>(null);
  const [providers, setProviders] = useState<RouterProvider[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  useTick(TICK_MS);

  const load = useCallback(async (refresh: boolean) => {
    const [limitResult, claudeResult, routerResult] = await Promise.allSettled([
      getRouterLimits(refresh),
      getClaudeAgent(),
      getRouter(),
    ]);
    if (limitResult.status === "fulfilled") {
      setLimits(limitResult.value);
      setError("");
    } else {
      setError(failureOf(limitResult.reason).message);
    }
    const status = claudeResult.status === "fulfilled" ? claudeResult.value : null;
    if (status) {
      rememberClaudeAccounts(status.accounts.map((account) => account.name));
    }
    setClaude(status);
    if (routerResult.status === "fulfilled") {
      setProviders(routerResult.value.providers);
    }
  }, []);

  useEffect(() => {
    void load(false);
  }, [load]);

  async function refresh() {
    setRefreshing(true);
    try {
      await load(true);
    } finally {
      setRefreshing(false);
    }
  }

  const index = useMemo(() => providerIndex(providers), [providers]);
  /* One snapshot per account, named by the account (API-CCT-012); the
     built-in account may still carry the old claude-agent name. */
  const accounts = useMemo(() => {
    const list = claude ? claude.accounts : [];
    return list.filter(
      (account) =>
        account.binary ||
        account.logged_in ||
        account.windows.length > 0 ||
        account.agents.length > 0 ||
        account.combos.length > 0 ||
        !account.builtin ||
        (limits || []).some((limit) => limit.provider === account.name)
    );
  }, [claude, limits]);
  const claudeNames = useMemo(() => (claude ? claude.accounts.map((account) => account.name) : []), [claude]);
  const limitOf = (name: string) =>
    (limits || []).find((limit) => limit.provider === name) ||
    (name === CLAUDE_CODE_PROVIDER
      ? (limits || []).find((limit) => limit.provider === CLAUDE_AGENT_PROVIDER) || null
      : null);
  const providerLimits = useMemo(
    () =>
      (limits || []).filter(
        (limit) =>
          limit.provider !== CLAUDE_AGENT_PROVIDER &&
          limit.kind !== "claude-code" &&
          !claudeNames.includes(limit.provider)
      ),
    [limits, claudeNames]
  );
  const showClaude = accounts.length > 0;

  return (
    <div className="rt-quota">
      <div className="rt-section-h">
        <h2>Limits</h2>
        <p className="muted">What each provider reported with its last answer.</p>
        <span className="sp" />
        <Button
          variant="secondary"
          size="sm"
          icon="refresh-cw"
          loading={refreshing}
          disabled={refreshing}
          onClick={() => void refresh()}
        >
          Refresh
        </Button>
      </div>
      {error ? (
        <p className="rt-limit-err" role="alert">
          {error}
        </p>
      ) : null}
      {limits === null && !error ? (
        <div className="home-empty">Loading limits…</div>
      ) : (
        <div className="rt-limit-grid">
          {accounts.map((account) => (
            <ClaudeAccountCard key={account.name} account={account} limit={limitOf(account.name)} />
          ))}
          {providerLimits.map((limit) => (
            <LimitCard key={limit.provider} limit={limit} provider={index[limit.provider]} />
          ))}
          {providerLimits.length === 0 && !showClaude && limits !== null ? (
            <EmptyState
              size="sm"
              icon="gauge"
              text="No provider has reported limits yet. Limits appear after the first call."
            />
          ) : null}
        </div>
      )}
    </div>
  );
}
