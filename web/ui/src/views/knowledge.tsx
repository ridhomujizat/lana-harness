import { useCallback, useEffect, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import { ApiError, errorMessage } from "../api/client";
import {
  consolidateKnowledge,
  getKnowledge,
  getKnowledgeReport,
  isKnowledgeBusy,
  isKnowledgeUnavailable,
  uploadKnowledge,
} from "../api/knowledge";
import type { KnowledgeReport, KnowledgeStatus } from "../api/knowledge";
import { Markdown } from "../lib/markdown";
import { ErrorNotice, Loading, Notice } from "../ui/ui";
import "../styles/dashboard.css";

/* REQ-KNU-040, REQ-KNU-041; AC-KNU-008: the Knowledge page shows the vault
   status and the upkeep log, uploads files into inbox/, and starts a
   consolidation run. The layout follows the monitoring dashboard: a status
   row, then one section per topic. The status is polled while a run is in
   progress. */

const POLL_MS = 10000;

function count(value: number | null | undefined): string {
  return Number(value || 0).toLocaleString("en-US");
}

/* Times are Unix ms with 0 meaning "never" (API-KNU-003). */
function formatTime(ms: number | null | undefined): string {
  const value = Number(ms) || 0;
  if (value <= 0) {
    return "";
  }
  return new Date(value).toLocaleString();
}

function embeddingText(status: KnowledgeStatus): string {
  const embedding = status.embedding;
  if (!embedding || !embedding.provider) {
    return "keyword search only";
  }
  return (
    embedding.provider +
    " " +
    embedding.model +
    " · " +
    count(embedding.vectors) +
    "/" +
    count(embedding.chunks)
  );
}

interface Feedback {
  tone: string;
  message: string;
}

export function KnowledgePage() {
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading"
  );
  const [data, setData] = useState<KnowledgeStatus | null>(null);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const [report, setReport] = useState<KnowledgeReport | null>(null);
  const [reportError, setReportError] = useState("");
  const [reportLoaded, setReportLoaded] = useState(false);

  const [picked, setPicked] = useState<File[]>([]);
  const [uploading, setUploading] = useState(false);
  const [uploadFeedback, setUploadFeedback] = useState<Feedback | null>(null);

  const [starting, setStarting] = useState(false);
  const [runFeedback, setRunFeedback] = useState<Feedback | null>(null);

  const [reloadToken, setReloadToken] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  /* A failed refresh keeps the last data and shows the notice; only the first
     load leaves the loading state. */
  const fetchStatus = useCallback(async (markLoading: boolean) => {
    if (markLoading) {
      setStatus("loading");
    }
    try {
      const result = await getKnowledge();
      setData(result);
      setError("");
      setDetails([]);
      setStatus("ready");
    } catch (failure) {
      setError(errorMessage(failure));
      setDetails(failure instanceof ApiError ? failure.details : []);
      setStatus((current) => (current === "ready" ? "ready" : "error"));
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch is async; state lands after the await.
    void fetchStatus(true);
  }, [fetchStatus, reloadToken]);

  /* REQ-KNU-040: poll every 10 s while a consolidation run is in progress and
     stop as soon as it finishes. */
  const running = Boolean(data?.consolidation?.running);
  useEffect(() => {
    if (!running) {
      return undefined;
    }
    const timer = setInterval(() => {
      void fetchStatus(false);
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [running, fetchStatus]);

  /* Fetch the latest lint report once the status is loaded (API-KNU-004). */
  useEffect(() => {
    if (status !== "ready") {
      return undefined;
    }
    let alive = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the report fetch is async; the loaded flag clears before the await.
    setReportLoaded(false);
    getKnowledgeReport()
      .then((result) => {
        if (!alive) {
          return;
        }
        setReport(result);
        setReportError("");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setReport(null);
        setReportError(errorMessage(failure));
      })
      .finally(() => {
        if (alive) {
          setReportLoaded(true);
        }
      });
    return () => {
      alive = false;
    };
  }, [status, reloadToken]);

  function reload() {
    setReloadToken((value) => value + 1);
  }

  function onPick(event: ChangeEvent<HTMLInputElement>) {
    const files = event.target.files ? Array.from(event.target.files) : [];
    setPicked(files);
    setUploadFeedback(null);
  }

  async function upload() {
    if (uploading || picked.length === 0) {
      return;
    }
    setUploading(true);
    setUploadFeedback(null);
    try {
      const result = await uploadKnowledge(picked);
      const saved = result && Array.isArray(result.saved) ? result.saved : [];
      setUploadFeedback({
        tone: "ok",
        message:
          "Saved " +
          saved.length +
          (saved.length === 1 ? " file" : " files") +
          (saved.length ? ": " + saved.join(", ") : "."),
      });
      setPicked([]);
      if (inputRef.current) {
        inputRef.current.value = "";
      }
      reload();
    } catch (failure) {
      setUploadFeedback({ tone: "warn", message: errorMessage(failure) });
    } finally {
      setUploading(false);
    }
  }

  async function runNow() {
    if (starting || running) {
      return;
    }
    setStarting(true);
    setRunFeedback(null);
    try {
      const result = await consolidateKnowledge();
      setRunFeedback({
        tone: "ok",
        message:
          result && result.started
            ? "Consolidation started. The status refreshes on its own."
            : "Consolidation is already scheduled.",
      });
      reload();
    } catch (failure) {
      if (isKnowledgeBusy(failure)) {
        setRunFeedback({
          tone: "warn",
          message: "A consolidation is already running",
        });
        reload();
      } else if (isKnowledgeUnavailable(failure)) {
        setRunFeedback({
          tone: "warn",
          message:
            "The knowledge service is not running. Consolidation runs only in the daemon.",
        });
      } else {
        setRunFeedback({ tone: "warn", message: errorMessage(failure) });
      }
    } finally {
      setStarting(false);
    }
  }

  if (!data) {
    return (
      <main id="main" className="page dash-page">
        <header className="dash-head">
          <h1 className="dash-title">Knowledge</h1>
        </header>
        {status === "error" ? (
          <div className="dash-error-notice" role="alert">
            <ErrorNotice message={error} details={details} />
            <button
              type="button"
              className="button-ui button-ui-secondary"
              onClick={reload}
            >
              Try again
            </button>
          </div>
        ) : (
          <Loading label="Loading knowledge…" />
        )}
      </main>
    );
  }

  if (!data.available) {
    return (
      <main id="main" className="page dash-page">
        <header className="dash-head">
          <h1 className="dash-title">Knowledge</h1>
        </header>
        <Notice tone="info">
          The knowledge service is not running, so there is nothing to show
          here. It starts with the daemon (lana daemon).
        </Notice>
      </main>
    );
  }

  const consolidation = data.consolidation;
  const pending = data.sources_pending || [];
  const failed = data.sources_failed || [];
  const unsupported = data.inbox_unsupported || [];
  const log = data.log || [];
  const lastAt = formatTime(consolidation.last_at);
  const nextAt = formatTime(consolidation.next_at);

  return (
    <main id="main" className="page dash-page">
      <header className="dash-head">
        <h1 className="dash-title">Knowledge</h1>
        <div className="dash-controls">
          <button
            type="button"
            className="button-ui button-ui-secondary dash-refresh"
            onClick={reload}
            disabled={status === "loading"}
          >
            Refresh
          </button>
        </div>
      </header>

      {error ? (
        <div className="dash-error-notice" role="alert">
          <ErrorNotice message={error} details={details} />
          <button
            type="button"
            className="button-ui button-ui-secondary"
            onClick={reload}
          >
            Retry
          </button>
        </div>
      ) : null}

      <section className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Vault</h2>
          <p className="dash-note">
            <code className="knowledge-path">{data.path}</code>
          </p>
        </div>
        {data.exists ? null : (
          <p className="dash-note">
            The vault has not been created yet; the first ingest creates it.
          </p>
        )}
        <div className="dash-tiles">
          <div className="dash-tile">
            <div className="dash-tile-label">Pages</div>
            <div className="dash-tile-value">{count(data.counts.pages)}</div>
            <div className="dash-tile-sub">wiki pages</div>
          </div>
          <div className="dash-tile">
            <div className="dash-tile-label">Sources</div>
            <div className="dash-tile-value">{count(data.counts.sources)}</div>
            <div className="dash-tile-sub">ingested sources</div>
          </div>
          <div className="dash-tile">
            <div className="dash-tile-label">Journal days</div>
            <div className="dash-tile-value">
              {count(data.counts.journal_days)}
            </div>
            <div className="dash-tile-sub">daily notes</div>
          </div>
        </div>
        <p className="dash-note">Embedding: {embeddingText(data)}</p>
        <p className="dash-note">
          Knowledge agent: {data.agent || "none configured"}
        </p>
        {data.agent_error ? <ErrorNotice message={data.agent_error} /> : null}
      </section>

      <section className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Upload to inbox</h2>
        </div>
        <p className="dash-note">
          Markdown, text, PDF, or HTML files (up to 30 MB each). They land in
          inbox/ and are compiled into wiki pages.
        </p>
        <div className="knowledge-actions">
          <label className="sr-only" htmlFor="knowledge-upload">
            Files to upload
          </label>
          <input
            id="knowledge-upload"
            ref={inputRef}
            type="file"
            multiple
            accept=".md,.txt,.pdf,.html"
            onChange={onPick}
          />
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={uploading || picked.length === 0}
            onClick={upload}
          >
            {uploading ? "Uploading…" : "Upload"}
          </button>
        </div>
        {uploadFeedback ? (
          <Notice tone={uploadFeedback.tone}>{uploadFeedback.message}</Notice>
        ) : null}
      </section>

      <section className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Consolidation</h2>
        </div>
        <div className="knowledge-actions">
          <button
            type="button"
            className="button-ui button-ui-primary"
            disabled={starting || running}
            onClick={runNow}
          >
            {running
              ? "Consolidation running…"
              : starting
                ? "Starting…"
                : "Run consolidation now"}
          </button>
        </div>
        {runFeedback ? (
          <Notice tone={runFeedback.tone}>{runFeedback.message}</Notice>
        ) : null}
        <dl className="knowledge-facts">
          <dt>Last run</dt>
          <dd>{lastAt || "never"}</dd>
          <dt>Next run</dt>
          <dd>{nextAt || "not scheduled"}</dd>
          <dt>Report</dt>
          <dd>{consolidation.report || "—"}</dd>
        </dl>
        {consolidation.last_error ? (
          <ErrorNotice
            message={"Last consolidation error: " + consolidation.last_error}
          />
        ) : null}
      </section>

      <section className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Sources</h2>
        </div>
        <h3 className="knowledge-subtitle">Pending</h3>
        {pending.length === 0 ? (
          <p className="dash-empty">None</p>
        ) : (
          <ul className="knowledge-list">
            {pending.map((path) => (
              <li key={path}>{path}</li>
            ))}
          </ul>
        )}
        <h3 className="knowledge-subtitle">Failed</h3>
        {failed.length === 0 ? (
          <p className="dash-empty">None</p>
        ) : (
          <ul className="knowledge-list">
            {failed.map((item) => (
              <li key={item.path}>
                <code className="knowledge-path">{item.path}</code> — {item.error}
              </li>
            ))}
          </ul>
        )}
        <h3 className="knowledge-subtitle">Unsupported inbox files</h3>
        {unsupported.length === 0 ? (
          <p className="dash-empty">None</p>
        ) : (
          <ul className="knowledge-list">
            {unsupported.map((path) => (
              <li key={path}>{path}</li>
            ))}
          </ul>
        )}
      </section>

      <section className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Latest lint report</h2>
          {report ? (
            <p className="dash-note">
              <code className="knowledge-path">{report.path}</code>
            </p>
          ) : null}
        </div>
        {reportError ? (
          <ErrorNotice message={reportError} />
        ) : !reportLoaded ? (
          <Loading label="Loading report…" />
        ) : report ? (
          <Markdown text={report.markdown} />
        ) : (
          <p className="dash-empty">No report yet</p>
        )}
      </section>

      <section className="dash-section">
        <div className="dash-section-head">
          <h2 className="dash-section-title">Recent log</h2>
        </div>
        {log.length === 0 ? (
          <p className="dash-empty">No log entries yet.</p>
        ) : (
          <ul className="knowledge-log">
            {log.map((entry, index) => (
              <li key={index} className="knowledge-log-entry">
                <h3 className="knowledge-log-heading">{entry.heading}</h3>
                {entry.lines && entry.lines.length > 0 ? (
                  <ul className="knowledge-list">
                    {entry.lines.map((line, lineIndex) => (
                      <li key={lineIndex}>{line}</li>
                    ))}
                  </ul>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
