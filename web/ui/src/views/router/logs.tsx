/* eslint-disable react-hooks/set-state-in-effect -- each tab loads its data on mount and on a filter change, like the other pages. */
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { getRouter, listRouterCalls } from "../../api/router";
import type { CallsQuery, RouterCall, RouterProvider } from "../../api/router";
import { Badge, Button, Drawer, EmptyState, ErrorState, Seg, Select } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import {
  DASH,
  KNOWN_SOURCES,
  compact,
  costText,
  count,
  failureOf,
  ms,
  outcomeLabel,
  outcomeTone,
  sourceLabel,
  timeLabel,
} from "./router-common";
import { ProviderMark, providerIndex } from "./provider-logo";
import { useClaudeAccounts } from "./claude-code";
import { CLAUDE_CODE_KIND } from "./presets";

/* Router → Logs (REQ-RTR-009, API-RTR-012): every attempt, newest first.
   "Load more" passes the last id as `before`, which is exclusive. */

export const PAGE_SIZE = 50;

const OUTCOMES = [
  { value: "", label: "All" },
  { value: "ok", label: "OK" },
  { value: "error", label: "Errors" },
];

/* The requested name is shown before the served target only when it says
   something new: a combo or another model. "ds/deepseek-flash" requested and
   served reads once. */
export function requestedDiffers(requested: string | undefined, served: string): boolean {
  const asked = String(requested || "").trim();
  return asked !== "" && asked.toLowerCase() !== served.trim().toLowerCase();
}

function CallDetail({
  call,
  provider,
  onClose,
}: {
  call: RouterCall;
  provider?: RouterProvider;
  onClose: () => void;
}) {
  const served = call.provider + "/" + call.model;
  return (
    <Drawer
      title={"Call " + call.id}
      onClose={onClose}
      className="rt-call-drawer"
      header={
        <span className="row" style={{ gap: 8 }}>
          <Badge tone={outcomeTone(call.outcome)} dot>
            {outcomeLabel(call.outcome)}
          </Badge>
          <ProviderMark provider={provider} name={call.provider} size={18} />
          <b className="mono trunc">{served}</b>
        </span>
      }
    >
      <dl className="rt-detail">
        <dt>Time</dt>
        <dd className="num">{call.at ? new Date(call.at).toLocaleString() : DASH}</dd>
        <dt>Source</dt>
        <dd>{sourceLabel(call.source)}</dd>
        <dt>Agent</dt>
        <dd>{call.agent || DASH}</dd>
        <dt>Requested</dt>
        <dd className="mono">{call.requested || DASH}</dd>
        <dt>Served by</dt>
        <dd className="mono">{served}</dd>
        <dt>Attempt</dt>
        <dd className="num">{call.attempt}</dd>
        <dt>Tokens</dt>
        <dd className="num">
          {count(call.input_tokens)} in · {count(call.output_tokens)} out · {count(call.cached_tokens)} cached
        </dd>
        <dt>Cost</dt>
        <dd className="num">{costText(call.cost_usd)}</dd>
        <dt>First token</dt>
        <dd className="num">{ms(call.ttft_ms)}</dd>
        <dt>Duration</dt>
        <dd className="num">{ms(call.duration_ms)}</dd>
        {call.session_id ? (
          <>
            <dt>Session</dt>
            <dd>
              <a href={"#/sessions/" + encodeURIComponent(call.session_id) + "?view=trajectory"}>
                {call.session_id}
              </a>
            </dd>
          </>
        ) : null}
      </dl>
      {call.error ? (
        <section className="rt-detail-err">
          <h3>Error</h3>
          <pre className="mono">{call.error}</pre>
          <p className="hint">Keys and tokens are removed from this message before it is stored.</p>
        </section>
      ) : null}
    </Drawer>
  );
}

