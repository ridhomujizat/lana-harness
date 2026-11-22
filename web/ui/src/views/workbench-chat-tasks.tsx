import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { Ic } from "../ui/icons";
import { Markdown } from "../lib/markdown";
import { errorMessage } from "../api/client";
import { getChatAgent, type ChatAgentDetail, type ChatItem, type ChatTask } from "../api/workbench-chat";

/* Spec 71 REQ-WC-061, 062: subagents and background tasks of the pane's
   Claude Code (owner decision 10). */

export interface ChatTaskContext {
  sessionId: string;
  tasks: ChatTask[];
}

export const TasksContext = createContext<ChatTaskContext>({ sessionId: "", tasks: [] });

function elapsed(from?: string, to?: string): string {
  if (!from) return "";
  const start = new Date(from).getTime();
  const end = to ? new Date(to).getTime() : Date.now();
  if (Number.isNaN(start) || Number.isNaN(end)) return "";
  const s = Math.max(0, Math.round((end - start) / 1000));
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m " + (s % 60) + "s";
  return Math.floor(m / 60) + "h " + (m % 60) + "m";
}

function StatusIcon({ status }: { status: string }) {
  if (status === "running") return <Ic name="loader" size={13} className="wbc-spin wbc-st-running" />;
  if (status === "completed") return <Ic name="check" size={13} className="wbc-st-done" />;
  if (status === "killed") return <Ic name="square" size={12} className="wbc-st-killed" />;
  return <Ic name="x" size={13} className="wbc-st-failed" />;
}

/* The steps of a subagent, from its own transcript. */
function AgentSteps({ detail }: { detail: ChatAgentDetail }) {
  /* SubagentHandback carries the subagent's report; it is not a step. */
  const handback = [...detail.items].reverse().find((i) => i.kind === "tool" && i.name === "SubagentHandback");
  const tools = detail.items.filter((i) => i.kind === "tool" && i.name !== "SubagentHandback");
  const texts = detail.items.filter((i) => i.kind === "assistant" && i.text);
  const handbackText = typeof handback?.input?.message === "string" ? (handback.input.message as string) : "";
  const report = handbackText || (detail.finished ? texts[texts.length - 1]?.text : "");
  return (
    <div className="wbc-agent-body">
      {tools.length ? (
        <div className="wbc-agent-steps">
          {tools.slice(-40).map((t) => (
            <div key={t.id} className="wbc-agent-step">
              <span className={"wbc-tool-st st-" + (t.status || "running")}>
                {t.status === "done" ? <Ic name="check" size={12} /> : t.status === "error" ? <Ic name="x" size={12} /> : <Ic name="loader" size={12} />}
              </span>
              <span className="wbc-verb">{t.summary?.verb || t.name}</span>
              <span className="wbc-target">{t.summary?.target || ""}</span>
            </div>
          ))}
          {tools.length > 40 ? <div className="wbc-more">{tools.length - 40} earlier steps</div> : null}
        </div>
      ) : (
        <div className="wbc-more">No steps yet</div>
      )}
      {report ? (
        <div className="wbc-agent-report">
          <div className="wbc-agent-report-head">Final report</div>
          <Markdown text={report} />
        </div>
      ) : null}
    </div>
  );
}

