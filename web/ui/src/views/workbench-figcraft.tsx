import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

import { ApiError, errorMessage } from "../api/client";
import type { GitFileChange } from "../api/workbench-git";
import {
  getFigInfo,
  getFigLayers,
  getFigRender,
  getFigSettings,
  openInFigcraft,
  putFigSettings,
  type FigInfo,
  type FigLayer,
  type FigPage,
  type FigRect,
  type FigRender,
} from "../api/workbench-figcraft";
import { Ic } from "../ui/icons";
import { StatusLetter } from "./workbench-changes";
import { baseName, dirName } from "./workbench-git-model";
import {
  detailRequests,
  fitRect,
  hitTest,
  layerIcon,
  pageRenderScale,
  rectToScreen,
  showsLabel,
  sizeLabel,
  toPage,
  zoomAt,
  zoomLabel,
  zoomTo,
  type View,
  type Viewport,
} from "./workbench-figcraft-model";

/* Spec 67 REQ-FV-060…069: a read-only view of a .figcraft file. The daemon
   renders pages with figcraft-cli (or the open FigCraft app in live mode); this
   view shows the render on a pannable canvas with the page and layer lists. */

const POLL_MS = 2000;
/* Sharp renders kept for one page: the layers in view and a few just left. */
const DETAIL_KEEP = 24;
const WIDE_MIN = 900;
const PHONE_MAX = 640;

interface Saved {
  page?: number;
  selected?: number | null;
  view?: View;
}

function storageKey(worktree: string, path: string): string {
  return "lana.figcraft.view:" + worktree.replace(/\/+$/, "") + "/" + path;
}

function loadSaved(key: string): Saved {
  try {
    const raw = sessionStorage.getItem(key);
    return raw ? (JSON.parse(raw) as Saved) : {};
  } catch {
    return {};
  }
}

function saveSaved(key: string, value: Saved) {
  try {
    sessionStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* storage can be off; the view just starts fitted next time */
  }
}

function pageRect(p: FigPage): FigRect {
  return { x: p.origin.x, y: p.origin.y, width: p.size.width, height: p.size.height };
}

