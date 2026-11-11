import { useMemo, useState } from "react";
import type { RoomRow } from "../api/rooms";
import { configVersion, createTeam, TEAM_DISCUSSION_DEFAULTS } from "../api/teams";
import type { TeamDiscussionSettings } from "../api/teams";
import { Button, Checkbox, Field, Input, Modal, SearchInput } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { displayLabel } from "./agent-label";
import { TeamAvatar } from "./discussion";
import { DiscussionSettingsForm, discussionSummaryLine } from "./discussion-settings";
import { gr8rColor, refreshRooms } from "./presence";

/* Spec 47 REQ-RDS-055: New group room (design "New group room – 3 members",
   "– more than 3 members"). The first agent picked is the lead; Make lead
   moves it. */

const LIST_LIMIT = 6;
const MAX_MEMBERS = 8;

/* The team name for a display name: lowercase letters, digits and dashes,
   starting with a letter, at most 32 characters (API-TEAM-001). */
export function teamSlug(name: string): string {
  let slug = name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (slug && !/^[a-z]/.test(slug)) {
    slug = "room-" + slug;
  }
  return slug.slice(0, 32).replace(/-+$/, "");
}

function errorText(error: unknown): string {
  if (error && typeof error === "object" && "message" in error) {
    return String((error as { message: unknown }).message);
  }
  return "Something went wrong.";
}