/* An Agent / Task tool call (REQ-WC-061). */
export function SubagentRow({ item }: { item: ChatItem }) {
  const { sessionId, tasks } = useContext(TasksContext);
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<ChatAgentDetail | null>(null);
  const [error, setError] = useState("");
  const agentId = item.agent?.agent_id || "";
  const task = tasks.find((t) => t.id === agentId || (t.tool_use_id && t.tool_use_id === item.tool_use_id));
  const input = (item.input || {}) as Record<string, unknown>;
  const type = item.agent?.agent_type || (typeof input.subagent_type === "string" ? input.subagent_type : "") || "agent";
  const title = (typeof input.description === "string" && input.description) || item.summary?.target || "Subagent";
  const status = task?.status || (item.status === "done" ? "completed" : item.status === "error" ? "failed" : "running");
  const running = status === "running";

  useEffect(() => {
    if (!open || !agentId) return;
    let stop = false;
    let timer = 0;
    const load = () => {
      getChatAgent(sessionId, agentId)
        .then((d) => {
          if (stop) return;
          setDetail(d);
          setError("");
          if (!d.finished) timer = window.setTimeout(load, 2000);
        })
        .catch((err) => {
          if (!stop) setError(errorMessage(err));
        });
    };
    load();
    return () => {
      stop = true;
      window.clearTimeout(timer);
    };
  }, [open, agentId, sessionId]);

  return (
    <div className={"wbc-tool-row wbc-agent" + (open ? " is-open" : "")}>
      <button type="button" className="wbc-tool-line" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="wbc-tool-st">
          <StatusIcon status={status} />
        </span>
        <Ic name="bot" size={13} className="wbc-agent-ic" />
        <span className="wbc-agent-type">{type}</span>
        <span className="wbc-target wbc-agent-title">{title}</span>
        <span className="wb-spacer" />
        <span className="wbc-meta">
          {item.agent?.background || task ? "background · " : ""}
          {running ? elapsed(task?.started || item.at) : status}
          {detail ? " · " + detail.steps + (detail.steps === 1 ? " step" : " steps") : ""}
        </span>
        <Ic name={open ? "chevron-up" : "chevron-down"} size={12} />
      </button>
      {open ? (
        agentId ? (
          detail ? (
            <AgentSteps detail={detail} />
          ) : (
            <div className="wbc-agent-body wbc-more">{error || "Loading the subagent…"}</div>
          )
        ) : (
          <pre className="wbc-out">{item.result?.text || String(input.prompt || "").slice(0, 2000)}</pre>
        )
      ) : null}
      {!open && task?.summary && !running ? <div className="wbc-agent-summary">{task.summary}</div> : null}
    </div>
  );
}

/* The Background chip above the composer and its list (REQ-WC-062). */
export function BackgroundChip({ tasks }: { tasks: ChatTask[] }) {
  const [open, setOpen] = useState(false);
  const [, tick] = useState(0);
  const running = useMemo(() => tasks.filter((t) => t.status === "running"), [tasks]);
  useEffect(() => {
    if (!open || !running.length) return;
    const t = window.setInterval(() => tick((n) => n + 1), 1000);
    return () => window.clearInterval(t);
  }, [open, running.length]);
  if (!tasks.length) return null;
  const list = [...tasks].reverse();
  return (
    <div className="wbc-bg">
      <span className={"wbc-anchor" + (open ? " is-open" : "")}>
        <button type="button" className={"wbc-bg-chip" + (running.length ? " is-running" : "")} onClick={() => setOpen(!open)}>
          {running.length ? <Ic name="loader" size={12} className="wbc-spin" /> : <Ic name="layers" size={12} />}
          {running.length ? running.length + " running in background" : "Background · " + tasks.length}
          <Ic name={open ? "chevron-down" : "chevron-up"} size={12} />
        </button>
        <div className="wbc-pop wbc-pop-left wbc-bg-pop" style={{ width: 460 }} onMouseDown={(e) => e.stopPropagation()}>
          <div className="wbc-pop-sec">Background tasks</div>
          {list.map((t) => (
            <div key={t.id} className={"wbc-bg-row" + (t.status === "running" ? "" : " is-done")}>
              <StatusIcon status={t.status} />
              <Ic name={t.kind === "agent" ? "bot" : "square-terminal"} size={13} className="wbc-bg-kind" />
              <span className="wbc-bg-text">
                <span className="wbc-bg-title">
                  {t.agent_type ? <em>{t.agent_type}</em> : null}
                  {t.title}
                </span>
                {t.summary ? <span className="wbc-bg-sum">{t.summary}</span> : null}
              </span>
              <span className="wbc-bg-time">{t.status === "running" ? elapsed(t.started) : elapsed(t.started, t.ended) || t.status}</span>
            </div>
          ))}
          <div className="wbc-pop-foot">From the Claude Code transcript · ask Claude to stop a task</div>
        </div>
      </span>
    </div>
  );
}
