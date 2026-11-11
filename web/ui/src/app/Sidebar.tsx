import {
  Fragment,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type {
  FocusEvent as ReactFocusEvent,
  KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { createPortal } from "react-dom";
import { Ic, LanaMark } from "../ui/icons";
import { getKnowledge, REVIEW_CHANGED_EVENT } from "../api/knowledge";
import type { Route } from "./router";
import { dragRegion, isMacAppShell } from "./shell";

export const SIDEBAR_KEY = "lana.sidebar";
/* The Router tree's manual open/closed choice ("open" | "closed"). */
export const ROUTER_TREE_KEY = "lana.sidebar.router";

export interface DaemonCard {
  state: string;
  title: string;
  meta: string;
}

export function readCollapsed(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_KEY) === "collapsed";
  } catch {
    return false;
  }
}

export function saveCollapsed(collapsed: boolean): void {
  try {
    window.localStorage.setItem(
      SIDEBAR_KEY,
      collapsed ? "collapsed" : "expanded"
    );
  } catch {
    /* The sidebar still collapses for this page when storage is disabled. */
  }
}

export function readRouterTreeOpen(): boolean {
  try {
    return window.localStorage.getItem(ROUTER_TREE_KEY) === "open";
  } catch {
    return false;
  }
}

export function saveRouterTreeOpen(open: boolean): void {
  try {
    window.localStorage.setItem(ROUTER_TREE_KEY, open ? "open" : "closed");
  } catch {
    /* The tree still toggles for this page when storage is disabled. */
  }
}

interface NavItem {
  key: string;
  label: string;
  icon: string;
  href: string;
  /* The accessible name when the label alone repeats another item's. */
  ariaLabel?: string;
  /* A Router page: the item matches its own router tab. */
  tab?: string;
}

interface NavSection {
  /* A missing title renders the items without a group heading. */
  title?: string;
  /* Renders the Router tree as the section's first entry. */
  routerTree?: boolean;
  items: NavItem[];
}

/* The Router pages (spec 37, API-RTR-017 revision 2), in 9router's order.
   They sit under one collapsible "Router" entry at the top of Configure
   (owner request 2026-11-08: six items of their own made the menu look long). */
export const ROUTER_ITEMS: NavItem[] = [
  { key: "router-providers", label: "Providers", icon: "server", href: "#/router/providers", tab: "providers" },
  { key: "router-combos", label: "Combos", icon: "layers", href: "#/router/combos", tab: "combos" },
  { key: "router-usage", label: "Usage", icon: "chart-column", href: "#/router/usage", tab: "usage" },
  { key: "router-quota", label: "Quota", icon: "gauge", href: "#/router/quota", tab: "quota" },
  { key: "router-logs", label: "Logs", ariaLabel: "Router logs", icon: "logs", href: "#/router/logs", tab: "logs" },
  { key: "router-endpoint", label: "Endpoint", icon: "waypoints", href: "#/router/endpoint", tab: "endpoint" },
];

const ROUTER_HOME = "#/router/providers";

/* Home and Chats sit under "Workspace" (owner request 2026-11-02, replacing
   the untitled group of REQ-ROOM-012); Observe and Configure group the rest.
   History is the existing Sessions page. Workflows (spec #33) and Calendar
   (spec #35) sit in the Workspace group. Configure opens with the Router tree
   (ROUTER_ITEMS). Settings lives in the sidebar footer. */
export const NAV_SECTIONS: NavSection[] = [
  {
    title: "Workspace",
    items: [
      { key: "home", label: "Home", icon: "house", href: "#/home" },
      { key: "chats", label: "Chats", icon: "messages-square", href: "#/chats" },
      { key: "workflows", label: "Workflows", icon: "workflow", href: "#/workflows" },
      { key: "calendar", label: "Calendar", icon: "calendar-days", href: "#/calendar" },
    ],
  },
  {
    title: "Observe",
    items: [
      { key: "sessions", label: "History", icon: "history", href: "#/sessions" },
      { key: "logs", label: "Logs", icon: "scroll-text", href: "#/logs" },
      { key: "schedules", label: "Schedules", icon: "calendar-clock", href: "#/schedules" },
    ],
  },
  {
    title: "Configure",
    routerTree: true,
    items: [
      { key: "agents", label: "Agents", icon: "bot", href: "#/agents" },
      { key: "knowledge", label: "Knowledge", icon: "book-open", href: "#/knowledge" },
      { key: "skills", label: "Skills", icon: "sparkles", href: "#/skills" },
      { key: "tools", label: "Tools", icon: "package", href: "#/tools" },
      { key: "mcp", label: "MCP", icon: "plug", href: "#/mcp" },
      { key: "channels", label: "Channels", icon: "radio-tower", href: "#/channels" },
    ],
  },
];

