/* Shared helpers for the ported Gr8r primitives. */

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

/* Gr8r placePop(): anchor below the trigger, flip up/left when it would
   overflow the viewport. */
export function placeAnchored(el: HTMLElement, anchor: HTMLElement, offset = 4): void {
  const rect = anchor.getBoundingClientRect();
  const width = el.offsetWidth;
  const height = el.offsetHeight;
  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;
  let x = rect.left;
  let y = rect.bottom + offset;
  if (x + width > viewportWidth - 8) {
    x = Math.max(8, rect.right - width);
  }
  if (y + height > viewportHeight - 8) {
    y = Math.max(8, rect.top - offset - height);
  }
  el.style.left = x + "px";
  el.style.top = y + "px";
}

export function focusableElements(root: HTMLElement): HTMLElement[] {
  const selector =
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
    'textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';
  return Array.from(root.querySelectorAll<HTMLElement>(selector)).filter(
    (el) => !el.hidden && el.getAttribute("aria-hidden") !== "true"
  );
}
