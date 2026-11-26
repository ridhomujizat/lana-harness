import { useEffect, useMemo, useRef, useState } from "react";
import { patchRoom, useRooms } from "./presence";
import { displayLabel } from "./agent-label";
import { RoomView } from "./room-timeline";
import {
  Button,
  EmptyState,
  Menu,
  MenuItem,
  SearchInput,
  Seg,
  Skeleton,
} from "../ui/gr8r";
import { archiveRoom, clearRoom } from "../api/rooms";
import type { RoomRow } from "../api/rooms";
import { cancelTurn } from "../api/turns";
import { NewGroupRoom } from "./new-group-room";
import { needsYou, RoomListRow, rowState, isArchived, rowTime } from "./room-list-row";
import {
  ArchivedChats,
  ArchivedLauncher,
  ChatMenuItems,
  ClearChatDialog,
  ListToast,
  PhoneChatRow,
  useIsPhone,
  useListToast,
} from "./chat-archive";

/* The Chats page of REQ-ROOM-001 to REQ-ROOM-005: the room list column and the
   room timeline. The list rows come from the live useRooms() store; the
   timeline lives in room-timeline.tsx. Spec 85 adds archive and clear:
   archived rooms leave the list (REQ-ARC-007) and live behind #/chats/archived
   (REQ-ARC-008); clearing empties a room's timeline (REQ-ARC-010/011). */

const FILTERS = [
  { key: "all", label: "All" },
  { key: "working", label: "Working" },
  { key: "needs", label: "Needs you" },
] as const;

type FilterKey = (typeof FILTERS)[number]["key"];

/* A module-scope helper: assigning window.location inside a component function
   trips react-hooks/immutability, so navigation goes through here. */
function goTo(hash: string): void {
  window.location.hash = hash;
}

export interface ChatsPageProps {
  roomName?: string;
  /* Spec 48: prefilled composer text for the opened room. */
  draft?: string;
  /* Spec 85 REQ-ARC-008: the #/chats/archived view. */
  archived?: boolean;
}

