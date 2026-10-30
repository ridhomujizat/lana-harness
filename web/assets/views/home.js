import { html } from "/assets/vendor/preact-htm.js";

/* Home is an empty state in v1; stats and recent activity follow later. */
export function HomePage() {
  return html`<main id="main" class="page page-home">
    <div class="empty-state">
      <div class="featured-icon">
        <img class="icon-20" src="/assets/icons/home.svg" alt="" aria-hidden="true" />
      </div>
      <h1 class="empty-title">Home is coming soon</h1>
      <p class="empty-text">
        Stats and recent activity will live here. Use Sessions to chat with your agents.
      </p>
      <a class="button-link" href="#/sessions">Go to sessions</a>
    </div>
  </main>`;
}
