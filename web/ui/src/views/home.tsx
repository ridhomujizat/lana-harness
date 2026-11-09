import { Button, ErrorState, Panel, Stat } from "../ui/gr8r";
import { Ic } from "../ui/icons";
import { DashboardFilters, count, share, useDashboard } from "./dashboard-common";
import { OutcomeDonut, ShareBars, TokenColumns, ToolBars } from "./home-charts";
import { useAgentLabel } from "./agent-label";

/* The monitoring dashboard (spec 28). On owner request (2026-11-05) Home shows
   only graphics: a KPI strip, token usage over time, turn outcomes, the busiest
   tools and the token share by model, agent and source. The error log and the
   tool table live on Observe → Logs. */

const BREAKDOWN_SOURCE_LABELS: Record<string, string> = {
  turn: "Chat turns",
  compaction: "Compaction",
  memory_capture: "Memory capture",
};

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function greeting(): string {
  const hour = new Date().getHours();
  if (hour < 12) {
    return "Good morning";
  }
  if (hour < 18) {
    return "Good afternoon";
  }
  return "Good evening";
}

function todayLabel(): string {
  const date = new Date();
  return WEEKDAYS[date.getDay()] + ", " + MONTHS[date.getMonth()] + " " + date.getDate();
}

function tokensOf(row: { input_tokens?: number; output_tokens?: number }): number {
  return (row.input_tokens || 0) + (row.output_tokens || 0);
}

export function HomePage() {
  const state = useDashboard("#/home");
  const { data, error } = state;
  const label = useAgentLabel();
  const summary = data?.summary;
  const now = data?.now;

  const tokensTotal = summary ? (summary.input_tokens || 0) + (summary.output_tokens || 0) : 0;

  return (
    <main id="main" className="page home-page">
      <header className="ph">
        <div>
          <h1>{greeting()}, Lana</h1>
          <p>
            <span className="num">{todayLabel()}</span> · Here's what's happening across your workspace.
          </p>
        </div>
        <div className="acts">
          <DashboardFilters state={state} />
        </div>
      </header>

      {error ? (
        <div className="home-alert" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not refresh"
            text={error}
            action={
              <Button variant="secondary" icon="refresh-cw" onClick={state.reload}>
                Retry
              </Button>
            }
          />
        </div>
      ) : null}

      {!data ? (
        <div className="home-empty">{error ? "No data yet." : "Loading the dashboard…"}</div>
      ) : (
        <>
          <div className="stats home-stats">
            <Stat
              icon="coins"
              label="Tokens"
              value={count(tokensTotal)}
              detail={"Cached " + share(summary?.cached_input_tokens || 0, summary?.input_tokens || 0) + " of input"}
            />
            <Stat
              icon="activity"
              label="Turns"
              value={count(summary?.turns || 0)}
              detail={"Failed " + count(summary?.turns_failed || 0) + " · " + share(summary?.turns_failed || 0, summary?.turns || 0)}
              tone={summary?.turns_failed ? "bad" : "up"}
            />
            <Stat
              icon="wrench"
              label="Tool calls"
              value={count(summary?.tool_calls || 0)}
              detail={"Errors " + count(summary?.tool_errors || 0) + " · " + share(summary?.tool_errors || 0, summary?.tool_calls || 0)}
              tone={summary?.tool_errors ? "bad" : "up"}
            />
            <Stat
              icon="repeat"
              label="Provider retries"
              value={count(summary?.provider_retries || 0)}
              detail="Retried model attempts"
            />
          </div>

          <div className="home-now" aria-label="Right now">
            <span className="home-now-k">Right now</span>
            <a href="#/sessions" className="home-now-item">
              <span className={"home-now-dot" + (now?.sessions_running ? " live" : "")} />
              <b className="num">{count(now?.sessions_running || 0)}</b> running
            </a>
            <a href="#/sessions" className="home-now-item">
              <Ic name="clock" size={13} />
              <b className="num">{count(now?.sessions_waiting_input || 0)}</b> waiting for an answer
            </a>
            <a href="#/logs" className="home-now-link">
              {count((data.errors || []).length)} errors in this range · Open logs
              <Ic name="arrow-right" size={13} />
            </a>
          </div>

          <Panel title="Token usage" className="home-wide">
            <TokenColumns data={data} />
          </Panel>

          <div className="home-charts2">
            <Panel title="Turn outcomes">
              <OutcomeDonut data={data} />
            </Panel>
            <Panel title="Tools">
              <ToolBars data={data} />
            </Panel>
          </div>

          <div className="home-charts3">
            <Panel title="By model">
              <ShareBars
                empty="No model usage in this range."
                rows={(data.by_model || []).map((row) => ({
                  key: row.provider + "/" + row.model,
                  label: row.model,
                  sub: row.provider + " · " + count(row.requests) + " requests",
                  value: tokensOf(row),
                }))}
              />
            </Panel>
            <Panel title="By agent">
              <ShareBars
                empty="No agent activity in this range."
                rows={(data.by_agent || []).map((row) => ({
                  key: row.agent,
                  label: label(row.agent),
                  sub: count(row.turns) + " turns",
                  value: tokensOf(row),
                }))}
              />
            </Panel>
            <Panel title="By source">
              <ShareBars
                empty="No token usage by source in this range."
                rows={(data.by_source || [])
                  .filter((row) => row.count > 0)
                  .map((row) => ({
                    key: row.source,
                    label: BREAKDOWN_SOURCE_LABELS[row.source] || row.source,
                    sub: count(row.count) + " calls",
                    value: tokensOf(row),
                  }))}
              />
            </Panel>
          </div>
        </>
      )}
    </main>
  );
}
