import { Fragment } from "react";
import { Ic, LanaMark } from "../ui/icons";
import type { Route } from "./router";

export const SIDEBAR_KEY = "lana.sidebar";

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

interface NavItem {
  key: string;
  label: string;
  icon: string;
  href: string;
}

interface NavSection {
  /* A missing title renders the items without a group heading. */
  title?: string;
  items: NavItem[];
}

/* Home and Chats sit under "Workspace" (owner request 2026-11-02, replacing
   the untitled group of REQ-ROOM-012); Observe and Configure group the rest.
   History is the existing Sessions page. Workflows (spec #33) sits in the
   Workspace group. Settings lives in the sidebar footer. */
export const NAV_SECTIONS: NavSection[] = [
  {
    title: "Workspace",
    items: [
      { key: "home", label: "Home", icon: "house", href: "#/home" },
      { key: "chats", label: "Chats", icon: "messages-square", href: "#/chats" },
      { key: "workflows", label: "Workflows", icon: "workflow", href: "#/workflows" },
    ],
  },
  {
    title: "Observe",
    items: [{ key: "sessions", label: "History", icon: "history", href: "#/sessions" }],
  },
  {
    title: "Configure",
    items: [
      { key: "agents", label: "Agents", icon: "bot", href: "#/agents" },
      { key: "knowledge", label: "Knowledge", icon: "book-open", href: "#/knowledge" },
      { key: "skills", label: "Skills", icon: "sparkles", href: "#/skills" },
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

function SItem({
  item,
  active,
  collapsed,
  onNavigate,
}: {
  item: NavItem;
  active: boolean;
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  return (
    <a
      className={"sitem" + (active ? " on" : "")}
      href={item.href}
      aria-current={active ? "page" : undefined}
      aria-label={item.label}
      data-tip={collapsed ? item.label : undefined}
      data-tip-pos={collapsed ? "right" : undefined}
      onClick={onNavigate}
    >
      <Ic name={item.icon} />
      <span className="trunc">{item.label}</span>
    </a>
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
  return (
    <nav id="sidebar-nav" className="side" aria-label="Main">
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
          {collapsed ? null : (
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
          )}
        </div>
      </div>

      <div className="side-scroll">
        {collapsed ? (
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
              active={route.section === item.key}
              collapsed={collapsed}
              onNavigate={onNavigate}
            />
          ));
          if (!section.title) {
            return <Fragment key={"section-" + index}>{items}</Fragment>;
          }
          return (
            <div className="sgroup" key={section.title}>
              <div className="sgroup-h">{section.title}</div>
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
