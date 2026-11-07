import { Fragment, useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import { authSession, logout as apiLogout } from "../api/auth";
import { setCSRF, setUnauthorizedHandler } from "../api/client";
import { daemonInfo as fetchDaemonInfo } from "../api/daemon";
import type { DaemonInfo } from "../api/daemon";
import {
  applyTheme,
  readTheme,
  saveTheme,
  themeLabel,
  watchSystemTheme,
} from "./theme";
import type { ThemeChoice } from "./theme";
import { DEFAULT_ROUTE, parseRoute } from "./router";
import type { Route } from "./router";
import { Sidebar, readCollapsed, saveCollapsed } from "./Sidebar";
import type { DaemonCard } from "./Sidebar";
import { useAnnounce } from "../ui/ui";
import { Ic } from "../ui/icons";
import { LoginLoading, LoginPage } from "../views/login";
import { WelcomePage } from "../views/welcome";
import { ForgotPage } from "../views/forgot";
import { HomePage } from "../views/home";
import { ChatsPage } from "../views/chats";
import { WorkflowsPage } from "../views/workflows";
import { LogsPage } from "../views/logs";
import { ToolsPage } from "../views/tools";
import { ToolDetailPage } from "../views/tool-detail";
import { SchedulesPage } from "../views/schedules";
import { CalendarPage } from "../views/calendar";
import { WorkflowPage } from "../views/workflow-page";
import { NotFoundPage } from "../views/placeholder";
import { SettingsGeneralPage } from "../views/settings-general";
import { SessionsWorkspace } from "../views/sessions";
import {
  SettingsProvidersPage,
  SettingsSecretsPage,
  SettingsConfigPage,
  SettingsAuditPage,
} from "../views/settings-pages";
import { AgentsPage } from "../views/agents";
import { AgentDetailPage } from "../views/agent-detail";
import { SkillsPage, SkillEditorPage } from "../views/skills";
import { KnowledgePage } from "../views/knowledge";
import { McpPage, McpDetailPage } from "../views/mcp";
import { SetupWizard, setupNeeded } from "../views/setup";
import { ChannelsPage, TelegramPage } from "../views/channels";
import { UpdateCard } from "../views/update-card";

type AuthState = "checking" | "loggedIn" | "loggedOut";

const initialTheme = readTheme();
applyTheme(initialTheme);

function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() =>
    parseRoute(window.location.hash)
  );
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.hash));
    window.addEventListener("hashchange", update);
    if (!window.location.hash || window.location.hash === "#") {
      window.location.hash = DEFAULT_ROUTE;
    } else {
      update();
    }
    return () => window.removeEventListener("hashchange", update);
  }, []);
  return route;
}

