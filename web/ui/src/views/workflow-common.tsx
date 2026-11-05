import { useEffect, useRef, useState } from "react";
import { listAgents } from "../api/agents";
import type { Agent } from "../api/agents";
import { openWorkflowStream } from "../api/workflows";
import type {
  NodeStatus,
  RunStatus,
  WorkflowChange,
  WorkflowStatus,
} from "../api/workflows";
import { avatarFor } from "../lib/avatar";
import { Avatar, Badge } from "../ui/gr8r";
import type { BadgeTone } from "../ui/gr8r";

/* Shared bits of the Workflows pages (spec #33): status badges, time words,
   guardian faces, the live stream hook and the agent list. */

const AVATAR_COLORS: Record<string, string> = {
  "tone-brand": "var(--violet)",
  "tone-success": "var(--green)",
  "tone-warning": "var(--amber)",
  "tone-error": "var(--red)",
  "tone-gray": "var(--gray)",
};

export function agentColor(name: string): string {
  return AVATAR_COLORS[avatarFor(name).colorClass] || "var(--violet)";
}

export function AgentFace({ name, size }: { name: string; size?: "sm" | "md" }) {
  return <Avatar name={name} color={agentColor(name)} size={size || "sm"} />;
}

const WF_TONES: Record<WorkflowStatus, BadgeTone> = {
  active: "green",
  paused: "gray",
  draft: "amber",
  error: "red",
};

const WF_WORDS: Record<WorkflowStatus, string> = {
  active: "Active",
  paused: "Paused",
  draft: "Draft",
  error: "Failing",
};

export function WorkflowBadge({
  status,
  waiting,
}: {
  status: WorkflowStatus;
  waiting?: boolean;
}) {
  if (waiting && status === "active") {
    return (
      <Badge tone="amber" dot>
        Waiting
      </Badge>
    );
  }
  return (
    <Badge tone={WF_TONES[status] || "gray"} dot>
      {WF_WORDS[status] || status}
    </Badge>
  );
}

export const RUN_TONES: Record<RunStatus, BadgeTone> = {
  queued: "gray",
  running: "accent",
  waiting: "amber",
  succeeded: "green",
  failed: "red",
  cancelled: "gray",
};

export const RUN_WORDS: Record<RunStatus, string> = {
  queued: "Queued",
  running: "Running",
  waiting: "Waiting",
  succeeded: "Succeeded",
  failed: "Failed",
  cancelled: "Cancelled",
};

export const NODE_WORDS: Record<NodeStatus, string> = {
  pending: "Pending",
  running: "Running",
  succeeded: "Done",
  failed: "Failed",
  skipped: "Skipped",
  waiting: "Waiting",
  fallback: "Fallback",
};

export function ago(ms: number, now = Date.now()): string {
  if (!ms) {
    return "never";
  }
  const sec = Math.max(0, Math.round((now - ms) / 1000));
  if (sec < 45) {
    return "just now";
  }
  const min = Math.round(sec / 60);
  if (min < 60) {
    return min + " min ago";
  }
  const hours = Math.round(min / 60);
  if (hours < 24) {
    return hours + " h ago";
  }
  const days = Math.round(hours / 24);
  if (days === 1) {
    return "yesterday";
  }
  return days + " days ago";
}

export function duration(ms: number): string {
  if (!ms || ms < 0) {
    return "0 s";
  }
  if (ms < 1000) {
    return "<1 s";
  }
  const sec = Math.round(ms / 1000);
  if (sec < 60) {
    return sec + " s";
  }
  const min = Math.floor(sec / 60);
  if (min < 60) {
    return min + " m " + String(sec % 60).padStart(2, "0") + " s";
  }
  return Math.floor(min / 60) + " h " + (min % 60) + " m";
}

export function clock(ms: number, seconds = false): string {
  if (!ms) {
    return "";
  }
  const date = new Date(ms);
  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  const ss = String(date.getSeconds()).padStart(2, "0");
  return seconds ? hh + ":" + mm + ":" + ss : hh + ":" + mm;
}

/* "Today 07:00", "Yesterday 07:00", "Oct 30, 07:00". */
export function dayClock(ms: number, now = Date.now()): string {
  if (!ms) {
    return "";
  }
  const date = new Date(ms);
  const today = new Date(now);
  const startToday = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime();
  if (ms >= startToday + 2 * 86400000) {
    const month = date.toLocaleString("en", { month: "short" });
    return month + " " + date.getDate() + ", " + clock(ms);
  }
  if (ms >= startToday + 86400000) {
    return "Tomorrow " + clock(ms);
  }
  if (ms >= startToday) {
    return "Today " + clock(ms);
  }
  if (ms >= startToday - 86400000) {
    return "Yesterday " + clock(ms);
  }
  const month = date.toLocaleString("en", { month: "short" });
  return month + " " + date.getDate() + ", " + clock(ms);
}

export function tokens(count: number): string {
  if (count >= 1000) {
    return (count / 1000).toFixed(count >= 10000 ? 0 : 1) + "k";
  }
  return String(count || 0);
}

export function pct(value: number | null | undefined, digits = 0): string {
  if (value === null || value === undefined) {
    return "—";
  }
  return (value * 100).toFixed(digits) + "%";
}

/* Subscribes to API-WF-019 and calls `onChange` for each change. The handler
   ref keeps the latest closure without reconnecting. */
export function useWorkflowStream(onChange: (change: WorkflowChange) => void) {
  const handler = useRef(onChange);
  useEffect(() => {
    handler.current = onChange;
  });
  useEffect(() => {
    const stream = openWorkflowStream((change) => handler.current(change));
    return () => stream.close();
  }, []);
}

export function useAgentNames(): Agent[] {
  const [agents, setAgents] = useState<Agent[]>([]);
  useEffect(() => {
    let live = true;
    listAgents()
      .then((response) => {
        if (live) {
          setAgents(response.agents.filter((agent) => agent.kind !== "team"));
        }
      })
      .catch(() => {
        /* The pickers fall back to free text when agents cannot load. */
      });
    return () => {
      live = false;
    };
  }, []);
  return agents;
}

/* A clock for live durations: re-renders every `ms` while mounted. */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(timer);
  }, [ms]);
  return now;
}

export function prettyValue(value: unknown): string {
  if (value === undefined) {
    return "";
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function shortValue(value: unknown, limit = 72): string {
  let text: string;
  try {
    text = typeof value === "string" ? JSON.stringify(value) : JSON.stringify(value);
  } catch {
    text = String(value);
  }
  if (Array.isArray(value) && text.length > limit) {
    return value.length + " items";
  }
  return text.length > limit ? text.slice(0, limit - 1) + "…" : text;
}