function hhmm(d: Date): string {
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

type Failure = { kind: "missing"; message: string } | { kind: "error"; message: string };

type Detail = FigRender & { node: number };

export function FigcraftView({
  worktree,
  path,
  change,
  phone,
}: {
  worktree: string;
  path: string;
  change?: GitFileChange;
  phone?: boolean;
}) {
  const key = storageKey(worktree, path);
  const saved = useMemo(() => loadSaved(key), [key]);
  const rootRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLDivElement>(null);

  const [info, setInfo] = useState<FigInfo | null>(null);
  const [failure, setFailure] = useState<Failure | null>(null);
  const [pageId, setPageId] = useState<number | null>(saved.page ?? null);
  const [children, setChildren] = useState<Map<number, FigLayer[]>>(() => new Map());
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const [selected, setSelected] = useState<FigLayer | null>(null);
  const [view, setView] = useState<View | null>(saved.view ?? null);
  const [render, setRender] = useState<FigRender | null>(null);
  const [details, setDetails] = useState<Map<number, Detail>>(() => new Map());
  const [rendering, setRendering] = useState(false);
  const [renderedAt, setRenderedAt] = useState<Date | null>(null);
  const [toast, setToast] = useState("");
  const [width, setWidth] = useState(0);
  const [vp, setVp] = useState<Viewport>({ width: 0, height: 0 });
  const [columnOpen, setColumnOpen] = useState<boolean | null>(null);
  const [sheetTab, setSheetTab] = useState<"pages" | "layers">("layers");
  const [menu, setMenu] = useState<"" | "more" | "pages">("");
  const [live, setLive] = useState<boolean | null>(null);
  const [full, setFull] = useState(false);

  const isPhone = !!phone || (width > 0 && width < PHONE_MAX);
  const wide = width >= WIDE_MIN && !isPhone;
  const showColumn = columnOpen ?? wide;

  /* ----- info and freshness (REQ-FV-021, 067) ----- */
  const fpRef = useRef("");
  const fetchInfo = useCallback(
    (force: boolean) =>
      getFigInfo(worktree, path, force)
        .then((next) => {
          setFailure(null);
          const prev = fpRef.current;
          fpRef.current = next.fingerprint;
          setInfo((cur) => (cur && cur.fingerprint === next.fingerprint && cur.stale === next.stale ? cur : next));
          if (prev && prev !== next.fingerprint && !next.stale) {
            setToast(next.source === "live" && next.unsaved ? "Updated from FigCraft (unsaved)" : "Updated from the file");
          }
        })
        .catch((err) => {
          if (err instanceof ApiError && err.code === "figcraft_cli_missing") setFailure({ kind: "missing", message: err.message });
          else setFailure({ kind: "error", message: errorMessage(err) });
        }),
    [worktree, path]
  );

  useEffect(() => {
    fpRef.current = "";
    void fetchInfo(true);
    const tick = () => {
      if (document.visibilityState === "visible") void fetchInfo(false);
    };
    const id = window.setInterval(tick, POLL_MS);
    return () => window.clearInterval(id);
  }, [fetchInfo]);

  useEffect(() => {
    if (!toast) return;
    const id = window.setTimeout(() => setToast(""), 2500);
    return () => window.clearTimeout(id);
  }, [toast]);

  const pages = useMemo(() => info?.pages ?? [], [info]);
  const realPages = useMemo(() => pages.filter((p) => !p.divider), [pages]);
  const page = useMemo(
    () => realPages.find((p) => p.id === pageId) ?? realPages[0] ?? null,
    [realPages, pageId]
  );

  /* Children loaded for one fingerprint go stale with the next; reload the
     open ones (REQ-FV-005). */
  const loadChildren = useCallback(
    (node: number) =>
      getFigLayers(worktree, path, node)
        .then((r) => setChildren((m) => new Map(m).set(node, r.children)))
        .catch(() => {}),
    [worktree, path]
  );
  const fingerprint = info?.fingerprint ?? "";
  useEffect(() => {
    if (!fingerprint) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a new fingerprint drops the children of the old file
    setChildren(new Map());
    for (const id of expanded) void loadChildren(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fingerprint, loadChildren]);

  /* ----- size ----- */
  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      setWidth(root.clientWidth);
      const c = canvasRef.current;
      if (c) setVp({ width: c.clientWidth, height: c.clientHeight });
    });
    ro.observe(root);
    if (canvasRef.current) ro.observe(canvasRef.current);
    return () => ro.disconnect();
  }, [info, full]);

  /* ----- view: fit on a new page, keep it otherwise (REQ-FV-064) ----- */
  const fitted = useMemo(() => (page && vp.width ? fitRect(pageRect(page), vp) : null), [page, vp]);
  const pageKey = page?.id ?? 0;
  const lastPage = useRef<number | null>(saved.page ?? null);
  useEffect(() => {
    if (!fitted) return;
    if (lastPage.current !== pageKey || !view) {
      if (lastPage.current !== pageKey) setSelected(null);
      if (lastPage.current !== pageKey || !view) setView(fitted);
      lastPage.current = pageKey;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageKey, fitted]);

  useEffect(() => {
    if (!page) return;
    saveSaved(key, { page: page.id, selected: selected?.id ?? null, view: view ?? undefined });
  }, [key, page, selected, view]);

  /* Restore the selection after a refresh, once the layer is known. */
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || !page || saved.selected == null) return;
    const hit = page.layers.find((l) => l.id === saved.selected);
    restored.current = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- restores the saved selection once
    if (hit) setSelected(hit);
  }, [page, saved.selected]);

  /* ----- renders (REQ-FV-022, 066) ----- */
  const dpr = typeof window !== "undefined" ? window.devicePixelRatio || 1 : 1;
  const scale = fitted ? pageRenderScale(fitted.zoom, dpr) : 1;
  const renderUrl = useRef<string>("");
  useEffect(() => {
    if (!page || !fingerprint || !page.size.width) return;
    const ctl = new AbortController();
    const started = performance.now();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- shows the spinner while the new render loads
    setRendering(true);
    getFigRender(worktree, path, { page: page.id }, scale, fingerprint, ctl.signal)
      .then((r) => {
        if (renderUrl.current) URL.revokeObjectURL(renderUrl.current);
        renderUrl.current = r.url;
        if (!r.ms) r.ms = Math.round(performance.now() - started);
        setRender(r);
        setRenderedAt(new Date());
        setRendering(false);
      })
      .catch((err) => {
        if (ctl.signal.aborted) return;
        setRendering(false);
        setFailure({ kind: "error", message: errorMessage(err) });
      });
    return () => ctl.abort();
  }, [worktree, path, page, fingerprint, scale]);

  useEffect(
    () => () => {
      if (renderUrl.current) URL.revokeObjectURL(renderUrl.current);
    },
    []
  );

  const want = useMemo(
    () => (view && page && render ? detailRequests(view, vp, render.scale, dpr, page.layers) : null),
    [view, vp, render, dpr, page]
  );
  const wantKey = want ? want.nodes.join(",") + "@" + want.scale : "";
  /* Sharp renders of the layers in view, fetched once the view settles. A
     render at a lower scale stays shown until the sharper one arrives. */
  useEffect(() => {
    if (!want || !fingerprint) return;
    const missing = want.nodes.filter((node) => {
      const d = details.get(node);
      return !d || d.fingerprint !== fingerprint || d.scale < want.scale;
    });
    if (!missing.length) return;
    const ctl = new AbortController();
    const id = window.setTimeout(() => {
      for (const node of missing) {
        getFigRender(worktree, path, { node }, want.scale, fingerprint, ctl.signal)
          .then((r) =>
            setDetails((cur) => {
              const next = new Map(cur);
              const old = next.get(node);
              if (old) URL.revokeObjectURL(old.url);
              next.delete(node);
              next.set(node, { ...r, node });
              for (const [k, d] of next) {
                if (next.size <= DETAIL_KEEP) break;
                URL.revokeObjectURL(d.url);
                next.delete(k);
              }
              return next;
            })
          )
          .catch(() => {});
      }
    }, 250);
    return () => {
      window.clearTimeout(id);
      ctl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [wantKey, fingerprint, worktree, path]);
  /* Renders of an older file or another page are dropped. */
  useEffect(() => {
    const ids = new Set(page?.layers.map((l) => l.id) ?? []);
    if (![...details.values()].some((d) => d.fingerprint !== fingerprint || !ids.has(d.node))) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- drops renders that no longer match the file or page
    setDetails((cur) => {
      const next = new Map<number, Detail>();
      for (const [k, d] of cur) {
        if (d.fingerprint === fingerprint && ids.has(d.node)) next.set(k, d);
        else URL.revokeObjectURL(d.url);
      }
      return next;
    });
  }, [details, fingerprint, page]);
  const detailsRef = useRef(details);
  useEffect(() => {
    detailsRef.current = details;
  }, [details]);
  useEffect(
    () => () => {
      for (const d of detailsRef.current.values()) URL.revokeObjectURL(d.url);
    },
    []
  );

  /* Full screen (Expand): Esc goes back unless something inside took it. */
  useEffect(() => {
    if (!full) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      e.preventDefault();
      e.stopPropagation();
      setFull(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [full]);

  /* ----- interaction ----- */
  const selectLayer = useCallback(
    (l: FigLayer | null, zoom: "never" | "if-hidden" | "always" = "never") => {
      setSelected(l);
      if (!l || !view || zoom === "never") return;
      const s = rectToScreen(view, l.bounds);
      const hidden = s.x < 0 || s.y < 0 || s.x + s.width > vp.width || s.y + s.height > vp.height;
      if (zoom === "always" || hidden) setView(fitRect(l.bounds, vp, 40));
    },
    [view, vp]
  );

  const expandPath = useCallback(
    (path: FigLayer[]) => {
      setExpanded((cur) => {
        const next = new Set(cur);
        for (const l of path.slice(0, -1)) {
          if (!next.has(l.id)) {
            next.add(l.id);
            if (!children.has(l.id)) void loadChildren(l.id);
          }
        }
        return next;
      });
    },
    [children, loadChildren]
  );

  const pickAt = useCallback(
    (sx: number, sy: number, zoom: "never" | "always") => {
      if (!view || !page) return;
      const p = toPage(view, sx, sy);
      const path = hitTest(page.layers, children, p.x, p.y);
      const hit = path[path.length - 1] ?? null;
      selectLayer(hit, zoom);
      if (path.length) expandPath(path);
    },
    [view, page, children, selectLayer, expandPath]
  );

  /* Wheel: pan, or zoom with ⌘/Ctrl and trackpad pinch. Non-passive so the
     panel does not scroll. */
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      setView((v) => {
        if (!v) return v;
        if (e.ctrlKey || e.metaKey) return zoomAt(v, Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
        return { ...v, tx: v.tx - e.deltaX, ty: v.ty - e.deltaY };
      });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [info, full]);

  /* Pointer: drag pans, a click selects, two touches pinch. */
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ moved: boolean; startX: number; startY: number; dist: number }>({ moved: false, startX: 0, startY: 0, dist: 0 });
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    e.currentTarget.focus({ preventScroll: true });
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const [a, b] = [...pointers.current.values()];
    gesture.current = {
      moved: !!b,
      startX: e.clientX,
      startY: e.clientY,
      dist: a && b ? Math.hypot(a.x - b.x, a.y - b.y) : 0,
    };
  };
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const prev = pointers.current.get(e.pointerId);
    if (!prev) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const [a, b] = [...pointers.current.values()];
    const g = gesture.current;
    if (a && b) {
      const r = e.currentTarget.getBoundingClientRect();
      const dist = Math.hypot(a.x - b.x, a.y - b.y);
      const cx = (a.x + b.x) / 2 - r.left;
      const cy = (a.y + b.y) / 2 - r.top;
      if (g.dist > 0) {
        const k = dist / g.dist;
        setView((v) => (v ? zoomAt({ ...v, tx: v.tx + (e.clientX - prev.x) / 2, ty: v.ty + (e.clientY - prev.y) / 2 }, k, cx, cy) : v));
      }
      g.dist = dist;
      g.moved = true;
      return;
    }
    if (!g.moved && Math.hypot(e.clientX - g.startX, e.clientY - g.startY) < 4) return;
    g.moved = true;
    setView((v) => (v ? { ...v, tx: v.tx + e.clientX - prev.x, ty: v.ty + e.clientY - prev.y } : v));
  };
  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    const had = pointers.current.delete(e.pointerId);
    if (!had) return;
    if (!gesture.current.moved && pointers.current.size === 0) {
      const r = e.currentTarget.getBoundingClientRect();
      pickAt(e.clientX - r.left, e.clientY - r.top, "never");
    }
  };
  const onDoubleClick = (e: React.MouseEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    pickAt(e.clientX - r.left, e.clientY - r.top, "always");
  };

  const fit = useCallback(() => fitted && setView(fitted), [fitted]);
  const zoomBy = (k: number) => setView((v) => (v ? zoomAt(v, k, vp.width / 2, vp.height / 2) : v));
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.shiftKey && e.code === "Digit1") fit();
    else if (e.shiftKey && e.code === "Digit2" && selected) selectLayer(selected, "always");
    else if (e.shiftKey && e.code === "Digit0") setView((v) => (v ? zoomTo(v, 1, vp) : v));
    else if (e.key === "+" || e.key === "=") zoomBy(1.25);
    else if (e.key === "-") zoomBy(0.8);
    else if (e.key === "Escape" && selected) setSelected(null);
    else return;
    e.preventDefault();
    e.stopPropagation();
  };

  /* ----- menu actions ----- */
  const toggleLive = () => {
    getFigSettings()
      .then((s) => putFigSettings({ ...s, live: !s.live }))
      .then((s) => {
        setLive(s.live);
        return fetchInfo(true);
      })
      .catch((err) => setToast(errorMessage(err)));
  };
  const setCliPath = () => {
    getFigSettings()
      .then((s) => {
        const next = window.prompt("figcraft-cli path (empty = find it automatically)", s.cli_path);
        if (next === null) return;
        return putFigSettings({ ...s, cli_path: next.trim() }).then(() => fetchInfo(true));
      })
      .catch((err) => setToast(errorMessage(err)));
  };
  useEffect(() => {
    if (menu !== "more" || live !== null) return;
    getFigSettings()
      .then((s) => setLive(s.live))
      .catch(() => {});
  }, [menu, live]);

  useEffect(() => {
    if (!menu) return;
    const close = (e: MouseEvent) => {
      if (!(e.target as HTMLElement).closest?.(".fv-menu-wrap")) setMenu("");
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [menu]);

  const absPath = worktree.replace(/\/+$/, "") + "/" + path;
  const pageIndex = page ? realPages.indexOf(page) + 1 : 0;

  /* ----- pieces ----- */
  const header = (
    <header className="wbg-viewer-head fv-head">
      <Ic name="pen-tool" size={14} className="fv-file-icon" />
      <b className="wbg-viewer-name" title={path}>
        {baseName(path)}
      </b>
      <span className="wbg-viewer-dir">{dirName(path)}</span>
      <StatusLetter status={change?.status} />
      {info?.source === "live" ? (
        <span
          className={"fv-live" + (info.unsaved ? " is-unsaved" : "")}
          data-tip={info.unsaved ? "Showing the open FigCraft document, with unsaved edits" : "Showing the open FigCraft document"}
        >
          <span className="fv-live-dot" />
          {info.unsaved ? "Live · unsaved" : "Live"}
        </span>
      ) : null}
      <span className="wb-spacer" />
      {!wide && info ? (
        <button
          type="button"
          className={"fv-layers-btn" + (showColumn ? " is-on" : "")}
          onClick={() => setColumnOpen(!showColumn)}
          aria-pressed={showColumn}
        >
          <Ic name="layers" size={14} />
          {isPhone ? null : <span>Layers</span>}
        </button>
      ) : null}
      <button
        type="button"
        className="wbg-icon-btn"
        aria-label={full ? "Exit full screen" : "Expand"}
        data-tip={full ? "Exit full screen  Esc" : "Expand"}
        aria-pressed={full}
        onClick={() => setFull(!full)}
      >
        <Ic name={full ? "minimize-2" : "maximize-2"} size={15} />
      </button>
      <span className="fv-menu-wrap">
        <button type="button" className="wbg-icon-btn" aria-label="More" data-tip="More" onClick={() => setMenu(menu === "more" ? "" : "more")}>
          <Ic name="ellipsis" size={15} />
        </button>
        {menu === "more" ? (
          <div className="wbg-menu fv-more-menu" role="menu">
            <MenuItem icon="rotate-cw" label="Refresh" onClick={() => (setMenu(""), void fetchInfo(true))} />
            <MenuItem
              icon="external-link"
              label="Open in FigCraft"
              hint="On the Mac that runs Lana"
              onClick={() => {
                setMenu("");
                openInFigcraft(worktree, path).catch((err) => setToast(errorMessage(err)));
              }}
            />
            <MenuItem icon="copy" label="Copy path" onClick={() => (setMenu(""), void navigator.clipboard?.writeText(absPath))} />
            <div className="fv-menu-sep" />
            <MenuItem
              icon={live ? "square-check" : "square"}
              label="Live from the FigCraft app"
              hint="Show unsaved edits of an app started with --control"
              onClick={() => (setMenu(""), toggleLive())}
            />
            <MenuItem icon="terminal" label="figcraft-cli path…" hint={info?.cli ? `${info.cli.version} · ${info.cli.path}` : undefined} onClick={() => (setMenu(""), setCliPath())} />
          </div>
        ) : null}
      </span>
    </header>
  );

  if (failure && !info) {
    return (
      <div className="wbg-viewer fv" ref={rootRef}>
        {header}
        <div className="fv-state">
          <Ic name="circle-alert" size={30} className="fv-state-icon" />
          <b>{failure.kind === "missing" ? "figcraft-cli not found" : "Could not read the file"}</b>
          <span>
            {failure.kind === "missing"
              ? "The viewer renders with figcraft-cli on the Mac that runs Lana. Install FigCraft's CLI (0.18 or newer) or set its path."
              : failure.message}
          </span>
          {failure.kind === "missing" ? <code className="fv-state-code">{failure.message}</code> : null}
          <div className="fv-state-actions">
            {failure.kind === "missing" ? (
              <button type="button" className="btn btn-ghost btn-sm" onClick={setCliPath}>
                Set path…
              </button>
            ) : null}
            <button type="button" className="btn btn-sm" onClick={() => void fetchInfo(true)}>
              Retry
            </button>
          </div>
        </div>
      </div>
    );
  }

  const pageMenu = (
    <span className="fv-menu-wrap fv-page-wrap">
      <button type="button" className="fv-page-btn" onClick={() => setMenu(menu === "pages" ? "" : "pages")} disabled={!page}>
        <span className="fv-page-name">{page?.name ?? "Loading…"}</span>
        {page ? <small>{pageIndex}/{realPages.length}</small> : null}
        <Ic name="chevron-down" size={13} />
      </button>
      {menu === "pages" ? (
        <div className="wbg-menu fv-pages-menu" role="menu">
          <div className="wbg-menu-scroll">
            {pages.map((p) =>
              p.divider ? (
                <div key={p.id} className="fv-menu-sep" />
              ) : (
                <button
                  key={p.id}
                  type="button"
                  role="menuitem"
                  className={"wbg-menu-item" + (p.id === page?.id ? " is-on" : "")}
                  onClick={() => {
                    setPageId(p.id);
                    setMenu("");
                  }}
                >
                  <span className="wbg-menu-text">
                    <b>{p.name}</b>
                  </span>
                  <small className="fv-menu-count">{p.layers.length}</small>
                </button>
              )
            )}
          </div>
        </div>
      ) : null}
    </span>
  );

  const zoomCtl = (
    <span className="fv-zoom">
      <button type="button" className="wbg-icon-btn" aria-label="Zoom out" data-tip="Zoom out  −" onClick={() => zoomBy(0.8)}>
        <Ic name="minus" size={14} />
      </button>
      <button type="button" className="fv-zoom-pct" data-tip="Zoom to 100%  ⇧0" onClick={() => setView((v) => (v ? zoomTo(v, 1, vp) : v))}>
        {view ? zoomLabel(view.zoom) : "–"}
      </button>
      <button type="button" className="wbg-icon-btn" aria-label="Zoom in" data-tip="Zoom in  +" onClick={() => zoomBy(1.25)}>
        <Ic name="plus" size={14} />
      </button>
      <button type="button" className="wbg-icon-btn" aria-label="Fit" data-tip="Zoom to fit  ⇧1" onClick={fit}>
        <Ic name="scan" size={14} />
      </button>
    </span>
  );

  const pagesList = (
    <div className="fv-pages">
      {realPages.map((p) => (
        <button key={p.id} type="button" className={"fv-row" + (p.id === page?.id ? " is-on" : "")} onClick={() => setPageId(p.id)}>
          <span className="fv-row-name">{p.name}</span>
        </button>
      ))}
    </div>
  );

  const layerRows: ReactNode[] = [];
  const walk = (list: FigLayer[], depth: number) => {
    for (let i = list.length - 1; i >= 0; i--) {
      const l = list[i];
      if (!l) continue;
      const open = expanded.has(l.id);
      const kids = children.get(l.id);
      layerRows.push(
        <button
          key={l.id}
          type="button"
          className={"fv-row fv-layer" + (selected?.id === l.id ? " is-on" : "") + (l.visible ? "" : " is-hidden")}
          style={{ paddingLeft: 6 + depth * 14 }}
          onClick={() => selectLayer(l, "if-hidden")}
          onDoubleClick={() => selectLayer(l, "always")}
          title={l.name}
        >
          {l.child_count > 0 ? (
            <span
              className="fv-chev"
              role="button"
              aria-label={open ? "Collapse" : "Expand"}
              onClick={(e) => {
                e.stopPropagation();
                setExpanded((cur) => {
                  const next = new Set(cur);
                  if (next.has(l.id)) next.delete(l.id);
                  else {
                    next.add(l.id);
                    if (!children.has(l.id)) void loadChildren(l.id);
                  }
                  return next;
                });
              }}
            >
              <Ic name={open ? "chevron-down" : "chevron-right"} size={12} />
            </span>
          ) : (
            <span className="fv-chev" />
          )}
          <Ic name={layerIcon(l.type)} size={13} className="fv-type" />
          <span className="fv-row-name">{l.name}</span>
        </button>
      );
      if (open) {
        if (kids) walk(kids, depth + 1);
        else
          layerRows.push(
            <div key={l.id + ":loading"} className="fv-row-note" style={{ paddingLeft: 26 + (depth + 1) * 14 }}>
              Loading…
            </div>
          );
      }
    }
  };
  if (page) walk(page.layers, 0);

  const column = (
    <aside className={"fv-column" + (wide ? "" : " is-over")}>
      <div className="fv-col-head">
        Pages <small>{realPages.length}</small>
      </div>
      {pagesList}
      <div className="fv-col-head fv-col-head-layers">Layers</div>
      <div className="fv-layers">{layerRows}</div>
    </aside>
  );

  const sheet = (
    <div className="fv-sheet-wrap" onClick={() => setColumnOpen(false)}>
      <div className="fv-sheet" onClick={(e) => e.stopPropagation()}>
        <div className="fv-sheet-grip" />
        <div className="fv-sheet-head">
          <span className="wbg-seg" role="group">
            <button type="button" className={sheetTab === "pages" ? "is-on" : ""} onClick={() => setSheetTab("pages")}>
              Pages <small>{realPages.length}</small>
            </button>
            <button type="button" className={sheetTab === "layers" ? "is-on" : ""} onClick={() => setSheetTab("layers")}>
              <Ic name="layers" size={13} /> Layers
            </button>
          </span>
          <span className="wb-spacer" />
          <button type="button" className="wbg-icon-btn" aria-label="Close" onClick={() => setColumnOpen(false)}>
            <Ic name="x" size={16} />
          </button>
        </div>
        <div className="fv-sheet-body">{sheetTab === "pages" ? pagesList : <div className="fv-layers">{layerRows}</div>}</div>
      </div>
    </div>
  );

  /* ----- canvas contents ----- */
  const content: ReactNode[] = [];
  if (view && page) {
    /* Every layer in view has a sharp render: the page render under them would
       only add a blurred halo. */
    const sharp = !!want?.complete && want.nodes.every((n) => details.get(n)?.fingerprint === fingerprint);
    if (render && !sharp) {
      const w = (render.width / render.scale) * view.zoom;
      const h = (render.height / render.scale) * view.zoom;
      const s = { x: render.originX * view.zoom + view.tx, y: render.originY * view.zoom + view.ty };
      content.push(<img key="page" className="fv-img" src={render.url} alt="" draggable={false} style={{ left: s.x, top: s.y, width: w, height: h }} />);
    } else if (!render) {
      for (const l of page.layers) {
        if (!l.visible) continue;
        const r = rectToScreen(view, l.bounds);
        content.push(<div key={"sk" + l.id} className="fv-skeleton" style={{ left: r.x, top: r.y, width: r.width, height: r.height }} />);
      }
    }
    for (const node of want?.nodes ?? []) {
      const detail = details.get(node);
      if (!detail || detail.fingerprint !== fingerprint) continue;
      const w = (detail.width / detail.scale) * view.zoom;
      const h = (detail.height / detail.scale) * view.zoom;
      content.push(
        <img
          key={"detail" + node}
          className="fv-img"
          src={detail.url}
          alt=""
          draggable={false}
          style={{ left: detail.originX * view.zoom + view.tx, top: detail.originY * view.zoom + view.ty, width: w, height: h }}
        />
      );
    }
    for (const l of page.layers) {
      if (!l.visible || !showsLabel(l.type)) continue;
      const r = rectToScreen(view, l.bounds);
      if (r.width < 48 || r.x + r.width < 0 || r.x > vp.width) continue;
      content.push(
        <span key={"lb" + l.id} className={"fv-label" + (selected?.id === l.id ? " is-on" : "")} style={{ left: r.x, top: r.y - 17, maxWidth: r.width }}>
          {l.name}
        </span>
      );
    }
    if (selected) {
      const r = rectToScreen(view, selected.bounds);
      content.push(<div key="sel" className="fv-sel" style={{ left: r.x, top: r.y, width: r.width, height: r.height }} />);
      content.push(
        <span key="selsize" className="fv-size" style={{ left: r.x + r.width / 2, top: r.y + r.height + 6 }}>
          {sizeLabel(selected.bounds)}
        </span>
      );
    }
  }

  const warnings = info?.warnings ?? [];
  const n = page?.layers.filter((l) => l.visible).length ?? 0;

  const viewer = (
    <div
      className={"wbg-viewer fv" + (wide ? " is-wide" : "") + (isPhone ? " is-phone" : "") + (full ? " is-full" : "")}
      ref={rootRef}
      role={full ? "dialog" : undefined}
      aria-modal={full ? true : undefined}
      aria-label={full ? baseName(path) : undefined}
    >
      {header}
      <div className="fv-toolbar">
        {pageMenu}
        {wide && selected ? (
          <span className="fv-crumb">
            <Ic name="chevron-right" size={12} />
            <b>{selected.name}</b>
            <small>{sizeLabel(selected.bounds)}</small>
          </span>
        ) : null}
        <span className="wb-spacer" />
        {isPhone ? null : zoomCtl}
      </div>
      {warnings.includes("cli_older_than_app") ? (
        <div className="fv-banner">
          <Ic name="triangle-alert" size={14} />
          <span>
            figcraft-cli {info?.cli?.version} is older than FigCraft {info?.app?.version}; the render can differ.
          </span>
        </div>
      ) : null}
      {info?.live_error ? (
        <div className="fv-banner fv-banner-dim">
          <Ic name="info" size={14} />
          <span>Live view unavailable ({info.live_error}); showing the saved file.</span>
        </div>
      ) : null}
      <div className="fv-main">
        {showColumn && !isPhone ? column : null}
        <div
          className="fv-canvas"
          ref={canvasRef}
          tabIndex={0}
          style={{ background: page?.background || undefined }}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
          onDoubleClick={onDoubleClick}
          onKeyDown={onKeyDown}
          aria-label={page ? "Canvas: " + page.name : "Canvas"}
        >
          {content}
          {!render && page ? (
            <div className="fv-loading">
              <Ic name="loader-circle" size={22} className="fv-spin" />
              <span>Rendering {page.name}…</span>
            </div>
          ) : null}
          {!info && !failure ? (
            <div className="fv-loading">
              <Ic name="loader-circle" size={22} className="fv-spin" />
              <span>Reading {baseName(path)}…</span>
            </div>
          ) : null}
          {page && !page.size.width ? <div className="fv-loading">This page is empty.</div> : null}
          {toast ? (
            <div className="fv-toast" role="status">
              <Ic name="rotate-cw" size={13} />
              {toast}
            </div>
          ) : null}
          {isPhone && view ? <div className="fv-zoom-pill">{zoomCtl}</div> : null}
        </div>
      </div>
      {isPhone && showColumn ? sheet : null}
      <footer className="wbg-viewer-foot fv-foot">
        <span>
          {n} {n === 1 ? "frame" : "frames"}
          {render ? ` · render ${(render.ms / 1000).toFixed(2)} s` : ""} · read-only
        </span>
        {rendering && render ? <span className="fv-foot-dim">rendering…</span> : null}
        {info?.stale || render?.stale ? <span className="fv-foot-amber">retrying…</span> : null}
        {failure && info ? <span className="fv-foot-amber" title={failure.message}>update failed</span> : null}
        <span className="wb-spacer" />
        {info?.source === "live" ? <span>FigCraft :{info.live_port}</span> : null}
        {renderedAt ? <span>rendered {hhmm(renderedAt)}</span> : null}
      </footer>
    </div>
  );
  /* Expand moves the viewer over the whole window, like the image viewer. */
  return full ? (
    <>
      <div className="wbg-viewer fv fv-placeholder">
        {header}
        <div className="fv-state">
          <Ic name="maximize-2" size={26} className="fv-state-icon" />
          <span>Shown full screen.</span>
        </div>
      </div>
      {createPortal(<div className="wb-page fv-full-host">{viewer}</div>, document.body)}
    </>
  ) : (
    viewer
  );
}

function MenuItem({ icon, label, hint, onClick }: { icon: string; label: string; hint?: string; onClick: () => void }) {
  return (
    <button type="button" role="menuitem" className="wbg-menu-item" onClick={onClick}>
      <Ic name={icon} size={15} />
      <span className="wbg-menu-text">
        <b>{label}</b>
        {hint ? <small>{hint}</small> : null}
      </span>
    </button>
  );
}
