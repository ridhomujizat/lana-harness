import { useCallback, useEffect, useLayoutEffect, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Ic } from "../ui/icons";

/* An image that zooms and pans: a pinch (or ⌘/Ctrl + scroll) zooms at the
   pointer, a plain scroll or a drag pans, a double-click switches between fit
   and 100%, and the toolbar has − / + / fit / 1:1 and, when given, expand.
   Used by the Workbench Files viewer and the chat image viewer. */

const MAX_SCALE = 16;
const STEP = 1.25;

interface View {
  scale: number;
  x: number;
  y: number;
  /* Fit follows the container as it resizes; any zoom or pan leaves it. */
  fit: boolean;
}

interface Size {
  w: number;
  h: number;
}

/* The scale that shows the whole image, never above 100%. */
export function fitScale(box: Size, img: Size): number {
  if (!img.w || !img.h || !box.w || !box.h) return 1;
  return Math.min(1, box.w / img.w, box.h / img.h);
}

/* Keep the image in view: centred on an axis where it is smaller than the
   box, else no gap at either edge. */
export function clampView(v: View, box: Size, img: Size): View {
  const w = img.w * v.scale;
  const h = img.h * v.scale;
  const x = w <= box.w ? (box.w - w) / 2 : Math.min(0, Math.max(box.w - w, v.x));
  const y = h <= box.h ? (box.h - h) / 2 : Math.min(0, Math.max(box.h - h, v.y));
  return { ...v, x, y };
}

/* Zoom to `scale` keeping the image point under (px, py) where it is. */
export function zoomAt(v: View, scale: number, px: number, py: number, box: Size, img: Size): View {
  const min = Math.min(fitScale(box, img), 1) / 4;
  const next = Math.min(MAX_SCALE, Math.max(min, scale));
  const k = next / v.scale;
  return clampView({ scale: next, x: px - (px - v.x) * k, y: py - (py - v.y) * k, fit: false }, box, img);
}

