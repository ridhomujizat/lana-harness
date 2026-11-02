import { useEffect, useMemo, useRef, useState } from "react";
import { ApiError, errorMessage } from "../api/client";
import { getAgentOverview } from "../api/rooms";
import type { AgentOverview as AgentOverviewData, RoomRow } from "../api/rooms";
import type { Agent } from "../api/agents";
import { ErrorNotice } from "../ui/ui";
import {
  agentFaceColors,
  AgentFace,
  PresenceLabel,
  Skeleton,
  TeamFace,
  useRooms,
} from "./presence";
import {
  agentHref,
  Icon,
  permissionMeta,
  providerLabel,
  splitModel,
} from "./agent-common";

/* REQ-ROOM-010: the alive agent profile. It loads API-ROOM-008 and refreshes
   every 10 s and whenever the live room row changes presence or last activity
   (design rooms-2). */

export interface AgentOverviewProps {
  name: string;
  agent?: Agent | null;
}

const FILTERS: readonly { key: string; label: string; kinds: string[] | null }[] =
  [
    { key: "all", label: "All", kinds: null },
    { key: "chats", label: "Chats", kinds: ["answered", "failed"] },
    { key: "learning", label: "Learning", kinds: ["learned", "remembered"] },
    { key: "problems", label: "Problems", kinds: ["failed", "approval"] },
  ];

const ACTIVITY_ICON: Record<string, string> = {
  answered: "check",
  failed: "close",
  asked: "search",
  approval: "lock",
  learned: "skills",
  remembered: "file",
  summarized: "sessions",
};

const ACTIVITY_COLOR: Record<string, string> = {
  answered: "success",
  failed: "error",
  asked: "warning",
  approval: "warning",
  learned: "brand",
  remembered: "blue",
  summarized: "gray",
};

function formatTokens(value: unknown): string {
  const count = Number(value) || 0;
  if (count >= 1000000) {
    const millions = count / 1000000;
    return (Number.isInteger(millions) ? millions : millions.toFixed(1)) + "M";
  }
  if (count >= 1000) {
    return Math.round(count / 1000) + "k";
  }
  return String(count);
}

function shortDate(ms: unknown): string {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) {
    return "—";
  }
  return new Date(value).toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
  });
}

function ageInDays(ms: unknown): number {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) {
    return 0;
  }
  return Math.max(0, Math.floor((Date.now() - value) / 86400000));
}

function factsLine(data: AgentOverviewData, agent?: Agent | null): string {
  const parts = [
    "With you since " +
      shortDate(data.created_at) +
      " (" +
      ageInDays(data.created_at) +
      " days)",
  ];
  if (agent) {
    const model = splitModel(agent).model || "Agent default";
    parts.push(model);
    parts.push(permissionMeta(agent.permission || agent.policy).label);
  }
  const channels = (data.looks_after.channels || [])
    .map((entry) => providerLabel(entry.channel))
    .filter(Boolean);
  parts.push(channels.length > 0 ? channels.join(" + ") : "Web");
  return parts.join(" · ");
}

function stepIcon(status: string) {
  if (status === "running") {
    return (
      <span className="ov-step-ring" aria-hidden="true">
        <span className="ov-step-dot" />
      </span>
    );
  }
  if (status === "ok") {
    return (
      <span className="ov-step-done" aria-hidden="true">
        <Icon name="check" className="icon-16" />
      </span>
    );
  }
  if (status === "error") {
    return (
      <span className="ov-step-failed" aria-hidden="true">
        <Icon name="close" className="icon-16" />
      </span>
    );
  }
  return <span className="ov-step-pending" aria-hidden="true" />;
}

