import type { ReactNode } from "react";
import type {
  BalanceLimit,
  ClaudeAgentStatus,
  CreditLimit,
  LimitSnapshot,
  PlanWindow,
  RateLimitValue,
  UsageProviderRow,
} from "../../api/router";
import { Badge, Progress } from "../../ui/gr8r";
import type { ProgressTone } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import {
  agoText,
  count,
  costText,
  kindLabel,
  timeLabel,
  usd,
  windowLabel,
} from "./router-common";

/* The Limits section of the Overview (REQ-RTR-012, REQ-CCR-008): one card per
   provider with what it last reported, and the Claude Agent card. */

const RATE_ROWS: Array<{ key: "requests" | "tokens" | "input_tokens" | "output_tokens"; unit: string }> = [
  { key: "requests", unit: "requests" },
  { key: "tokens", unit: "tokens" },
  { key: "input_tokens", unit: "input tokens" },
  { key: "output_tokens", unit: "output tokens" },
];

/* A meter of what is left: full is good, the tone turns at a fifth and a
   twentieth left. */
function leftTone(left: number, whole: number): ProgressTone {
  if (whole <= 0) {
    return "green";
  }
  const share = left / whole;
  if (share <= 0.05) {
    return "red";
  }
  if (share <= 0.2) {
    return "amber";
  }
  return "green";
}

function usedTone(used: number): ProgressTone | undefined {
  if (used >= 0.9) {
    return "red";
  }
  if (used >= 0.7) {
    return "amber";
  }
  return undefined;
}

function Meter({
  label,
  text,
  value,
  tone,
}: {
  label: string;
  text: ReactNode;
  value: number;
  tone?: ProgressTone;
}) {
  return (
    <div className="rt-meter">
      <div className="rt-meter-top">
        <span className="rt-meter-k">{label}</span>
        <span className="rt-meter-v num">{text}</span>
      </div>
      <Progress value={value} tone={tone} aria-label={label} />
    </div>
  );
}

function rateText(value: RateLimitValue, unit: string): string {
  const reset = value.reset_at ? " · resets " + timeLabel(value.reset_at) : "";
  return count(value.remaining) + " of " + count(value.limit) + " " + unit + " left" + reset;
}

/* AC-RTR-017: "$7.50 of $10 left". */
export function creditText(credit: CreditLimit): string {
  if (credit.limit_usd === null || credit.limit_usd === undefined) {
    return usd(credit.used_usd) + " used · no credit limit";
  }
  const remaining =
    credit.remaining_usd === null || credit.remaining_usd === undefined
      ? Math.max(0, credit.limit_usd - credit.used_usd)
      : credit.remaining_usd;
  return usd(remaining) + " of " + usdShort(credit.limit_usd) + " left";
}

/* Whole dollars drop the cents: $10, $7.50. */
function usdShort(value: number): string {
  return Number.isInteger(value) ? "$" + value.toLocaleString("en-US") : usd(value);
}

function money(value: number, currency: string): string {
  const code = String(currency || "USD").toUpperCase();
  if (code === "USD") {
    return usd(value);
  }
  if (code === "CNY") {
    return "¥" + value.toFixed(2);
  }
  return value.toFixed(2) + " " + code;
}

function balanceText(balance: BalanceLimit): string {
  return money(balance.total, balance.currency) + " balance";
}

export function hasReport(limit: LimitSnapshot): boolean {
  const rate = limit.rate;
  const hasRate = Boolean(
    rate && RATE_ROWS.some((row) => rate[row.key] && Number(rate[row.key]?.limit) > 0)
  );
  return (
    hasRate ||
    Boolean(limit.credit) ||
    Boolean(limit.balance) ||
    (Array.isArray(limit.windows) && limit.windows.length > 0)
  );
}

function CardFoot({ limit }: { limit: LimitSnapshot }) {
  return (
    <>
      {limit.error ? (
        <p className="rt-limit-err" role="status">
          <Ic name="circle-alert" size={12} />
          <span>{limit.error}</span>
        </p>
      ) : null}
      <p className="rt-limit-foot">
        {limit.updated_at ? "Updated " + agoText(limit.updated_at) : "Not updated yet"}
      </p>
    </>
  );
}

