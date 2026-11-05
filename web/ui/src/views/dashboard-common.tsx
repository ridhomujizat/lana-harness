import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import { getDashboard } from "../api/dashboard";
import type { Dashboard, DashboardRange } from "../api/dashboard";
import { Button, Seg, Select } from "../ui/gr8r";

/* Shared state and helpers of the monitoring pages (spec 28): Home shows the
   charts, Logs shows the error log and tool health. Both read the same
   endpoint with the same range and agent filter kept in the hash query
   (REQ-DSH-011). */

export const RANGES: Array<{ value: DashboardRange; label: string }> = [
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

export const DEFAULT_RANGE: DashboardRange = "24h";

const REFRESH_MS = 30000;

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function hashQuery(): URLSearchParams {
  if (typeof window === "undefined") {
    return new URLSearchParams();
  }
  const hash = window.location.hash || "";
  const queryIndex = hash.indexOf("?");
  if (queryIndex < 0) {
    return new URLSearchParams();
  }
  try {
    return new URLSearchParams(hash.slice(queryIndex + 1));
  } catch {
    return new URLSearchParams();
  }
}

function rangeFromHash(): DashboardRange {
  const value = hashQuery().get("range");
  const match = RANGES.find((option) => option.value === value);
  return match ? match.value : DEFAULT_RANGE;
}

function agentFromHash(): string {
  return hashQuery().get("agent") || "";
}

/* Write both choices back without adding a history entry. Default values stay
   out of the URL. */
function writeHash(path: string, nextRange: DashboardRange, nextAgent: string) {
  if (typeof window === "undefined") {
    return;
  }
  const query = new URLSearchParams();
  if (nextRange !== DEFAULT_RANGE) {
    query.set("range", nextRange);
  }
  if (nextAgent) {
    query.set("agent", nextAgent);
  }
  const suffix = query.toString();
  window.history.replaceState(null, "", path + (suffix ? "?" + suffix : ""));
}

function trimDecimal(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

/* Compact chart units: 12k, 1.2M. */
export function compact(value: number): string {
  const number = Number(value) || 0;
  const abs = Math.abs(number);
  if (abs < 1000) {
    return String(Math.round(number));
  }
  if (abs < 1000000) {
    return trimDecimal(number / 1000) + "k";
  }
  if (abs < 1000000000) {
    return trimDecimal(number / 1000000) + "M";
  }
  return trimDecimal(number / 1000000000) + "B";
}

export function count(value: number): string {
  return (Number(value) || 0).toLocaleString("en-US");
}

export function share(part: number, whole: number): string {
  if (!whole || whole <= 0) {
    return "0%";
  }
  return Math.round((part / whole) * 100) + "%";
}

function pad2(value: number): string {
  return value < 10 ? "0" + value : String(value);
}

export function clockTime(ms: number): string {
  const date = new Date(Number(ms) || 0);
  return pad2(date.getHours()) + ":" + pad2(date.getMinutes());
}

export function bucketLabel(ms: number, bucketMs: number): string {
  const date = new Date(Number(ms) || 0);
  if (bucketMs < 86400000) {
    return pad2(date.getHours()) + ":" + pad2(date.getMinutes());
  }
  return MONTHS[date.getMonth()] + " " + date.getDate();
}

export function duration(ms: number): string {
  const value = Number(ms) || 0;
  if (value < 1000) {
    return Math.round(value) + " ms";
  }
  if (value < 60000) {
    return trimDecimal(value / 1000) + " s";
  }
  return trimDecimal(value / 60000) + " min";
}

export interface DashboardState {
  range: DashboardRange;
  agent: string;
  data: Dashboard | null;
  error: string;
  loading: boolean;
  updatedAt: number;
  agentOptions: string[];
  setRange: (next: DashboardRange) => void;
  setAgent: (next: string) => void;
  reload: () => void;
}

/* Loads the dashboard for the range and agent in the hash of `path`, refreshes
   every 30 s while the tab is visible, and applies only the latest response
   (REQ-DSH-001, REQ-DSH-010). */
export function useDashboard(path: string): DashboardState {
  const [range, setRangeState] = useState<DashboardRange>(rangeFromHash);
  const [agent, setAgentState] = useState<string>(agentFromHash);
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [updatedAt, setUpdatedAt] = useState(0);
  const requestId = useRef(0);

  const load = useCallback(async (nextRange: DashboardRange, nextAgent: string) => {
    const id = ++requestId.current;
    try {
      /* An absent agent keeps the one-argument call and query. */
      const result = nextAgent
        ? await getDashboard(nextRange, nextAgent)
        : await getDashboard(nextRange);
      if (id !== requestId.current) {
        return;
      }
      setData(result);
      setError("");
      setUpdatedAt(Date.now());
    } catch (failure) {
      if (id !== requestId.current) {
        return;
      }
      setError(errorMessage(failure));
    } finally {
      if (id === requestId.current) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    /* Fetching on mount and on a range or agent change matches the other
       views; the first state update happens after the await. */
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void load(range, agent);
  }, [load, range, agent]);

  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") {
        void load(range, agent);
      }
    }, REFRESH_MS);
    return () => clearInterval(timer);
  }, [load, range, agent]);

  /* Keep the chosen agent as an option even when the latest response no
     longer lists it, so the select never jumps to another value. */
  const agentOptions = useMemo(() => {
    const names = new Set(data?.agents || []);
    if (agent) {
      names.add(agent);
    }
    return Array.from(names).sort((a, b) => a.localeCompare(b));
  }, [data, agent]);

  return {
    range,
    agent,
    data,
    error,
    loading,
    updatedAt,
    agentOptions,
    setRange(next) {
      if (next === range) {
        return;
      }
      writeHash(path, next, agent);
      setLoading(true);
      setRangeState(next);
    },
    setAgent(next) {
      if (next === agent) {
        return;
      }
      writeHash(path, range, next);
      setLoading(true);
      setAgentState(next);
    },
    reload() {
      setLoading(true);
      void load(range, agent);
    },
  };
}

/* The range switch, agent filter, last update and refresh of the header. */
export function DashboardFilters({ state }: { state: DashboardState }) {
  return (
    <>
      <Seg
        className="home-range"
        aria-label="Time range"
        options={RANGES.map((option) => ({ value: option.value, label: option.label }))}
        value={state.range}
        onChange={(next) => state.setRange(next as DashboardRange)}
      />
      <div className="home-agent">
        <label className="home-agent-label" htmlFor="home-agent-select">
          Agent
        </label>
        <Select
          id="home-agent-select"
          placeholder="All agents"
          options={[
            { value: "", label: "All agents" },
            ...state.agentOptions.map((name) => ({ value: name, label: name })),
          ]}
          value={state.agent}
          onChange={state.setAgent}
        />
      </div>
      <span className="home-updated num" aria-live="polite">
        {state.updatedAt ? "Updated " + clockTime(state.updatedAt) : "Loading"}
      </span>
      <Button
        variant="secondary"
        icon="refresh-cw"
        size="sm"
        onClick={state.reload}
        disabled={state.loading}
      >
        Refresh
      </Button>
    </>
  );
}
