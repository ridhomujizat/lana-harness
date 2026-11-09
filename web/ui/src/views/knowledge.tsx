import { useCallback, useEffect, useRef, useState } from "react";
import type { ChangeEvent, ReactNode } from "react";
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
import { detailText } from "../ui/ui";
import { Ic } from "../ui/icons";
import { useAgentLabel } from "./agent-label";
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Panel,
  Skeleton,
  Stat,
} from "../ui/gr8r";
import { AgentName } from "./agent-avatar";

/* REQ-KNU-040, REQ-KNU-041; AC-KNU-008: the Knowledge page shows the vault
   status and the upkeep log, uploads files into inbox/, and starts a
   consolidation run. The layout follows the Gr8r files view: a page header,
   a KPI strip, panels for the vault, upload, consolidation and sources, and
   the latest lint report with the upkeep log. The status is polled while a
   run is in progress. */

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

/* Gr8r inline alert for the upload and consolidation feedback. */
function Alert({ tone, children }: { tone: string; children: ReactNode }) {
  const warn = tone === "warn";
  return (
    <div
      className={"alert " + (warn ? "warn" : "ok")}
      role="status"
      aria-live="polite"
    >
      <Ic name={warn ? "triangle-alert" : "circle-check"} size={15} />
      <span className="grow">{children}</span>
    </div>
  );
}

function DetailsList({ details }: { details: unknown[] }) {
  if (!details || details.length === 0) {
    return null;
  }
  return (
    <ul className="knowledge-details">
      {details.map((detail, index) => (
        <li key={index}>{detailText(detail)}</li>
      ))}
    </ul>
  );
}