const SETTINGS_ITEM: NavItem = {
  key: "settings",
  label: "Settings",
  icon: "settings",
  href: "#/settings/general",
};

function isActive(item: NavItem, route: Route): boolean {
  if (item.tab) {
    return route.section === "router" && (route.params || {}).tab === item.tab;
  }
  return route.section === item.key;
}

/* The mobile drawer (Gr8r responsive.css, max-width 900px) always shows the
   full sidebar, so the icon-only rail and its flyout only apply above it. */
const NARROW_QUERY = "(max-width: 900px)";

function subscribeNarrow(onChange: () => void): () => void {
  if (typeof window.matchMedia !== "function") {
    return () => {};
  }
  const query = window.matchMedia(NARROW_QUERY);
  query.addEventListener?.("change", onChange);
  return () => query.removeEventListener?.("change", onChange);
}

function readNarrow(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia(NARROW_QUERY).matches
  );
}

/* Spec 46 REQ-DKN-071: the number of pending review items, shown on the
   Knowledge item. It loads once, refreshes after a review decision and when
   the window regains focus, and stays 0 when the knowledge service is off or
   the call fails. */
function useReviewPending(): number {
  const [pending, setPending] = useState(0);
  useEffect(() => {
    let alive = true;
    const load = () => {
      getKnowledge()
        .then((status) => {
          if (alive) {
            setPending(Number(status?.review_pending) || 0);
          }
        })
        .catch(() => {
          if (alive) {
            setPending(0);
          }
        });
    };
    load();
    window.addEventListener(REVIEW_CHANGED_EVENT, load);
    window.addEventListener("focus", load);
    return () => {
      alive = false;
      window.removeEventListener(REVIEW_CHANGED_EVENT, load);
      window.removeEventListener("focus", load);
    };
  }, []);
  return pending;
}

function SItem({
  item,
  active,
  collapsed,
  onNavigate,
  count,
}: {
  item: NavItem;
  active: boolean;
  collapsed: boolean;
  onNavigate?: () => void;
  count?: number;
}) {
  return (
    <a
      className={"sitem" + (active ? " on" : "")}
      href={item.href}
      aria-current={active ? "page" : undefined}
      aria-label={item.ariaLabel || item.label}
      data-tip={collapsed ? item.label : undefined}
      data-tip-pos={collapsed ? "right" : undefined}
      onClick={onNavigate}
    >
      <Ic name={item.icon} />
      <span className="trunc">{item.label}</span>
      {count && count > 0 ? (
        <span className="ct dotc" aria-label={count + " to review"}>
          {count}
        </span>
      ) : null}
    </a>
  );
}

/* Expanded sidebar and mobile drawer: "Router" with a chevron, the pages
   below it as an indented tree (Gr8r .sub). The label navigates to Providers
   and opens the tree; the chevron only toggles it. */
