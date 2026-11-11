import { useEffect, useMemo, useState } from "react";
import { addRoomNote, deleteRoomNote, listRoomMemory, updateRoomNote } from "../api/discussion";
import type { RoomNote, RoomNoteKind } from "../api/discussion";
import type { RoomRow } from "../api/rooms";
import { configVersion, deleteTeam, getTeam, saveTeam, TEAM_DISCUSSION_DEFAULTS } from "../api/teams";
import type { TeamDiscussionSettings, TeamResponse } from "../api/teams";
import { Button, Checkbox, Input, Modal, SearchInput, Seg } from "../ui/gr8r";
import { AgentAvatar } from "./agent-avatar";
import { displayLabel } from "./agent-label";
import { DiscussionSettingsForm } from "./discussion-settings";
import { AgentKnows, TeamAvatar } from "./discussion";
import { gr8rColor, refreshRooms } from "./presence";

/* Spec 47 REQ-RDS-054: Room settings with tabs Members, Memory and
   Discussion (design frames "Room settings – Memory" / "– Discussion"). */

export type RoomSettingsTab = "members" | "memory" | "discussion";

function errorText(error: unknown): string {
  if (error && typeof error === "object" && "message" in error) {
    return String((error as { message: unknown }).message);
  }
  return "Something went wrong.";
}

function settingsOf(team: TeamResponse | null): TeamDiscussionSettings {
  if (!team) {
    return TEAM_DISCUSSION_DEFAULTS;
  }
  return {
    mode: team.mode === "lead" ? "lead" : "group",
    discuss: team.discuss === "ask" ? "ask" : "auto",
    rounds: team.rounds || 3,
    extra_rounds: team.extra_rounds === "ask" || team.extra_rounds === "never" ? team.extra_rounds : "once",
    room_memory: team.room_memory !== false,
    share_summary: team.share_summary !== false,
    learn: team.learn !== false,
    telegram_all: team.telegram_all !== false,
  };
}

/* ------------------------------------------------------------------ */
/* Members                                                             */
/* ------------------------------------------------------------------ */

