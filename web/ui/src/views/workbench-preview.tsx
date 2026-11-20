import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

import type { WorkbenchPreview } from "../api/workbench";
import { Ic } from "../ui/icons";

/* Spec 61 REQ-WP-012, REQ-WP-013: the file preview. On desktop it is a panel
   at the right of the panes with a draggable divider; on a phone it is a
   full-screen sheet. The space folder is not part of the shared layout
   document, so the width is kept in this browser's localStorage. */

const WIDTH_KEY = "lana.workbench.previewWidth";
const MIN_WIDTH = 280;
const MAX_FRACTION = 0.9;
const DEFAULT_FRACTION = 0.45;

function defaultWidth(): number {
  const viewport = typeof window !== "undefined" ? window.innerWidth : 1280;
  return Math.max(MIN_WIDTH, Math.round(viewport * DEFAULT_FRACTION));
}

function readWidth(): number {
  const fallback = defaultWidth();
  try {
    const value = Number(window.localStorage.getItem(WIDTH_KEY));
    return Number.isFinite(value) && value >= MIN_WIDTH ? value : fallback;
  } catch {
    return fallback;
  }
}

function saveWidth(width: number): void {
  try {
    window.localStorage.setItem(WIDTH_KEY, String(Math.round(width)));
  } catch {
    /* Storage is optional. */
  }
}

/* "/Users/me/code/x" reads as "~/code/x" (REQ-WP-013). */
export function shortenHome(path: string): string {
  return path.replace(/^\/(?:Users|home)\/[^/]+/, "~");
}

export function humanSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return bytes + " B";
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  const rounded = value >= 10 ? Math.round(value) : Math.round(value * 10) / 10;
  return rounded + " " + (units[unit] ?? "KB");
}

function folderOf(path: string): string {
  const index = path.lastIndexOf("/");
  return index > 0 ? path.slice(0, index) : "";
}

export interface PreviewPanelProps {
  data: WorkbenchPreview;
  phone?: boolean;
  onClose: () => void;
}

export function WorkbenchPreview({ data, phone, onClose }: PreviewPanelProps) {
  const [width, setWidth] = useState(readWidth);
  const closeRef = useRef(onClose);
  useEffect(() => {
    closeRef.current = onClose;
  }, [onClose]);

  /* Esc closes the panel (REQ-WP-012). */
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      closeRef.current();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);

  /* The divider sits on the panel's left edge: dragging it left widens the
     panel. Double the panel's resize pattern from workbench.tsx. */
  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = width;
    const max = Math.round(window.innerWidth * MAX_FRACTION);
    let latest = startWidth;
    const move = (e: PointerEvent) => {
      latest = Math.min(max, Math.max(MIN_WIDTH, startWidth + (startX - e.clientX)));
      setWidth(latest);
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      document.body.classList.remove("wb-resizing");
      saveWidth(latest);
    };
    document.body.classList.add("wb-resizing");
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  const folder = shortenHome(data.dir || folderOf(data.path));

  return (
    <aside
      className={"wb-preview" + (phone ? " wb-preview-sheet" : "")}
      aria-label={"Preview of " + data.name}
      style={phone ? undefined : { width, flexBasis: width }}
    >
      {phone ? null : (
        <div
          className="wb-preview-resizer"
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize preview"
          onPointerDown={startResize}
        />
      )}
      <header className="wb-preview-head">
        <div className="wb-preview-title">
          <b title={data.path}>{data.name}</b>
          <span title={data.path}>{folder}</span>
        </div>
        <div className="wb-preview-tools">
          <a className="wb-preview-btn" href={data.raw_url} target="_blank" rel="noopener">
            Open in new tab
          </a>
          <a className="wb-preview-btn" href={data.raw_url} download={data.name}>
            Download
          </a>
          <button type="button" className="wb-preview-btn wb-preview-close" aria-label="Close preview" onClick={onClose}>
            <Ic name="x" size={14} />
          </button>
        </div>
      </header>
      {data.kind === "binary" ? (
        <div className="wb-preview-binary">
          <Ic name="file" size={30} />
          <b>{data.name}</b>
          <span className="wb-preview-bin-sub">{humanSize(data.size)}</span>
          <a className="wb-preview-btn wb-preview-download" href={data.raw_url} download={data.name}>
            <Ic name="download" size={14} /> Download
          </a>
        </div>
      ) : (
        /* Never allow-same-origin: the preview runs in an opaque origin
           (REQ-WP-013). */
        <iframe
          className="wb-preview-frame"
          sandbox="allow-scripts allow-forms allow-popups"
          src={data.url}
          title={data.name}
        />
      )}
    </aside>
  );
}