export function AgentOverview({ name, agent }: AgentOverviewProps) {
  const { byName } = useRooms();
  const row = byName[name];
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading"
  );
  const [data, setData] = useState<AgentOverviewData | null>(null);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);
  const [filter, setFilter] = useState("all");
  const [token, setToken] = useState(0);
  const dataRef = useRef<AgentOverviewData | null>(null);
  const presenceKeyRef = useRef<string | null>(null);

  useEffect(() => {
    let alive = true;
    getAgentOverview(name)
      .then((next) => {
        if (!alive) {
          return;
        }
        dataRef.current = next;
        setData(next);
        setStatus("ready");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        if (!dataRef.current) {
          setError(errorMessage(failure));
          setDetails(failure instanceof ApiError ? failure.details : []);
          setStatus("error");
        }
      });
    return () => {
      alive = false;
    };
  }, [name, token]);

  /* REQ-ROOM-010: refresh at once when the room row changes presence or last
     activity. */
  const presenceKey = row
    ? (row.presence ? row.presence.state + ":" + row.presence.label : "") +
      "@" +
      row.last_active_at
    : "";
  useEffect(() => {
    if (presenceKeyRef.current === presenceKey) {
      return;
    }
    presenceKeyRef.current = presenceKey;
    if (!dataRef.current) {
      return;
    }
    setToken((value) => value + 1);
  }, [presenceKey]);

  /* Poll every 10 s so the profile stays alive without a reload. */
  useEffect(() => {
    const timer = setInterval(() => setToken((value) => value + 1), 10000);
    return () => clearInterval(timer);
  }, [name]);

  const activeFilter = FILTERS.find((entry) => entry.key === filter);
  const activeKinds = activeFilter ? activeFilter.kinds : null;

  const activity = useMemo(() => {
    const items = data ? data.activity || [] : [];
    if (!activeKinds) {
      return items;
    }
    return items.filter((item) => activeKinds.includes(item.kind));
  }, [data, activeKinds]);

  if (status === "loading") {
    return (
      <div className="ov-page" aria-hidden="true">
        <Skeleton className="ov-skeleton-hero" height={140} radius={14} />
        <div className="ov-columns">
          <div className="ov-col">
            <Skeleton height={160} radius={14} />
            <Skeleton height={220} radius={14} />
          </div>
          <div className="ov-col ov-col-side">
            <Skeleton height={180} radius={14} />
            <Skeleton height={140} radius={14} />
          </div>
        </div>
      </div>
    );
  }

  if (status === "error" || !data) {
    return (
      <div className="ov-page">
        <div role="alert">
          <ErrorNotice message={error} details={details} />
        </div>
        <button
          type="button"
          className="button-ui button-ui-secondary"
          onClick={() => setToken((value) => value + 1)}
        >
          Try again
        </button>
      </div>
    );
  }

  if (data.kind === "team") {
    return <TeamOverview data={data} agent={agent} byName={byName} />;
  }

  const colors = agentFaceColors(data.color);
  const week = data.week || { days: [], turns: 0, questions: 0, notes: 0, tokens: 0 };
  const days = week.days || [];
  const maxTurns = days.reduce(
    (max, day) => Math.max(max, Number(day.turns) || 0),
    0
  );
  const entries = (data.memory && data.memory.entries) || [];
  const total = Number(data.memory && data.memory.total) || entries.length;
  const more = Math.max(0, total - entries.length);
  const job = String(data.job || "");
  const channels = (data.looks_after && data.looks_after.channels) || [];
  const teams = (data.looks_after && data.looks_after.teams) || [];
  const hasLooksAfter = channels.length > 0 || teams.length > 0;

  const runLabel = data.now && data.now.turn_id
    ? "run #" + data.now.turn_id
    : "Open run";

  return (
    <div className="ov-page">
      <section
        className="ov-hero"
        style={{
          background:
            "linear-gradient(180deg, " + colors.bg + " 0%, var(--bg-primary) 100%)",
          borderColor: colors.bg,
        }}
      >
        <AgentFace
          name={data.name}
          color={data.color}
          presence={data.presence}
          size={96}
        />
        <div className="ov-hero-body">
          <div className="ov-hero-name-row">
            <h1 className="ov-hero-name">{data.name}</h1>
            {data.title ? (
              <span
                className="badge-ui ov-hero-title"
                style={{ background: colors.bg, color: colors.fg }}
              >
                {data.title}
              </span>
            ) : null}
          </div>
          {data.persona_line ? (
            <p className="ov-hero-persona">“{data.persona_line}”</p>
          ) : null}
          <p className="ov-status-pill">
            <PresenceLabel presence={data.presence} />
          </p>
          <p className="ov-hero-facts">{factsLine(data, agent)}</p>
        </div>
        <div className="ov-hero-actions">
          <a
            className="button-ui button-ui-primary"
            href={"#/chats/" + encodeURIComponent(data.name)}
          >
            {"Chat with " + data.name}
          </a>
          <a
            className="button-ui button-ui-secondary"
            href={agentHref(data.name, "general")}
          >
            Settings
          </a>
        </div>
      </section>

      <div className="ov-columns">
        <div className="ov-col">
          <section className="ov-card">
            <div className="ov-card-head">
              <h2 className="ov-card-title">Right now</h2>
              {data.now ? (
                <a
                  className="ov-card-link"
                  href={
                    "#/sessions/" + encodeURIComponent(data.now.session_id)
                  }
                >
                  {runLabel + " →"}
                </a>
              ) : null}
            </div>
            {data.now ? (
              <ol className="ov-steps">
                {data.now.steps.map((step, index) => (
                  <li
                    key={step.name + ":" + index}
                    className={"ov-step status-" + step.status}
                  >
                    {stepIcon(step.status)}
                    <span className="ov-step-name">{step.name}</span>
                    {step.at ? (
                      <span className="ov-step-time">
                        {new Date(step.at).toLocaleTimeString(undefined, {
                          hour: "2-digit",
                          minute: "2-digit",
                        })}
                      </span>
                    ) : null}
                  </li>
                ))}
              </ol>
            ) : (
              <p className="ov-idle">
                {"Idle — " +
                  (data.presence && data.presence.label
                    ? data.presence.label
                    : "resting")}
              </p>
            )}
          </section>

          <section className="ov-card">
            <div className="ov-card-head">
              <h2 className="ov-card-title">
                {"What " + data.name + " has been doing"}
              </h2>
              <div className="ov-filters" role="tablist" aria-label="Activity">
                {FILTERS.map((entry) => {
                  const active = entry.key === filter;
                  return (
                    <button
                      key={entry.key}
                      type="button"
                      role="tab"
                      aria-selected={active ? "true" : "false"}
                      className={"ov-filter" + (active ? " is-active" : "")}
                      onClick={() => setFilter(entry.key)}
                    >
                      {entry.label}
                    </button>
                  );
                })}
              </div>
            </div>
            {activity.length === 0 ? (
              <p className="muted">Nothing here yet.</p>
            ) : (
              <ul className="ov-activity">
                {activity.map((item, index) => (
                  <li
                    key={item.kind + ":" + item.at + ":" + index}
                    className="ov-activity-row"
                  >
                    <ActivityIcon kind={item.kind} />
                    <span className="ov-activity-body">
                      <span className="ov-activity-title">{item.title}</span>
                      {item.detail ? (
                        <span className="ov-activity-detail">
                          {item.detail}
                        </span>
                      ) : null}
                    </span>
                    <span className="ov-activity-time">
                      {shortDate(item.at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section className="ov-card">
            <div className="ov-card-head">
              <h2 className="ov-card-title">
                {"What " + data.name + " remembers about you"}
              </h2>
            </div>
            {entries.length === 0 ? (
              <p className="muted">Nothing remembered yet.</p>
            ) : (
              <div className="ov-chips">
                {entries.map((entry, index) => (
                  <span key={index} className="ov-chip">
                    {entry}
                  </span>
                ))}
                {more > 0 ? (
                  <a
                    className="ov-chip ov-chip-more"
                    href={agentHref(data.name, "persona")}
                  >
                    {"+ " + more + " more"}
                  </a>
                ) : null}
              </div>
            )}
          </section>
        </div>

        <div className="ov-col ov-col-side">
          <section className="ov-card">
            <div className="ov-card-head">
              <h2 className="ov-card-title">This week</h2>
            </div>
            <div className="ov-bars">
              {days.map((day, index) => {
                const today = index === days.length - 1;
                const turns = Number(day.turns) || 0;
                const height =
                  maxTurns > 0 ? Math.max(8, Math.round((turns / maxTurns) * 100)) : 8;
                return (
                  <span
                    key={day.day + ":" + index}
                    className={"ov-bar" + (today ? " is-today" : "")}
                    style={{
                      height: height + "%",
                      background: today ? colors.fg : colors.bg,
                    }}
                    title={day.day + " · " + turns}
                  />
                );
              })}
            </div>
            <div className="ov-stats">
              <div className="ov-stat">
                <span className="ov-stat-value">{week.turns}</span>
                <span className="ov-stat-label">tasks done</span>
              </div>
              <div className="ov-stat">
                <span className="ov-stat-value">{week.questions}</span>
                <span className="ov-stat-label">times asked you</span>
              </div>
              <div className="ov-stat">
                <span className="ov-stat-value">{week.notes}</span>
                <span className="ov-stat-label">new notes learned</span>
              </div>
              <div className="ov-stat">
                <span className="ov-stat-value">
                  {formatTokens(week.tokens)}
                </span>
                <span className="ov-stat-label">tokens</span>
              </div>
            </div>
          </section>

          <section className="ov-card">
            <div className="ov-card-head">
              <h2 className="ov-card-title">Job</h2>
              <a
                className="ov-card-link"
                href={agentHref(data.name, "persona")}
              >
                Edit JOB.md
              </a>
            </div>
            {job.trim() ? (
              <div className="ov-job">{job}</div>
            ) : (
              <p className="ov-job-empty">
                {"No job yet — describe what " +
                  data.name +
                  " is responsible for."}
              </p>
            )}
          </section>

          <section className="ov-card">
            <div className="ov-card-head">
              <h2 className="ov-card-title">Looks after</h2>
            </div>
            {hasLooksAfter ? (
              <ul className="ov-look">
                {channels.map((entry, index) => (
                  <li key={"c" + index} className="ov-look-row">
                    <span className="ov-look-icon" aria-hidden="true">
                      <Icon name="channels" className="icon-16" />
                    </span>
                    <span className="ov-look-body">
                      <span className="ov-look-name">{entry.channel}</span>
                      {entry.match ? (
                        <span className="ov-look-detail">{entry.match}</span>
                      ) : null}
                    </span>
                  </li>
                ))}
                {teams.map((team, index) => (
                  <li key={"t" + index} className="ov-look-row">
                    <span className="ov-look-icon" aria-hidden="true">
                      <Icon name="agents" className="icon-16" />
                    </span>
                    <span className="ov-look-body">
                      <span className="ov-look-name">{team}</span>
                      <span className="ov-look-detail">Team</span>
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="muted">Nothing yet.</p>
            )}
          </section>
        </div>
      </div>
    </div>
  );
}

function ActivityIcon({ kind }: { kind: string }) {
  const colors = agentFaceColors(ACTIVITY_COLOR[kind] || "gray");
  return (
    <span
      className="ov-activity-icon"
      aria-hidden="true"
      style={{ background: colors.bg, color: colors.fg }}
    >
      <Icon name={ACTIVITY_ICON[kind] || "activity"} className="icon-16" />
    </span>
  );
}

function TeamOverview({
  data,
  agent,
  byName,
}: {
  data: AgentOverviewData;
  agent?: Agent | null;
  byName: Record<string, RoomRow>;
}) {
  const colors = agentFaceColors(data.color);
  const lead = agent && agent.lead ? String(agent.lead) : "";
  const members = agent && Array.isArray(agent.members)
    ? agent.members.map((member) => String(member))
    : [];
  const names = [lead, ...members].filter(Boolean);
  const faces = names.map((member) => ({
    name: member,
    presence: byName[member] ? byName[member].presence : null,
  }));
  const count = names.length;

  return (
    <div className="ov-page">
      <section
        className="ov-hero"
        style={{
          background:
            "linear-gradient(180deg, " + colors.bg + " 0%, var(--bg-primary) 100%)",
          borderColor: colors.bg,
        }}
      >
        {faces.length > 0 ? (
          <TeamFace faces={faces} size={52} />
        ) : (
          <AgentFace name={data.name} color="brand" size={96} />
        )}
        <div className="ov-hero-body">
          <div className="ov-hero-name-row">
            <h1 className="ov-hero-name">{data.name}</h1>
            <span className="badge-ui badge-ui-brand">Team</span>
          </div>
          {data.title ? <p className="ov-hero-persona">{data.title}</p> : null}
          <p className="ov-status-pill">
            <PresenceLabel presence={data.presence} />
          </p>
          <p className="ov-hero-facts">
            {count + (count === 1 ? " member" : " members") +
              " · created " +
              shortDate(data.created_at)}
          </p>
        </div>
        <div className="ov-hero-actions">
          <a
            className="button-ui button-ui-primary"
            href={"#/chats/" + encodeURIComponent(data.name)}
          >
            {"Chat with " + data.name}
          </a>
        </div>
      </section>

      <section className="ov-card">
        <div className="ov-card-head">
          <h2 className="ov-card-title">Members</h2>
        </div>
        {names.length === 0 ? (
          <p className="muted">No members yet.</p>
        ) : (
          <ul className="ov-members">
            {names.map((member) => {
              const memberRow = byName[member];
              const isLead = member === lead;
              return (
                <li key={member} className="ov-member">
                  <AgentFace
                    name={member}
                    color={memberRow ? memberRow.color : undefined}
                    presence={memberRow ? memberRow.presence : null}
                    size={32}
                  />
                  <span className="ov-member-body">
                    <span className="ov-member-name">{member}</span>
                    <span className="ov-member-status">
                      {memberRow && memberRow.presence
                        ? memberRow.presence.label
                        : "No activity yet"}
                    </span>
                  </span>
                  {isLead ? (
                    <span className="badge-ui badge-ui-brand ov-member-lead">
                      Lead
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