function routeContent(
  route: Route,
  theme: ThemeChoice,
  onThemeChange: (choice: ThemeChoice) => void
): ReactNode {
  const params = route.params || {};
  switch (route.view) {
    case "login":
    case "welcome":
    case "forgot":
      return (
        <main id="main" className="page">
          <p className="muted">Opening sessions.</p>
        </main>
      );
    case "home":
      return <HomePage />;
    case "chats":
      return <ChatsPage />;
    case "chat":
      return <ChatsPage roomName={params.name} />;
    case "logs":
      return <LogsPage />;
    case "tools":
      return <ToolsPage />;
    case "tool":
      return <ToolDetailPage key={params.id} id={params.id || ""} />;
    case "schedules":
      return <SchedulesPage />;
    case "calendar":
      return <CalendarPage params={route.params || {}} />;
    case "workflows":
      return <WorkflowsPage />;
    case "workflow-new":
      return <WorkflowPage key="new" tab="editor" />;
    case "workflow":
      return <WorkflowPage key={params.id} id={params.id} tab="editor" />;
    case "workflow-runs":
      return <WorkflowPage key={params.id} id={params.id} tab="runs" />;
    case "workflow-run":
      return <WorkflowPage key={params.id} id={params.id} tab="runs" run={params.run} />;
    case "workflow-settings":
      return <WorkflowPage key={params.id} id={params.id} tab="settings" />;
    case "sessions":
      return <SessionsWorkspace />;
    case "new-session":
      return <SessionsWorkspace newSession={true} />;
    case "session":
      return <SessionsWorkspace selectedId={params.id} />;
    case "agents":
      return <AgentsPage />;
    case "agent":
      return <AgentDetailPage name={params.name} tab={params.tab} />;
    case "skills":
      return <SkillsPage />;
    case "knowledge":
      return <KnowledgePage />;
    case "new-skill":
      return <SkillEditorPage isNew={true} />;
    case "skill":
      return (
        <SkillEditorPage
          key={(params.scope || "") + "/" + (params.name || "")}
          scope={params.scope}
          name={params.name}
        />
      );
    case "mcp":
      return <McpPage />;
    case "mcp-detail":
      return <McpDetailPage key={params.name} name={params.name || ""} />;
    case "channels":
      return <ChannelsPage />;
    case "telegram":
      return <TelegramPage />;
    case "settings":
      switch (params.tab) {
        case "general":
          return (
            <SettingsGeneralPage theme={theme} onThemeChange={onThemeChange} />
          );
        case "providers":
          return <SettingsProvidersPage />;
        case "secrets":
          return <SettingsSecretsPage />;
        case "config":
          return <SettingsConfigPage />;
        case "audit":
          return <SettingsAuditPage />;
        default:
          return <NotFoundPage path={route.path} />;
      }
    case "setup":
      return <SetupWizard key={params.step} step={params.step} />;
    default:
      return <NotFoundPage path={route.path} />;
  }
}

interface Crumb {
  label: string;
  /* Absent on the trail's last crumb, which is the current page. */
  href?: string;
}

function titleCase(value: string): string {
  return value ? value.charAt(0).toUpperCase() + value.slice(1) : value;
}

/* The topbar trail, e.g. "Agents / cody", "Settings / Providers". */
export function crumbsFor(route: Route): Crumb[] {
  const params = route.params || {};
  switch (route.view) {
    case "home":
      return [{ label: "Home" }];
    case "chats":
      return [{ label: "Chats" }];
    case "chat":
      return [
        { label: "Chats", href: "#/chats" },
        { label: params.name || "Chat" },
      ];
    case "logs":
      return [{ label: "Logs" }];
    case "tools":
      return [{ label: "Tools" }];
    case "tool":
      return [{ label: "Tools", href: "#/tools" }, { label: params.id || "Tool" }];
    case "schedules":
      return [{ label: "Schedules" }];
    case "calendar":
      return [{ label: "Calendar" }];
    case "workflows":
      return [{ label: "Workflows" }];
    case "workflow-new":
      return [{ label: "Workflows", href: "#/workflows" }, { label: "New workflow" }];
    case "workflow":
    case "workflow-runs":
    case "workflow-run":
    case "workflow-settings":
      return [{ label: "Workflows", href: "#/workflows" }, { label: params.id || "Workflow" }];
    case "sessions":
    case "new-session":
    case "session":
      return [{ label: "History" }];
    case "agents":
      return [{ label: "Agents" }];
    case "agent":
      return [
        { label: "Agents", href: "#/agents" },
        { label: params.name || "Agent" },
      ];
    case "skills":
      return [{ label: "Skills" }];
    case "new-skill":
      return [
        { label: "Skills", href: "#/skills" },
        { label: "New skill" },
      ];
    case "skill":
      return [
        { label: "Skills", href: "#/skills" },
        { label: params.name || "Skill" },
      ];
    case "knowledge":
      return [{ label: "Knowledge" }];
    case "mcp":
      return [{ label: "MCP" }];
    case "mcp-detail":
      return [
        { label: "MCP", href: "#/mcp" },
        { label: params.name || "Server" },
      ];
    case "channels":
      return [{ label: "Channels" }];
    case "telegram":
      return [
        { label: "Channels", href: "#/channels" },
        { label: "Telegram" },
      ];
    case "settings":
      return [
        { label: "Settings", href: "#/settings/general" },
        { label: titleCase(params.tab || "general") },
      ];
    default:
      return [{ label: "Not found" }];
  }
}