function MembersTab({
  team,
  agents,
  onSaved,
  onDeleted,
}: {
  team: TeamResponse;
  agents: RoomRow[];
  onSaved: (team: TeamResponse) => void;
  onDeleted: () => void;
}) {
  const [lead, setLead] = useState(team.lead);
  const [members, setMembers] = useState<string[]>(team.members);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const changed = lead !== team.lead || members.join(",") !== team.members.join(",");

  function toggle(name: string, on: boolean) {
    setMembers((current) => (on ? current.concat([name]) : current.filter((item) => item !== name)));
  }
  function makeLead(name: string) {
    setMembers((current) => current.filter((item) => item !== name).concat(lead ? [lead] : []));
    setLead(name);
  }
  async function save() {
    setBusy(true);
    setError("");
    try {
      const saved = await saveTeam(team.name, {
        lead,
        members,
        description: team.description,
        expected_version: await configVersion(),
      });
      onSaved(saved);
      void refreshRooms();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    setBusy(true);
    setError("");
    try {
      await deleteTeam(team.name, await configVersion());
      void refreshRooms();
      onDeleted();
    } catch (err) {
      setError(errorText(err));
      setBusy(false);
    }
  }
  return (
    <div className="rs-members">
      <ul className="ng-list">
        {agents.map((agent) => {
          const isLead = agent.name === lead;
          const on = isLead || members.includes(agent.name);
          return (
            <li key={agent.name} className={"ng-row" + (isLead ? " is-lead" : "")}>
              <Checkbox
                checked={on}
                disabled={isLead}
                aria-label={"Member " + displayLabel(agent)}
                onChange={(v) => toggle(agent.name, v)}
              />
              <AgentAvatar name={agent.name} color={gr8rColor(agent.color)} size="md" />
              <span className="ng-row-text">
                <span className="ng-row-name">{displayLabel(agent)}</span>
                <span className="ng-row-role">{agent.title || "Agent"}</span>
                <AgentKnows name={agent.name} can={agent.can} />
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
      </ul>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <div className="rs-actions">
        {confirmDelete ? (
          <>
            <span className="rs-confirm">Delete this room? Its history stays in History.</span>
            <Button size="sm" onClick={() => setConfirmDelete(false)}>
              Keep
            </Button>
            <Button size="sm" variant="danger" disabled={busy} onClick={() => void remove()}>
              Delete room
            </Button>
          </>
        ) : (
          <>
            <Button size="sm" variant="ghost" className="rs-delete" onClick={() => setConfirmDelete(true)}>
              Delete room
            </Button>
            <span className="rs-spacer"></span>
            <Button variant="primary" disabled={!changed || busy || members.length === 0} onClick={() => void save()}>
              Save members
            </Button>
          </>
        )}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Memory                                                              */
/* ------------------------------------------------------------------ */

const KIND_TITLES: Record<RoomNoteKind, string> = { decision: "Decisions", fact: "Facts", open: "Open" };

function noteMeta(note: RoomNote, label: (name: string) => string): string {
  const who = note.agent ? label(note.agent) : "You";
  const when = new Date(note.created_at).toLocaleDateString([], { day: "numeric", month: "short" });
  const from = note.source && note.source !== "owner" ? " · from a discussion" : "";
  return who + " · " + when + from;
}

function MemoryTab({ room, label }: { room: string; label: (name: string) => string }) {
  const [notes, setNotes] = useState<RoomNote[] | null>(null);
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<RoomNoteKind>("fact");
  const [text, setText] = useState("");
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    listRoomMemory(room)
      .then((list) => {
        if (live) {
          setNotes(list);
        }
      })
      .catch((err) => {
        if (live) {
          setNotes([]);
          setError(errorText(err));
        }
      });
    return () => {
      live = false;
    };
  }, [room]);

  const replacedText = useMemo(() => {
    const byId = new Map((notes || []).map((note) => [note.id, note]));
    return (note: RoomNote) => {
      const next = note.replaced_by ? byId.get(note.replaced_by) : undefined;
      return next ? next.text : "";
    };
  }, [notes]);

  const shown = (notes || []).filter((note) => !query || note.text.toLowerCase().includes(query.toLowerCase()));
  const current = shown.filter((note) => !note.replaced_by);

  async function add() {
    if (!text.trim()) {
      return;
    }
    setError("");
    try {
      const note = await addRoomNote(room, kind, text.trim());
      setNotes((list) => [note].concat(list || []));
      setText("");
    } catch (err) {
      setError(errorText(err));
    }
  }
  async function saveEdit() {
    if (!editing) {
      return;
    }
    try {
      const note = await updateRoomNote(room, editing.id, { text: editing.text });
      setNotes((list) => (list || []).map((item) => (item.id === note.id ? note : item)));
      setEditing(null);
    } catch (err) {
      setError(errorText(err));
    }
  }
  async function remove(id: string) {
    try {
      await deleteRoomNote(room, id);
      setNotes((list) => (list || []).filter((item) => item.id !== id));
    } catch (err) {
      setError(errorText(err));
    }
  }

  return (
    <div className="rs-memory">
      <SearchInput
        placeholder="Search room memory"
        aria-label="Search room memory"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
        kbd={null}
      />
      <p className="disc-help">
        What this room remembers. Members read it when they work here; the lead adds to it after each discussion.
      </p>
      {notes === null ? <p className="muted">Loading…</p> : null}
      {notes !== null && current.length === 0 && !query ? (
        <p className="muted rs-empty">Nothing yet. Notes appear after a discussion, or add one below.</p>
      ) : null}
      {(["decision", "fact", "open"] as RoomNoteKind[]).map((k) => {
        const list = shown.filter((note) => note.kind === k);
        if (list.length === 0) {
          return null;
        }
        return (
          <section key={k} className="rs-kind">
            <h3 className="disc-sum-label">{KIND_TITLES[k]}</h3>
            {list.map((note) => (
              <div key={note.id} className={"rs-note" + (note.replaced_by ? " is-replaced" : "")}>
                {editing && editing.id === note.id ? (
                  <div className="rs-edit">
                    <Input
                      value={editing.text}
                      aria-label="Note text"
                      maxLength={400}
                      onChange={(event) => setEditing({ id: note.id, text: event.target.value })}
                    />
                    <Button size="sm" onClick={() => setEditing(null)}>
                      Cancel
                    </Button>
                    <Button size="sm" variant="primary" onClick={() => void saveEdit()}>
                      Save
                    </Button>
                  </div>
                ) : (
                  <>
                    <span className="rs-note-text">{note.text}</span>
                    <span className="rs-note-meta">
                      {note.replaced_by ? (
                        <span className="disc-chip">{"Replaced" + (replacedText(note) ? " by “" + replacedText(note) + "”" : "")}</span>
                      ) : null}
                      {noteMeta(note, label)}
                    </span>
                    <span className="rs-note-actions">
                      {!note.replaced_by ? (
                        <button type="button" className="disc-link muted" onClick={() => setEditing({ id: note.id, text: note.text })}>
                          Edit
                        </button>
                      ) : null}
                      <button type="button" className="disc-link muted" onClick={() => void remove(note.id)}>
                        Delete
                      </button>
                    </span>
                  </>
                )}
              </div>
            ))}
          </section>
        );
      })}
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <div className="rs-add">
        <Seg
          aria-label="Note kind"
          options={[
            { value: "decision", label: "Decision" },
            { value: "fact", label: "Fact" },
            { value: "open", label: "Open" },
          ]}
          value={kind}
          onChange={(v) => setKind(v as RoomNoteKind)}
        />
        <Input
          placeholder="Add a note"
          aria-label="New note"
          maxLength={400}
          value={text}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void add();
            }
          }}
        />
        <Button size="sm" disabled={!text.trim()} onClick={() => void add()}>
          Add note
        </Button>
      </div>
      <p className="rs-foot muted">{(notes || []).filter((n) => !n.replaced_by).length + " notes · stored in rooms/" + room + "/"}</p>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Discussion                                                          */
/* ------------------------------------------------------------------ */

function DiscussionTab({ team, leadLabel, onSaved }: { team: TeamResponse; leadLabel: string; onSaved: (team: TeamResponse) => void }) {
  const [value, setValue] = useState<TeamDiscussionSettings>(() => settingsOf(team));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  async function save() {
    setBusy(true);
    setError("");
    try {
      const next = await saveTeam(team.name, {
        lead: team.lead,
        members: team.members,
        description: team.description,
        expected_version: await configVersion(),
        ...value,
      });
      onSaved(next);
      setSaved(true);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="rs-discussion">
      <section className="disc-field">
        <h3>Moderator</h3>
        <p className="disc-help">{leadLabel + " (lead) runs every discussion. Change the lead in Members."}</p>
      </section>
      <DiscussionSettingsForm
        value={value}
        onChange={(next) => {
          setValue(next);
          setSaved(false);
        }}
        lead={leadLabel}
      />
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      <div className="rs-actions">
        {saved ? <span className="muted">Saved</span> : null}
        <span className="rs-spacer"></span>
        <Button variant="primary" disabled={busy} onClick={() => void save()}>
          Save
        </Button>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Dialog                                                              */
/* ------------------------------------------------------------------ */

export function RoomSettings({
  room,
  rows,
  initialTab = "members",
  onClose,
}: {
  room: RoomRow;
  rows: RoomRow[];
  initialTab?: RoomSettingsTab;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<RoomSettingsTab>(initialTab);
  const [team, setTeam] = useState<TeamResponse | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    getTeam(room.name)
      .then((t) => live && setTeam(t))
      .catch((err) => live && setError(errorText(err)));
    return () => {
      live = false;
    };
  }, [room.name]);
  const byName = useMemo(() => new Map(rows.map((row) => [row.name, row])), [rows]);
  const label = (name: string) => {
    const row = byName.get(name);
    return row ? displayLabel(row) : name;
  };
  const agents = rows.filter((row) => row.kind !== "team");
  const lead = team ? team.lead : room.lead;
  const members = team ? team.members : room.members || [];
  const avatarItems = [lead, ...members].map((name) => ({ name, color: gr8rColor(byName.get(name)?.color) }));
  return (
    <Modal
      title={displayLabel(room)}
      subtitle={"Team room · lead " + label(lead) + (members.length ? " · " + members.map(label).join(", ") : "")}
      onClose={onClose}
      className="room-settings"
    >
      <div className="rs-top">
        <TeamAvatar items={avatarItems} size={40} />
        <Seg
          aria-label="Room settings"
          options={[
            { value: "members", label: "Members" },
            { value: "memory", label: "Memory" },
            { value: "discussion", label: "Discussion" },
          ]}
          value={tab}
          onChange={(v) => setTab(v as RoomSettingsTab)}
        />
      </div>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      {tab === "memory" ? <MemoryTab room={room.name} label={label} /> : null}
      {tab !== "memory" && !team && !error ? <p className="muted">Loading…</p> : null}
      {tab === "members" && team ? (
        <MembersTab team={team} agents={agents} onSaved={setTeam} onDeleted={() => {
          onClose();
          window.location.hash = "#/chats";
        }} />
      ) : null}
      {tab === "discussion" && team ? <DiscussionTab team={team} leadLabel={label(team.lead)} onSaved={setTeam} /> : null}
    </Modal>
  );
}
