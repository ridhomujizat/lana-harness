import type { ReactNode } from "react";
import type {
  BalanceLimit,
  ClaudeAccount,
  CreditLimit,
  LimitSnapshot,
  PlanWindow,
  RateLimitValue,
} from "../../api/router";
import { Badge, Progress } from "../../ui/gr8r";
import type { ProgressTone } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import {
  CopyButton,
  agoText,
  count,
  kindLabel,
  resetsIn,
  usd,
  windowLabel,
} from "./router-common";
import { ProviderLogo, ProviderMark } from "./provider-logo";
import { familyLabel } from "./presets";
import type { ProviderShape } from "./presets";

/* The quota cards (REQ-RTR-012, REQ-CCR-008, REQ-CCT-011): one card per
   provider with what it last reported, and one card per Claude Code account
   with its plan windows.
   Shown on the Quota page and on each provider's page. */

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
  const reset = value.reset_at ? " · " + resetsIn(value.reset_at) : "";
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

export function LimitCard({ limit, provider }: { limit: LimitSnapshot; provider?: ProviderShape | null }) {
  const rate = limit.rate || {};
  const rows = RATE_ROWS.filter((row) => {
    const value = rate[row.key];
    return value && Number(value.limit) > 0;
  });
  return (
    <article className="rt-limit" aria-label={"Limits of " + limit.provider}>
      <header className="rt-limit-h">
        <ProviderMark provider={provider || { name: limit.provider, kind: limit.kind || "" }} size={28} />
        <span className="rt-limit-title">
          <span className="rt-limit-name trunc">
            {familyLabel(provider || { name: limit.provider, kind: limit.kind || "" })}
          </span>
          <span className="rt-limit-sub mono trunc">{limit.provider}</span>
        </span>
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

/* Whether Claude Code reported the window's utilization; null means it did
   not (API-RTR-013). */
function hasUtilization(window: PlanWindow): boolean {
  return window.utilization !== null && window.utilization !== undefined;
}

/* AC-CCR-009: "5-hour window · 42 % used · resets <time>". Utilization is a
   fraction; a window without one says "usage not reported". */
export function windowText(window: PlanWindow): string {
  const parts = [windowLabel(window.type)];
  if (hasUtilization(window)) {
    parts.push(Math.round(Number(window.utilization) * 100) + " % used");
  } else if (window.status === "rejected") {
    parts.push("limit reached");
  } else {
    parts.push("usage not reported");
  }
  if (window.resets_at) {
    parts.push(resetsIn(window.resets_at));
  }
  return parts.join(" · ");
}

/* The plan name of `claude auth status`: "max" → "Max". */
export function planLabel(plan: string): string {
  const text = String(plan || "").replace(/[_-]+/g, " ").trim();
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : "";
}

/* The plan windows of one Claude Code account (AC-CCR-009): a bar per
   window with "42 % used · resets in 1h 32m". */
export function PlanWindows({ windows }: { windows: PlanWindow[] }) {
  if (windows.length === 0) {
    return <p className="rt-limit-none">No plan windows reported yet. They appear after the first turn.</p>;
  }
  return (
    <div className="rt-windows">
      {windows.map((window) => {
        const reported = hasUtilization(window);
        const used = reported ? Math.max(0, Math.min(1, Number(window.utilization) || 0)) : 0;
        return (
          <Meter
            key={window.type}
            label={windowLabel(window.type)}
            text={windowText(window).split(" · ").slice(1).join(" · ")}
            value={window.status === "rejected" && (!reported || !used) ? 1 : used}
            tone={window.status === "rejected" ? "red" : usedTone(used)}
          />
        );
      })}
    </div>
  );
}

/* REQ-CCT-010/012: the command that signs an account in from a terminal,
   with a copy button, for a daemon on another machine. The current command
   is `… claude auth login`, which opens the browser itself; the older
   `claude` form needs /login typed after it. */
export function LoginCommand({ command }: { command: string }) {
  const text = command || "claude auth login";
  const needsSlashLogin = !/\bauth login$/.test(text.trim());
  return (
    <div className="rt-login" role="note">
      <span className="rt-login-k">Log in once in a terminal:</span>
      <span className="rt-login-cmd">
        <code className="mono">{text}</code>
        <CopyButton text={text} label="Copy login command" />
      </span>
      {needsSlashLogin ? (
        <span className="rt-login-k">
          then type <code className="mono">/login</code>.
        </span>
      ) : null}
    </div>
  );
}

/* The windows of an account: its own, else its limit snapshot's. */
export function accountWindows(account: ClaudeAccount, limit: LimitSnapshot | null): PlanWindow[] {
  if (account.windows.length > 0) {
    return account.windows;
  }
  return limit && Array.isArray(limit.windows) ? limit.windows : [];
}

/* One Claude Code account on the Quota page (REQ-CCT-011): its email, plan,
   login state, and its own plan windows. */
export function ClaudeAccountCard({
  account,
  limit,
}: {
  account: ClaudeAccount;
  limit: LimitSnapshot | null;
}) {
  const windows = accountWindows(account, limit);
  return (
    <article className="rt-limit rt-claude" aria-label={"Claude Code plan of " + account.name}>
      <header className="rt-limit-h">
        <ProviderLogo logo="claude" name="Claude Code" size={28} />
        <span className="rt-limit-title">
          <span className="rt-limit-name trunc">{account.email || "Claude Code"}</span>
          <span className="rt-limit-sub mono trunc">{account.name}</span>
        </span>
        {account.plan ? <Badge tone="violet">{planLabel(account.plan)}</Badge> : null}
        {account.logged_in ? (
          <Badge tone="green" dot>
            Logged in
          </Badge>
        ) : (
          <Badge tone="amber" dot>
            Not logged in
          </Badge>
        )}
      </header>
      <div className="rt-limit-b">
        {!account.logged_in ? <LoginCommand command={account.login_command} /> : null}
        <PlanWindows windows={windows} />
        <p className="rt-limit-none">
          {count(account.running)} running · {count(account.queued)} queued
        </p>
        {account.error ? (
          <p className="rt-limit-err" role="status">
            <Ic name="circle-alert" size={12} />
            <span>{account.error}</span>
          </p>
        ) : null}
      </div>
      {limit ? <CardFoot limit={limit} /> : null}
    </article>
  );
}
