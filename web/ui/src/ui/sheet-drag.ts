import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";

/* Bottom sheets with a grip (owner, 2026-11-22): drag the grip up to give the
   sheet the full height, down to bring it back, and further down (from its
   normal height) to close it. A tap or Enter on the grip toggles the full
   height. While dragging, the sheet follows the finger: its height is set
   inline and its top released, so a sheet pinned with top and bottom grows
   from the bottom edge. The sheet's own CSS gives `.is-full` its height. */

const SLOP = 4;
const OPEN_PX = 40;
const CLOSE_PX = 70;
const MIN_PX = 120;

/* The room kept above a full sheet: the status bar on a phone, the traffic
   lights in Lana.app. */
function topGap(): number {
  const root = document.documentElement;
  const app = root.dataset.shell === "macos-app" && !root.hasAttribute("data-shell-fullscreen");
  return app ? 44 : 12;
}

export interface SheetDrag {
  /* Callback ref for the sheet element. */
  attach: (el: HTMLElement | null) => void;
  full: boolean;
  /* Spread on the grip element. */
  gripProps: {
    role: "button";
    tabIndex: number;
    "aria-label": string;
    "aria-expanded": boolean;
    onPointerDown: (e: ReactPointerEvent<HTMLElement>) => void;
    onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => void;
  };
}

export function useSheetDrag(onClose?: () => void): SheetDrag {
  const sheetRef = useRef<HTMLElement | null>(null);
  const [full, setFull] = useState(false);
  const fullRef = useRef(false);
  const closeRef = useRef(onClose);
  useEffect(() => {
    fullRef.current = full;
    closeRef.current = onClose;
  });

  const attach = useCallback((el: HTMLElement | null) => {
    sheetRef.current = el;
    /* A sheet that closes opens at its normal height next time. */
    if (!el) setFull(false);
  }, []);

  const onPointerDown = (e: ReactPointerEvent<HTMLElement>) => {
    const sheet = sheetRef.current;
    if (!sheet || e.button !== 0) return;
    e.preventDefault();
    const grip = e.currentTarget;
    try {
      grip.setPointerCapture(e.pointerId);
    } catch {
      /* Synthetic events have no active pointer. */
    }
    const y0 = e.clientY;
    const h0 = sheet.getBoundingClientRect().height;
    const maxH = window.innerHeight - topGap();
    let dy = 0;
    let moved = false;
    const move = (ev: PointerEvent) => {
      dy = ev.clientY - y0;
      if (!moved && Math.abs(dy) < SLOP) return;
      if (!moved) {
        moved = true;
        sheet.classList.add("is-sheet-dragging");
      }
      const h = Math.min(maxH, Math.max(MIN_PX, h0 - dy));
      sheet.style.top = "auto";
      sheet.style.maxHeight = "none";
      sheet.style.height = h + "px";
    };
    const end = () => {
      grip.removeEventListener("pointermove", move);
      grip.removeEventListener("pointerup", end);
      grip.removeEventListener("pointercancel", end);
      sheet.classList.remove("is-sheet-dragging");
      sheet.style.top = "";
      sheet.style.maxHeight = "";
      sheet.style.height = "";
      if (!moved) {
        setFull((f) => !f);
        return;
      }
      if (dy < -OPEN_PX) setFull(true);
      else if (dy > CLOSE_PX) {
        if (fullRef.current) setFull(false);
        else closeRef.current?.();
      }
    };
    grip.addEventListener("pointermove", move);
    grip.addEventListener("pointerup", end);
    grip.addEventListener("pointercancel", end);
  };

  const onKeyDown = (e: ReactKeyboardEvent<HTMLElement>) => {
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    setFull((f) => !f);
  };

  return {
    attach,
    full,
    gripProps: {
      role: "button",
      tabIndex: 0,
      "aria-label": full ? "Shrink sheet" : "Expand sheet",
      "aria-expanded": full,
      onPointerDown,
      onKeyDown,
    },
  };
}