/* One vault source row: an icon tile, the path, and the error when it failed. */
function SourceRow({
  icon,
  path,
  error,
}: {
  icon: string;
  path: string;
  error?: string;
}) {
  return (
    <div className="mini knowledge-source">
      <span
        className={"knowledge-tile" + (error ? " is-error" : "")}
        aria-hidden="true"
      >
        <Ic name={icon} size={15} />
      </span>
      <span className="knowledge-source-main">
        <code className="mono knowledge-path">{path}</code>
        {error ? <span className="knowledge-source-error">{error}</span> : null}
      </span>
    </div>
  );
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
  const label = useAgentLabel();

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
      <main id="main" className="page knowledge-page">
        <header className="ph">
          <div>
            <h1>Knowledge</h1>
            <p>The vault, its sources, and the upkeep that keeps them current.</p>
          </div>
          <div className="acts">
            <Button
              variant="secondary"
              icon="refresh-cw"
              onClick={reload}
              disabled={status === "loading"}
            >
              Refresh
            </Button>
          </div>
        </header>

        {status === "error" ? (
          <div className="panel" role="alert">
            <ErrorState
              icon="cloud-alert"
              title="Could not load knowledge"
              text={error}
              action={
                <Button variant="secondary" onClick={reload}>
                  Try again
                </Button>
              }
            >
              <DetailsList details={details} />
            </ErrorState>
          </div>
        ) : (
          <div className="panel knowledge-loading" aria-busy="true">
            <span className="sr">Loading knowledge…</span>
            <Skeleton width="42%" height={14} />
            <Skeleton width="100%" height={64} radius={10} />
            <Skeleton width="88%" height={12} />
            <Skeleton width="70%" height={12} />
          </div>
        )}
      </main>
    );
  }

  if (!data.available) {
    return (
      <main id="main" className="page knowledge-page">
        <header className="ph">
          <div>
            <h1>Knowledge</h1>
            <p>The vault, its sources, and the upkeep that keeps them current.</p>
          </div>
        </header>
        <div className="panel">
          <EmptyState
            icon="database"
            title="Knowledge is off"
            text="The knowledge service is not running, so there is nothing to show here. It starts with the daemon (lana daemon)."
          />
        </div>
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
    <main id="main" className="page knowledge-page">
      <header className="ph">
        <div>
          <h1>Knowledge</h1>
          <p>The vault, its sources, and the upkeep that keeps them current.</p>
        </div>
        <div className="acts">
          <Button
            variant="secondary"
            icon="refresh-cw"
            onClick={reload}
            disabled={status === "loading"}
          >
            Refresh
          </Button>
          <Button
            variant="primary"
            icon="upload"
            onClick={() => inputRef.current?.click()}
          >
            Upload files
          </Button>
        </div>
      </header>

      <div className="stats knowledge-stats">
        <Stat
          icon="file-text"
          label="Pages"
          value={count(data.counts.pages)}
          detail="wiki pages"
        />
        <Stat
          icon="inbox"
          label="Sources"
          value={count(data.counts.sources)}
          detail="ingested sources"
        />
        <Stat
          icon="calendar-days"
          label="Journal days"
          value={count(data.counts.journal_days)}
          detail="daily notes"
        />
      </div>

      {error ? (
        <div className="knowledge-error" role="alert">
          <ErrorState
            icon="cloud-alert"
            title="Could not refresh knowledge"
            text={error}
            action={
              <Button variant="secondary" icon="refresh-cw" onClick={reload}>
                Retry
              </Button>
            }
          >
            <DetailsList details={details} />
          </ErrorState>
        </div>
      ) : null}

      <Panel
        title="Vault"
        actions={
          data.exists ? (
            <Badge tone="green" dot>
              Ready
            </Badge>
          ) : (
            <Badge tone="amber" dot>
              Not created
            </Badge>
          )
        }
      >
        <div>
          <p className="knowledge-note">
            <code className="mono knowledge-path">{data.path}</code>
          </p>
          {data.exists ? null : (
            <p className="knowledge-note">
              The vault has not been created yet; the first ingest creates it.
            </p>
          )}
          <dl className="knowledge-facts">
            <dt>Embedding</dt>
            <dd>{embeddingText(data)}</dd>
          </dl>
          <p className="knowledge-note">
            Knowledge agent:{" "}
            {data.agent ? <AgentName name={data.agent} label={label(data.agent)} /> : "none configured"}
          </p>
          {data.agent_error ? (
            <div className="alert danger" role="alert">
              <Ic name="circle-alert" size={15} />
              <span className="grow">{data.agent_error}</span>
            </div>
          ) : null}
        </div>
      </Panel>

      <Panel title="Upload to inbox">
        <p className="knowledge-note">
          Markdown, text, PDF, or HTML files (up to 30 MB each). They land in
          inbox/ and are compiled into wiki pages.
        </p>
        <div className="knowledge-upload">
          <label
            className="knowledge-dropzone"
            htmlFor="knowledge-upload"
          >
            <Ic name="upload-cloud" size={18} />
            <span>Drop files here or browse</span>
          </label>
          <input
            id="knowledge-upload"
            ref={inputRef}
            className="sr"
            type="file"
            multiple
            accept=".md,.txt,.pdf,.html"
            aria-label="Files to upload"
            onChange={onPick}
          />
          <Button
            variant="primary"
            icon="upload"
            disabled={uploading || picked.length === 0}
            loading={uploading}
            onClick={upload}
          >
            {uploading ? "Uploading…" : "Upload"}
          </Button>
        </div>
        {picked.length > 0 ? (
          <p className="knowledge-note">
            {picked.length} file{picked.length === 1 ? "" : "s"} ready:{" "}
            {picked.map((file) => file.name).join(", ")}
          </p>
        ) : null}
        {uploadFeedback ? (
          <Alert tone={uploadFeedback.tone}>{uploadFeedback.message}</Alert>
        ) : null}
      </Panel>

      <Panel
        title="Consolidation"
        actions={
          <Button
            variant="primary"
            size="sm"
            icon="play"
            disabled={starting || running}
            loading={starting}
            onClick={runNow}
          >
            {running
              ? "Consolidation running…"
              : starting
                ? "Starting…"
                : "Run consolidation now"}
          </Button>
        }
      >
        <dl className="knowledge-facts">
          <dt>Last run</dt>
          <dd>{lastAt || "never"}</dd>
          <dt>Next run</dt>
          <dd>{nextAt || "not scheduled"}</dd>
          <dt>Report</dt>
          <dd>{consolidation.report || "—"}</dd>
        </dl>
        {runFeedback ? (
          <Alert tone={runFeedback.tone}>{runFeedback.message}</Alert>
        ) : null}
        {consolidation.last_error ? (
          <div className="alert danger knowledge-inline-alert" role="alert">
            <Ic name="circle-alert" size={15} />
            <span className="grow">
              {"Last consolidation error: " + consolidation.last_error}
            </span>
          </div>
        ) : null}
      </Panel>

      <Panel title="Sources">
        <h3 className="knowledge-subtitle">
          Pending <span className="cnt">{pending.length}</span>
        </h3>
        {pending.length === 0 ? (
          <p className="knowledge-empty">None</p>
        ) : (
          <div className="knowledge-rows">
            {pending.map((path) => (
              <SourceRow key={path} icon="hourglass" path={path} />
            ))}
          </div>
        )}

        <h3 className="knowledge-subtitle">
          Failed <span className="cnt">{failed.length}</span>
        </h3>
        {failed.length === 0 ? (
          <p className="knowledge-empty">None</p>
        ) : (
          <div className="knowledge-rows">
            {failed.map((item) => (
              <SourceRow
                key={item.path}
                icon="file-x"
                path={item.path}
                error={item.error}
              />
            ))}
          </div>
        )}

        <h3 className="knowledge-subtitle">
          Unsupported inbox files{" "}
          <span className="cnt">{unsupported.length}</span>
        </h3>
        {unsupported.length === 0 ? (
          <p className="knowledge-empty">None</p>
        ) : (
          <div className="knowledge-rows">
            {unsupported.map((path) => (
              <SourceRow key={path} icon="file-warning" path={path} />
            ))}
          </div>
        )}
      </Panel>

      <Panel
        title="Latest lint report"
        actions={
          report ? (
            <code className="mono knowledge-panel-path">{report.path}</code>
          ) : null
        }
      >
        {reportError ? (
          <div className="alert danger" role="alert">
            <Ic name="circle-alert" size={15} />
            <span className="grow">{reportError}</span>
          </div>
        ) : !reportLoaded ? (
          <div className="knowledge-loading" aria-busy="true">
            <span className="sr">Loading report…</span>
            <Skeleton width="60%" height={12} />
            <Skeleton width="90%" height={12} />
            <Skeleton width="75%" height={12} />
          </div>
        ) : report ? (
          <div className="knowledge-report">
            <Markdown text={report.markdown} />
          </div>
        ) : (
          <p className="knowledge-empty">No report yet</p>
        )}
      </Panel>

      <Panel title="Recent log">
        {log.length === 0 ? (
          <p className="knowledge-empty">No log entries yet.</p>
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
      </Panel>
    </main>
  );
}
