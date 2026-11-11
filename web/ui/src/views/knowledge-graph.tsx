import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { errorMessage } from "../api/client";
import { getKnowledgeGraph, getKnowledgePageDetail } from "../api/knowledge";
import type {
  KnowledgeGraph,
  KnowledgePageDetail,
  KnowledgeScopeStat,
} from "../api/knowledge";
import { Ic } from "../ui/icons";
import { Button, Checkbox, EmptyState, IconButton, SearchInput, Skeleton } from "../ui/gr8r";
import { useAgentLabel } from "./agent-label";
import {
  askHref,
  firstParagraph,
  isSourcePath,
  latestChange,
  makeScopeColors,
  pageHref,
  ScopeDot,
  scopeLabel,
  shortDate,
} from "./knowledge-shared";
import type { ScopeColors } from "./knowledge-shared";

/* Spec 48 REQ-KGR-051, -052: the knowledge map. Agents are hub nodes, pages
   are dots in the agent's colour, [[links]] are lines. Hover or select a node
   to light up its neighbours; selecting a page opens its summary. */

export interface GraphNodeDatum {
  id: string;
  title: string;
  scope: string;
  kind: "hub" | "page" | "journal" | "source";
  degree: number;
  x?: number;
  y?: number;
}

export interface GraphLinkDatum {
  source: string | GraphNodeDatum;
  target: string | GraphNodeDatum;
  hub: boolean;
}

export interface GraphData {
  nodes: GraphNodeDatum[];
  links: GraphLinkDatum[];
  neighbors: Map<string, Set<string>>;
}

export const HUB_PREFIX = "hub:";

/* Hub nodes and hub edges are added on the client (API-KGR-001). */
export function buildGraphData(
  graph: KnowledgeGraph,
  label: (scope: string) => string
): GraphData {
  const nodes: GraphNodeDatum[] = graph.hubs.map((hub) => ({
    id: HUB_PREFIX + hub.scope,
    title: label(hub.scope),
    scope: hub.scope,
    kind: "hub",
    degree: hub.pages,
  }));
  const hubIds = new Set(nodes.map((node) => node.id));
  const ids = new Set<string>();
  for (const node of graph.nodes) {
    ids.add(node.id);
    nodes.push({
      id: node.id,
      title: node.title,
      scope: node.scope,
      kind: node.kind,
      degree: node.degree,
    });
  }
  const links: GraphLinkDatum[] = [];
  const neighbors = new Map<string, Set<string>>();
  const join = (a: string, b: string) => {
    if (!neighbors.has(a)) neighbors.set(a, new Set());
    if (!neighbors.has(b)) neighbors.set(b, new Set());
    neighbors.get(a)?.add(b);
    neighbors.get(b)?.add(a);
  };
  for (const node of graph.nodes) {
    const hub = HUB_PREFIX + node.scope;
    if (hubIds.has(hub)) {
      links.push({ source: hub, target: node.id, hub: true });
      join(hub, node.id);
    }
  }
  for (const edge of graph.edges) {
    if (ids.has(edge.source) && ids.has(edge.target) && edge.source !== edge.target) {
      links.push({ source: edge.source, target: edge.target, hub: false });
      join(edge.source, edge.target);
    }
  }
  return { nodes, links, neighbors };
}

function linkEnd(end: string | GraphNodeDatum): string {
  return typeof end === "string" ? end : end.id;
}

function nodeRadius(node: GraphNodeDatum): number {
  if (node.kind === "hub") return 9;
  if (node.kind === "page") return 2.6 + Math.sqrt(Math.max(0, node.degree)) * 1.3;
  return 2.2;
}

interface Theme {
  text: string;
  text3: string;
  line: string;
  lineStrong: string;
  surface: string;
}

function readTheme(): Theme {
  const style = getComputedStyle(document.documentElement);
  const get = (name: string, fallback: string) =>
    style.getPropertyValue(name).trim() || fallback;
  return {
    text: get("--text", "#1D1C1A"),
    text3: get("--text-3", "#6B6760"),
    line: get("--border", "#E9E7E2"),
    lineStrong: get("--border-strong", "#D6D3CC"),
    surface: get("--surface", "#FFFFFF"),
  };
}

/* The force-graph instance, typed loosely: the module is loaded lazily and
   mocked in tests. */
