/* One shared helper keeps a long-lived stream from holding its HTTP connection
   while the dashboard tab is hidden. Over HTTP/1.1 on 127.0.0.1 a browser
   allows about six connections per host, and each open tab holds two streams
   (the rooms stream plus a workflow, session, calendar or toolbox stream), so a
   hidden tab that keeps its streams makes every later tab wait forever. Once a
   tab has been hidden for HIDDEN_RELEASE_MS the helper aborts the fetch (freeing
   the connection) and blocks reconnects; when the tab is visible again it
   reconnects at once, so callers can catch up on what they missed. */

export const HIDDEN_RELEASE_MS = 15000;

export interface VisibilityHandlers {
  /* Release the connection: abort the in-flight fetch and clear any pending
     reconnect timer. */
  onRelease: () => void;
  /* Reconnect at once after the tab becomes visible again. */
  onResume: () => void;
}

export interface StreamVisibility {
  /* True while the stream was released for a hidden tab; the stream must not
     reconnect until it is false again. */
  blocked: () => boolean;
  /* Stop watching and clear the pending timer. */
  close: () => void;
}

export function watchVisibility(handlers: VisibilityHandlers): StreamVisibility {
  /* No document (a non-DOM test run): never release. */
  const doc = typeof document === "undefined" ? null : document;
  let released = false;
  let timer: ReturnType<typeof setTimeout> | null = null;

  function clearTimer(): void {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  }

  function evaluate(): void {
    if (!doc) {
      return;
    }
    if (doc.visibilityState === "hidden") {
      if (timer === null && !released) {
        timer = setTimeout(() => {
          timer = null;
          released = true;
          handlers.onRelease();
        }, HIDDEN_RELEASE_MS);
      }
      return;
    }
    clearTimer();
    if (released) {
      released = false;
      handlers.onResume();
    }
  }

  if (doc) {
    doc.addEventListener("visibilitychange", evaluate);
    /* The stream can open in an already hidden tab. */
    evaluate();
  }

  return {
    blocked: () => released,
    close: () => {
      clearTimer();
      if (doc) {
        doc.removeEventListener("visibilitychange", evaluate);
      }
    },
  };
}
