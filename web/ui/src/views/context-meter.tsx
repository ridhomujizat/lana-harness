import { useCallback, useEffect, useRef, useState } from "react";
import { errorMessage } from "../api/client";
import { getSessionUsage } from "../api/sessions";
import type { SessionUsage } from "../api/sessions";
import { compactSession } from "../api/turns";

/* The chat's context meter (owner 2026-11-12): a small ring left of the send
   button shows how full the session's context window is; a click opens the
   details and a Compact context button. */

export function formatTokens(n: number): string {
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    return (m >= 10 || Number.isInteger(m) ? Math.round(m) : Math.round(m * 10) / 10) + "m";
  }
  if (n >= 1000) {
    return Math.round(n / 1000) + "k";
  }
  return String(n);
}

export function ContextMeter({
  sessionId,
  refreshKey,
  busy,
}: {
  sessionId: string;
  /* Changes when a turn ends, so the meter reloads. */
  refreshKey: string | number;
  /* A turn is running: compacting is not possible. */
  busy: boolean;
}) {
  const [usage, setUsage] = useState<SessionUsage | null>(null);
  const [open, setOpen] = useState(false);
  const [compacting, setCompacting] = useState(false);
  const [note, setNote] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);

  const load = useCallback(() => {
    if (!sessionId) {
      setUsage(null);
      return;
    }
    getSessionUsage(sessionId)
      .then(setUsage)
      .catch(() => setUsage(null));
  }, [sessionId]);

  useEffect(() => {
    load();
  }, [load, refreshKey]);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onDown = (event: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!sessionId || !usage || usage.context_window <= 0) {
    return null;
  }
  const used = Math.max(0, usage.context_tokens);
  const ratio = Math.min(1, used / usage.context_window);
  const percent = Math.round(ratio * 100);
  const near = usage.compact_at > 0 && used >= usage.compact_at * 0.9;
  const tone = ratio >= 0.95 ? "danger" : near ? "warn" : "";
  const r = 7;
  const c = 2 * Math.PI * r;
  const summary = percent + "% · " + formatTokens(used) + "/" + formatTokens(usage.context_window);

  async function compactNow() {
    setCompacting(true);
    setNote("");
    try {
      const result = await compactSession(sessionId);
      setNote("Compacted: " + formatTokens(result.tokens_before) + " → " + formatTokens(result.tokens_after) + " tokens.");
      load();
    } catch (failure) {
      setNote(errorMessage(failure));
    } finally {
      setCompacting(false);
    }
  }

  return (
    <div className="ctx-meter" ref={rootRef}>
      <button
        type="button"
        className={"ctx-ring" + (tone ? " " + tone : "") + (open ? " on" : "")}
        aria-label={"Context window " + summary}
        aria-expanded={open}
        title={"Context " + summary}
        onClick={() => setOpen((value) => !value)}
      >
        <svg width="20" height="20" viewBox="0 0 20 20" aria-hidden="true">
          <circle className="ctx-ring-track" cx="10" cy="10" r={r} />
          <circle
            className="ctx-ring-fill"
            cx="10"
            cy="10"
            r={r}
            strokeDasharray={c}
            strokeDashoffset={c * (1 - ratio)}
            transform="rotate(-90 10 10)"
          />
        </svg>
      </button>
      {open ? (
        <div className="ctx-pop" role="dialog" aria-label="Context window">
          <div className="ctx-pop-head">
            <span className="ctx-pop-title">Context window</span>
            <span className="ctx-pop-value">{summary}</span>
          </div>
          <div className="ctx-bar" aria-hidden="true">
            <span className={"ctx-bar-fill" + (tone ? " " + tone : "")} style={{ width: percent + "%" }} />
            {usage.compact_at > 0 ? (
              <span className="ctx-bar-mark" style={{ left: Math.round((usage.compact_at / usage.context_window) * 100) + "%" }} />
            ) : null}
          </div>
          <p className="ctx-pop-note">
            {usage.compact_at > 0
              ? "Compacts automatically at " + formatTokens(usage.compact_at) + " (" + Math.round((usage.compact_at / usage.context_window) * 100) + "%)."
              : "Compacts automatically as the window fills."}
          </p>
          {note ? <p className="ctx-pop-note">{note}</p> : null}
          <button type="button" className="ctx-pop-btn" disabled={busy || compacting || used === 0} onClick={() => void compactNow()}>
            {compacting ? "Compacting…" : "Compact context"}
          </button>
        </div>
      ) : null}
    </div>
  );
}
