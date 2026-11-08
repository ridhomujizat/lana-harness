import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

/* jsdom has no HTML Popover API, and its user-agent stylesheet keeps
   [popover] elements hidden until :popover-open matches — which it never does.
   Stand in for the API so the top-layer menus stay visible and queryable. */
if (typeof HTMLElement !== "undefined" && !HTMLElement.prototype.showPopover) {
  HTMLElement.prototype.showPopover = function (this: HTMLElement) {
    this.style.display = "block";
  };
  HTMLElement.prototype.hidePopover = function (this: HTMLElement) {
    this.style.display = "none";
  };
}
