import { Icon } from "../ui/ui";
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

const NAV_ITEMS = [
  { key: "home", label: "Home", icon: "home", href: "#/home" },
  { key: "sessions", label: "Sessions", icon: "sessions", href: "#/sessions" },
  { key: "agents", label: "Agents", icon: "agents", href: "#/agents" },
  { key: "skills", label: "Skills", icon: "skills", href: "#/skills" },
  { key: "mcp", label: "MCP", icon: "mcp", href: "#/mcp" },
  { key: "channels", label: "Channels", icon: "channels", href: "#/channels" },
  {
    key: "settings",
    label: "Settings",
    icon: "settings",
    href: "#/settings/general",
  },
];

function SidebarToggle({
  collapsed,
  onToggle,
}: {
  collapsed: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      className="icon-button sidebar-toggle"
      aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      aria-expanded={!collapsed}
      aria-controls="sidebar-nav"
      title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      onClick={onToggle}
    >
      <Icon name={collapsed ? "sidebar-expand" : "sidebar-collapse"} />
    </button>
  );
}

export interface SidebarProps {
  route: Route;
  collapsed: boolean;
  onToggle: () => void;
  onLogout: () => void;
  daemon: DaemonCard;
}

export function Sidebar({
  route,
  collapsed,
  onToggle,
  onLogout,
  daemon,
}: SidebarProps) {
  return (
    <aside className="sidebar" aria-label="Sidebar">
      <div className="sidebar-top">
        <div className="sidebar-logo">
          <a className="logo-lockup" href="#/home" aria-label="lana">
            <img
              className="logo-mark"
              src="/assets/icons/logo-mark.svg"
              alt=""
              aria-hidden="true"
            />
            {collapsed ? null : <span className="logo-word">LΛNΛ</span>}
          </a>
          {collapsed ? null : (
            <SidebarToggle collapsed={collapsed} onToggle={onToggle} />
          )}
        </div>

        <nav id="sidebar-nav" className="sidebar-nav" aria-label="Main">
          {NAV_ITEMS.map((item) => {
            const active = route.section === item.key;
            return (
              <a
                key={item.key}
                className="nav-item"
                href={item.href}
                aria-current={active ? "page" : undefined}
                aria-label={collapsed ? item.label : undefined}
                title={collapsed ? item.label : undefined}
              >
                <Icon name={item.icon} />
                <span className="nav-label">{item.label}</span>
              </a>
            );
          })}
        </nav>
      </div>

      <div className="sidebar-bottom">
        {collapsed ? (
          <SidebarToggle collapsed={collapsed} onToggle={onToggle} />
        ) : null}

        <div
          className={
            "daemon-card" +
            (daemon.state === "unavailable" ? " is-unavailable" : "")
          }
          title={collapsed ? daemon.title + " · " + daemon.meta : undefined}
        >
          <div className="daemon-title">
            <span
              className={
                "status-dot" +
                (daemon.state === "unavailable" ? " is-unavailable" : "")
              }
              aria-hidden="true"
            ></span>
            <span className="daemon-label">{daemon.title}</span>
          </div>
          <p className="daemon-meta">{daemon.meta}</p>
        </div>

        {collapsed ? null : <hr className="sidebar-divider" />}

        <div className="owner-row">
          {collapsed ? (
            <button
              type="button"
              className="owner-avatar avatar-button"
              aria-label="Log out"
              title="Log out"
              onClick={onLogout}
            >
              OW
            </button>
          ) : (
            <>
              <span className="owner-avatar" aria-hidden="true">
                OW
              </span>
              <div className="owner-text">
                <div className="owner-name">Owner</div>
                <div className="owner-meta">This browser</div>
              </div>
              <button
                type="button"
                className="icon-button owner-logout"
                aria-label="Log out"
                title="Log out"
                onClick={onLogout}
              >
                <Icon name="logout" />
              </button>
            </>
          )}
        </div>
      </div>
    </aside>
  );
}