interface GraphInstance {
  graphData(data: { nodes: GraphNodeDatum[]; links: GraphLinkDatum[] }): GraphInstance;
  width(value: number): GraphInstance;
  height(value: number): GraphInstance;
  zoom(): number;
  zoom(value: number, ms?: number): GraphInstance;
  zoomToFit(ms?: number, padding?: number): GraphInstance;
  centerAt(x?: number, y?: number, ms?: number): GraphInstance;
  nodeCanvasObject(fn: unknown): GraphInstance;
  linkColor(fn: unknown): GraphInstance;
  linkWidth(fn: unknown): GraphInstance;
  _destructor?: () => void;
  [key: string]: unknown;
}

function ScopeChips({
  scopes,
  value,
  onChange,
  colors,
}: {
  scopes: KnowledgeScopeStat[];
  value: string;
  onChange: (scope: string) => void;
  colors: ScopeColors;
}) {
  const label = useAgentLabel();
  const all = scopes.reduce((sum, item) => sum + (item.pages || 0), 0);
  const items = [{ scope: "all", pages: all }, ...scopes];
  return (
    <div className="kg-chips" role="group" aria-label="Knowledge scope">
      {items.map((item) => {
        const on = item.scope === value;
        return (
          <button
            key={item.scope}
            type="button"
            className={"kg-chip" + (on ? " on" : "")}
            aria-pressed={on}
            onClick={() => onChange(item.scope)}
          >
            {item.scope === "all" ? null : <ScopeDot color={colors(item.scope)} />}
            <span>{item.scope === "all" ? "All" : scopeLabel(item.scope, label)}</span>
            <span className="kg-chip-n">{item.pages}</span>
          </button>
        );
      })}
    </div>
  );
}

export { ScopeChips };

/* The summary of one page: the graph side panel on desktop and the bottom
   sheet on phones (REQ-KGR-051). */
