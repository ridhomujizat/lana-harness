import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import type { KeyboardEvent, ReactNode, SyntheticEvent } from "react";

const AnnounceContext = createContext<(text: unknown) => void>(() => {});

let announceCounter = 0;

export function AnnouncerProvider({ children }: { children?: ReactNode }) {
  const [message, setMessage] = useState<{
    id: number;
    text: string;
  } | null>(null);

  const announce = useCallback((text: unknown) => {
    announceCounter += 1;
    setMessage({ id: announceCounter, text: String(text || "") });
  }, []);

  useEffect(() => {
    const node = document.getElementById("announcer");
    if (!node || !message) {
      return undefined;
    }
    node.textContent = "";
    const timer = setTimeout(() => {
      node.textContent = message.text;
    }, 40);
    return () => clearTimeout(timer);
  }, [message]);

  return (
    <AnnounceContext.Provider value={announce}>
      {children}
    </AnnounceContext.Provider>
  );
}

export function useAnnounce(): (text: unknown) => void {
  return useContext(AnnounceContext);
}

function focusableElements(dialog: HTMLElement): HTMLElement[] {  const selector =
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
    'textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';
  return Array.from(dialog.querySelectorAll<HTMLElement>(selector)).filter(
    (element) => {
      return !element.hidden && element.getAttribute("aria-hidden") !== "true";
    }
  );
}

export interface DialogProps {
  title: ReactNode;
  dismissible?: boolean;
  className?: string;
  onClose?: () => void;
  children?: ReactNode;
}

export function Dialog({
  title,
  dismissible = true,
  className = "",
  onClose,
  children,
}: DialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const returnFocusRef = useRef<Element | null>(null);
  const titleId = "dialog-title-" + useId().replace(/[^A-Za-z0-9_-]/g, "");

  useEffect(() => {
    const dialog = dialogRef.current;
    returnFocusRef.current = document.activeElement;
    if (!dialog) {
      return undefined;
    }
    if (!dialog.open) {
      dialog.showModal();
    }
    const preferred = dialog.querySelector<HTMLElement>("[data-autofocus]");
    const target = preferred || focusableElements(dialog)[0];
    if (target) {
      target.focus();
    }
    return () => {
      if (dialog.open) {
        dialog.close();
      }
      const previous = returnFocusRef.current;
      if (
        previous instanceof HTMLElement &&
        typeof previous.focus === "function"
      ) {
        previous.focus();
      }
    };
  }, []);

  function handleKeyDown(event: KeyboardEvent<HTMLDialogElement>) {
    if (event.key !== "Tab") {
      return;
    }
    const dialog = dialogRef.current;
    if (!dialog) {
      return;
    }
    const focusable = focusableElements(dialog);
    if (focusable.length === 0) {
      event.preventDefault();
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = document.activeElement;
    if (!first || !last) {
      return;
    }
    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function handleCancel(event: SyntheticEvent<HTMLDialogElement>) {
    event.preventDefault();
    if (dismissible && onClose) {
      onClose();
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className={"dialog" + (className ? " " + className : "")}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      onKeyDown={handleKeyDown}
      onCancel={handleCancel}
    >
      <h2 id={titleId} className="dialog-title">
        {title}
      </h2>
      <div className="dialog-content">{children}</div>
    </dialog>
  );
}

export function detailText(detail: unknown): string {
  if (detail === null || detail === undefined) {
    return "";
  }
  if (typeof detail !== "object") {
    return String(detail);
  }
  const record = detail as Record<string, unknown>;
  const location: string[] = [];
  if (record.file) {
    location.push(String(record.file));
  }
  if (record.key) {
    location.push(String(record.key));
  }
  if (record.field) {
    location.push(String(record.field));
  }
  if (record.path) {
    location.push(String(record.path));
  }
  const head = location.join(" ");
  if (record.characters !== undefined && record.limit !== undefined) {
    return (
      (head ? head + ": " : "") +
      String(record.characters) +
      " characters, limit " +
      String(record.limit) +
      "."
    );
  }
  const rawMessage = record.message || record.error;
  const message = rawMessage ? String(rawMessage) : "";
  if (head && message) {
    return head + ": " + message;
  }
  return head || message || "Invalid value.";
}

export function ErrorDetails({ details }: { details?: unknown }) {
  if (!Array.isArray(details) || details.length === 0) {
    return null;
  }
  return (
    <ul className="error-details">
      {details.map((detail, index) => (
        <li key={index}>{detailText(detail)}</li>
      ))}
    </ul>
  );
}

export function ErrorNotice({
  message,
  details,
}: {
  message?: string;
  details?: unknown;
}) {
  if (!message && (!Array.isArray(details) || details.length === 0)) {
    return null;
  }
  return (
    <div className="error-box">
      {message ? <p className="error-message">{message}</p> : null}
      <ErrorDetails details={details} />
    </div>
  );
}

export function Notice({
  tone = "info",
  children,
}: {
  tone?: string;
  children?: ReactNode;
}) {
  return <p className={"notice notice-" + tone}>{children}</p>;
}

export function Loading({ label = "Loading." }: { label?: string }) {
  return <p className="muted">{label}</p>;
}

export function Icon({
  name,
  className = "icon-20",
}: {
  name: string;
  className?: string;
}) {
  return (
    <img
      className={className}
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
    />
  );
}