export function ZoomImage({
  src,
  alt,
  imgClassName,
  onExpand,
  onBackdrop,
  onLoad,
  keys,
  extra,
}: {
  src: string;
  alt: string;
  imgClassName?: string;
  /* Shows the expand button. */
  onExpand?: () => void;
  /* A click (not a drag) on the empty area around the image. */
  onBackdrop?: () => void;
  onLoad?: (img: HTMLImageElement) => void;
  /* + / - / 0 zoom from the keyboard (full-screen views only). */
  keys?: boolean;
  /* More toolbar buttons, after the zoom ones. */
  extra?: ReactNode;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  const [box, setBox] = useState<Size>({ w: 0, h: 0 });
  const [img, setImg] = useState<Size>({ w: 0, h: 0 });
  const [view, setView] = useState<View>({ scale: 1, x: 0, y: 0, fit: true });
  const [failed, setFailed] = useState(false);
  const viewRef = useRef(view);
  const sizes = useRef({ box, img });
  useLayoutEffect(() => {
    viewRef.current = view;
    sizes.current = { box, img };
  });

  useLayoutEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const read = () => setBox({ w: el.clientWidth, h: el.clientHeight });
    read();
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", read);
      return () => window.removeEventListener("resize", read);
    }
    const ro = new ResizeObserver(read);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  /* Fit tracks the box and the image; a zoomed view only stays in bounds. */
  useLayoutEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the view follows the measured box and image
    setView((v) => (v.fit ? clampView({ scale: fitScale(box, img), x: 0, y: 0, fit: true }, box, img) : clampView(v, box, img)));
  }, [box, img]);

  const set = useCallback((fn: (v: View, box: Size, img: Size) => View) => {
    const { box: b, img: i } = sizes.current;
    setView((v) => fn(v, b, i));
  }, []);
  const zoomBy = useCallback(
    (factor: number) => set((v, b, i) => zoomAt(v, v.scale * factor, b.w / 2, b.h / 2, b, i)),
    [set]
  );
  const fit = useCallback(() => set((_, b, i) => clampView({ scale: fitScale(b, i), x: 0, y: 0, fit: true }, b, i)), [set]);
  const actual = useCallback(() => set((v, b, i) => zoomAt(v, 1, b.w / 2, b.h / 2, b, i)), [set]);

  /* Wheel: pinch (ctrlKey) or ⌘ zooms at the pointer, else it pans. Bound by
     hand because React's wheel listener is passive. */
  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      if (e.ctrlKey || e.metaKey) {
        const factor = Math.exp(-e.deltaY * (e.ctrlKey && !e.metaKey ? 0.01 : 0.002));
        set((v, b, i) => zoomAt(v, v.scale * factor, e.clientX - r.left, e.clientY - r.top, b, i));
      } else {
        set((v, b, i) => clampView({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY, fit: v.fit }, b, i));
      }
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [set]);

  useEffect(() => {
    if (!keys) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === "+" || e.key === "=") zoomBy(STEP);
      else if (e.key === "-" || e.key === "_") zoomBy(1 / STEP);
      else if (e.key === "0") fit();
      else if (e.key === "1") actual();
      else return;
      e.preventDefault();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [keys, zoomBy, fit, actual]);

  /* Pointers: one drags, two pinch. */
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{ moved: boolean; dist: number; onImage: boolean }>({ moved: false, dist: 0, onImage: false });
  const local = (e: { clientX: number; clientY: number }) => {
    const r = boxRef.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };
  const spread = () => {
    const [a, b] = [...pointers.current.values()];
    return a && b ? { d: Math.hypot(a.x - b.x, a.y - b.y), cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2 } : null;
  };

  const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest(".zi-bar")) return;
    boxRef.current?.setPointerCapture(e.pointerId);
    pointers.current.set(e.pointerId, local(e));
    if (pointers.current.size === 1) gesture.current = { moved: false, dist: 0, onImage: e.target instanceof HTMLImageElement };
    const s = spread();
    if (s) gesture.current.dist = s.d;
  };
  const onPointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const prev = pointers.current.get(e.pointerId);
    if (!prev) return;
    const p = local(e);
    pointers.current.set(e.pointerId, p);
    if (pointers.current.size >= 2) {
      const s = spread();
      if (s && gesture.current.dist) {
        const k = s.d / gesture.current.dist;
        gesture.current.dist = s.d;
        gesture.current.moved = true;
        set((v, b, i) => zoomAt(v, v.scale * k, s.cx, s.cy, b, i));
      }
      return;
    }
    const dx = p.x - prev.x;
    const dy = p.y - prev.y;
    if (Math.abs(dx) + Math.abs(dy) > 0) {
      if (!gesture.current.moved && Math.hypot(dx, dy) < 2) return;
      gesture.current.moved = true;
      set((v, b, i) => clampView({ ...v, x: v.x + dx, y: v.y + dy }, b, i));
    }
  };
  const onPointerUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (!pointers.current.delete(e.pointerId)) return;
    if (pointers.current.size === 0 && !gesture.current.moved && !gesture.current.onImage && e.type === "pointerup") onBackdrop?.();
  };

  const onDoubleClick = (e: ReactMouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest(".zi-bar")) return;
    const p = local(e);
    const { box: b, img: i } = sizes.current;
    const fitS = fitScale(b, i);
    if (viewRef.current.fit || Math.abs(viewRef.current.scale - fitS) < 0.01) {
      set((v) => zoomAt(v, Math.max(1, fitS * 2), p.x, p.y, b, i));
    } else fit();
  };

  const pct = Math.round(view.scale * 100);
  return (
    <div
      ref={boxRef}
      className={"zi" + (view.scale * img.w > box.w + 1 || view.scale * img.h > box.h + 1 ? " is-pannable" : "")}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={onDoubleClick}
    >
      {failed ? (
        <p className="zi-note">Could not load the image.</p>
      ) : (
        <img
          className={"zi-img" + (imgClassName ? " " + imgClassName : "")}
          src={src}
          alt={alt}
          draggable={false}
          style={{
            width: img.w || undefined,
            height: img.h || undefined,
            transform: "translate(" + view.x + "px, " + view.y + "px) scale(" + view.scale + ")",
            visibility: img.w ? "visible" : "hidden",
          }}
          onLoad={(e) => {
            const el = e.currentTarget;
            setImg({ w: el.naturalWidth || 1, h: el.naturalHeight || 1 });
            onLoad?.(el);
          }}
          onError={() => setFailed(true)}
        />
      )}
      <div className="zi-bar" role="toolbar" aria-label="Zoom">
        <button type="button" className="zi-btn" aria-label="Zoom out" data-tip="Zoom out" onClick={() => zoomBy(1 / STEP)}>
          <Ic name="zoom-out" size={15} />
        </button>
        <button type="button" className="zi-btn zi-pct" aria-label="Fit to view" data-tip="Fit" onClick={fit}>
          {pct}%
        </button>
        <button type="button" className="zi-btn" aria-label="Zoom in" data-tip="Zoom in" onClick={() => zoomBy(STEP)}>
          <Ic name="zoom-in" size={15} />
        </button>
        <span className="zi-sep" />
        <button type="button" className={"zi-btn" + (view.fit ? " is-on" : "")} aria-label="Fit to view" data-tip="Fit" onClick={fit}>
          <Ic name="scan" size={15} />
        </button>
        <button type="button" className={"zi-btn zi-txt" + (!view.fit && pct === 100 ? " is-on" : "")} aria-label="Actual size" data-tip="Actual size" onClick={actual}>
          1:1
        </button>
        {onExpand || extra ? <span className="zi-sep" /> : null}
        {extra}
        {onExpand ? (
          <button type="button" className="zi-btn" aria-label="Expand" data-tip="Expand" onClick={onExpand}>
            <Ic name="maximize-2" size={15} />
          </button>
        ) : null}
      </div>
    </div>
  );
}

/* The full-screen view of one image: dark backdrop, name, close; Esc closes
   and is kept from reaching the panel's own Esc. */
export function ZoomOverlay({ src, name, detail, onClose }: { src: string; name: string; detail?: string; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const previous = document.activeElement;
    closeRef.current?.focus({ preventScroll: true });
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      e.stopPropagation();
      onClose();
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      if (previous instanceof HTMLElement) previous.focus({ preventScroll: true });
    };
  }, [onClose]);
  return createPortal(
    <div className="iv zi-full" role="dialog" aria-modal="true" aria-label={name}>
      <div className="iv-bar">
        <div className="iv-title">
          <span className="iv-name">{name}</span>
          {detail ? <span className="iv-meta">{detail}</span> : null}
        </div>
        <div className="iv-actions">
          <a className="iv-btn iv-btn-text" href={src} target="_blank" rel="noopener noreferrer">
            Open original
          </a>
          <button ref={closeRef} type="button" className="iv-btn iv-close" aria-label="Close" onClick={onClose}>
            <Ic name="x" size={18} />
          </button>
        </div>
      </div>
      <div className="iv-stage zi-stage">
        <ZoomImage src={src} alt={name} imgClassName="iv-img" onBackdrop={onClose} keys />
      </div>
    </div>,
    document.body
  );
}
