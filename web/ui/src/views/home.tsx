/* Home is an empty state in v1; stats and recent activity follow later. */
export function HomePage() {
  return (
    <main id="main" className="page page-home">
      <div className="empty-state">
        <div className="featured-icon">
          <img
            className="icon-20"
            src="/assets/icons/home.svg"
            alt=""
            aria-hidden="true"
          />
        </div>
        <h1 className="empty-title">Home is coming soon</h1>
        <p className="empty-text">
          Stats and recent activity will live here. Use Sessions to chat with your
          agents.
        </p>
        <a className="button-link" href="#/sessions">
          Go to sessions
        </a>
      </div>
    </main>
  );
}
