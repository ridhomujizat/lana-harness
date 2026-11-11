import { useEffect, useMemo, useState } from "react";
import { errorMessage } from "../api/client";
import {
  getKnowledgeGraph,
  getKnowledgePageDetail,
  getKnowledgePages,
} from "../api/knowledge";
import type {
  KnowledgePageDetail,
  KnowledgePageInfo,
  KnowledgeScopeStat,
} from "../api/knowledge";
import { Markdown } from "../lib/markdown";
import { Ic } from "../ui/icons";
import { Button, EmptyState, SearchInput, Select, Skeleton } from "../ui/gr8r";
import { useAgentLabel } from "./agent-label";
import { ScopeChips } from "./knowledge-graph";
import {
  askHref,
  knowledgeHref,
  makeScopeColors,
  pageBody,
  pageHref,
  ScopeDot,
  scopeLabel,
  shortDate,
} from "./knowledge-shared";
import type { ScopeColors } from "./knowledge-shared";

/* Spec 48 REQ-KGR-053: the page list and a reader. [[links]] in a page open
   the linked page in the reader. */

const SEARCH_DELAY_MS = 250;

type SortKey = "updated" | "title" | "links";

function sortPages(
  pages: KnowledgePageInfo[],
  sort: SortKey,
  degrees: Map<string, number> | null
): KnowledgePageInfo[] {
  const list = pages.slice();
  if (sort === "title") {
    list.sort((a, b) => a.title.localeCompare(b.title));
  } else if (sort === "links" && degrees) {
    list.sort((a, b) => (degrees.get(b.path) || 0) - (degrees.get(a.path) || 0));
  } else {
    list.sort((a, b) => (b.mtime || 0) - (a.mtime || 0) || b.updated.localeCompare(a.updated));
  }
  return list;
}

/* The target of a [[link]] as the reader resolves it: through the page's
   resolved links first (API-KGR-002), then as a vault path. */
function resolveTarget(doc: KnowledgePageDetail, target: string): string {
  const needle = target.trim().toLowerCase();
  for (const link of doc.links_out || []) {
    const path = link.path.toLowerCase();
    if (
      link.title.toLowerCase() === needle ||
      path === needle ||
      path === needle + ".md"
    ) {
      return link.path;
    }
  }
  return "";
}

export function PageReader({
  path,
  colors,
  defaultAgent,
}: {
  path: string;
  colors: ScopeColors;
  defaultAgent: string;
}) {
  const label = useAgentLabel();
  const [doc, setDoc] = useState<KnowledgePageDetail | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let alive = true;
    getKnowledgePageDetail(path)
      .then((result) => {
        if (alive) {
          setDoc(result);
          setError("");
        }
      })
      .catch((failure) => {
        if (alive) {
          setDoc(null);
          setError(errorMessage(failure));
        }
      });
    return () => {
      alive = false;
    };
  }, [path]);

  if (error) {
    return (
      <div className="kg-reader">
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      </div>
    );
  }
  if (!doc) {
    return (
      <div className="kg-reader knowledge-loading" aria-busy="true">
        <span className="sr">Loading page…</span>
        <Skeleton width="40%" height={12} />
        <Skeleton width="60%" height={26} />
        <Skeleton width="100%" height={80} radius={8} />
      </div>
    );
  }
  const scope = doc.scope;
  const agent = scope && scope !== "shared" ? scope : defaultAgent;
  const incoming = doc.links_in || [];
  const renderLink = (target: string, text: string) => {
    const resolved = resolveTarget(doc, target);
    if (!resolved) {
      return (
        <span className="kg-wikilink is-broken" title="No page with this title yet">
          {text}
        </span>
      );
    }
    return (
      <a className="kg-wikilink" href={pageHref(resolved)}>
        {text}
      </a>
    );
  };
  return (
    <article className="kg-reader" aria-label={doc.title}>
      <div className="kg-reader-top">
        <code className="mono kg-reader-path trunc">{doc.path}</code>
        <div className="kg-reader-acts">
          <Button
            variant="secondary"
            size="sm"
            icon="share-2"
            as="a"
            href={knowledgeHref({ tab: "graph", page: doc.path })}
          >
            Show in graph
          </Button>
          {agent ? (
            <Button variant="secondary" size="sm" as="a" href={askHref(agent, doc.title)}>
              Ask {label(agent) || agent}
            </Button>
          ) : null}
        </div>
      </div>
      <h2 className="kg-reader-title">{doc.title}</h2>
      <div className="kg-reader-meta">
        <span className="kg-scope-chip" style={{ color: colors(scope) }}>
          <ScopeDot color={colors(scope)} />
          <span>{scopeLabel(scope, label)}</span>
        </span>
        {doc.type ? <span className="kg-type-chip">{doc.type}</span> : null}
        <span>
          {doc.updated ? "Updated " + shortDate(doc.updated) : ""}
          {doc.sources && doc.sources.length
            ? (doc.updated ? " · " : "") +
              "learned from " +
              doc.sources.length +
              (doc.sources.length === 1 ? " source" : " sources")
            : ""}
        </span>
      </div>
      <div className="kg-reader-body knowledge-report">
        <Markdown text={pageBody(doc.markdown)} wikiLink={renderLink} />
      </div>
      <section className="kg-backlinks">
        <h3 className="kg-lbl">Linked from · {incoming.length}</h3>
        {incoming.length === 0 ? (
          <p className="kg-none">No other page links here yet.</p>
        ) : (
          <ul>
            {incoming.map((link) => (
              <li key={link.path}>
                <a href={pageHref(link.path)} className="kg-backlink">
                  <span className="kg-backlink-t">
                    <ScopeDot color={colors(link.scope)} />
                    {link.title}
                  </span>
                  {link.context ? <span className="kg-backlink-c">{link.context}</span> : null}
                </a>
              </li>
            ))}
          </ul>
        )}
      </section>
    </article>
  );
}

