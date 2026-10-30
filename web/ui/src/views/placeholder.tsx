/* A route that is in the information architecture but not built yet. */
export interface PlaceholderPageProps {
  title: string;
  note?: string;
}

export function PlaceholderPage({ title, note }: PlaceholderPageProps) {
  return (
    <main id="main" className="page">
      <div className="page-head">
        <div>
          <h1>{title}</h1>
          <p className="muted">{note}</p>
        </div>
      </div>
    </main>
  );
}

export interface NotFoundPageProps {
  path?: string;
}

export function NotFoundPage({ path }: NotFoundPageProps) {
  return (
    <main id="main" className="page">
      <div className="page-head">
        <div>
          <h1>Page not found</h1>
          <p className="muted">
            No dashboard page matches <code>{path || "this address"}</code>.
          </p>
        </div>
      </div>
      <p>
        <a className="button-link" href="#/sessions">
          Go to sessions
        </a>
      </p>
    </main>
  );
}