function RouterTree({
  route,
  onNavigate,
}: {
  route: Route;
  onNavigate?: () => void;
}) {
  const inRouter = route.section === "router";
  const listId = "sidebar-router-tree";
  /* Open on any router page (entering one re-opens it); elsewhere the
     owner's saved choice, closed by default. Adjusted during render rather
     than in an effect so the first paint is already right. */
  const [tree, setTree] = useState(() => ({
    open: inRouter || readRouterTreeOpen(),
    inRouter,
  }));
  if (tree.inRouter !== inRouter) {
    setTree({ open: inRouter || readRouterTreeOpen(), inRouter });
  }
  const open = tree.open;

  function toggle() {
    const next = !open;
    setTree({ open: next, inRouter });
    saveRouterTreeOpen(next);
  }

  /* With the tree open the current child carries the highlight and the
     parent only a quiet ancestor state; closed, the parent stands in. */
  const parentClass =
    "sitem stree-parent" + (inRouter ? (open ? " anc" : " on") : "");

  return (
    <div className="stree">
      <div className="stree-row">
        <a
          className={parentClass}
          href={ROUTER_HOME}
          aria-label="Router"
          onClick={() => {
            setTree({ open: true, inRouter });
            onNavigate?.();
          }}
        >
          <Ic name="route" />
          <span className="trunc">Router</span>
        </a>
        <button
          type="button"
          className="ibtn ibtn-xs stree-chev"
          aria-label="Router pages"
          aria-expanded={open}
          aria-controls={listId}
          onClick={toggle}
        >
          <Ic
            name="chevron-right"
            size={14}
            className={"chev" + (open ? " open" : "")}
          />
        </button>
      </div>
      <ul
        id={listId}
        className={"sub stree-sub" + (open ? " open" : "")}
        aria-label="Router pages"
        hidden={!open}
      >
        {ROUTER_ITEMS.map((item) => {
          const active = isActive(item, route);
          return (
            <li key={item.key}>
              <a
                className={"sitem stree-item" + (active ? " on" : "")}
                href={item.href}
                aria-current={active ? "page" : undefined}
                aria-label={item.ariaLabel || item.label}
                onClick={onNavigate}
              >
                <span className="trunc">{item.label}</span>
              </a>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/* Delay before a hover-opened flyout closes, so the pointer can cross the
   gap between the rail icon and the flyout. */
const FLYOUT_CLOSE_MS = 150;

function menuItems(menu: HTMLElement | null): HTMLElement[] {
  return Array.from(menu?.querySelectorAll<HTMLElement>(".mi") || []);
}

/* Focus the current router page in the flyout, else its first page. */
function focusCurrent(menu: HTMLElement | null): void {
  const list = menuItems(menu);
  const current = list.find((el) => el.getAttribute("aria-current") === "page");
  (current || list[0])?.focus({ preventScroll: true });
}

/* Collapsed (icon-only) rail: the Router icon links to Providers; hovering or
   focusing it shows a small flyout of the router pages. Enter, Space or
   ArrowDown/ArrowRight open it with focus on a page; arrows move, Escape or
   ArrowLeft close it and return focus to the icon.

   Gr8r's Popover (ui/gr8r/overlays.tsx) always moves focus into the menu on
   open and anchors below its trigger, which would steal focus on a mere
   hover; the flyout reuses its .pop/.mi styling and keyboard model instead. */
function RouterFlyout({
  route,
  onNavigate,
}: {
  route: Route;
  onNavigate?: () => void;
}) {
  const inRouter = route.section === "router";
  const menuId =
    "sidebar-router-flyout-" + useId().replace(/[^A-Za-z0-9_-]/g, "");
  const anchorRef = useRef<HTMLAnchorElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<number | undefined>(undefined);
  /* Set when the keyboard opened the flyout: focus a page once it renders. */
  const focusOnOpen = useRef(false);
  /* Set while focus goes back to the icon, so that focus does not reopen. */
  const refocusing = useRef(false);
  const [open, setOpen] = useState(false);

  function cancelClose() {
    if (closeTimer.current !== undefined) {
      window.clearTimeout(closeTimer.current);
      closeTimer.current = undefined;
    }
  }

  function show() {
    cancelClose();
    setOpen(true);
  }

  function scheduleClose() {
    cancelClose();
    closeTimer.current = window.setTimeout(() => {
      closeTimer.current = undefined;
      setOpen(false);
    }, FLYOUT_CLOSE_MS);
  }

  function close(refocus: boolean) {
    cancelClose();
    setOpen(false);
    if (refocus) {
      refocusing.current = true;
      anchorRef.current?.focus({ preventScroll: true });
      refocusing.current = false;
    }
  }

  useEffect(() => {
    const timer = closeTimer;
    return () => window.clearTimeout(timer.current);
  }, []);

  /* Place the flyout beside the icon, moved up when it would run off the
     bottom of the viewport. */
  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    const menu = menuRef.current;
    const anchor = anchorRef.current;
    if (!menu || !anchor) {
      return;
    }
    const rect = anchor.getBoundingClientRect();
    const height = menu.offsetHeight;
    let top = rect.top - 4;
    if (top + height > window.innerHeight - 8) {
      top = Math.max(8, window.innerHeight - 8 - height);
    }
    menu.style.left = rect.right + 8 + "px";
    menu.style.top = top + "px";
    if (focusOnOpen.current) {
      focusOnOpen.current = false;
      focusCurrent(menu);
    }
  }, [open]);

  /* A pointer press outside the icon and the flyout closes it. */
  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (
        menuRef.current?.contains(target) ||
        anchorRef.current?.contains(target)
      ) {
        return;
      }
      window.clearTimeout(closeTimer.current);
      setOpen(false);
    };
    document.addEventListener("pointerdown", onPointer, true);
    return () => document.removeEventListener("pointerdown", onPointer, true);
  }, [open]);

  function onBlurWithin(event: ReactFocusEvent) {
    const next = event.relatedTarget as Node | null;
    if (menuRef.current?.contains(next) || anchorRef.current?.contains(next)) {
      return;
    }
    scheduleClose();
  }

  function onAnchorKeyDown(event: ReactKeyboardEvent<HTMLAnchorElement>) {
    const { key } = event;
    if (
      key === "Enter" ||
      key === " " ||
      key === "ArrowDown" ||
      key === "ArrowRight"
    ) {
      event.preventDefault();
      cancelClose();
      if (open) {
        focusCurrent(menuRef.current);
        return;
      }
      focusOnOpen.current = true;
      setOpen(true);
    } else if (key === "Escape" && open) {
      event.preventDefault();
      close(false);
    }
  }

  function onMenuKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    const { key } = event;
    if (key === "Escape" || key === "ArrowLeft") {
      event.preventDefault();
      close(true);
      return;
    }
    if (key === "Tab") {
      /* The menu is portalled to the end of the page: leave it from the
         icon's place in the tab order. Tab then moves on past the icon. */
      if (event.shiftKey) {
        event.preventDefault();
      }
      close(true);
      return;
    }
    if (
      key !== "ArrowDown" &&
      key !== "ArrowUp" &&
      key !== "Home" &&
      key !== "End"
    ) {
      return;
    }
    const list = menuItems(menuRef.current);
    if (list.length === 0) {
      return;
    }
    event.preventDefault();
    const index = list.indexOf(document.activeElement as HTMLElement);
    let next: number;
    if (key === "ArrowDown") {
      next = index < 0 ? 0 : (index + 1) % list.length;
    } else if (key === "ArrowUp") {
      next = index <= 0 ? list.length - 1 : index - 1;
    } else if (key === "Home") {
      next = 0;
    } else {
      next = list.length - 1;
    }
    list[next]?.focus({ preventScroll: true });
  }

  return (
    <>
      <a
        ref={anchorRef}
        className={"sitem stree-parent" + (inRouter ? " on" : "")}
        href={ROUTER_HOME}
        aria-label="Router"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => {
          close(false);
          onNavigate?.();
        }}
        onMouseEnter={show}
        onMouseLeave={scheduleClose}
        onFocus={() => {
          if (!refocusing.current) {
            show();
          }
        }}
        onBlur={onBlurWithin}
        onKeyDown={onAnchorKeyDown}
      >
        <Ic name="route" />
        <span className="trunc">Router</span>
      </a>
      {open
        ? createPortal(
            <div
              ref={menuRef}
              id={menuId}
              className="pop side-fly enter"
              role="menu"
              aria-label="Router"
              style={{ left: 0, top: 0 }}
              onMouseEnter={cancelClose}
              onMouseLeave={scheduleClose}
              onBlur={onBlurWithin}
              onKeyDown={onMenuKeyDown}
            >
              <div className="side-fly-h" aria-hidden="true">
                Router
              </div>
              {ROUTER_ITEMS.map((item) => {
                const active = isActive(item, route);
                return (
                  <a
                    key={item.key}
                    role="menuitem"
                    tabIndex={-1}
                    className={"mi" + (active ? " on" : "")}
                    href={item.href}
                    aria-current={active ? "page" : undefined}
                    aria-label={item.ariaLabel || item.label}
                    onClick={() => {
                      close(false);
                      onNavigate?.();
                    }}
                  >
                    <span className="trunc">{item.label}</span>
                  </a>
                );
              })}
            </div>,
            document.body
          )
        : null}
    </>
  );
}

