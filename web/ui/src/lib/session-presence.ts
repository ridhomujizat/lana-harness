import { useEffect } from "react";
import { reportPresence } from "../api/presence";

/* Spec #42: the session page reports while it is visible and focused. The
   daemon keeps a visible report for 45 s, so a visible tab repeats it every
   30 s; a hidden, blurred, or closed tab reports hidden at once. */

export const PRESENCE_INTERVAL_MS = 30_000;

export type PresenceReporter = (sessionId: string, visible: boolean) => Promise<void>;

/* tabShowsPage is true when the tab is in front and the window has focus. */
export function tabShowsPage(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  const focused = typeof document.hasFocus === "function" ? document.hasFocus() : true;
  return document.visibilityState === "visible" && focused;
}

export function useSessionPresence(
  sessionId: string | null | undefined,
  report: PresenceReporter = reportPresence
): void {
  useEffect(() => {
    if (!sessionId || typeof window === "undefined") {
      return;
    }
    let shown = false;
    const send = (visible: boolean) => {
      shown = visible;
      report(sessionId, visible).catch(() => {
        /* Presence is best effort: a failed report only means one more
           notification. */
      });
    };
    const update = () => {
      const visible = tabShowsPage();
      if (visible !== shown) {
        send(visible);
      }
    };
    const onBlur = () => {
      if (shown) {
        send(false);
      }
    };
    update();
    const timer = window.setInterval(() => {
      if (tabShowsPage()) {
        send(true);
      } else if (shown) {
        send(false);
      }
    }, PRESENCE_INTERVAL_MS);
    document.addEventListener("visibilitychange", update);
    window.addEventListener("focus", update);
    window.addEventListener("blur", onBlur);
    window.addEventListener("pagehide", onBlur);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
      window.removeEventListener("focus", update);
      window.removeEventListener("blur", onBlur);
      window.removeEventListener("pagehide", onBlur);
      if (shown) {
        send(false);
      }
    };
  }, [sessionId, report]);
}