export function LimitCard({ limit }: { limit: LimitSnapshot }) {
  const rate = limit.rate || {};
  const rows = RATE_ROWS.filter((row) => {
    const value = rate[row.key];
    return value && Number(value.limit) > 0;
  });
  return (
    <article className="rt-limit" aria-label={"Limits of " + limit.provider}>
      <header className="rt-limit-h">
        <span className="rt-limit-name mono trunc">{limit.provider}</span>
        {limit.kind ? <Badge>{kindLabel(limit.kind)}</Badge> : null}
      </header>
      <div className="rt-limit-b">
        {rows.map((row) => {
          const value = rate[row.key] as RateLimitValue;
          return (
            <Meter
              key={row.key}
              label={row.unit.charAt(0).toUpperCase() + row.unit.slice(1)}
              text={rateText(value, row.unit)}
              value={value.limit > 0 ? value.remaining / value.limit : 0}
              tone={leftTone(value.remaining, value.limit)}
            />
          );
        })}
        {limit.credit ? (
          <Meter
            label="Credit"
            text={creditText(limit.credit)}
            value={
              limit.credit.limit_usd
                ? Math.max(0, limit.credit.limit_usd - limit.credit.used_usd) /
                  limit.credit.limit_usd
                : 1
            }
            tone={
              limit.credit.limit_usd
                ? leftTone(limit.credit.limit_usd - limit.credit.used_usd, limit.credit.limit_usd)
                : "green"
            }
          />
        ) : null}
        {limit.balance ? (
          <div className="rt-balance">
            <span className="rt-balance-v num">{balanceText(limit.balance)}</span>
            {limit.balance.granted > 0 || limit.balance.topped_up > 0 ? (
              <span className="rt-balance-s num">
                {money(limit.balance.topped_up, limit.balance.currency)} topped up ·{" "}
                {money(limit.balance.granted, limit.balance.currency)} granted
              </span>
            ) : null}
          </div>
        ) : null}
        {!hasReport(limit) ? (
          <p className="rt-limit-none">Not reported by this provider</p>
        ) : null}
      </div>
      <CardFoot limit={limit} />
    </article>
  );
}

/* AC-CCR-009: "5-hour window · 42 % used · resets <time>". Utilization is a
   fraction. */
export function windowText(window: PlanWindow): string {
  const parts = [windowLabel(window.type)];
  if (window.utilization !== null && window.utilization !== undefined) {
    parts.push(Math.round(Number(window.utilization) * 100) + " % used");
  } else if (window.status === "rejected") {
    parts.push("limit reached");
  }
  if (window.resets_at) {
    parts.push("resets " + timeLabel(window.resets_at));
  }
  return parts.join(" · ");
}

function authIsPlan(method: string): boolean {
  return !/api|console|key/i.test(method || "");
}

export function ClaudeAgentCard({
  status,
  limit,
  usage,
}: {
  status: ClaudeAgentStatus | null;
  limit: LimitSnapshot | null;
  usage: UsageProviderRow | null;
}) {
  const windows = limit && Array.isArray(limit.windows) ? limit.windows : [];
  const plan = status ? authIsPlan(status.auth_method) : true;
  return (
    <article className="rt-limit rt-claude" aria-label="Claude Agent">
      <header className="rt-limit-h">
        <span className="rt-limit-name">Claude Agent</span>
        {status ? (
          status.logged_in ? (
            <Badge tone="green" dot>
              Logged in{status.auth_method ? " · " + status.auth_method : ""}
            </Badge>
          ) : (
            <Badge tone="amber" dot>
              Not logged in
            </Badge>
          )
        ) : null}
      </header>
      <div className="rt-limit-b">
        {status ? (
          <dl className="rt-facts">
            <dt>Binary</dt>
            <dd className="mono trunc" title={status.binary}>
              {status.binary || "Not found on PATH"}
            </dd>
            <dt>Version</dt>
            <dd className="num">
              {status.version || "Unknown"}
              {status.version && !status.version_ok ? (
                <span className="rt-warn">
                  <Ic name="triangle-alert" size={12} /> Too old. Update Claude Code to 2.1.0 or newer.
                </span>
              ) : null}
            </dd>
            <dt>Runs</dt>
            <dd className="num">
              {count(status.running)} running · {count(status.queued)} queued
              {status.max_concurrent > 0 ? " of " + count(status.max_concurrent) + " at once" : ""}
            </dd>
            {status.configured_agents.length > 0 ? (
              <>
                <dt>Agents</dt>
                <dd className="trunc">{status.configured_agents.join(", ")}</dd>
              </>
            ) : null}
          </dl>
        ) : null}
        {status && !status.logged_in ? (
          <p className="rt-claude-login">
            Run <code>claude</code>, then <code>/login</code>.
          </p>
        ) : null}
        {windows.map((window) => {
          const used = Math.max(0, Math.min(1, Number(window.utilization) || 0));
          return (
            <Meter
              key={window.type}
              label={windowLabel(window.type)}
              text={windowText(window)}
              value={window.status === "rejected" && !window.utilization ? 1 : used}
              tone={window.status === "rejected" ? "red" : usedTone(used)}
            />
          );
        })}
        {windows.length === 0 ? (
          <p className="rt-limit-none">No plan windows reported yet.</p>
        ) : null}
        {usage ? (
          <p className="rt-claude-cost">
            <span className="num">{costText(usage.cost_usd, usage.cost_partial)}</span>{" "}
            <span className="muted">
              {plan
                ? "reported by Claude Code; included in your plan"
                : "reported by Claude Code"}
            </span>
          </p>
        ) : null}
        {status && status.error ? (
          <p className="rt-limit-err" role="status">
            <Ic name="circle-alert" size={12} />
            <span>{status.error}</span>
          </p>
        ) : null}
      </div>
      {limit ? <CardFoot limit={limit} /> : null}
    </article>
  );
}
