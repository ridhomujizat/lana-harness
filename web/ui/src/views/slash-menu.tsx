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
      <div className="slash-menu-head">Commands</div>
      {state.matches.map((command, index) => (
        <button
          key={command.name}
          type="button"
          role="option"
          aria-selected={index === state.index}
          tabIndex={-1}
          className={"slash-option" + (index === state.index ? " is-active" : "")}
          onMouseDown={(event) => {
            event.preventDefault();
            state.pick(command);
          }}
        >
          <span className="slash-option-icon">
            <Ic name={command.icon} size={16} />
          </span>
          <span className="slash-option-text">
            <span className="slash-option-title">
              {command.title}
              <code className="slash-option-name">{"/" + command.name}</code>
            </span>
            <span className="slash-option-desc">{command.description}</span>
          </span>
        </button>
      ))}
      <div className="slash-menu-foot">
        <span>
          <kbd>↑</kbd> <kbd>↓</kbd> to move
        </span>
        <span>
          <kbd>Enter</kbd> to run
        </span>
        <span>
          <kbd>Esc</kbd> to close
        </span>
      </div>
    </div>
  );
}

/* The banner above the composer while the next message starts a new
   session. */
export function NewSessionBanner({ name, onCancel }: { name: string; onCancel: () => void }) {
  return (
    <div className="new-session-banner" role="status">
      <span className="new-session-icon">
        <Ic name="square-pen" size={16} />
      </span>
      <span className="new-session-text">
        <span className="new-session-title">New session</span>
        <span className="new-session-sub">
          {"Your next message starts a fresh conversation. " + name + " will not see the messages above."}
        </span>
      </span>
      <button type="button" className="ibtn ibtn-xs" aria-label="Cancel new session" onClick={onCancel}>
        <Ic name="x" size={12} />
      </button>
    </div>
  );
}