export function NewGroupRoom({
  rows,
  defaultLead,
  onClose,
}: {
  rows: RoomRow[];
  defaultLead?: string;
  onClose: () => void;
}) {
  const agents = useMemo(() => rows.filter((row) => row.kind !== "team"), [rows]);
  const taken = useMemo(() => new Set(rows.map((row) => row.name)), [rows]);
  const firstLead = defaultLead && agents.some((a) => a.name === defaultLead) ? defaultLead : agents[0]?.name || "";
  const [name, setName] = useState("");
  const [purpose, setPurpose] = useState("");
  const [lead, setLead] = useState(firstLead);
  const [members, setMembers] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [showAll, setShowAll] = useState(false);
  const [settings, setSettings] = useState<TeamDiscussionSettings>(TEAM_DISCUSSION_DEFAULTS);
  const [editSettings, setEditSettings] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const slug = teamSlug(name);
  const slugTaken = slug !== "" && taken.has(slug);
  const byName = useMemo(() => new Map(agents.map((a) => [a.name, a])), [agents]);
  const label = (n: string) => {
    const row = byName.get(n);
    return row ? displayLabel(row) : n;
  };
  const ordered = useMemo(() => {
    const filtered = agents.filter((a) => !query || displayLabel(a).toLowerCase().includes(query.toLowerCase()) || (a.title || "").toLowerCase().includes(query.toLowerCase()));
    /* The lead and the picked members first, so a long list keeps them in view. */
    return filtered.sort((a, b) => {
      const rank = (r: RoomRow) => (r.name === lead ? 0 : members.includes(r.name) ? 1 : 2);
      return rank(a) - rank(b);
    });
  }, [agents, query, lead, members]);
  const visible = showAll || query ? ordered : ordered.slice(0, LIST_LIMIT);
  const hidden = ordered.length - visible.length;
  const hiddenPicked = ordered.slice(visible.length).filter((a) => members.includes(a.name));
  const total = (lead ? 1 : 0) + members.length;
  const leadLabel = lead ? label(lead) : "The lead";
  const canCreate = slug !== "" && !slugTaken && lead !== "" && members.length > 0 && members.length <= MAX_MEMBERS && !busy;

  function toggle(n: string, on: boolean) {
    setMembers((current) => (on ? current.concat([n]) : current.filter((item) => item !== n)));
  }
  function makeLead(n: string) {
    setMembers((current) => current.filter((item) => item !== n).concat(lead ? [lead] : []));
    setLead(n);
  }
  async function create() {
    setBusy(true);
    setError("");
    try {
      await createTeam({
        name: slug,
        display_name: name.trim(),
        lead,
        members,
        description: purpose.trim(),
        expected_version: await configVersion(),
        ...settings,
      });
      await refreshRooms();
      onClose();
      window.location.hash = "#/chats/" + encodeURIComponent(slug);
    } catch (err) {
      setError(errorText(err));
      setBusy(false);
    }
  }

  const preview = [lead, ...members].filter(Boolean).map((n) => ({ name: n, color: gr8rColor(byName.get(n)?.color) }));
  const others = members.map(label);
  const tail = others.slice(0, 3).join(", ") + (others.length > 3 ? " +" + (others.length - 3) : "");

  return (
    <Modal
      title="New group room"
      subtitle="One room for several agents. The lead reads your messages first, brings in members and runs discussions."
      onClose={onClose}
      className="new-group-room"
      footer={
        <>
          <span className="ng-foot-note muted">You can change members later</span>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" disabled={!canCreate} onClick={() => void create()}>
            {busy ? "Creating…" : "Create room"}
          </Button>
        </>
      }
    >
      <Field
        label="Name"
        hint={slugTaken ? undefined : slug ? "Shown in Chats. @mentions and Telegram routes use “" + slug + "”." : "For example Career plan."}
        error={slugTaken ? "An agent or room named “" + slug + "” already exists." : undefined}
      >
        <Input value={name} maxLength={40} placeholder="Career plan" onChange={(event) => setName(event.target.value)} data-autofocus />
      </Field>
      <Field label="What is it for?" hint="The lead uses this to pick members and to decide when a question needs a discussion.">
        <Input
          value={purpose}
          maxLength={120}
          placeholder="Keputusan karier: tawaran kerja, gaji, pindah kota"
          onChange={(event) => setPurpose(event.target.value)}
        />
      </Field>
      <div className="ng-members">
        <div className="ng-members-head">
          <span className="ng-label">Members</span>
          <span className="ng-count">{total + " selected"}</span>
        </div>
        <SearchInput placeholder="Search agents" aria-label="Search agents" value={query} onChange={(event) => setQuery(event.target.value)} kbd={null} />
        <ul className="ng-list">
          {visible.map((agent) => {
            const isLead = agent.name === lead;
            const on = isLead || members.includes(agent.name);
            return (
              <li key={agent.name} className={"ng-row" + (isLead ? " is-lead" : "")}>
                <Checkbox checked={on} disabled={isLead} aria-label={"Add " + displayLabel(agent)} onChange={(v) => toggle(agent.name, v)} />
                <AgentAvatar name={agent.name} color={gr8rColor(agent.color)} size="md" />
                <span className="ng-row-text">
                  <span className="ng-row-name">{displayLabel(agent)}</span>
                  <span className="ng-row-role">{agent.title || "Agent"}</span>
                </span>
                {isLead ? (
                  <span className="disc-chip disc-chip-lead">Lead</span>
                ) : on ? (
                  <button type="button" className="disc-link muted" onClick={() => makeLead(agent.name)}>
                    Make lead
                  </button>
                ) : null}
              </li>
            );
          })}
          {hidden > 0 ? (
            <li className="ng-row ng-more">
              <button type="button" className="disc-link muted" onClick={() => setShowAll(true)}>
                {hiddenPicked.length > 0
                  ? hiddenPicked.length + " more selected · " + hiddenPicked.map((a) => displayLabel(a)).join(", ") + " · Show"
                  : "Show " + hidden + " more agents"}
              </button>
            </li>
          ) : null}
        </ul>
        {members.length > MAX_MEMBERS ? <p className="form-error">A room has at most {MAX_MEMBERS} members besides the lead.</p> : null}
        {total > 3 ? (
          <p className="ng-hint" role="note">
            {total + " agents. In a discussion " + leadLabel + " asks everyone once, then only the members who disagree, so bigger rooms cost more."}
          </p>
        ) : null}
      </div>
      <div className="ng-setting">
        <span className="ng-setting-text">
          <span className="ng-setting-title">Discussions</span>
          <span className="ng-setting-sub">{discussionSummaryLine(settings, leadLabel)}</span>
        </span>
        <button type="button" className="disc-link" onClick={() => setEditSettings((v) => !v)}>
          {editSettings ? "Done" : "Change"}
        </button>
      </div>
      {editSettings ? <DiscussionSettingsForm value={settings} onChange={setSettings} lead={leadLabel} /> : null}
      <div className="ng-setting">
        <span className="ng-setting-text">
          <span className="ng-setting-title">Telegram group</span>
          <span className="ng-setting-sub">Not linked. Route a Telegram group to this room in Channels.</span>
        </span>
      </div>
      {preview.length > 0 ? (
        <div className="ng-preview">
          <TeamAvatar items={preview} size={40} />
          <span className="ng-row-text">
            <span className="ng-row-name">{name.trim() || "New room"}</span>
            <span className="ng-row-role">{"Team · lead " + leadLabel + (tail ? " · " + tail : "")}</span>
          </span>
          <span className="muted ng-preview-note">Appears in Chats</span>
        </div>
      ) : null}
      {error ? <p className="form-error" role="alert">{error}</p> : null}
    </Modal>
  );
}
