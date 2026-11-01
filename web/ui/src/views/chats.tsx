import { AgentFace, PresenceLabel, SkeletonRows, UnreadBadge, useRooms } from "./presence";

/* The Chats page placeholder of REQ-ROOM-012. T-006 renders the room list from
   useRooms(); the timeline, composer and group room land in later tasks. */

export interface ChatsPageProps {
  name?: string;
}

export function ChatsPage({ name }: ChatsPageProps) {
  const { rows, loading } = useRooms();

  return (
    <main id="main" className="page">
      <header className="page-head">
        <h1>Chats</h1>
      </header>

      {loading && rows.length === 0 ? (
        <SkeletonRows count={4} />
      ) : rows.length === 0 ? (
        <p className="muted">No rooms yet. Create an agent to start a chat.</p>
      ) : (
        <ul className="room-list">
          {rows.map((row) => {
            const active = name === row.name;
            return (
              <li key={row.name} className="room-item">
                <a
                  className="room-item-link"
                  href={"#/chats/" + encodeURIComponent(row.name)}
                  aria-current={active ? "page" : undefined}
                >
                  <AgentFace
                    name={row.name}
                    color={row.color}
                    presence={row.presence}
                    size={32}
                  />
                  <span className="room-item-body">
                    <span className="room-item-line">
                      <span className="room-item-name">{row.name}</span>
                      {row.title ? (
                        <span className="room-item-title">{row.title}</span>
                      ) : null}
                    </span>
                    <PresenceLabel presence={row.presence} />
                  </span>
                  <UnreadBadge count={row.unread} />
                </a>
              </li>
            );
          })}
        </ul>
      )}
    </main>
  );
}