function formatUptime(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) {
    return "uptime unknown";
  }
  const totalMinutes = Math.floor(milliseconds / 60000);
  if (totalMinutes < 1) {
    return "up just now";
  }
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours > 0) {
    return "up " + hours + " h " + minutes + " min";
  }
  return "up " + minutes + " min";
}

type DaemonState = DaemonInfo | { unavailable: true };

function daemonCard(info: DaemonState | null): DaemonCard {
  if (!info) {
    return {
      state: "loading",
      title: "Checking daemon…",
      meta: window.location.host,
    };
  }
  if ("unavailable" in info) {
    return {
      state: "unavailable",
      title: "Daemon status unavailable",
      meta: window.location.host,
    };
  }
  const address = info.addr || window.location.host;
  return {
    state: "running",
    title: "Daemon running",
    meta: address + " · " + formatUptime(info.uptime_ms),
  };
}

/* The remount key of the content area. Switching tabs inside one page (agent
   profile, workflow, settings) or picking another item in a list + detail
   page (chats, history) keeps the page mounted, so it neither reloads nor
   replays the page-enter animation (owner report 2026-11-05). */
export function pageKey(route: Route): string {
  const params = route.params || {};
  switch (route.view) {
    case "agent":
      return "agent:" + (params.name || "");
    case "workflow":
    case "workflow-runs":
    case "workflow-run":
    case "workflow-settings":
      return "workflow:" + (params.id || "");
    case "settings":
      return "settings";
    case "chats":
    case "chat":
      return "chats";
    case "sessions":
    case "new-session":
    case "session":
      return "sessions";
    default:
      return route.path;
  }
}

