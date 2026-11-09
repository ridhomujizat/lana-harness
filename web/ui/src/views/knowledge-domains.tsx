import { useCallback, useEffect, useState } from "react";
import { errorMessage } from "../api/client";
import {
  approveReview,
  getKnowledgePage,
  getKnowledgePages,
  getReview,
  rejectReview,
} from "../api/knowledge";
import type {
  KnowledgePageDoc,
  KnowledgePageInfo,
  KnowledgeScopeStat,
  ReviewItem,
} from "../api/knowledge";
import { Markdown } from "../lib/markdown";
import { Ic } from "../ui/icons";
import {
  Badge,
  Button,
  Drawer,
  EmptyState,
  Panel,
  SearchInput,
  Seg,
  Skeleton,
} from "../ui/gr8r";
import { AgentName } from "./agent-avatar";
import { useAgentLabel } from "./agent-label";

/* Spec 46 REQ-DKN-060, -061, -070, -071: the review queue of proposals and
   learned skills, and the vault page list with a scope filter (shared or one
   agent's domain) and a page viewer. */

const SEARCH_DELAY_MS = 250;

function formatTime(ms: number): string {
  return ms > 0 ? new Date(ms).toLocaleString() : "";
}

/* ------------------------------------------------------------------ */
/* Review queue                                                       */
/* ------------------------------------------------------------------ */

function ReviewCard({
  item,
  busy,
  onDecide,
}: {
  item: ReviewItem;
  busy: boolean;
  onDecide: (item: ReviewItem, approve: boolean) => void;
}) {
  const label = useAgentLabel();
  const [open, setOpen] = useState(false);
  const skill = item.kind === "skill";
  return (
    <li className="review-card" data-testid="review-item">
      <div className="review-head">
        <Badge tone={skill ? "violet" : "accent"}>{skill ? "Skill" : "Knowledge"}</Badge>
        <span className="review-title trunc">{item.title}</span>
        <span className="review-action">{item.action}</span>
      </div>
      <div className="review-meta">
        <AgentName name={item.agent} label={label(item.agent)} />
        {item.from && item.from !== item.agent ? (
          <span className="review-from">
            <Ic name="arrow-left" size={12} /> from{" "}
            <AgentName name={item.from} label={label(item.from)} />
          </span>
        ) : null}
        {item.owner_turn ? null : (
          <Badge tone="amber" dot title="This did not come from a turn you started">
            Needs review
          </Badge>
        )}
        <span className="review-time">{formatTime(item.created_at)}</span>
      </div>
      {item.description ? <p className="review-desc">{item.description}</p> : null}
      <button
        type="button"
        className="review-toggle"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <Ic name={open ? "chevron-down" : "chevron-right"} size={13} />
        {open ? "Hide change" : "Show change"}
      </button>
      {open ? <pre className="review-body">{item.body}</pre> : null}
      <div className="review-acts">
        <Button
          variant="secondary"
          size="sm"
          icon="x"
          disabled={busy}
          onClick={() => onDecide(item, false)}
        >
          Reject
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="check"
          disabled={busy}
          loading={busy}
          onClick={() => onDecide(item, true)}
        >
          Approve
        </Button>
      </div>
    </li>
  );
}

export function ReviewPanel({ onChanged }: { onChanged?: () => void }) {
  const [items, setItems] = useState<ReviewItem[] | null>(null);
  const [error, setError] = useState("");
  const [busyId, setBusyId] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    try {
      setItems(await getReview());
      setError("");
    } catch (failure) {
      setError(errorMessage(failure));
      setItems((current) => current || []);
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the fetch is async; state lands after the await.
    void load();
  }, [load]);

  async function decide(item: ReviewItem, approve: boolean) {
    if (busyId) {
      return;
    }
    setBusyId(item.id);
    setNotice("");
    try {
      const result = approve ? await approveReview(item.id) : await rejectReview(item.id);
      setNotice(
        result.status === "rejected"
          ? "Rejected " + item.title + "."
          : result.status === "applied"
            ? "Applied " + item.title + "."
            : "Approved " + item.title + ". " + item.agent + " applies it at its next promotion."
      );
      await load();
      onChanged?.();
    } catch (failure) {
      setNotice(errorMessage(failure));
    } finally {
      setBusyId("");
    }
  }

  if (items === null) {
    return null;
  }
  if (items.length === 0 && !error && !notice) {
    return null;
  }
  return (
    <Panel
      title={
        <>
          Review <span className="cnt">{items.length}</span>
        </>
      }
      className="review-panel"
    >
      <p className="knowledge-note">
        Agents propose changes to other agents&apos; knowledge and new skills. Approve what
        looks right; nothing here is applied until you or the owning agent take it.
      </p>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : null}
      {notice ? (
        <div className="alert ok" role="status" aria-live="polite">
          <Ic name="circle-check" size={15} />
          <span className="grow">{notice}</span>
        </div>
      ) : null}
      {items.length === 0 ? (
        <p className="knowledge-empty">Nothing left to review.</p>
      ) : (
        <ul className="review-list">
          {items.map((item) => (
            <ReviewCard
              key={item.id}
              item={item}
              busy={busyId === item.id}
              onDecide={decide}
            />
          ))}
        </ul>
      )}
    </Panel>
  );
}

