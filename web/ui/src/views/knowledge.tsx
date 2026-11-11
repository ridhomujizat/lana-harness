import { useCallback, useEffect, useRef, useState } from "react";
import type { ChangeEvent, ReactNode } from "react";
import { ApiError, errorMessage } from "../api/client";
import {
  consolidateKnowledge,
  getKnowledge,
  isKnowledgeBusy,
  isKnowledgeUnavailable,
  uploadKnowledge,
} from "../api/knowledge";
import type { KnowledgeStatus } from "../api/knowledge";
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
  Tabs,
} from "../ui/gr8r";
import { AgentName } from "./agent-avatar";
import { ActivityTab } from "./knowledge-activity";
import { GraphTab } from "./knowledge-graph";
import { PagesTab } from "./knowledge-pages";
import { ReviewTab } from "./knowledge-review";
import { knowledgeHref } from "./knowledge-shared";

/* REQ-KNU-040, REQ-KNU-041 (spec 31) and spec 48 REQ-KGR-050, -056: the
   Knowledge page. A header with the last run, Upload and Run now, then tabs:
   Graph (default), Pages, Review, Activity. The vault, sources and
   consolidation details live at the end of the Activity tab. The status is
   polled while a run is in progress. */

type TabKey = "graph" | "pages" | "review" | "activity";
const TAB_KEYS: TabKey[] = ["graph", "pages", "review", "activity"];

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

/* Vault, consolidation and sources: the spec 31 panels, now under Activity →
   Details (REQ-KGR-055). */
function KnowledgeDetails({
  data,
  label,
}: {
  data: KnowledgeStatus;
  label: (name: string) => string;
}) {
  const consolidation = data.consolidation;
  const pending = data.sources_pending || [];
  const failed = data.sources_failed || [];
  const unsupported = data.inbox_unsupported || [];
  const lastAt = formatTime(consolidation.last_at);
  const nextAt = formatTime(consolidation.next_at);
  return (
    <div className="knowledge-details-grid">
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
            <dt>Pages</dt>
            <dd>{count(data.counts.pages)}</dd>
            <dt>Sources</dt>
            <dd>{count(data.counts.sources)}</dd>
            <dt>Journal days</dt>
            <dd>{count(data.counts.journal_days)}</dd>
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

      <Panel title="Consolidation">
        <dl className="knowledge-facts">
          <dt>Last run</dt>
          <dd>{lastAt || "never"}</dd>
          <dt>Next run</dt>
          <dd>{nextAt || "not scheduled"}</dd>
          <dt>Report</dt>
          <dd>{consolidation.report || "—"}</dd>
        </dl>
        {consolidation.last_error ? (
          <div className="alert danger knowledge-inline-alert" role="alert">
            <Ic name="circle-alert" size={15} />
            <span className="grow">
              {"Last consolidation error: " + consolidation.last_error}
            </span>
          </div>
        ) : null}
        <p className="knowledge-note">
          Upload takes Markdown, text, PDF, or HTML files (up to 30 MB each). They land in
          inbox/ and are compiled into wiki pages.
        </p>
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
              <SourceRow key={item.path} icon="file-x" path={item.path} error={item.error} />
            ))}
          </div>
        )}
        <h3 className="knowledge-subtitle">
          Unsupported inbox files <span className="cnt">{unsupported.length}</span>
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
    </div>
  );
}

function lastRunLine(data: KnowledgeStatus): string {
  const consolidation = data.consolidation;
  if (consolidation.running) {
    return "Consolidation running…";
  }
  const last = Number(consolidation.last_at) || 0;
  if (!last) {
    return "No nightly run yet";
  }
  const date = new Date(last);
  const today = new Date();
  const sameDay = date.toDateString() === today.toDateString();
  const time = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  return (
    "Last consolidation " +
    (sameDay ? "today " + time : date.toLocaleDateString(undefined, { day: "numeric", month: "short" }) + " " + time)
  );
}