export function ChatsPage({ roomName, draft, archived = false }: ChatsPageProps) {
  const { rows, loading } = useRooms();
  const phone = useIsPhone();
  const { toast, show, dismiss } = useListToast();
  const newRef = useRef<HTMLSpanElement>(null);
  const [newOpen, setNewOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [clearTarget, setClearTarget] = useState<RoomRow | null>(null);
  const [clearing, setClearing] = useState(false);
  const colors = useMemo(() => {
    const out: Record<string, string> = {};
    for (const row of rows) {
      out[row.name] = row.color;
    }
    return out;
  }, [rows]);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<FilterKey>("all");

  /* REQ-ARC-007: the list, the counts, the search and the default room use only
     rooms that are not archived. */
  const activeRows = useMemo(() => rows.filter((row) => !isArchived(row)), [rows]);
  const archivedRows = useMemo(
    () => rows.filter(isArchived).slice().sort((a, b) => rowTime(b) - rowTime(a)),
    [rows]
  );

  const counts = useMemo(() => {
    let working = 0;
    let needs = 0;
    for (const row of activeRows) {
      if (rowState(row) === "working") {
        working += 1;
      }
      if (needsYou(row)) {
        needs += 1;
      }
    }
    return { all: activeRows.length, working, needs };
  }, [activeRows]);

  const visible = useMemo(() => {
    const text = query.trim().toLowerCase();
    return activeRows.filter((row) => {
      if (filter === "working" && rowState(row) !== "working") {
        return false;
      }
      if (filter === "needs" && !needsYou(row)) {
        return false;
      }
      if (!text) {
        return true;
      }
      const preview = row.last_message ? row.last_message.preview : "";
      return (
        row.name.toLowerCase().includes(text) ||
        displayLabel(row).toLowerCase().includes(text) ||
        (row.title || "").toLowerCase().includes(text) ||
        preview.toLowerCase().includes(text)
      );
    });
  }, [activeRows, query, filter]);

  const selected = roomName
    ? rows.find((row) => row.name === roomName) || null
    : null;

  /* REQ-ROOM-001 and REQ-ARC-008: with no room in the URL, open the first room.
     The archived view opens the first archived row instead (or stays on the list
     when none is archived); the active view keeps opening the first active row.
     On a phone the list is its own screen, so it stays on the list. */
  useEffect(() => {
    if (roomName || phone) {
      return;
    }
    const first = archived ? archivedRows[0] : activeRows[0];
    if (!first) {
      return;
    }
    window.location.hash = "#/chats/" + encodeURIComponent(first.name);
  }, [roomName, archived, activeRows, archivedRows, phone]);

  function countFor(key: FilterKey): number {
    if (key === "all") {
      return counts.all;
    }
    return key === "working" ? counts.working : counts.needs;
  }

  /* REQ-ARC-002/007/008: archive or unarchive, patch the row at once, and show
     the toast with Undo. quiet skips the toast (the Undo itself). */
  async function setArchived(row: RoomRow, next: boolean, quiet = false) {
    try {
      const result = await archiveRoom(row.name, next);
      patchRoom(row.name, { archived_at: result.archived_at });
      if (next && roomName === row.name) {
        goTo("#/chats");
      }
      if (!quiet) {
        show(next ? "Chat archived" : "Chat unarchived", () => {
          void setArchived(row, !next, true);
        });
      }
    } catch {
      show("Could not update the chat");
    }
  }

  /* REQ-ARC-003/010/011: stop the running turn first when the room is working,
     then clear; the toast Undo restores the previous cleared_at. */
  async function confirmClear(row: RoomRow) {
    if (clearing) {
      return;
    }
    setClearing(true);
    const working = Boolean(row.presence && row.presence.state === "working");
    const sessionId = row.presence ? row.presence.session_id : "";
    try {
      if (working && sessionId) {
        await cancelTurn(sessionId);
      }
      const result = await clearRoom(row.name);
      patchRoom(row.name, { cleared_at: result.cleared_at, last_message: null, unread: 0 });
      setClearTarget(null);
      show("Chat cleared", () => {
        void clearRoom(row.name, result.previous)
          .then((restored) => patchRoom(row.name, { cleared_at: restored.cleared_at }))
          .catch(() => undefined);
      });
    } catch {
      show("Could not clear the chat");
    } finally {
      setClearing(false);
    }
  }

  /* REQ-ARC-008: an archived room opened at #/chats/<name> keeps the archived
     list column while the room itself shows in the panel. */
  const archivedView = archived || Boolean(selected && isArchived(selected));
  const searchEmpty =
    visible.length === 0 && (query.trim() !== "" || filter !== "all");

  return (
    <main
      id="main"
      className={
        "rooms-page" + (roomName ? " has-room" : "") + (archived ? " is-archived" : "")
      }
    >
      <aside className="rooms-list" aria-label="Chats">
        {archivedView ? (
          <ArchivedChats
            rows={archivedRows}
            colors={colors}
            onUnarchive={(row) => void setArchived(row, false)}
          />
        ) : (
          <>
            <header className="rooms-list-head">
              <h1 className="rooms-list-title">Chats</h1>
              <span ref={newRef} className="rooms-new">
                <Button size="sm" icon="plus" aria-haspopup="menu" aria-expanded={newOpen} onClick={() => setNewOpen((v) => !v)}>
                  New
                </Button>
              </span>
              <Menu anchorRef={newRef} open={newOpen} onClose={() => setNewOpen(false)} className="new-menu">
                <MenuItem
                  icon="user-plus"
                  onClick={() => {
                    setNewOpen(false);
                    window.location.hash = "#/agents";
                  }}
                >
                  New agent
                </MenuItem>
                <MenuItem
                  icon="users"
                  onClick={() => {
                    setNewOpen(false);
                    setCreating(true);
                  }}
                >
                  New group room
                </MenuItem>
              </Menu>
              {creating ? <NewGroupRoom rows={rows} defaultLead="lana" onClose={() => setCreating(false)} /> : null}
            </header>
            <SearchInput
              wrapClassName="rooms-search"
              placeholder="Search agents and messages"
              aria-label="Search agents and messages"
              autoComplete="off"
              spellCheck={false}
              kbd={null}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <Seg
              className="rooms-filters"
              aria-label="Room filters"
              value={filter}
              onChange={(value) => setFilter(value as FilterKey)}
              options={FILTERS.map((entry) => ({
                value: entry.key,
                label: entry.label + " ",
                count: countFor(entry.key),
              }))}
            />
            <div className="rooms-list-body">
              {loading && rows.length === 0 ? (
                <div className="rooms-skeleton" aria-hidden="true">
                  {Array.from({ length: 5 }).map((_, index) => (
                    <div className="rooms-skeleton-row" key={index}>
                      <Skeleton width={36} height={36} radius="50%" />
                      <div className="rooms-skeleton-lines">
                        <Skeleton width="60%" height={10} />
                        <Skeleton width="90%" height={10} />
                      </div>
                    </div>
                  ))}
                </div>
              ) : rows.length === 0 ? (
                <EmptyState
                  size="sm"
                  icon="message-square"
                  title="No chats yet"
                  text="Create an agent to start a chat."
                  action={
                    <Button as="a" href="#/agents" variant="secondary" icon="plus">
                      New agent
                    </Button>
                  }
                />
              ) : searchEmpty ? (
                <EmptyState
                  size="sm"
                  icon="search-x"
                  title="No chats match"
                  text="Try a different search or filter."
                />
              ) : (
                <>
                  <ul className="rooms-rows">
                    {visible.map((row) => (
                      <li key={row.name}>
                        {phone ? (
                          <PhoneChatRow
                            row={row}
                            colors={colors}
                            active={row.name === roomName}
                            href={"#/chats/" + encodeURIComponent(row.name)}
                            onArchive={() => void setArchived(row, true)}
                            onClear={() => setClearTarget(row)}
                          />
                        ) : (
                          <RoomListRow
                            row={row}
                            colors={colors}
                            active={row.name === roomName}
                            href={"#/chats/" + encodeURIComponent(row.name)}
                            menu={
                              <ChatMenuItems
                                row={row}
                                onArchive={() => void setArchived(row, true)}
                                onClear={() => setClearTarget(row)}
                              />
                            }
                          />
                        )}
                      </li>
                    ))}
                  </ul>
                  {/* REQ-ARC-007: the Archived row ends the list under every
                      filter, but not while a search has text. */}
                  {query.trim() === "" && archivedRows.length > 0 ? (
                    <ArchivedLauncher
                      count={archivedRows.length}
                      names={archivedRows.map((row) => displayLabel(row)).join(", ")}
                      unread={archivedRows.reduce((sum, row) => sum + (Number(row.unread) || 0), 0)}
                    />
                  ) : null}
                </>
              )}
            </div>
          </>
        )}
        {toast ? <ListToast toast={toast} onClose={dismiss} /> : null}
      </aside>
      <section className="rooms-panel">
        {selected ? (
          <RoomView
            key={selected.name}
            room={selected}
            initialDraft={draft}
            onArchive={(next) => void setArchived(selected, next)}
            onClear={() => setClearTarget(selected)}
          />
        ) : rows.length === 0 && !loading ? (
          <div className="rooms-placeholder">
            <EmptyState
              icon="message-square"
              title="No chats yet"
              text="Create an agent to start a chat."
              action={
                <Button as="a" href="#/agents" variant="primary" icon="plus">
                  New agent
                </Button>
              }
            />
          </div>
        ) : (
          <div className="rooms-placeholder">
            <div className="rooms-placeholder-skeleton" aria-hidden="true">
              <Skeleton width={220} height={12} />
              <Skeleton width={160} height={12} />
              <Skeleton width={200} height={12} />
            </div>
          </div>
        )}
      </section>
      {clearTarget ? (
        <ClearChatDialog
          label={displayLabel(clearTarget)}
          working={Boolean(clearTarget.presence && clearTarget.presence.state === "working")}
          busy={clearing}
          onCancel={() => setClearTarget(null)}
          onConfirm={() => void confirmClear(clearTarget)}
        />
      ) : null}
    </main>
  );
}
