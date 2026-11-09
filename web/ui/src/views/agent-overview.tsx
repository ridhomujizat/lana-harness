import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, HTMLAttributes, ReactNode } from "react";
import { ApiError, errorMessage } from "../api/client";
import { getAgentOverview } from "../api/rooms";
import type { AgentOverview as AgentOverviewData, RoomRow } from "../api/rooms";
import type { Agent } from "../api/agents";
import { detailText } from "../ui/ui";
import { Ic } from "../ui/icons";
import {
  Avatar,
  AvatarStack,
  Badge,
  Button,
  Chip,
  EmptyState,
  ErrorState,
  Panel,
  Seg,
  Skeleton,
  Stat,
} from "../ui/gr8r";
import {
  agentHref,
  permissionMeta,
  presenceTone,
  providerLabel,
  splitModel,
} from "./agent-common";
import { gr8rColor, useRooms } from "./presence";
import { displayLabel } from "./agent-label";

/* REQ-ROOM-010: the alive agent profile. It loads API-ROOM-008 and refreshes
   every 10 s and whenever the live room row changes presence or last activity
   (design rooms-2). Rendered with the Gr8r page markup: a hero, a two-column
   panel grid, checklist steps, a lined activity feed, and stat tiles. */

export interface AgentOverviewProps {
  name: string;
  agent?: Agent | null;
  /* The agent detail tab bar. The Overview hero sits above it (REQ-WEB-047:
     the hero replaces the page header on this tab), so the page passes the
     tabs in and the tab panel wraps only the content below them. */
  tabs?: ReactNode;
  panelProps?: HTMLAttributes<HTMLElement>;
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
  failed: "x",
  asked: "search",
  approval: "lock",
  learned: "layers",
  remembered: "file-text",
  summarized: "history",
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

/* The error details of the overview API, rendered under the error state. */
function DetailsList({ details }: { details: unknown[] }) {
  if (!details || details.length === 0) {
    return null;
  }
  return (
    <ul className="err-list">
      {details.map((detail, index) => (
        <li key={index}>{detailText(detail)}</li>
      ))}
    </ul>
  );
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
        <Ic name="check" size={12} />
      </span>
    );
  }
  if (status === "error") {
    return (
      <span className="ov-step-failed" aria-hidden="true">
        <Ic name="x" size={12} />
      </span>
    );
  }
  return <span className="ov-step-pending" aria-hidden="true" />;
}

