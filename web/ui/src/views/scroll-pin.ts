import { useEffect } from "react";
import type { RefObject } from "react";

/* Keeps a bottom-anchored chat scroller pinned while `stick` is true, also
   when content grows after render (markdown, images, the turn process line
   opening, the composer shrinking). Owner report 2026-11-05: after Enter the
   chat did not scroll down. */
export function useStickToBottom(
  scrollRef: RefObject<HTMLElement | null>,
  stickRef: { current: boolean }
): void {
  useEffect(() => {
    const node = scrollRef.current;
    if (!node || typeof ResizeObserver === "undefined") {
      return undefined;
    }
    const pin = () => {
      if (stickRef.current) {
        node.scrollTop = node.scrollHeight;
      }
    };
    const observer = new ResizeObserver(pin);
    observer.observe(node);
    for (const child of Array.from(node.children)) {
      observer.observe(child);
    }
    const mutations = new MutationObserver(() => {
      for (const child of Array.from(node.children)) {
        observer.observe(child);
      }
      pin();
    });
    mutations.observe(node, { childList: true, subtree: true, characterData: true });
    return () => {
      observer.disconnect();
      mutations.disconnect();
    };
  });
}

/* Scrolls to the newest message now and on the next frame, and re-arms the
   pin. Called when the owner sends a message. */
export function pinToBottom(
  scrollRef: RefObject<HTMLElement | null>,
  stickRef: { current: boolean }
): void {
  stickRef.current = true;
  const scroll = () => {
    const node = scrollRef.current;
    if (node) {
      node.scrollTop = node.scrollHeight;
    }
  };
  scroll();
  if (typeof requestAnimationFrame === "function") {
    requestAnimationFrame(scroll);
  }
}
