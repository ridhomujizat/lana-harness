import {
  html,
  createContext,
  useContext,
  useState,
  useEffect,
  useRef,
  useMemo,
  useCallback,
} from "/assets/vendor/preact-htm.js";

const AnnounceContext = createContext(() => {});

let announceCounter = 0;

export function AnnouncerProvider({ children }) {
  const [message, setMessage] = useState(null);

  const announce = useCallback((text) => {
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

  return html`<${AnnounceContext.Provider} value=${announce} children=${children} />`;
}

export function useAnnounce() {
  return useContext(AnnounceContext);
}

let dialogCounter = 0;

function focusableElements(dialog) {
  const selector =
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
    'textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';
  return Array.from(dialog.querySelectorAll(selector)).filter((element) => {
    return !element.hidden && element.getAttribute("aria-hidden") !== "true";
  });
}

export function Dialog({
  title,
  dismissible = true,
  className = "",
  onClose,
  children,
}) {
  const dialogRef = useRef(null);
  const returnFocusRef = useRef(null);
  const titleId = useMemo(() => {
    dialogCounter += 1;
    return "dialog-title-" + dialogCounter;
  }, []);

  useEffect(() => {
    const dialog = dialogRef.current;
    returnFocusRef.current = document.activeElement;
    if (!dialog) {
      return undefined;
    }
    if (!dialog.open) {
      dialog.showModal();
    }
    const preferred = dialog.querySelector("[data-autofocus]");
    const target = preferred || focusableElements(dialog)[0];
    if (target) {
      target.focus();
    }
    return () => {
      if (dialog.open) {
        dialog.close();
      }
      const previous = returnFocusRef.current;
      if (previous && typeof previous.focus === "function") {
        previous.focus();
      }
    };
  }, []);

  function handleKeyDown(event) {
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
    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function handleCancel(event) {
    event.preventDefault();
    if (dismissible && onClose) {
      onClose();
    }
  }

  return html`<dialog
    ref=${dialogRef}
    class=${"dialog" + (className ? " " + className : "")}
    role="dialog"
    aria-modal="true"
    aria-labelledby=${titleId}
    onKeyDown=${handleKeyDown}
    onCancel=${handleCancel}
  >
    <h2 id=${titleId} class="dialog-title">${title}</h2>
    <div class="dialog-content">${children}</div>
  </dialog>`;
}

export function detailText(detail) {
  if (detail === null || detail === undefined) {
    return "";
  }
  if (typeof detail !== "object") {
    return String(detail);
  }
  const location = [];
  if (detail.file) {
    location.push(String(detail.file));
  }
  if (detail.key) {
    location.push(String(detail.key));
  }
  if (detail.field) {
    location.push(String(detail.field));
  }
  const head = location.join(" ");
  if (detail.characters !== undefined && detail.limit !== undefined) {
    return (
      (head ? head + ": " : "") +
      detail.characters +
      " characters, limit " +
      detail.limit +
      "."
    );
  }
  const message = detail.message || detail.error || "";
  if (head && message) {
    return head + ": " + message;
  }
  return head || message || "Invalid value.";
}

export function ErrorDetails({ details }) {
  if (!Array.isArray(details) || details.length === 0) {
    return null;
  }
  return html`<ul class="error-details">
    ${details.map((detail, index) =>
      html`<li key=${index}>${detailText(detail)}</li>`
    )}
  </ul>`;
}

export function ErrorNotice({ message, details }) {
  if (!message && (!Array.isArray(details) || details.length === 0)) {
    return null;
  }
  return html`<div class="error-box">
    ${message ? html`<p class="error-message">${message}</p>` : null}
    <${ErrorDetails} details=${details} />
  </div>`;
}

export function Notice({ tone = "info", children }) {
  return html`<p class=${"notice notice-" + tone}>${children}</p>`;
}

export function Loading({ label = "Loading." }) {
  return html`<p class="muted">${label}</p>`;
}