export function PagesTab({
  scopes,
  initialScope,
  page,
  open,
  defaultAgent,
}: {
  scopes: KnowledgeScopeStat[];
  initialScope?: string;
  page?: string;
  open?: string;
  defaultAgent: string;
}) {
  const label = useAgentLabel();
  const [scope, setScope] = useState(initialScope || "all");
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  const [sort, setSort] = useState<SortKey>("updated");
  const [pages, setPages] = useState<KnowledgePageInfo[] | null>(null);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState("");
  const [degrees, setDegrees] = useState<Map<string, number> | null>(null);
  const [resolvedOpen, setResolvedOpen] = useState("");
  const colors = useMemo(() => makeScopeColors(scopes.map((item) => item.scope)), [scopes]);
  const current = page || resolvedOpen;

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), SEARCH_DELAY_MS);
    return () => clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    let alive = true;
    getKnowledgePages(scope, debounced, 500)
      .then((result) => {
        if (!alive) return;
        setPages(Array.isArray(result.pages) ? result.pages : []);
        setTotal(Number(result.total) || 0);
        setError("");
      })
      .catch((failure) => {
        if (!alive) return;
        setPages([]);
        setTotal(0);
        setError(errorMessage(failure));
      });
    return () => {
      alive = false;
    };
  }, [scope, debounced]);

  /* "Most linked" needs the graph degrees (API-KGR-001). */
  useEffect(() => {
    if (sort !== "links") return undefined;
    let alive = true;
    getKnowledgeGraph(scope)
      .then((graph) => {
        if (alive) setDegrees(new Map(graph.nodes.map((node) => [node.id, node.degree])));
      })
      .catch(() => {
        if (alive) setDegrees(new Map());
      });
    return () => {
      alive = false;
    };
  }, [sort, scope]);

  /* ?open= carries a [[target]] from the log: a path when it has a slash,
     else a title found through the page search. */
  useEffect(() => {
    if (page || !open) return undefined;
    let alive = true;
    const target = open.trim();
    if (target.includes("/")) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- a path needs no lookup.
      setResolvedOpen(target.toLowerCase().endsWith(".md") ? target : target + ".md");
      return undefined;
    }
    getKnowledgePages("all", target, 20)
      .then((result) => {
        if (!alive) return;
        const list = Array.isArray(result.pages) ? result.pages : [];
        const exact = list.find((item) => item.title.toLowerCase() === target.toLowerCase());
        setResolvedOpen((exact || list[0])?.path || "");
      })
      .catch(() => {
        if (alive) setResolvedOpen("");
      });
    return () => {
      alive = false;
    };
  }, [open, page]);

  const sorted = useMemo(
    () => (pages ? sortPages(pages, sort, degrees) : null),
    [pages, sort, degrees]
  );

  return (
    <div className={"kg-tab kg-pages-tab" + (current ? " has-page" : "")}>
      <div className="kg-toolbar">
        <SearchInput
          size="sm"
          kbd={null}
          placeholder="Search pages"
          aria-label="Filter pages"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          wrapClassName="kg-search"
        />
        <ScopeChips scopes={scopes} value={scope} onChange={setScope} colors={colors} />
        <div className="kg-sort">
          <Select
            size="sm"
            aria-label="Sort pages"
            value={sort}
            onChange={(value: string) => setSort(value as SortKey)}
            options={[
              { value: "updated", label: "Recently updated" },
              { value: "title", label: "Title" },
              { value: "links", label: "Most linked" },
            ]}
          />
        </div>
      </div>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : null}
      <div className="kg-pages-grid">
        <div className="kg-list" aria-label="Pages">
          {sorted === null ? (
            <div className="knowledge-loading" aria-busy="true">
              <span className="sr">Loading pages…</span>
              <Skeleton width="100%" height={44} radius={8} />
              <Skeleton width="100%" height={44} radius={8} />
              <Skeleton width="100%" height={44} radius={8} />
            </div>
          ) : sorted.length === 0 ? (
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
              <ul>
                {sorted.map((item) => {
                  const on = item.path === current;
                  return (
                    <li key={item.path}>
                      <a
                        href={pageHref(item.path)}
                        className={"kg-row" + (on ? " on" : "")}
                        aria-current={on ? "page" : undefined}
                      >
                        <ScopeDot color={colors(item.scope)} size={9} />
                        <span className="kg-row-main">
                          <span className="kg-row-title trunc">{item.title}</span>
                          <span className="kg-row-meta trunc">
                            {scopeLabel(item.scope, label)}
                            {item.type ? " · " + item.type : ""}
                            {degrees && degrees.has(item.path)
                              ? " · " + degrees.get(item.path) + " links"
                              : ""}
                          </span>
                        </span>
                        <span className="kg-row-date">{shortDate(item.updated)}</span>
                      </a>
                    </li>
                  );
                })}
              </ul>
              {total > sorted.length ? (
                <p className="knowledge-note">
                  Showing {sorted.length} of {total}. Search to narrow the list.
                </p>
              ) : null}
            </>
          )}
        </div>
        <div className="kg-reader-wrap">
          {current ? (
            <>
              <a className="kg-back" href={knowledgeHref({ tab: "pages" })}>
                <Ic name="arrow-left" size={14} /> All pages
              </a>
              <PageReader key={current} path={current} colors={colors} defaultAgent={defaultAgent} />
            </>
          ) : (
            <div className="kg-reader kg-reader-empty">
              <EmptyState
                icon="file-text"
                title="Pick a page"
                text="Choose a page on the left to read it here, with the pages that link to it."
              />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
