import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "../api/client";
import {
  approveReview,
  getDecidedReview,
  getReview,
  rejectReview,
} from "../api/knowledge";
import type { DecidedReviewItem, ReviewItem } from "../api/knowledge";
import { Ic } from "../ui/icons";
import { Badge, Button, EmptyState, Skeleton } from "../ui/gr8r";
import { AgentName } from "./agent-avatar";
import { useAgentLabel } from "./agent-label";

/* Spec 48 REQ-KGR-054 (and spec 46 REQ-DKN-060, -061): proposals waiting for
   the owner, as cards with the change itself, plus recent decisions. */

type Filter = "waiting" | "knowledge" | "skills" | "decided";

/* created_at and decided_at may be Unix seconds or milliseconds. */
function toMs(value: number | undefined): number {
  const n = Number(value) || 0;
  return n > 0 && n < 1e12 ? n * 1000 : n;
}

function when(value: number | undefined): string {
  const ms = toMs(value);
  if (!ms) return "";
  const minutes = Math.round((Date.now() - ms) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return minutes + " min ago";
  if (minutes < 24 * 60) return Math.round(minutes / 60) + " h ago";
  return new Date(ms).toLocaleString(undefined, {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function actionTitle(item: ReviewItem): string {
  switch (item.action) {
    case "append":
    case "update":
    case "add":
      return "Add to " + item.title;
    case "replace":
    case "edit":
      return "Change " + item.title;
    case "supersede":
      return "Update a fact in " + item.title;
    case "create":
    case "new":
      return "New page: " + item.title;
    default:
      return item.title;
  }
}

function ChangeBlock({ item }: { item: ReviewItem }) {
  const added = String(item.body || "")
    .split("\n")
    .filter((line) => line.trim());
  const removed = String(item.old_text || "")
    .split("\n")
    .filter((line) => line.trim());
  return (
    <div className="kr-diff">
      {added.map((line, index) => (
        <p key={"a" + index} className="kr-add">
          <span aria-hidden="true">+</span>
          <span>{line}</span>
        </p>
      ))}
      {removed.map((line, index) => (
        <p key={"r" + index} className="kr-del">
          <span aria-hidden="true">~</span>
          <del>{line}</del>
        </p>
      ))}
    </div>
  );
}

function SkillBlock({ item }: { item: ReviewItem }) {
  const [full, setFull] = useState(false);
  const lines = String(item.body || "").split("\n");
  const long = lines.length > 8;
  return (
    <>
      <pre className={"kr-skill" + (full ? " full" : "")}>
        {full || !long ? item.body : lines.slice(0, 8).join("\n") + "\n…"}
      </pre>
      {long ? (
        <button type="button" className="review-toggle" onClick={() => setFull((v) => !v)}>
          <Ic name={full ? "chevron-up" : "chevron-down"} size={13} />
          {full ? "Show less" : "Open full"}
        </button>
      ) : null}
    </>
  );
}

function ReviewCard({
  item,
  busy,
  onDecide,
}: {
  item: ReviewItem;
  busy: boolean;
  onDecide: (item: ReviewItem, approve: boolean) => void;
}) {
  const label = useAgentLabel();
  const skill = item.kind === "skill";
  const cross = item.from && item.from !== item.agent;
  return (
    <li className="kr-card" data-testid="review-item">
      <div className="kr-head">
        <Badge tone={skill ? "violet" : "gray"}>{skill ? "New skill" : "Knowledge"}</Badge>
        {cross ? (
          <span className="kr-route">
            <AgentName name={item.from} label={label(item.from)} />
            <Ic name="arrow-right" size={13} />
            <AgentName name={item.agent} label={label(item.agent)} />
          </span>
        ) : (
          <AgentName name={item.agent} label={label(item.agent)} />
        )}
        {item.owner_turn ? null : (
          <Badge tone="amber" dot title="This did not come from a turn you started">
            Needs review
          </Badge>
        )}
        <span className="kr-time">{when(item.created_at)}</span>
      </div>
      <h3 className="kr-title">{skill ? item.title : actionTitle(item)}</h3>
      {item.description ? <p className="kr-desc">{item.description}</p> : null}
      {!skill && cross ? (
        <p className="kr-desc">
          {label(item.from) || item.from} found this outside its own field, so it asks before
          writing into {label(item.agent) || item.agent}&apos;s knowledge.
        </p>
      ) : null}
      {skill ? <SkillBlock item={item} /> : <ChangeBlock item={item} />}
      <div className="kr-acts">
        <Button
          variant="primary"
          icon="check"
          disabled={busy}
          loading={busy}
          onClick={() => onDecide(item, true)}
        >
          Approve
        </Button>
        <Button variant="secondary" icon="x" disabled={busy} onClick={() => onDecide(item, false)}>
          Reject
        </Button>
        {!skill ? (
          <span className="kr-note">Approved changes are written at the agent&apos;s next promotion.</span>
        ) : null}
      </div>
    </li>
  );
}

function statusBadge(status: string) {
  if (status === "rejected") return <Badge tone="red">Rejected</Badge>;
  if (status === "applied") return <Badge tone="green">Applied</Badge>;
  return <Badge tone="accent">Approved</Badge>;
}

export function ReviewTab({ onChanged }: { onChanged?: () => void }) {
  const label = useAgentLabel();
  const [filter, setFilter] = useState<Filter>("waiting");
  const [items, setItems] = useState<ReviewItem[] | null>(null);
  const [decided, setDecided] = useState<DecidedReviewItem[] | null>(null);
  const [error, setError] = useState("");
  const [busyId, setBusyId] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    try {
      setItems(await getReview());
      setError("");
    } catch (failure) {
      setError(errorMessage(failure));
      setItems((current) => current || []);
    }
  }, []);

  const loadDecided = useCallback(async () => {
    try {
      setDecided(await getDecidedReview());
    } catch (failure) {
      setError(errorMessage(failure));
      setDecided((current) => current || []);
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch is async; state lands after the await.
    void load();
  }, [load]);

  useEffect(() => {
    if (filter === "decided") {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch is async; state lands after the await.
      void loadDecided();
    }
  }, [filter, loadDecided]);

  async function decide(item: ReviewItem, approve: boolean) {
    if (busyId) return;
    setBusyId(item.id);
    setNotice("");
    try {
      const result = approve ? await approveReview(item.id) : await rejectReview(item.id);
      setNotice(
        result.status === "rejected"
          ? "Rejected " + item.title + "."
          : result.status === "applied"
            ? "Applied " + item.title + "."
            : "Approved " + item.title + ". " + (label(item.agent) || item.agent) + " applies it at its next promotion."
      );
      await load();
      onChanged?.();
    } catch (failure) {
      setNotice(errorMessage(failure));
    } finally {
      setBusyId("");
    }
  }

  const all = items || [];
  const knowledge = all.filter((item) => item.kind !== "skill");
  const skills = all.filter((item) => item.kind === "skill");
  const shown = filter === "knowledge" ? knowledge : filter === "skills" ? skills : all;
  const filters: { key: Filter; label: string; count?: number }[] = [
    { key: "waiting", label: "Waiting", count: all.length },
    { key: "knowledge", label: "Knowledge", count: knowledge.length },
    { key: "skills", label: "Skills", count: skills.length },
    { key: "decided", label: "Decided" },
  ];

  return (
    <div className="kg-tab kr-tab">
      <p className="kr-explain">
        Agents ask before they change another agent&apos;s knowledge or add a skill. Nothing
        changes until you approve.
      </p>
      <div className="kg-chips" role="group" aria-label="Review filter">
        {filters.map((item) => (
          <button
            key={item.key}
            type="button"
            className={"kg-chip" + (filter === item.key ? " on" : "")}
            aria-pressed={filter === item.key}
            onClick={() => setFilter(item.key)}
          >
            <span>{item.label}</span>
            {item.count !== undefined ? <span className="kg-chip-n">{item.count}</span> : null}
          </button>
        ))}
      </div>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : null}
      {notice ? (
        <div className="alert ok" role="status" aria-live="polite">
          <Ic name="circle-check" size={15} />
          <span className="grow">{notice}</span>
        </div>
      ) : null}
      <div className="kr-grid">
        <div className="kr-main">
          {filter === "decided" ? (
            decided === null ? (
              <div className="knowledge-loading" aria-busy="true">
                <Skeleton width="100%" height={36} radius={8} />
                <Skeleton width="100%" height={36} radius={8} />
              </div>
            ) : decided.length === 0 ? (
              <EmptyState icon="history" title="Nothing decided yet" text="Approved and rejected proposals show here." />
            ) : (
              <ul className="kr-decided">
                {decided.map((item) => (
                  <li key={item.id}>
                    <span className="kr-decided-t">
                      {item.from && item.from !== item.agent
                        ? (label(item.from) || item.from) + " → " + (label(item.agent) || item.agent)
                        : label(item.agent) || item.agent}
                      {" · "}
                      {item.title}
                    </span>
                    <span className="kr-time">{when(item.decided_at)}</span>
                    {statusBadge(item.status || "")}
                  </li>
                ))}
              </ul>
            )
          ) : items === null ? (
            <div className="knowledge-loading" aria-busy="true">
              <span className="sr">Loading review…</span>
              <Skeleton width="100%" height={140} radius={12} />
            </div>
          ) : shown.length === 0 ? (
            <EmptyState
              icon="circle-check"
              title="Nothing waiting"
              text="When an agent wants to write into another agent's knowledge or add a skill, it shows up here."
            />
          ) : (
            <ul className="kr-list">
              {shown.map((item) => (
                <ReviewCard key={item.id} item={item} busy={busyId === item.id} onDecide={decide} />
              ))}
            </ul>
          )}
        </div>
        <aside className="kr-how">
          <h3>How review works</h3>
          <ol>
            <li>Each agent owns the pages in its own field.</li>
            <li>Writing into another agent&apos;s pages needs your OK.</li>
            <li>New skills that agents learn also wait here.</li>
            <li>Rejected items stay in the log and are not applied.</li>
          </ol>
        </aside>
      </div>
    </div>
  );
}
