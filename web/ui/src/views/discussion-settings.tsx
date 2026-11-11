import type { ReactNode } from "react";
import { Radio, Seg, Toggle } from "../ui/gr8r";
import type { TeamDiscussionSettings } from "../api/teams";

/* Spec 47 REQ-RDS-001: the discussion settings of a team, shared by Room
   settings › Discussion and New group room. */

function Option({
  title,
  sub,
  control,
}: {
  title: string;
  sub?: string;
  control: ReactNode;
}) {
  return (
    <label className="disc-opt">
      <span className="disc-opt-control">{control}</span>
      <span className="disc-opt-text">
        <span className="disc-opt-title">{title}</span>
        {sub ? <span className="disc-opt-sub">{sub}</span> : null}
      </span>
    </label>
  );
}

export function DiscussionSettingsForm({
  value,
  onChange,
  lead,
  telegram,
}: {
  value: TeamDiscussionSettings;
  onChange: (next: TeamDiscussionSettings) => void;
  lead: string;
  /* The Telegram group the team is routed to, if any. */
  telegram?: string;
}) {
  const set = <K extends keyof TeamDiscussionSettings>(key: K, v: TeamDiscussionSettings[K]) =>
    onChange({ ...value, [key]: v });
  return (
    <div className="disc-form">
      <section className="disc-field">
        <h3>Start a discussion</h3>
        <Option
          title={"When " + lead + " sees a question that needs more than one view"}
          sub="Recommended. You can still start one with Discuss or by saying “diskusikan”."
          control={<Radio name="disc-start" checked={value.discuss === "auto"} onChange={() => set("discuss", "auto")} />}
        />
        <Option
          title="Only when I ask"
          sub={lead + " answers alone or hands off, as today."}
          control={<Radio name="disc-start" checked={value.discuss === "ask"} onChange={() => set("discuss", "ask")} />}
        />
      </section>
      <section className="disc-field">
        <h3>Rounds</h3>
        <Seg
          aria-label="Rounds"
          options={[1, 2, 3, 4, 5].map((n) => ({ value: String(n), label: String(n) }))}
          value={String(value.rounds)}
          onChange={(v) => set("rounds", Number(v))}
        />
        <p className="disc-help">
          The normal limit. In one round each member speaks once; {lead} stops early when members agree. Never more
          than 5.
        </p>
      </section>
      <section className="disc-field">
        <h3>Extra rounds</h3>
        <Option
          title={lead + " adds one round on its own, then asks me"}
          sub="Recommended. Only when the last round brought something new and one question is still open."
          control={<Radio name="disc-extra" checked={value.extra_rounds === "once"} onChange={() => set("extra_rounds", "once")} />}
        />
        <Option
          title="Always ask me"
          control={<Radio name="disc-extra" checked={value.extra_rounds === "ask"} onChange={() => set("extra_rounds", "ask")} />}
        />
        <Option
          title="Never"
          control={<Radio name="disc-extra" checked={value.extra_rounds === "never"} onChange={() => set("extra_rounds", "never")} />}
        />
      </section>
      <section className="disc-field">
        <h3>After a discussion</h3>
        <Option
          title="Save decisions and facts to room memory"
          control={<Toggle checked={value.room_memory} onChange={(v) => set("room_memory", v)} />}
        />
        <Option
          title="Send the summary to each member's room"
          sub="Each member keeps it, so it can answer follow-ups alone."
          control={<Toggle checked={value.share_summary} onChange={(v) => set("share_summary", v)} />}
        />
        <Option
          title="Let members learn from it"
          sub="Members with knowledge get a journal note; promotion decides what becomes a page."
          control={<Toggle checked={value.learn} onChange={(v) => set("learn", v)} />}
        />
      </section>
      <section className="disc-field">
        <h3>{telegram ? "Telegram group " + telegram : "Telegram group"}</h3>
        <Option
          title="Post every message of the discussion"
          sub="Off: only the answer goes to the group."
          control={<Toggle checked={value.telegram_all} onChange={(v) => set("telegram_all", v)} />}
        />
      </section>
    </div>
  );
}

export function discussionSummaryLine(value: TeamDiscussionSettings, lead: string): string {
  const start = value.discuss === "auto" ? lead + " decides when" : "Only when you ask";
  const extra =
    value.extra_rounds === "once"
      ? "one extra round on its own"
      : value.extra_rounds === "ask"
        ? "extra rounds ask you"
        : "no extra rounds";
  return start + " · up to " + value.rounds + " rounds · " + extra;
}