export function PageSummary({
  path,
  colors,
  defaultAgent,
  onSelect,
  onClose,
}: {
  path: string;
  colors: ScopeColors;
  defaultAgent: string;
  onSelect: (path: string) => void;
  onClose: () => void;
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

  const scope = doc?.scope || "";
  const agent = scope && scope !== "shared" ? scope : defaultAgent;
  const summary = doc ? doc.description || firstParagraph(doc.markdown) : "";
  const change = doc ? latestChange(doc.markdown) : null;
  const allOut = doc?.links_out || [];
  const out = allOut.filter((link) => !isSourcePath(link.path));
  const incoming = doc?.links_in || [];
  /* Front matter sources are often written as [[path]]. */
  const sources = Array.from(
    new Set([
      ...(doc?.sources || []).map((item) =>
        item.replace(/^\s*\[\[/, "").replace(/\]\]\s*$/, "")
      ),
      ...allOut.filter((link) => isSourcePath(link.path)).map((link) => link.path.replace(/\.md$/, "")),
    ])
  );

  return (
    <aside className="kg-panel" aria-label="Page summary" data-testid="graph-panel">
      <div className="kg-panel-grip" aria-hidden="true" />
      <div className="kg-panel-head">
        {doc ? (
          <>
            <span className="kg-scope-chip" style={{ color: colors(scope) }}>
              <ScopeDot color={colors(scope)} />
              <span>{scopeLabel(scope, label)}</span>
            </span>
            {doc.type ? <span className="kg-type-chip">{doc.type}</span> : null}
          </>
        ) : null}
        <IconButton icon="x" size="sm" label="Close" onClick={onClose} className="kg-panel-x" />
      </div>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : !doc ? (
        <div className="knowledge-loading" aria-busy="true">
          <Skeleton width="70%" height={18} />
          <Skeleton width="45%" height={12} />
          <Skeleton width="100%" height={60} radius={8} />
        </div>
      ) : (
        <div className="kg-panel-body">
          <h2 className="kg-panel-title">{doc.title}</h2>
          <p className="kg-panel-meta">
            {doc.updated ? "Updated " + shortDate(doc.updated) : doc.path}
            {sources.length ? " · from " + sources.length + (sources.length === 1 ? " source" : " sources") : ""}
          </p>
          {summary ? (
            <section className="kg-sec">
              <h3 className="kg-lbl">Summary</h3>
              <p className="kg-summary">{summary}</p>
            </section>
          ) : null}
          {change ? (
            <div className="kg-change">
              <span className="kg-change-h">Changed {change.since}</span>
              <span className="kg-change-l">
                <del>{change.old}</del>
                <Ic name="arrow-right" size={13} />
                <span>{change.current}</span>
              </span>
            </div>
          ) : null}
          <section className="kg-sec">
            <h3 className="kg-lbl">Links to · {out.length}</h3>
            {out.length === 0 ? (
              <p className="kg-none">No links yet.</p>
            ) : (
              <ul className="kg-links">
                {out.map((link) => (
                  <li key={link.path}>
                    <button type="button" onClick={() => onSelect(link.path)}>
                      <ScopeDot color={colors(link.scope)} />
                      <span className="trunc">{link.title}</span>
                      <span className="kg-links-scope">{link.scope}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
          <section className="kg-sec">
            <h3 className="kg-lbl">Linked from · {incoming.length}</h3>
            {incoming.length === 0 ? (
              <p className="kg-none">No page links here yet.</p>
            ) : (
              <ul className="kg-links">
                {incoming.map((link) => (
                  <li key={link.path}>
                    <button type="button" onClick={() => onSelect(link.path)} title={link.context}>
                      <ScopeDot color={colors(link.scope)} />
                      <span className="trunc">{link.title}</span>
                      <span className="kg-links-scope">{link.scope}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
          {sources.length ? (
            <section className="kg-sec">
              <h3 className="kg-lbl">Learned from</h3>
              <p className="kg-summary">{sources.join(", ")}</p>
            </section>
          ) : null}
        </div>
      )}
      {doc ? (
        <div className="kg-panel-acts">
          <Button variant="primary" as="a" href={pageHref(doc.path)}>
            Open page
          </Button>
          {agent ? (
            <Button variant="secondary" as="a" href={askHref(agent, doc.title)}>
              Ask {label(agent) || agent}
            </Button>
          ) : null}
        </div>
      ) : null}
    </aside>
  );
}

export function GraphTab({
  scopes,
  initialScope,
  initialPage,
  defaultAgent,
}: {
  scopes: KnowledgeScopeStat[];
  initialScope?: string;
  initialPage?: string;
  defaultAgent: string;
}) {
  const label = useAgentLabel();
  const [scope, setScope] = useState(initialScope || "all");
  const [journals, setJournals] = useState(false);
  const [sources, setSources] = useState(false);
  const [graph, setGraph] = useState<KnowledgeGraph | null>(null);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState(initialPage || "");
  const [hovered, setHovered] = useState("");
  const [query, setQuery] = useState("");
  const [notFound, setNotFound] = useState(false);
  const [ready, setReady] = useState(false);

  const colors = useMemo(
    () => makeScopeColors(scopes.map((item) => item.scope)),
    [scopes]
  );
  const labelOf = useCallback((s: string) => scopeLabel(s, label), [label]);

  useEffect(() => {
    let alive = true;
    getKnowledgeGraph(scope, journals, sources)
      .then((result) => {
        if (alive) {
          setGraph(result);
          setError("");
        }
      })
      .catch((failure) => {
        if (alive) {
          setError(errorMessage(failure));
          setGraph((current) => current || null);
        }
      });
    return () => {
      alive = false;
    };
  }, [scope, journals, sources]);

  const data = useMemo(
    () => (graph ? buildGraphData(graph, labelOf) : null),
    [graph, labelOf]
  );

  /* Refs read by the canvas painters, so a hover does not rebuild the graph. */
  const containerRef = useRef<HTMLDivElement>(null);
  const instanceRef = useRef<GraphInstance | null>(null);
  const focusRef = useRef("");
  const selectedRef = useRef("");
  const dataRef = useRef<GraphData | null>(null);
  const themeRef = useRef<Theme | null>(null);
  const colorsRef = useRef<ScopeColors>(colors);
  const fittedRef = useRef(false);
  /* Layout effects run before the redraw effect below, so the painters see
     the current state. */
  useLayoutEffect(() => {
    focusRef.current = hovered || selected;
    selectedRef.current = selected;
    dataRef.current = data;
    colorsRef.current = colors;
  });

  const isLit = useCallback((id: string): boolean => {
    const focus = focusRef.current;
    if (!focus) return true;
    if (id === focus) return true;
    return Boolean(dataRef.current?.neighbors.get(focus)?.has(id));
  }, []);

  const paintNode = useCallback(
    (node: GraphNodeDatum, ctx: CanvasRenderingContext2D, scale: number) => {
      const theme = themeRef.current || readTheme();
      const color = colorsRef.current(node.scope);
      const r = nodeRadius(node);
      const lit = isLit(node.id);
      const x = node.x || 0;
      const y = node.y || 0;
      ctx.globalAlpha = lit ? 1 : 0.2;
      if (node.kind === "hub") {
        ctx.beginPath();
        ctx.arc(x, y, r * 1.5, 0, 2 * Math.PI);
        ctx.fillStyle = color + "26";
        ctx.fill();
      }
      ctx.beginPath();
      if (node.kind === "source") {
        ctx.rect(x - r, y - r, r * 2, r * 2);
      } else {
        ctx.arc(x, y, r, 0, 2 * Math.PI);
      }
      if (node.kind === "journal") {
        ctx.lineWidth = 1 / scale + 0.6;
        ctx.strokeStyle = color;
        ctx.stroke();
      } else {
        ctx.fillStyle = color;
        ctx.fill();
      }
      if (node.id === selectedRef.current) {
        ctx.beginPath();
        ctx.arc(x, y, r + 3, 0, 2 * Math.PI);
        ctx.lineWidth = 1.6;
        ctx.strokeStyle = color;
        ctx.stroke();
      }
      if (node.kind === "hub") {
        ctx.fillStyle = "#FFFFFF";
        ctx.font = "600 " + r + "px Geist, Inter, sans-serif";
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        ctx.fillText((node.title || node.scope).slice(0, 1).toUpperCase(), x, y + 0.5);
      }
      const focus = focusRef.current;
      const near = Boolean(focus) && lit;
      const showLabel =
        node.kind === "hub" || node.id === focus || near || r * scale > 7;
      if (showLabel) {
        const size = (node.kind === "hub" ? 12.5 : node.id === focus ? 12 : 11) / scale;
        const weight = node.kind === "hub" || node.id === focus ? "600 " : "400 ";
        ctx.font = weight + size + "px Geist, Inter, sans-serif";
        ctx.textAlign = "center";
        ctx.textBaseline = "top";
        const ty = y + r * (node.kind === "hub" ? 1.5 : 1) + 2 / scale;
        if (node.id === focus) {
          const width = ctx.measureText(node.title).width + 8 / scale;
          ctx.fillStyle = theme.surface;
          ctx.fillRect(x - width / 2, ty - 1 / scale, width, size + 4 / scale);
        }
        ctx.fillStyle = node.kind === "hub" || node.id === focus ? theme.text : theme.text3;
        ctx.fillText(node.title, x, ty);
      }
      ctx.globalAlpha = 1;
    },
    [isLit]
  );

  const paintArea = useCallback(
    (node: GraphNodeDatum, color: string, ctx: CanvasRenderingContext2D) => {
      ctx.fillStyle = color;
      ctx.beginPath();
      ctx.arc(node.x || 0, node.y || 0, nodeRadius(node) + 3, 0, 2 * Math.PI);
      ctx.fill();
    },
    []
  );

  const linkColor = useCallback((link: GraphLinkDatum) => {
    const theme = themeRef.current || readTheme();
    const focus = focusRef.current;
    const a = linkEnd(link.source);
    const b = linkEnd(link.target);
    if (focus && (a === focus || b === focus)) {
      const node = dataRef.current?.nodes.find((item) => item.id === focus);
      return colorsRef.current(node ? node.scope : "shared");
    }
    const base = link.hub ? theme.line : theme.lineStrong;
    return focus ? base + "55" : base;
  }, []);

  const linkWidth = useCallback((link: GraphLinkDatum) => {
    const focus = focusRef.current;
    const a = linkEnd(link.source);
    const b = linkEnd(link.target);
    return focus && (a === focus || b === focus) ? 1.8 : link.hub ? 0.6 : 1;
  }, []);

  const redraw = useCallback(() => {
    const instance = instanceRef.current;
    if (instance) {
      instance.nodeCanvasObject(paintNode);
      instance.linkColor(linkColor);
      instance.linkWidth(linkWidth);
    }
  }, [paintNode, linkColor, linkWidth]);

  /* Create the canvas once; force-graph is loaded on demand (design §4). */
  useEffect(() => {
    let alive = true;
    let observer: ResizeObserver | null = null;
    let themeWatch: MutationObserver | null = null;
    const element = containerRef.current;
    if (!element) return undefined;
    themeRef.current = readTheme();
    import("force-graph")
      .then((module) => {
        if (!alive || !element) return;
        const ForceGraph = module.default as unknown as new (el: HTMLElement) => GraphInstance;
        const instance = new ForceGraph(element);
        const methods = instance as unknown as Record<string, ((...args: unknown[]) => unknown) | undefined>;
        const call = (name: string, ...args: unknown[]): unknown => methods[name]?.(...args);
        call("nodeId", "id");
        call("nodeLabel", () => "");
        call("nodeCanvasObject", paintNode);
        call("nodePointerAreaPaint", paintArea);
        call("linkColor", linkColor);
        call("linkWidth", linkWidth);
        call("cooldownTicks", 160);
        call("onNodeHover", (node: GraphNodeDatum | null) => {
          setHovered(node ? node.id : "");
          element.style.cursor = node ? "pointer" : "";
        });
        call("onNodeClick", (node: GraphNodeDatum) => {
          if (node.kind === "hub") {
            setScope(node.scope);
            setSelected("");
          } else {
            setSelected(node.id);
          }
        });
        call("onBackgroundClick", () => setSelected(""));
        call("onEngineStop", () => {
          if (!fittedRef.current) {
            fittedRef.current = true;
            instance.zoomToFit(400, 48);
          }
        });
        const charge = (call("d3Force", "charge") as { strength?: (v: number) => void } | undefined);
        charge?.strength?.(-60);
        /* A gentle pull to the centre keeps separate clusters (an agent with
           no links to the others) on screen. */
        let pulled: GraphNodeDatum[] = [];
        const gravity = Object.assign(
          (alpha: number) => {
            for (const node of pulled) {
              const item = node as GraphNodeDatum & { vx?: number; vy?: number };
              item.vx = (item.vx || 0) - (item.x || 0) * 0.06 * alpha;
              item.vy = (item.vy || 0) - (item.y || 0) * 0.06 * alpha;
            }
          },
          { initialize: (nodes: GraphNodeDatum[]) => { pulled = nodes; } }
        );
        call("d3Force", "gravity", gravity);
        instanceRef.current = instance;
        const resize = () => {
          const rect = element.getBoundingClientRect();
          instance.width(Math.max(200, rect.width));
          instance.height(Math.max(240, rect.height));
        };
        resize();
        if (typeof ResizeObserver !== "undefined") {
          observer = new ResizeObserver(resize);
          observer.observe(element);
        }
        if (typeof MutationObserver !== "undefined") {
          themeWatch = new MutationObserver(() => {
            themeRef.current = readTheme();
          });
          themeWatch.observe(document.documentElement, {
            attributes: true,
            attributeFilter: ["data-theme", "class"],
          });
        }
        if (dataRef.current) {
          instance.graphData({
            nodes: dataRef.current.nodes,
            links: dataRef.current.links,
          });
        }
        setReady(true);
      })
      .catch((failure) => {
        if (alive) setError(errorMessage(failure));
      });
    return () => {
      alive = false;
      observer?.disconnect();
      themeWatch?.disconnect();
      const instance = instanceRef.current;
      instanceRef.current = null;
      instance?._destructor?.();
      element.innerHTML = "";
    };
    // The painters read refs; the instance is created once per mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /* New data: hand it to the canvas and fit once the layout settles. */
  useEffect(() => {
    const instance = instanceRef.current;
    if (instance && data) {
      fittedRef.current = false;
      instance.graphData({ nodes: data.nodes, links: data.links });
    }
  }, [data, ready]);

  useEffect(() => {
    redraw();
  }, [hovered, selected, redraw]);

  function focusNode(id: string) {
    const instance = instanceRef.current;
    const node = data?.nodes.find((item) => item.id === id);
    setSelected(id);
    if (instance && node && node.x !== undefined && node.y !== undefined) {
      instance.centerAt(node.x, node.y, 500);
      instance.zoom(Math.max(2.2, instance.zoom()), 500);
    }
  }

  function search(text: string) {
    const needle = text.trim().toLowerCase();
    if (!needle || !data) {
      setNotFound(false);
      return;
    }
    const match =
      data.nodes.find((node) => node.kind !== "hub" && node.title.toLowerCase() === needle) ||
      data.nodes.find((node) => node.kind !== "hub" && node.title.toLowerCase().includes(needle));
    setNotFound(!match);
    if (match) {
      focusNode(match.id);
    }
  }

  function onSearchKey(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter") {
      event.preventDefault();
      search(query);
    }
  }

  function zoomBy(factor: number) {
    const instance = instanceRef.current;
    if (instance) instance.zoom(instance.zoom() * factor, 250);
  }

  const counts = graph?.counts;
  const hubs = graph?.hubs || [];
  const empty = graph !== null && graph.nodes.length === 0;
  const few = graph !== null && graph.nodes.length > 0 && (counts?.pages || 0) < 3;

  return (
    <div className="kg-tab kg-graph-tab">
      <div className="kg-toolbar">
        <SearchInput
          size="sm"
          kbd={null}
          placeholder="Search pages"
          aria-label="Search pages in the graph"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setNotFound(false);
          }}
          onKeyDown={onSearchKey}
          wrapClassName="kg-search"
        />
        <ScopeChips
          scopes={scopes}
          value={scope}
          onChange={(value) => {
            setScope(value);
            setSelected("");
          }}
          colors={colors}
        />
        <div className="kg-toggles">
          <label className="kg-toggle">
            <Checkbox checked={journals} onChange={setJournals} aria-label="Show journals" />
            <span>Journals</span>
          </label>
          <label className="kg-toggle">
            <Checkbox checked={sources} onChange={setSources} aria-label="Show sources" />
            <span>Sources</span>
          </label>
        </div>
      </div>
      {error ? (
        <div className="alert danger" role="alert">
          <Ic name="circle-alert" size={15} />
          <span className="grow">{error}</span>
        </div>
      ) : null}
      <div className={"kg-stage" + (selected ? " has-panel" : "")}>
        <div className="kg-canvas-wrap">
          <div ref={containerRef} className="kg-canvas" data-testid="graph-canvas" />
          {graph === null && !error ? (
            <div className="kg-overlay-center" aria-busy="true">
              <span className="sr">Loading graph…</span>
              <Skeleton width={160} height={12} />
            </div>
          ) : null}
          {empty ? (
            <div className="kg-overlay-center">
              <EmptyState
                icon="share-2"
                title="No pages yet"
                text="Pages appear as agents learn from their chats. The map fills in after the nightly run."
              />
            </div>
          ) : null}
          <p className="kg-hint">
            <Ic name="info" size={13} />
            Each dot is a page an agent wrote. Lines are [[links]] between pages.
          </p>
          {counts ? (
            <p className="kg-counts">
              {counts.pages} {counts.pages === 1 ? "page" : "pages"} · {counts.links}{" "}
              {counts.links === 1 ? "link" : "links"}
              {counts.broken ? " · " + counts.broken + " broken" : ""}
              {graph?.truncated ? " · showing the most linked" : ""}
            </p>
          ) : null}
          {few ? (
            <p className="kg-few">
              The map grows as agents learn. {counts?.pages} {counts?.pages === 1 ? "page" : "pages"} so far.
            </p>
          ) : null}
          {notFound ? <p className="kg-few">No page matches “{query}”.</p> : null}
          {hubs.length ? (
            <div className="kg-legend" aria-label="Legend">
              {hubs.map((hub) => (
                <span key={hub.scope}>
                  <ScopeDot color={colors(hub.scope)} />
                  {scopeLabel(hub.scope, label)}
                </span>
              ))}
              {journals ? (
                <span>
                  <span className="kg-dot kg-dot-ring" aria-hidden="true" />
                  journal
                </span>
              ) : null}
              {sources ? (
                <span>
                  <span className="kg-dot kg-dot-square" aria-hidden="true" />
                  source
                </span>
              ) : null}
            </div>
          ) : null}
          <div className="kg-zoom" role="group" aria-label="Zoom">
            <IconButton icon="plus" size="sm" label="Zoom in" onClick={() => zoomBy(1.4)} />
            <IconButton icon="minus" size="sm" label="Zoom out" onClick={() => zoomBy(1 / 1.4)} />
            <IconButton
              icon="maximize-2"
              size="sm"
              label="Fit to screen"
              onClick={() => instanceRef.current?.zoomToFit(400, 48)}
            />
          </div>
        </div>
        {selected && !selected.startsWith(HUB_PREFIX) ? (
          <PageSummary
            key={selected}
            path={selected}
            colors={colors}
            defaultAgent={defaultAgent}
            onSelect={focusNode}
            onClose={() => setSelected("")}
          />
        ) : null}
      </div>
    </div>
  );
}