export function AgentOverview({
  name,
  agent,
  tabs,
  panelProps,
}: AgentOverviewProps) {
  const inPanel = (body: ReactNode) =>
    panelProps ? <section {...panelProps}>{body}</section> : body;
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

  /* The display name of the profile: the room row carries display_name, and the
     overview payload keeps only the ID name. */
  const viewName = displayLabel(row) || (data ? data.name : name);

  if (status === "loading") {
    return (
      <div className="ov-page" aria-hidden="true">
        <Skeleton className="ov-skeleton-hero" height={120} radius={12} />
        {tabs}
        {inPanel(
          <div className="grid2 ov-grid">
            <div className="stack">
              <Skeleton height={160} radius={12} />
              <Skeleton height={220} radius={12} />
            </div>
            <div className="stack">
              <Skeleton height={180} radius={12} />
              <Skeleton height={140} radius={12} />
            </div>
          </div>
        )}
      </div>
    );
  }

  if (status === "error" || !data) {
    return (
      <div className="ov-page">
        {tabs}
        {inPanel(
        <div className="panel" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not load the profile"
            text={error}
            action={
              <Button
                variant="secondary"
                onClick={() => setToken((value) => value + 1)}
              >
                Try again
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
        )}
      </div>
    );
  }

  if (data.kind === "team") {
    return (
      <div className="ov-page">
        {tabs}
        {inPanel(<TeamOverview data={data} agent={agent} byName={byName} />)}
      </div>
    );
  }

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
  const presenceState = data.presence ? data.presence.state : "";

  const runLabel =
    data.now && data.now.turn_id ? "run #" + data.now.turn_id : "Open run";

  return (
    <div className="ov-page">
      <section className="panel ov-hero">
        <div className="ov-hero-in">
          <Avatar
            size="xl"
            name={viewName}
            color={gr8rColor(data.color)}
            presence={presenceState === "online" || presenceState === "working"}
          />
          <div className="ov-hero-body">
            <div className="ov-hero-name-row">
              <h1 className="ov-hero-name">{viewName}</h1>
              {data.title ? <Badge tone="accent">{data.title}</Badge> : null}
              {data.presence ? (
                <Badge tone={presenceTone(presenceState)} dot>
                  {data.presence.label}
                </Badge>
              ) : null}
            </div>
            {data.persona_line ? (
              <p className="ov-hero-persona">“{data.persona_line}”</p>
            ) : null}
            <p className="ov-hero-facts">{factsLine(data, agent)}</p>
          </div>
          <div className="ov-hero-actions">
            <Button
              as="a"
              variant="primary"
              href={"#/chats/" + encodeURIComponent(data.name)}
            >
              {"Chat with " + viewName}
            </Button>
            <Button
              as="a"
              variant="secondary"
              href={agentHref(data.name, "general")}
            >
              Settings
            </Button>
          </div>
        </div>
      </section>

      {tabs}
      {inPanel(
      <div className="grid2 ov-grid">
        <div className="stack">
          <Panel
            title="Right now"
            actions={
              data.now ? (
                <Button
                  as="a"
                  size="sm"
                  variant="ghost"
                  href={
                    "#/sessions/" + encodeURIComponent(data.now.session_id)
                  }
                >
                  {runLabel}
                </Button>
              ) : null
            }
          >
            {data.now ? (
              <ol className="ov-steps">
                {data.now.steps.map((step, index) => (
                  <li
                    key={step.name + ":" + index}
                    className={"ov-step status-" + step.status}
                  >
                    {stepIcon(step.status)}
                    <span className="ov-step-name grow">{step.name}</span>
                    {step.at ? (
                      <span className="ov-step-time num">
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
          </Panel>

          <Panel
            title={"What " + viewName + " has been doing"}
            actions={
              <Seg
                className="ov-filters"
                aria-label="Activity"
                options={FILTERS.map((entry) => ({
                  value: entry.key,
                  label: entry.label,
                }))}
                value={filter}
                onChange={setFilter}
              />
            }
          >
            {activity.length === 0 ? (
              <EmptyState size="sm" icon="activity" text="Nothing here yet." />
            ) : (
              <ul className="feed lined ov-activity">
                {activity.map((item, index) => (
                  <li
                    key={item.kind + ":" + item.at + ":" + index}
                    className="fitem ov-activity-row"
                  >
                    <ActivityIcon kind={item.kind} />
                    <div className="grow ov-activity-body">
                      <span className="ov-activity-title">{item.title}</span>
                      {item.detail ? (
                        <span className="ov-activity-detail">
                          {item.detail}
                        </span>
                      ) : null}
                    </div>
                    <span className="ov-activity-time">
                      {shortDate(item.at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Panel>

          <Panel title={"What " + viewName + " remembers about you"}>
            {entries.length === 0 ? (
              <p className="muted">Nothing remembered yet.</p>
            ) : (
              <div className="ov-chips">
                {entries.map((entry, index) => (
                  <Chip key={index}>{entry}</Chip>
                ))}
                {more > 0 ? (
                  <a
                    className="chip ov-chip-more"
                    href={agentHref(data.name, "persona")}
                  >
                    {"+ " + more + " more"}
                  </a>
                ) : null}
              </div>
            )}
          </Panel>
        </div>

        <div className="stack">
          <Panel title="This week">
            <div className="ov-bars">
              {days.map((day, index) => {
                const today = index === days.length - 1;
                const turns = Number(day.turns) || 0;
                const height =
                  maxTurns > 0
                    ? Math.max(8, Math.round((turns / maxTurns) * 100))
                    : 8;
                return (
                  <span
                    key={day.day + ":" + index}
                    className={"ov-bar" + (today ? " is-today" : "")}
                    style={{ height: height + "%" }}
                    title={day.day + " · " + turns}
                  />
                );
              })}
            </div>
            <div className="stats ov-stats">
              <Stat label="tasks done" value={week.turns} />
              <Stat label="times asked you" value={week.questions} />
              <Stat label="new notes learned" value={week.notes} />
              <Stat label="tokens" value={formatTokens(week.tokens)} />
            </div>
          </Panel>

          <Panel
            title="Job"
            actions={
              <Button
                as="a"
                size="sm"
                variant="ghost"
                href={agentHref(data.name, "persona")}
              >
                Edit JOB.md
              </Button>
            }
          >
            {job.trim() ? (
              <p className="ov-job">{job}</p>
            ) : (
              <p className="muted">
                {"No job yet — describe what " +
                  viewName +
                  " is responsible for."}
              </p>
            )}
          </Panel>

          <Panel title="Looks after">
            {hasLooksAfter ? (
              <ul className="ov-look">
                {channels.map((entry, index) => (
                  <li key={"c" + index} className="ov-look-row">
                    <span className="ov-look-icon" aria-hidden="true">
                      <Ic name="radio-tower" size={15} />
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
                      <Ic name="users" size={15} />
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
          </Panel>
        </div>
      </div>
      )}
    </div>
  );
}

function ActivityIcon({ kind }: { kind: string }) {
  return (
    <span
      className="ov-activity-icon"
      aria-hidden="true"
      style={
        {
          "--c": gr8rColor(ACTIVITY_COLOR[kind] || "gray"),
        } as CSSProperties
      }
    >
      <Ic name={ACTIVITY_ICON[kind] || "activity"} size={14} />
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
  const lead = agent && agent.lead ? String(agent.lead) : "";
  const members =
    agent && Array.isArray(agent.members)
      ? agent.members.map((member) => String(member))
      : [];
  const names = [lead, ...members].filter(Boolean);
  const count = names.length;
  const presenceState = data.presence ? data.presence.state : "";
  const teamName = displayLabel(byName[data.name]) || data.name;

  return (
    <div className="ov-page">
      <section className="panel ov-hero">
        <div className="ov-hero-in">
          {names.length > 0 ? (
            <AvatarStack
              size="lg"
              max={4}
              items={names.map((member) => ({
                name: displayLabel(byName[member]) || member,
                color: gr8rColor(member),
              }))}
            />
          ) : (
            <Avatar size="xl" name={data.name} color="var(--violet)" />
          )}
          <div className="ov-hero-body">
            <div className="ov-hero-name-row">
              <h1 className="ov-hero-name">{teamName}</h1>
              <Badge tone="accent" dot>
                Team
              </Badge>
              {data.presence ? (
                <Badge tone={presenceTone(presenceState)} dot>
                  {data.presence.label}
                </Badge>
              ) : null}
            </div>
            {data.title ? <p className="ov-hero-persona">{data.title}</p> : null}
            <p className="ov-hero-facts">
              {count +
                (count === 1 ? " member" : " members") +
                " · created " +
                shortDate(data.created_at)}
            </p>
          </div>
          <div className="ov-hero-actions">
            <Button
              as="a"
              variant="primary"
              href={"#/chats/" + encodeURIComponent(data.name)}
            >
              {"Chat with " + teamName}
            </Button>
          </div>
        </div>
      </section>

      <Panel title="Members">
        {names.length === 0 ? (
          <p className="muted">No members yet.</p>
        ) : (
          <ul className="ov-members">
            {names.map((member) => {
              const memberRow = byName[member];
              const isLead = member === lead;
              return (
                <li key={member} className="ov-member">
                  <Avatar
                    size="lg"
                    name={displayLabel(memberRow) || member}
                    color={gr8rColor(memberRow ? memberRow.color : undefined)}
                  />
                  <span className="ov-member-body">
                    <span className="ov-member-name">
                      {displayLabel(memberRow) || member}
                    </span>
                    <span className="ov-member-status">
                      {memberRow && memberRow.presence
                        ? memberRow.presence.label
                        : "No activity yet"}
                    </span>
                  </span>
                  {isLead ? (
                    <Badge tone="accent" dot>
                      Lead
                    </Badge>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </Panel>
    </div>
  );
}