export function KnowledgePage({
  params = {},
}: {
  params?: Record<string, string>;
} = {}) {
  const [status, setStatus] = useState<"loading" | "ready" | "error">(
    "loading"
  );
  const [data, setData] = useState<KnowledgeStatus | null>(null);
  const [error, setError] = useState("");
  const [details, setDetails] = useState<unknown[]>([]);

  const [uploading, setUploading] = useState(false);
  const [starting, setStarting] = useState(false);
  const [feedback, setFeedback] = useState<Feedback | null>(null);

  const [reloadToken, setReloadToken] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const label = useAgentLabel();

  const tab: TabKey = TAB_KEYS.includes(params.tab as TabKey)
    ? (params.tab as TabKey)
    : params.page || params.open
      ? "pages"
      : "graph";

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

  function reload() {
    setReloadToken((value) => value + 1);
  }

  function selectTab(next: string) {
    const keep: Record<string, string | undefined> = { tab: next };
    if (params.scope) keep.scope = params.scope;
    window.location.hash = knowledgeHref(keep);
  }

  /* Upload starts as soon as files are picked (API-KNU-005). */
  async function onPick(event: ChangeEvent<HTMLInputElement>) {
    const files = event.target.files ? Array.from(event.target.files) : [];
    if (files.length === 0 || uploading) {
      return;
    }
    setUploading(true);
    setFeedback(null);
    try {
      const result = await uploadKnowledge(files);
      const saved = result && Array.isArray(result.saved) ? result.saved : [];
      setFeedback({
        tone: "ok",
        message:
          "Saved " +
          saved.length +
          (saved.length === 1 ? " file" : " files") +
          (saved.length ? ": " + saved.join(", ") : "."),
      });
      reload();
    } catch (failure) {
      setFeedback({ tone: "warn", message: errorMessage(failure) });
    } finally {
      setUploading(false);
      if (inputRef.current) {
        inputRef.current.value = "";
      }
    }
  }

  async function runNow() {
    if (starting || running) {
      return;
    }
    setStarting(true);
    setFeedback(null);
    try {
      const result = await consolidateKnowledge();
      setFeedback({
        tone: "ok",
        message:
          result && result.started
            ? "Consolidation started. The status refreshes on its own."
            : "Consolidation is already scheduled.",
      });
      reload();
    } catch (failure) {
      if (isKnowledgeBusy(failure)) {
        setFeedback({ tone: "warn", message: "A consolidation is already running" });
        reload();
      } else if (isKnowledgeUnavailable(failure)) {
        setFeedback({
          tone: "warn",
          message:
            "The knowledge service is not running. Consolidation runs only in the daemon.",
        });
      } else {
        setFeedback({ tone: "warn", message: errorMessage(failure) });
      }
    } finally {
      setStarting(false);
    }
  }

  const subtitle = "Everything your agents have learned, drawn as a map.";

  if (!data) {
    return (
      <main id="main" className="page knowledge-page">
        <header className="ph">
          <div>
            <h1>Knowledge</h1>
            <p>{subtitle}</p>
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
            <p>{subtitle}</p>
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

  const scopes =
    data.scopes && data.scopes.length > 0
      ? data.scopes
      : [
          {
            scope: "shared",
            pages: data.counts.pages,
            journal_days: data.counts.journal_days,
            updated_at: 0,
          },
        ];
  const pending = Number(data.review_pending) || 0;
  const defaultAgent = data.agent || "";

  return (
    <main id="main" className={"page knowledge-page kg-page kg-on-" + tab}>
      <header className="ph">
        <div>
          <h1>Knowledge</h1>
          <p>{subtitle}</p>
        </div>
        <div className="acts">
          <span className="kg-lastrun">{lastRunLine(data)}</span>
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
            variant="secondary"
            icon="upload"
            loading={uploading}
            disabled={uploading}
            onClick={() => inputRef.current?.click()}
          >
            {uploading ? "Uploading…" : "Upload"}
          </Button>
          <Button
            variant="secondary"
            icon="play"
            disabled={starting || running}
            loading={starting}
            onClick={runNow}
          >
            {running ? "Running…" : "Run now"}
          </Button>
        </div>
      </header>

      {feedback ? <Alert tone={feedback.tone}>{feedback.message}</Alert> : null}

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

      <Tabs
        className="kg-tabs"
        aria-label="Knowledge views"
        value={tab}
        onChange={selectTab}
        items={[
          { value: "graph", label: "Graph", icon: "share-2" },
          { value: "pages", label: "Pages", count: data.counts.pages },
          {
            value: "review",
            label: (
              <>
                Review
                {pending > 0 ? <span className="kg-badge-red">{pending}</span> : null}
              </>
            ),
          },
          { value: "activity", label: "Activity" },
        ]}
      />

      {tab === "graph" ? (
        <GraphTab
          scopes={scopes}
          initialScope={params.scope}
          initialPage={params.page}
          defaultAgent={defaultAgent}
        />
      ) : tab === "pages" ? (
        <PagesTab
          scopes={scopes}
          initialScope={params.scope}
          page={params.page}
          open={params.open}
          defaultAgent={defaultAgent}
        />
      ) : tab === "review" ? (
        <ReviewTab onChanged={() => void fetchStatus(false)} />
      ) : (
        <ActivityTab status={data} details={<KnowledgeDetails data={data} label={label} />} />
      )}
    </main>
  );
}
