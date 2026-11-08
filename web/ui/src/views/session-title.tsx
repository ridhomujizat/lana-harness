import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";

/* Spec 40: session titles. The History list and the session header share the
   inline title editor and a window event that carries a title change from
   whichever view learned it first (the session stream, a rename, or a
   regenerate) to the other. */

export const UNTITLED_SESSION = "Untitled session";
export const NAMING_SESSION = "Naming…";
export const MAX_SESSION_TITLE = 80;

const TITLE_EVENT = "lana:session-title";

export interface SessionTitleUpdate {
  id: string;
  title: string;
  title_source: string;
  title_pending: boolean;
}

export function publishSessionTitle(update: SessionTitleUpdate): void {
  if (typeof window === "undefined") {
    return;
  }
  window.dispatchEvent(
    new CustomEvent<SessionTitleUpdate>(TITLE_EVENT, { detail: update })
  );
}

/* Calls handler for every published title change until unmount. The handler
   is read through a ref so callers can pass an inline function. */
export function useSessionTitleUpdates(
  handler: (update: SessionTitleUpdate) => void
): void {
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => {
    const listener = (event: Event) => {
      const detail = (event as CustomEvent<SessionTitleUpdate>).detail;
      if (detail && typeof detail.id === "string") {
        ref.current(detail);
      }
    };
    window.addEventListener(TITLE_EVENT, listener);
    return () => window.removeEventListener(TITLE_EVENT, listener);
  }, []);
}

/* Reads the title fields of a stream payload or an API row. */
export function titleUpdateFrom(
  id: string,
  value: Record<string, unknown>
): SessionTitleUpdate {
  return {
    id,
    title: typeof value.title === "string" ? value.title : "",
    title_source:
      typeof value.title_source === "string" ? value.title_source : "",
    title_pending: value.title_pending === true,
  };
}

/* The visible title: the stored one, "Naming…" while the first title is being
   made, else the "Untitled session" placeholder. */
export function visibleTitle(title: string | undefined, pending: boolean | undefined): string {
  if (title) {
    return title;
  }
  return pending ? NAMING_SESSION : UNTITLED_SESSION;
}

export interface TitleInputProps {
  initial: string;
  label: string;
  className?: string;
  /* Saves the value; a rejected promise keeps the editor open. */
  onSave: (value: string) => Promise<void>;
  onCancel: () => void;
}

/* The inline title editor: Enter or leaving the field saves, Escape cancels.
   An empty value saves too: the daemon then makes an automatic title. */
export function TitleInput({
  initial,
  label,
  className,
  onSave,
  onCancel,
}: TitleInputProps) {
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const doneRef = useRef(false);

  useEffect(() => {
    const input = inputRef.current;
    if (input) {
      input.focus();
      input.select();
    }
  }, []);

  async function commit() {
    if (doneRef.current || busy) {
      return;
    }
    const next = value.trim();
    if (next === initial.trim()) {
      doneRef.current = true;
      onCancel();
      return;
    }
    doneRef.current = true;
    setBusy(true);
    try {
      await onSave(next);
    } catch {
      doneRef.current = false;
      setBusy(false);
      inputRef.current?.focus();
    }
  }

  function cancel() {
    if (doneRef.current) {
      return;
    }
    doneRef.current = true;
    onCancel();
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter") {
      event.preventDefault();
      void commit();
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      cancel();
    }
  }

  return (
    <input
      ref={inputRef}
      className={"input title-input" + (className ? " " + className : "")}
      type="text"
      aria-label={label}
      maxLength={MAX_SESSION_TITLE}
      placeholder="Leave empty for an automatic title"
      value={value}
      disabled={busy}
      aria-busy={busy || undefined}
      onChange={(event) => setValue(event.target.value)}
      onKeyDown={handleKeyDown}
      onBlur={() => void commit()}
    />
  );
}