export interface SidebarProps {
  route: Route;
  collapsed: boolean;
  onToggle: () => void;
  onLogout: () => void;
  onNavigate?: () => void;
  daemon: DaemonCard;
}

export function Sidebar({
  route,
  collapsed,
  onToggle,
  onLogout,
  onNavigate,
  daemon,
}: SidebarProps) {
  const daemonTip = daemon.title + " · " + daemon.meta;
  const narrow = useSyncExternalStore(subscribeNarrow, readNarrow, () => false);
  /* The icon-only rail; the mobile drawer keeps the full tree. */
  const rail = collapsed && !narrow;
  const reviewPending = useReviewPending();
  /* Spec 52: in the Lana.app window the traffic lights and the collapse
     toggle share the top row, and the brand moves below it. */
  const shell = isMacAppShell();
  const collapseButton = (
    <button
      type="button"
      className="ibtn ibtn-sm hide-m"
      aria-label="Collapse sidebar"
      aria-expanded={true}
      aria-controls="sidebar-nav"
      title="Collapse sidebar"
      onClick={onToggle}
    >
      <Ic name="panel-left" size={15} />
    </button>
  );
  return (
    <nav id="sidebar-nav" className="side" aria-label="Main">
      {shell ? (
        <div className="side-chrome" {...dragRegion(shell)}>
          {collapsed ? null : collapseButton}
        </div>
      ) : null}
      <div className="side-top">
        <div className="row" style={{ gap: 2 }}>
          <a
            className="ws grow"
            href="#/home"
            aria-label="Lana home"
            data-tip={collapsed ? daemonTip : undefined}
            data-tip-pos={collapsed ? "right" : undefined}
            onClick={onNavigate}
          >
            <LanaMark size={24} />
            <span className="ws-text grow">
              <span className="ws-name lana-word trunc">LΛNΛ</span>
              <span className="ws-sub trunc" title={daemonTip}>
                {daemon.meta}
              </span>
            </span>
          </a>
          {collapsed || shell ? null : collapseButton}
        </div>
      </div>

      <div className="side-scroll">
        {/* Spec 52: in the Lana.app window the topbar carries the expand
            toggle next to the traffic lights, so the rail does not. */}
        {collapsed && !shell ? (
          <button
            type="button"
            className="sitem"
            aria-label="Expand sidebar"
            aria-expanded={false}
            aria-controls="sidebar-nav"
            data-tip="Expand sidebar"
            data-tip-pos="right"
            onClick={onToggle}
          >
            <Ic name="panel-left" size={16} />
          </button>
        ) : null}

        {NAV_SECTIONS.map((section, index) => {
          const items = section.items.map((item) => (
            <SItem
              key={item.key}
              item={item}
              active={isActive(item, route)}
              collapsed={collapsed}
              onNavigate={onNavigate}
              count={item.key === "knowledge" ? reviewPending : undefined}
            />
          ));
          const tree = !section.routerTree ? null : rail ? (
            <RouterFlyout route={route} onNavigate={onNavigate} />
          ) : (
            <RouterTree route={route} onNavigate={onNavigate} />
          );
          if (!section.title) {
            return (
              <Fragment key={"section-" + index}>
                {tree}
                {items}
              </Fragment>
            );
          }
          return (
            <div className="sgroup" key={section.title}>
              <div className="sgroup-h">{section.title}</div>
              {tree}
              {items}
            </div>
          );
        })}
      </div>

      <div className="side-bot">
        <SItem
          item={SETTINGS_ITEM}
          active={route.section === SETTINGS_ITEM.key}
          collapsed={collapsed}
          onNavigate={onNavigate}
        />
        <div className="row">
          <span
            className="av md"
            aria-hidden="true"
            style={{ background: "var(--surface-3)", color: "var(--text-2)" }}
          >
            OW
          </span>
          <span className="trunc grow">Owner</span>
          <button
            type="button"
            className="ibtn ibtn-sm"
            aria-label="Log out"
            title="Log out"
            onClick={onLogout}
          >
            <Ic name="log-out" size={15} />
          </button>
        </div>
      </div>
    </nav>
  );
}
