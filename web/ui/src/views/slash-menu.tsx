import { useCallback, useMemo, useState } from "react";
import type { Dispatch, KeyboardEvent as ReactKeyboardEvent, SetStateAction } from "react";
import { Ic } from "../ui/icons";

/* The composer's slash command menu (owner 2026-11-11, like the Claude and
   Codex apps): typing "/" at the start of the draft opens a list of commands
   above the composer; arrows move, Enter or Tab runs the highlighted one, and
   Escape closes it. */

export interface SlashCommand {
  name: string;
  title: string;
  description: string;
  icon: string;
  run: () => void;
  /* The group heading the command sits under (composer redesign
     2026-11-25), e.g. "Session"; commands without one go under "Commands". */
  group?: string;
  /* The current value shown on the right, e.g. "2 running". */
  value?: string;
}

export interface SlashMenuState {
  open: boolean;
  matches: SlashCommand[];
  index: number;
  pick: (command: SlashCommand) => void;
  reset: () => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLTextAreaElement>) => boolean;
}

export function useSlashMenu(
  draft: string,
  setDraft: Dispatch<SetStateAction<string>>,
  commands: SlashCommand[]
): SlashMenuState {
  const [index, setIndex] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const match = /^\/(\S*)$/.exec(draft);
  const prefix = match ? (match[1] || "").toLowerCase() : null;
  const matches = useMemo(
    () => (prefix === null ? [] : commands.filter((command) => command.name.startsWith(prefix))),
    [commands, prefix]
  );
  const open = matches.length > 0 && !dismissed;
  const active = matches.length > 0 ? Math.min(index, matches.length - 1) : 0;

  const pick = useCallback(
    (command: SlashCommand) => {
      setDraft("");
      setDismissed(false);
      setIndex(0);
      command.run();
    },
    [setDraft]
  );

  const reset = useCallback(() => {
    setDismissed(false);
    setIndex(0);
  }, []);

  const onKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
      if (!open) {
        return false;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setIndex((current) => (current + 1) % matches.length);
        return true;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setIndex((current) => (current - 1 + matches.length) % matches.length);
        return true;
      }
      if ((event.key === "Enter" && !event.shiftKey) || event.key === "Tab") {
        event.preventDefault();
        const command = matches[active] || matches[0];
        if (command) {
          pick(command);
        }
        return true;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setDismissed(true);
        return true;
      }
      return false;
    },
    [open, matches, active, pick]
  );

  return { open, matches, index: active, pick, reset, onKeyDown };
}

export function SlashMenu({ state }: { state: SlashMenuState }) {
  if (!state.open) {
    return null;
  }
  return (
    <div className="slash-menu" role="listbox" aria-label="Commands">
      {state.matches.map((command, index) => {
        const group = command.group || "Commands";
        const before = index > 0 ? state.matches[index - 1] : undefined;
        const previous = before ? before.group || "Commands" : "";
        const active = index === state.index;
        return (
          <div key={command.name} className="slash-item">
            {group !== previous ? <div className="slash-menu-head">{group}</div> : null}
            <button
              type="button"
              role="option"
              aria-selected={active}
              aria-label={command.title + " /" + command.name}
              tabIndex={-1}
              className={"slash-option" + (active ? " is-active" : "")}
              onMouseDown={(event) => {
                event.preventDefault();
                state.pick(command);
              }}
            >
              <span className="slash-option-icon">
                <Ic name={command.icon} size={15} />
              </span>
              <span className="slash-option-text">
                <code className="slash-option-name">{"/" + command.name}</code>
                <span className="slash-option-desc">{command.description}</span>
              </span>
              {command.value ? <span className="slash-option-value">{command.value}</span> : null}
              {active ? <kbd className="slash-option-key">↵</kbd> : null}
            </button>
          </div>
        );
      })}
      <div className="slash-menu-foot">
        <span>
          <kbd>↑↓</kbd> move
        </span>
        <span>
          <kbd>↵</kbd> run
        </span>
        <span>
          <kbd>esc</kbd> close
        </span>
      </div>
    </div>
  );
}

/* A compact one-line chip above the input while the next message starts a new
   session (composer redesign: no big card inside the composer). */
export function NewSessionBanner({ name, onCancel }: { name: string; onCancel: () => void }) {
  return (
    <div className="new-session-banner" role="status">
      <Ic name="square-pen" size={13} className="new-session-icon" />
      <span className="new-session-title">New session</span>
      <span className="new-session-sub">
        {name + " won't see the messages above"}
      </span>
      <button type="button" className="new-session-x" aria-label="Cancel new session" onClick={onCancel}>
        <Ic name="x" size={12} />
      </button>
    </div>
  );
}
