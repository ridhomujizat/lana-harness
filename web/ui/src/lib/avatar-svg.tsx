import { createElement } from "react";
import type { ReactElement } from "react";

/* Generated agent avatars (spec 43). The server stores a sanitized SVG of a
   solid blob with two white eye holes. The dashboard never injects that markup:
   it parses the string and rebuilds React elements from a small allowlist, so
   anything unexpected falls back to the letter avatar. */

const SVG_NS = "http://www.w3.org/2000/svg";

const ELEMENTS = new Set(["svg", "g", "path", "ellipse", "circle", "rect"]);

const NUMBER = /^[-+]?(\d+\.?\d*|\.\d+)(e[-+]?\d+)?%?$/i;
const NUMBER_LIST = /^[-+0-9.eE\s,]+$/;
const PATH_DATA = /^[MmLlHhVvCcSsQqTtAaZz0-9eE.,+\-\s]+$/;
const TRANSFORM = /^[a-zA-Z0-9.,+\-\s()]+$/;
const TRANSFORM_WORDS = new Set([
  "rotate",
  "translate",
  "scale",
  "matrix",
  "skewx",
  "skewy",
]);

const MAX_LENGTH = 20000;
const MAX_NODES = 64;

function validTransform(value: string): boolean {
  if (!TRANSFORM.test(value)) {
    return false;
  }
  const words = value.match(/[a-zA-Z]+/g) || [];
  return words.every(
    (word) => TRANSFORM_WORDS.has(word.toLowerCase()) || /^e$/i.test(word)
  );
}

function validFill(value: string): boolean {
  const text = value.trim().toLowerCase();
  return text === "currentcolor" || text === "#ffffff" || text === "#fff";
}

/* The canonical value of one allowed attribute, or null when the attribute
   or its value is not allowed. */
function attributeValue(name: string, value: string, root: boolean): string | null {
  switch (name) {
    case "viewBox":
      return root && NUMBER_LIST.test(value) ? value : null;
    case "d":
      return PATH_DATA.test(value) ? value : null;
    case "cx":
    case "cy":
    case "rx":
    case "ry":
    case "r":
    case "x":
    case "y":
    case "width":
    case "height":
      return NUMBER.test(value.trim()) ? value.trim() : null;
    case "fill": {
      if (!validFill(value)) {
        return null;
      }
      return value.trim().toLowerCase() === "currentcolor" ? "currentColor" : "#ffffff";
    }
    case "transform":
      return validTransform(value) ? value : null;
    default:
      return null;
  }
}

interface BuildState {
  nodes: number;
  key: number;
}

function build(node: Element, root: boolean, state: BuildState): ReactElement | null {
  state.nodes += 1;
  if (state.nodes > MAX_NODES) {
    return null;
  }
  const tag = node.localName;
  if (node.namespaceURI !== SVG_NS || !ELEMENTS.has(tag)) {
    return null;
  }
  if ((tag === "svg") !== root) {
    return null;
  }
  const props: Record<string, string | boolean | number> = {};
  for (const attribute of Array.from(node.attributes)) {
    const name = attribute.name;
    /* The namespace declaration: inline SVG in HTML needs none, so it is
       checked and dropped. Some engines report it as ":xmlns". */
    if (attribute.localName === "xmlns" || name === "xmlns" || name === ":xmlns") {
      if (attribute.value !== SVG_NS) {
        return null;
      }
      continue;
    }
    /* The root size is the container's; the server writes none. */
    if (root && (name === "width" || name === "height")) {
      continue;
    }
    const value = attributeValue(name, attribute.value, root);
    if (value === null) {
      return null;
    }
    props[name] = value;
  }
  const children: ReactElement[] = [];
  for (const child of Array.from(node.childNodes)) {
    if (child.nodeType === 1) {
      const element = build(child as Element, false, state);
      if (!element) {
        return null;
      }
      children.push(element);
    } else if (child.nodeType === 3 || child.nodeType === 4) {
      if ((child.textContent || "").trim() !== "") {
        return null;
      }
    } else if (child.nodeType !== 8) {
      /* Processing instructions, entities and the like are never allowed. */
      return null;
    }
  }
  state.key += 1;
  props.key = state.key;
  if (root) {
    if (!props.viewBox) {
      props.viewBox = "0 0 100 100";
    }
    props.width = "100%";
    props.height = "100%";
    props["aria-hidden"] = "true";
    props.focusable = "false";
    props.className = "av-svg";
  }
  return createElement(tag, props, ...children);
}