export function App() {

  const route = useRoute();
  /* The page-enter animation plays once per page: after it, the content is
     marked settled so a tab panel or list item that mounts later shows at
     once (owner report 2026-11-05). */
  const currentPage = pageKey(route);
  const settleRoute = useCallback((node: HTMLDivElement | null) => {
    if (!node) {
      return;
    }
    window.setTimeout(() => node.classList.add("settled"), 700);
  }, []);
  const announce = useAnnounce();
  const [auth, setAuth] = useState<AuthState>("checking");
  const [theme, setTheme] = useState<ThemeChoice>(initialTheme);
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const [mnav, setMnav] = useState(false);
  const [daemonInfo, setDaemonInfo] = useState<DaemonState | null>(null);

  useEffect(() => {
    applyTheme(theme);
    if (theme !== "system") {
      return undefined;
    }
    return watchSystemTheme(() => {
      applyTheme("system");
    });
  }, [theme]);

  useEffect(() => {
    let alive = true;
    setUnauthorizedHandler(() => {
      setCSRF("");
      setAuth((current) => {
        if (current === "loggedIn") {
          announce("Your session expired. Please log in again.");
        }
        return "loggedOut";
      });
    });

    authSession()
      .then((session) => {
        if (!alive) {
          return;
        }
        setCSRF(session.csrf);
        setAuth("loggedIn");
      })
      .catch(() => {
        if (!alive) {
          return;
        }
        setCSRF("");
        setAuth("loggedOut");
      });

    return () => {
      alive = false;
      setUnauthorizedHandler(null);
    };
  }, [announce]);

  useEffect(() => {
    if (
      auth === "loggedIn" &&
      (route.view === "login" ||
        route.view === "welcome" ||
        route.view === "forgot")
    ) {
      window.location.hash = DEFAULT_ROUTE;
    }
  }, [auth, route.view]);

  useEffect(() => {
    if (
      auth === "loggedIn" &&
      route.view === "setup" &&
      route.params.step === "unlock"
    ) {
      /* Already unlocked: step 1 is done. React requires this hash change in
         an effect instead of the render body. */
      window.location.hash = DEFAULT_ROUTE;
    }
  }, [auth, route.view, route.params.step]);

  useEffect(() => {
    if (auth !== "loggedIn") {
      return undefined;
    }
    let alive = true;
    /* REQ-WEB-032: a fresh install continues with the setup wizard. */
    setupNeeded()
      .then((needed) => {
        if (alive && needed && !window.location.hash.startsWith("#/setup/")) {
          window.location.hash = "#/setup/provider";
        }
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [auth]);

  useEffect(() => {
    if (auth !== "loggedIn") {
      return undefined;
    }
    let alive = true;
    fetchDaemonInfo()
      .then((info) => {
        if (alive) {
          setDaemonInfo(info);
        }
      })
      .catch(() => {
        if (alive) {
          setDaemonInfo({ unavailable: true });
        }
      });
    return () => {
      alive = false;
    };
  }, [auth]);

  function changeTheme(choice: ThemeChoice) {
    saveTheme(choice);
    setTheme(choice);
    announce("Theme set to " + themeLabel(choice) + ".");
  }

  function toggleSidebar() {
    const next = !collapsed;
    saveCollapsed(next);
    setCollapsed(next);
    announce(next ? "Sidebar collapsed." : "Sidebar expanded.");
  }

  function authenticated(csrf: string) {
    setCSRF(csrf);
    setAuth("loggedIn");
    if (window.location.hash !== DEFAULT_ROUTE) {
      window.location.hash = DEFAULT_ROUTE;
    }
  }

  async function logout() {
    try {
      await apiLogout();
    } catch {
      /* The cookie may already be gone; clear the page either way. */
    }
    setCSRF("");
    setAuth("loggedOut");
    window.location.hash = "#/login";
    announce("Signed out.");
  }

  if (auth === "checking") {
    return <LoginLoading />;
  }

  if (auth !== "loggedIn") {
    if (route.view === "welcome") {
      return (
        <WelcomePage code={route.params.code} onLoggedIn={authenticated} />
      );
    }
    if (route.view === "forgot") {
      return <ForgotPage onLoggedIn={authenticated} />;
    }
    return <LoginPage onLoggedIn={authenticated} />;
  }

  if (route.view === "setup" && route.params.step === "unlock") {
    /* Already unlocked: step 1 is done; the effect above returns home. */
    return <LoginLoading />;
  }

  if (route.view === "setup") {
    /* The wizard is a full page with its own progress rail (mockup 02-05). */
    return <SetupWizard key={route.params.step} step={route.params.step} />;
  }

  const daemon = daemonCard(daemonInfo);
  const crumbs = crumbsFor(route);
  return (
    <div
      className={
        "shell" + (collapsed ? " collapsed" : "") + (mnav ? " mnav" : "")
      }
    >
      <Sidebar
        route={route}
        collapsed={collapsed}
        onToggle={toggleSidebar}
        onLogout={logout}
        onNavigate={() => setMnav(false)}
        daemon={daemon}
      />
      {mnav ? (
        <div
          className="side-scrim"
          aria-hidden="true"
          onClick={() => setMnav(false)}
        />
      ) : null}
      <div className="main">
        <header className="topbar">
          <button
            type="button"
            className="ibtn mnav-btn"
            aria-label="Open navigation"
            onClick={() => setMnav(true)}
          >
            <Ic name="menu" size={17} />
          </button>
          <nav className="crumbs trunc" aria-label="Breadcrumb">
            {crumbs.map((crumb, index) => (
              <Fragment key={crumb.label + "-" + index}>
                {index > 0 ? <span className="sep">/</span> : null}
                {crumb.href ? (
                  <a href={crumb.href}>{crumb.label}</a>
                ) : (
                  <span className="cur">{crumb.label}</span>
                )}
              </Fragment>
            ))}
          </nav>
          <span className="sp"></span>
          <a
            className="btn btn-secondary btn-sm"
            href="#/sessions/new"
            aria-label="New session"
          >
            <Ic name="plus" size={14} />
            <span className="hide-m">New</span>
            <Ic name="chevron-down" size={12} className="hide-m" />
          </a>
        </header>
        <div
          className="content fx-route"
          id="main-content"
          tabIndex={-1}
          key={currentPage}
          ref={settleRoute}
        >
          {routeContent(route, theme, changeTheme)}
        </div>
      </div>
      {/* Spec 36: the self-update card floats over every signed-in page. */}
      <UpdateCard />
    </div>
  );
}