export function RouterLogs() {
  const [providers, setProviders] = useState<RouterProvider[]>([]);
  const [provider, setProvider] = useState("");
  const [status, setStatus] = useState<"" | "ok" | "error">("");
  const [source, setSource] = useState("");
  const [calls, setCalls] = useState<RouterCall[] | null>(null);
  const [nextBefore, setNextBefore] = useState(0);
  const [error, setError] = useState("");
  const [loadingMore, setLoadingMore] = useState(false);
  const [open, setOpen] = useState<RouterCall | null>(null);
  const requestId = useRef(0);

  useEffect(() => {
    let alive = true;
    getRouter()
      .then((data) => {
        if (alive) {
          setProviders(data.providers);
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  /* Spec 39 (API-CCT-012): Claude Code records name their account; the
     filter lists every account and the rows show the Claude logo. */
  const { accounts } = useClaudeAccounts();
  const filterProviders = useMemo(() => {
    const list: RouterProvider[] = providers.slice();
    for (const account of accounts) {
      if (!list.some((entry) => entry.name === account.name)) {
        list.push({ name: account.name, kind: CLAUDE_CODE_KIND, base_url: "", key_state: "", used_by: [] });
      }
    }
    return list;
  }, [providers, accounts]);
  const index = useMemo(() => providerIndex(filterProviders), [filterProviders]);
  const query = useMemo<CallsQuery>(
    () => ({ limit: PAGE_SIZE, provider, status, source }),
    [provider, status, source]
  );

  const loadFirst = useCallback(async (next: CallsQuery) => {
    const id = ++requestId.current;
    try {
      const page = await listRouterCalls(next);
      if (id === requestId.current) {
        setCalls(page.calls);
        setNextBefore(page.next_before);
        setError("");
      }
    } catch (failure) {
      if (id === requestId.current) {
        setError(failureOf(failure).message);
      }
    }
  }, []);

  useEffect(() => {
    void loadFirst(query);
  }, [loadFirst, query]);

  async function loadMore() {
    if (!nextBefore || loadingMore) {
      return;
    }
    const id = requestId.current;
    setLoadingMore(true);
    try {
      const page = await listRouterCalls({ ...query, before: nextBefore });
      if (id === requestId.current) {
        setCalls((current) => (current || []).concat(page.calls));
        setNextBefore(page.next_before);
      }
    } catch (failure) {
      setError(failureOf(failure).message);
    } finally {
      setLoadingMore(false);
    }
  }

  const sources = useMemo(() => {
    const names = new Set<string>(KNOWN_SOURCES);
    for (const call of calls || []) {
      if (call.source) {
        names.add(call.source);
      }
    }
    if (source) {
      names.add(source);
    }
    return Array.from(names);
  }, [calls, source]);

  function rowKey(event: KeyboardEvent<HTMLTableRowElement>, call: RouterCall) {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setOpen(call);
    }
  }

  return (
    <div className="rt-logs">
      <div className="rt-toolbar rt-log-filters">
        <div className="rt-filter">
          <label className="rt-filter-k" htmlFor="rt-logs-provider">
            Provider
          </label>
          <Select
            id="rt-logs-provider"
            size="sm"
            options={[
              { value: "", label: "All providers" },
              ...filterProviders.map((entry) => ({
                value: entry.name,
                label: entry.name,
                icon: <ProviderMark provider={entry} size={16} />,
              })),
            ]}
            value={provider}
            onChange={setProvider}
          />
        </div>
        <div className="rt-filter">
          <label className="rt-filter-k" htmlFor="rt-logs-source">
            Source
          </label>
          <Select
            id="rt-logs-source"
            size="sm"
            options={[{ value: "", label: "All sources" }, ...sources.map((name) => ({ value: name, label: sourceLabel(name) }))]}
            value={source}
            onChange={setSource}
          />
        </div>
        <Seg
          aria-label="Outcome"
          options={OUTCOMES}
          value={status}
          onChange={(next) => setStatus(next as "" | "ok" | "error")}
        />
        <span className="sp" />
        <Button variant="secondary" size="sm" icon="refresh-cw" onClick={() => void loadFirst(query)}>
          Refresh
        </Button>
      </div>

      {error ? (
        <div className="home-alert" role="alert">
          <ErrorState icon="cloud-alert" title="Could not load calls" text={error} />
        </div>
      ) : null}
      {calls === null && !error ? <div className="home-empty">Loading calls…</div> : null}
      {calls !== null && calls.length === 0 ? (
        <div className="panel">
          <EmptyState size="sm" icon="scroll-text" text="No calls match these filters." />
        </div>
      ) : null}

      {calls !== null && calls.length > 0 ? (
        <section className="panel rt-calls" aria-label="Calls">
          <div className="home-table-wrap">
            <table className="perm-t home-table rt-calls-table">
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Source</th>
                  <th scope="col">Agent</th>
                  <th scope="col">Requested → served</th>
                  <th scope="col" className="is-num">Try</th>
                  <th scope="col">Outcome</th>
                  <th scope="col" className="is-num">Tokens</th>
                  <th scope="col" className="is-num">Cost</th>
                  <th scope="col" className="is-num">TTFT</th>
                  <th scope="col" className="is-num">Duration</th>
                </tr>
              </thead>
              <tbody>
                {calls.map((call) => {
                  const served = call.provider + "/" + call.model;
                  return (
                    <tr
                      key={call.id}
                      className="rt-call-row"
                      tabIndex={0}
                      aria-label={"Call " + call.id + ", " + outcomeLabel(call.outcome) + ", open details"}
                      onClick={() => setOpen(call)}
                      onKeyDown={(event) => rowKey(event, call)}
                    >
                      <td className="num">{timeLabel(call.at)}</td>
                      <td>{sourceLabel(call.source)}</td>
                      <td>
                        <span className="trunc rt-agent" title={call.agent || undefined}>
                          {call.agent || DASH}
                        </span>
                      </td>
                      <td className="rt-route-cell">
                        <span className="rt-route-wrap">
                        <ProviderMark provider={index[call.provider]} name={call.provider} size={18} />
                        <span className="rt-route">
                          {requestedDiffers(call.requested, served) ? (
                            <>
                              <span className="mono">{call.requested}</span>
                              <Ic name="arrow-right" size={12} />
                            </>
                          ) : null}
                          <span className="mono">{served}</span>
                        </span>
                        </span>
                      </td>
                      <td className="is-num num">{call.attempt}</td>
                      <td>
                        <Badge tone={outcomeTone(call.outcome)} dot>
                          {outcomeLabel(call.outcome)}
                        </Badge>
                      </td>
                      <td className="is-num num">
                        {compact(call.input_tokens)} / {compact(call.output_tokens)}
                      </td>
                      <td className="is-num num">{costText(call.cost_usd)}</td>
                      <td className="is-num num">{ms(call.ttft_ms)}</td>
                      <td className="is-num num">{ms(call.duration_ms)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="rt-more">
            {nextBefore ? (
              <Button variant="secondary" size="sm" loading={loadingMore} disabled={loadingMore} onClick={() => void loadMore()}>
                Load more
              </Button>
            ) : (
              <span className="hint">That is every call in the last 30 days.</span>
            )}
          </div>
        </section>
      ) : null}

      {open ? <CallDetail call={open} provider={index[open.provider]} onClose={() => setOpen(null)} /> : null}
    </div>
  );
}