function parse(svg: string): ReactElement | null {
  const text = String(svg || "").trim();
  if (!text || text.length > MAX_LENGTH || typeof DOMParser === "undefined") {
    return null;
  }
  /* A DOCTYPE could declare entities; the canonical markup never has one. */
  if (/<!DOCTYPE|<!ENTITY|<\?/i.test(text)) {
    return null;
  }
  let doc: Document;
  try {
    doc = new DOMParser().parseFromString(text, "image/svg+xml");
  } catch {
    return null;
  }
  if (doc.getElementsByTagName("parsererror").length > 0) {
    return null;
  }
  let root: Element | null = doc.documentElement;
  /* An engine without an XML parser hands back an HTML document. Its HTML
     parser still puts <svg> in the SVG namespace, so take the lone <svg> of
     the body; the allowlist below checks it the same way. */
  if (root && root.localName === "html") {
    const body = doc.body;
    const head = doc.head;
    const children = body ? Array.from(body.children) : [];
    const strayText = body
      ? (body.textContent || "").trim() !== (children[0]?.textContent || "").trim()
      : true;
    root =
      children.length === 1 && !strayText && (!head || head.children.length === 0)
        ? children[0] || null
        : null;
  }
  if (!root) {
    return null;
  }
  const element = build(root, true, { nodes: 0, key: 0 });
  if (!element) {
    return null;
  }
  return element;
}

const cache = new Map<string, ReactElement | null>();
const CACHE_LIMIT = 300;

/* parseAvatarSvg turns a stored avatar SVG into React elements, or null when
   the string is empty or breaks the allowlist. Results are memoized per
   string so a long list renders each shape once. */
export function parseAvatarSvg(svg: string | null | undefined): ReactElement | null {
  const text = String(svg || "");
  if (!text) {
    return null;
  }
  if (cache.has(text)) {
    return cache.get(text) ?? null;
  }
  const element = parse(text);
  if (cache.size >= CACHE_LIMIT) {
    const first = cache.keys().next();
    if (!first.done) {
      cache.delete(first.value);
    }
  }
  cache.set(text, element);
  return element;
}

/* ------------------------------------------------------------------ */
/* Agent colors                                                       */
/* ------------------------------------------------------------------ */

/* The six agent color tokens of REQ-ROOM-009 as Gr8r tokens. Since spec 43 an
   agent color may also be a "#rrggbb" hex, which passes through as is. */
export const AGENT_COLOR_TOKENS: Record<string, string> = {
  brand: "var(--violet)",
  success: "var(--green)",
  warning: "var(--amber)",
  error: "var(--red)",
  blue: "var(--blue)",
  gray: "var(--gray)",
};

const HEX = /^#([0-9a-f]{6}|[0-9a-f]{3})$/i;

export function isHexColor(value: unknown): value is string {
  return typeof value === "string" && HEX.test(value.trim());
}

export function isColorToken(value: unknown): value is string {
  return typeof value === "string" && Object.prototype.hasOwnProperty.call(AGENT_COLOR_TOKENS, value);
}

/* The CSS color of an agent color: a token maps onto its Gr8r variable, a hex
   passes through, anything else uses the fallback. */
export function agentCssColor(
  color: string | null | undefined,
  fallback = "var(--violet)"
): string {
  const text = String(color || "").trim();
  if (isHexColor(text)) {
    return text.toLowerCase();
  }
  if (isColorToken(text)) {
    return AGENT_COLOR_TOKENS[text] || fallback;
  }
  return fallback;
}
