/* eslint-disable react-hooks/set-state-in-effect -- the page loads its data on mount, like the other pages. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  CLAUDE_AGENT_PROVIDER,
  CLAUDE_CODE_PROVIDER,
  getClaudeAgent,
  getRouter,
  getRouterLimits,
  refreshRouterLimits,
} from "../../api/router";
import type { ClaudeAgentStatus, LimitSnapshot, RouterProvider } from "../../api/router";
import { EmptyState } from "../../ui/gr8r";
import { ClaudeAccountCard, LimitCard } from "./limits";
import { RefreshControl, claudeLimitsStale, claudeUpdatedAt, newestUpdatedAt, useTick } from "./limits-refresh";
import { rememberClaudeAccounts } from "./presets";
import { providerIndex } from "./provider-logo";
import { failureOf } from "./router-common";

/* Router → Quota (REQ-RTR-012, REQ-CCR-008, REQ-CCT-011), like 9router's
   Quota Tracker: one Claude Code card per account with its email, plan, and
   windows first, then one card per provider with its rate limits, credit, or
   balance. Reset countdowns tick every 30 seconds. Refresh (and a mount
   whose Claude Code snapshots are over five minutes old) fetches the
   balances and probes each account's plan usage (spec 39 Revision 4). */

export function RouterQuota() {
  const [limits, setLimits] = useState<LimitSnapshot[] | null>(null);
  const [error, setError] = useState("");
  const [claude, setClaude] = useState<ClaudeAgentStatus | null>(null);
  const [providers, setProviders] = useState<RouterProvider[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const autoChecked = useRef(false);
  useTick();

  /* A refresh runs first, so the account rows read the new windows. */
  const load = useCallback(async (refresh: boolean) => {
    const refreshed = refresh ? await Promise.allSettled([refreshRouterLimits()]) : null;
    const [limitResult, claudeResult, routerResult] = await Promise.allSettled([
      refreshed && refreshed[0].status === "fulfilled" ? Promise.resolve(refreshed[0].value) : getRouterLimits(false),
      getClaudeAgent(),
      getRouter(),
    ]);
    if (refreshed && refreshed[0].status === "rejected") {
      setError(failureOf(refreshed[0].reason).message);
    }
    if (limitResult.status === "fulfilled") {
      setLimits(limitResult.value);
      if (!refreshed || refreshed[0].status === "fulfilled") {
        setError("");
      }
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
    return {
      limits: limitResult.status === "fulfilled" ? limitResult.value : null,
      names: status ? status.accounts.filter((account) => account.logged_in).map((account) => account.name) : [],
    };
  }, []);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await load(true);
    } finally {
      setRefreshing(false);
    }
  }, [load]);

  useEffect(() => {
    void load(false).then((loaded) => {
      if (!autoChecked.current && loaded.limits && claudeLimitsStale(loaded.limits, loaded.names)) {
        autoChecked.current = true;
        void refresh();
      }
      autoChecked.current = true;
    });
  }, [load, refresh]);

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
  /* The Claude Code plans first: they are what goes stale between turns. */
  const updatedAt = useMemo(
    () => claudeUpdatedAt(limits || [], claudeNames) || newestUpdatedAt(limits || []),
    [limits, claudeNames]
  );

  return (
    <div className="rt-quota">
      <div className="rt-section-h">
        <h2>Limits</h2>
        <p className="muted">What each provider reported with its last answer.</p>
        <span className="sp" />
        <RefreshControl refreshing={refreshing} updatedAt={updatedAt} onRefresh={() => void refresh()} />
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
