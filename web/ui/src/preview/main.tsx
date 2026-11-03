import { createRoot } from "react-dom/client";
import "../styles/index.css";
import { AnnouncerProvider } from "../ui/ui";
import { applyTheme, isThemeChoice, saveTheme } from "../app/theme";
import { mockApi } from "./fixtures";

/* Dev-only preview harness (web/ui/preview.html). It renders the real App tree
   with the same providers and stylesheet as src/main.tsx, but answers every
   network call from fixtures.ts instead of a running daemon. It is never part
   of the production build: vite.config.ts keeps index.html as the only input,
   so `vite build` never sees preview.html. */

function resolveURL(input: RequestInfo | URL): URL {
  if (typeof input === "string") {
    return new URL(input, window.location.origin);
  }
  if (input instanceof URL) {
    return input;
  }
  return new URL(input.url, window.location.origin);
}

function methodOf(input: RequestInfo | URL, init?: RequestInit): string {
  if (init && init.method) {
    return init.method;
  }
  if (typeof input !== "string" && !(input instanceof URL)) {
    return input.method;
  }
  return "GET";
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body ?? null), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/* An SSE body that emits its text once and then stays open so the reader in
   api/client.ts keeps waiting instead of reconnecting. Closing on abort lets a
   view release the stream without leaking the reader. */
function streamResponse(body: string, signal?: AbortSignal | null): Response {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(body));
      if (!signal) {
        return;
      }
      const close = () => {
        try {
          controller.close();
        } catch {
          /* The stream is already closed. */
        }
      };
      if (signal.aborted) {
        close();
        return;
      }
      signal.addEventListener("abort", close);
    },
  });
  return new Response(stream, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
    },
  });
}

function installMockApi(): void {
  window.fetch = (async (
    input: RequestInfo | URL,
    init?: RequestInit
  ): Promise<Response> => {
    const url = resolveURL(input);
    const result = mockApi(methodOf(input, init), url);
    if (result.stream !== undefined) {
      return streamResponse(result.stream, init ? init.signal : null);
    }
    return jsonResponse(result.status, result.body);
  }) as typeof fetch;
}

/* ?theme=dark / ?theme=light writes the app's theme preference before App's
   module body reads it, so a screenshot can pin the theme. */
const params = new URLSearchParams(window.location.search);
const requestedTheme = params.get("theme");
if (isThemeChoice(requestedTheme)) {
  saveTheme(requestedTheme);
  applyTheme(requestedTheme);
}

/* ?state=login / ?state=setup pin the mocked app state for screenshots; when
   the caller did not pass a hash route, land on the matching page. The API side
   of the switch lives in fixtures.ts. */
const requestedState = params.get("state");
if (!window.location.hash || window.location.hash === "#") {
  if (requestedState === "login") {
    window.location.hash = "#/login";
  } else if (requestedState === "setup") {
    window.location.hash = "#/setup/provider";
  }
}

installMockApi();

const mount = document.getElementById("app");
if (mount) {
  /* Drop the static loading copy before the first render, as src/main.tsx does. */
  mount.textContent = "";
  /* The dynamic import runs App's module body after saveTheme, so readTheme()
     there picks up the requested theme. */
  void import("../app/App").then(({ App }) => {
    createRoot(mount).render(
      <AnnouncerProvider>
        <App />
      </AnnouncerProvider>
    );
  });
}
