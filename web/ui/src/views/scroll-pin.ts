import { useEffect, useRef } from "react";
import type { RefObject } from "react";

/* Keeps a bottom-anchored chat scroller pinned while `stick` is true, also
   when content grows after render (markdown, images, the turn process line
   opening, the composer shrinking). Owner report 2026-11-05: after Enter the
   chat did not scroll down. */
export function useStickToBottom(
  scrollRef: RefObject<HTMLElement | null>,
  stickRef: { current: boolean }
): void {
  /* The scroller node the observers are attached to, and how to tear them down.
     Setting them up once per node keeps a re-render from rebuilding the whole
     timeline's observers (perf: chat load). */
  const attachedRef = useRef<HTMLElement | null>(null);
  const teardownRef = useRef<(() => void) | null>(null);
  const frameRef = useRef<number | null>(null);

  /* Runs after every render, but only touches the DOM when the scroller node
     changed (for example once a loading state gives way to the list); the
     comparison itself is free. */
  useEffect(() => {
    const node = scrollRef.current;
    if (node === attachedRef.current) {
      return;
    }
    if (teardownRef.current) {
      teardownRef.current();
      teardownRef.current = null;
    }
    attachedRef.current = node;
    if (
      !node ||
      typeof ResizeObserver === "undefined" ||
      typeof MutationObserver === "undefined"
    ) {
      return;
    }
    const pin = () => {
      if (stickRef.current) {
        node.scrollTop = node.scrollHeight;
      }
    };
    /* At most one scrollHeight read/write per frame; call pin() directly where
       requestAnimationFrame is missing (jsdom). */
    const pinSoon = () => {
      if (typeof requestAnimationFrame !== "function") {
        pin();
        return;
      }
      if (frameRef.current !== null) {
        return;
      }
      frameRef.current = requestAnimationFrame(() => {
        frameRef.current = null;
        pin();
      });
    };
    const observer = new ResizeObserver(pinSoon);
    observer.observe(node);
    for (const child of Array.from(node.children)) {
      observer.observe(child);
    }
    const mutations = new MutationObserver(() => {
      for (const child of Array.from(node.children)) {
        observer.observe(child);
      }
      pinSoon();
    });
    mutations.observe(node, { childList: true, subtree: true, characterData: true });
    teardownRef.current = () => {
      observer.disconnect();
      mutations.disconnect();
      if (frameRef.current !== null && typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(frameRef.current);
        frameRef.current = null;
      }
    };
  });

  /* Disconnect on unmount. */
  useEffect(
    () => () => {
      if (teardownRef.current) {
        teardownRef.current();
        teardownRef.current = null;
      }
      attachedRef.current = null;
    },
    []
  );
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