/* ------------------------------------------------------------------ */
/* Pages                                                              */
/* ------------------------------------------------------------------ */

function PageViewer({ path, onClose }: { path: string; onClose: () => void }) {
  const [doc, setDoc] = useState<KnowledgePageDoc | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let alive = true;
    getKnowledgePage(path)
      .then((result) => {
        if (alive) {
          setDoc(result);
          setError("");
        }
      })
      .catch((failure) => {
        if (alive) {
          setError(errorMessage(failure));
        }
      });
    return () => {
      alive = false;
    };
  }, [path]);

  return (
    <Drawer
      title={doc ? doc.title : "Page"}
      onClose={onClose}
      className="knowledge-viewer"
      header={
        <div className="knowledge-viewer-h">
          <h3 className="trunc">{doc ? doc.title : "Loading…"}</h3>
          <code className="mono knowledge-path trunc">{path}</code>
        </div>
      }
    >
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : doc ? (
        <div className="knowledge-report knowledge-viewer-body">
          <Markdown text={doc.markdown} />
        </div>
      ) : (
        <div className="knowledge-loading" aria-busy="true">
          <Skeleton width="50%" height={14} />
          <Skeleton width="90%" height={12} />
          <Skeleton width="80%" height={12} />
        </div>
      )}
    </Drawer>
  );
}

function scopeLabel(scope: string, label: (name: string) => string): string {
  return scope === "shared" ? "Shared" : label(scope);
}

export function PagesPanel({
  scopes,
  initialScope,
}: {
  scopes: KnowledgeScopeStat[];
  initialScope?: string;
}) {
  const label = useAgentLabel();
  const [scope, setScope] = useState(initialScope || "all");
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  const [pages, setPages] = useState<KnowledgePageInfo[] | null>(null);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState("");
  const [openPath, setOpenPath] = useState("");

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), SEARCH_DELAY_MS);
    return () => clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    let alive = true;
    getKnowledgePages(scope, debounced)
      .then((result) => {
        if (!alive) {
          return;
        }
        setPages(Array.isArray(result.pages) ? result.pages : []);
        setTotal(Number(result.total) || 0);
        setError("");
      })
      .catch((failure) => {
        if (!alive) {
          return;
        }
        setPages([]);
        setTotal(0);
        setError(errorMessage(failure));
      });
    return () => {
      alive = false;
    };
  }, [scope, debounced]);

  const all = scopes.reduce((sum, item) => sum + (item.pages || 0), 0);
  const options = [
    { value: "all", label: "All", count: all },
    ...scopes.map((item) => ({
      value: item.scope,
      label: scopeLabel(item.scope, label),
      count: item.pages,
    })),
  ];
  const closeViewer = useCallback(() => setOpenPath(""), []);

  return (
    <Panel title="Pages" className="knowledge-pages">
      <div className="knowledge-pages-bar">
        <Seg
          options={options}
          value={scope}
          onChange={setScope}
          aria-label="Knowledge scope"
          className="knowledge-scopes"
        />
        <SearchInput
          size="sm"
          kbd={null}
          placeholder="Filter pages"
          aria-label="Filter pages"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          wrapClassName="knowledge-pages-search"
        />
      </div>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : null}
      {pages === null ? (
        <div className="knowledge-loading" aria-busy="true">
          <span className="sr">Loading pages…</span>
          <Skeleton width="100%" height={36} radius={8} />
          <Skeleton width="100%" height={36} radius={8} />
          <Skeleton width="100%" height={36} radius={8} />
        </div>
      ) : pages.length === 0 ? (
        error ? null : (
          <EmptyState
            icon="book-open"
            title={debounced ? "No pages match" : "No pages yet"}
            text={
              scope !== "all" && scope !== "shared"
                ? "This agent's domain fills up as it learns from its conversations."
                : "Pages appear as agents learn and sources are compiled."
            }
          />
        )
      ) : (
        <>
          <ul className="knowledge-page-list">
            {pages.map((page) => (
              <li key={page.path}>
                <button
                  type="button"
                  className="knowledge-page-row"
                  onClick={() => setOpenPath(page.path)}
                >
                  <span className="knowledge-tile" aria-hidden="true">
                    <Ic name="file-text" size={15} />
                  </span>
                  <span className="knowledge-page-main">
                    <span className="knowledge-page-title trunc">{page.title}</span>
                    {page.description ? (
                      <span className="knowledge-page-desc trunc">{page.description}</span>
                    ) : null}
                  </span>
                  {page.type ? <Badge tone="gray">{page.type}</Badge> : null}
                  {page.scope === "shared" ? (
                    <Badge tone="gray">Shared</Badge>
                  ) : (
                    <AgentName name={page.scope} label={label(page.scope)} />
                  )}
                  <span className="knowledge-page-date">{page.updated}</span>
                </button>
              </li>
            ))}
          </ul>
          {total > pages.length ? (
            <p className="knowledge-note">
              Showing {pages.length} of {total}. Filter to narrow the list.
            </p>
          ) : null}
        </>
      )}
      {openPath ? <PageViewer path={openPath} onClose={closeViewer} /> : null}
    </Panel>
  );
}
