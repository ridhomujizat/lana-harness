/* Shared helpers for the ported Gr8r primitives. */
import { useEffect, useRef } from "react";
import type { RefObject } from "react";

export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

/* Gr8r av(): first letters of the name, capped at two, upper-cased. */
export function initials(name: string): string {
  let out = "";
  for (const part of name.trim().split(/\s+/)) {
    if (part) {
      out += part[0] ?? "";
    }
  }
  return out.slice(0, 2).toUpperCase();
}

export function toSize(value?: number | string): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  return typeof value === "number" ? value + "px" : value;
}

/* The HTML Popover API lifts a portaled menu into the browser top layer, so it
   can render above an open modal <dialog>, which also lives there. jsdom and
   older browsers lack showPopover; callers then keep the plain fixed menu. */
export function showPopover(el: HTMLElement): void {
  if (typeof el.showPopover === "function") {
    try {
      el.showPopover();
    } catch {
      /* already showing, or the user agent refused it */
    }
  }
}

export function hidePopover(el: HTMLElement): void {
  if (typeof el.hidePopover === "function") {
    try {
      el.hidePopover();
    } catch {
      /* not showing */
    }
  }
}

/* While a modal <dialog> is open, every node outside it is inert: the browser
   paints a top-layer menu portaled to <body> but skips it for hit testing, so
   clicks and wheel scrolls fall through to the dialog. A menu whose trigger
   sits in an open dialog is therefore portaled into that dialog; showPopover
   still lifts it into the top layer, so the dialog's overflow does not clip it
   and position: fixed stays relative to the viewport. */
export function overlayContainer(anchor: Element | null | undefined): HTMLElement {
  const dialog = anchor?.closest<HTMLDialogElement>("dialog[open]");
  return dialog ?? document.body;
}

/* Reposition an anchored menu on window scroll and resize. Scroll events from
   inside the menu itself are ignored: placeAnchored clears the height cap to
   measure, which would snap the menu's own scroll position back to the top. */
export function trackAnchor(el: HTMLElement, place: () => void): () => void {
  const onResize = () => place();
  const onScroll = (event: Event) => {
    const target = event.target;
    if (target instanceof Node && el.contains(target)) {
      return;
    }
    place();
  };
  window.addEventListener("resize", onResize, { passive: true });
  window.addEventListener("scroll", onScroll, { passive: true, capture: true });
  return () => {
    window.removeEventListener("resize", onResize);
    window.removeEventListener("scroll", onScroll, { capture: true });
  };
}

/* Gr8r placePop(): anchor below the trigger, flip above when the space below
   is too small, cap the height to the chosen side so it scrolls instead of
   running off the screen, and keep it inside the viewport with an 8px margin
   horizontally. */
export function placeAnchored(el: HTMLElement, anchor: HTMLElement, offset = 4): void {
  const rect = anchor.getBoundingClientRect();
  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;

  /* Measure the natural height before capping it on a previous placement. */
  el.style.maxHeight = "";
  if (rect.width > 0 && !el.style.width) {
    el.style.minWidth = Math.round(rect.width) + "px";
  }
  const naturalHeight = el.offsetHeight;
  const width = el.offsetWidth;

  const spaceBelow = viewportHeight - rect.bottom - offset;
  const spaceAbove = rect.top - offset;
  const above = naturalHeight > spaceBelow && spaceAbove > spaceBelow;
  const sideSpace = above ? spaceAbove : spaceBelow;
  const maxHeight = Math.max(0, sideSpace - 8);
  const height = Math.min(naturalHeight, maxHeight);

  let x = rect.left;
  if (x + width > viewportWidth - 8) {
    x = viewportWidth - width - 8;
  }
  if (x < 8) {
    x = 8;
  }

  el.style.maxHeight = maxHeight + "px";
  el.style.left = x + "px";
  el.style.top = (above ? rect.top - offset - height : rect.bottom + offset) + "px";
}

/* Chromium still closes the surrounding <dialog> on Escape even when a
   portaled menu holds focus, because the dialog's close request is handled by
   its own close watcher. A menu marks the Escape it consumed here; this then
   swallows the dialog's cancel event so only the menu closes. */
export function useEscapeGuard(): RefObject<boolean> {
  const consumed = useRef(false);
  useEffect(() => {
    const onKeyUp = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        consumed.current = false;
      }
    };
    const onCancel = (event: Event) => {
      if (consumed.current) {
        consumed.current = false;
        event.preventDefault();
        event.stopPropagation();
      }
    };
    document.addEventListener("keyup", onKeyUp, true);
    document.addEventListener("cancel", onCancel, true);
    return () => {
      document.removeEventListener("keyup", onKeyUp, true);
      document.removeEventListener("cancel", onCancel, true);
    };
  }, []);
  return consumed;
}

export function focusableElements(root: HTMLElement): HTMLElement[] {
  const selector =
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
    'textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';
  return Array.from(root.querySelectorAll<HTMLElement>(selector)).filter(
    (el) => !el.hidden && el.getAttribute("aria-hidden") !== "true"
  );
}
