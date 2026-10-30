import { html } from "/assets/vendor/preact-htm.js";

/* A route that is in the information architecture but not built yet. */
export function PlaceholderPage({ title, note }) {
  return html`<main id="main" class="page">
    <div class="page-head">
      <div>
        <h1>${title}</h1>
        <p class="muted">${note}</p>
      </div>
    </div>
  </main>`;
}

export function NotFoundPage({ path }) {
  return html`<main id="main" class="page">
    <div class="page-head">
      <div>
        <h1>Page not found</h1>
        <p class="muted">
          No dashboard page matches <code>${path || "this address"}</code>.
        </p>
      </div>
    </div>
    <p><a class="button-link" href="#/sessions">Go to sessions</a></p>
  </main>`;
}
