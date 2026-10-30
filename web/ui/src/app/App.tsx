import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { authSession, logout as apiLogout } from "../api/auth";
import type { AuthSession } from "../api/auth";
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
import { LoginLoading, LoginPage } from "../views/login";
import { HomePage } from "../views/home";
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
import { McpPage, McpDetailPage } from "../views/mcp";
import { SetupWizard, setupNeeded } from "../views/setup";
import { ChannelsPage, TelegramPage } from "../views/channels";

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
      return (
        <main id="main" className="page">
          <p className="muted">Opening sessions.</p>
        </main>
      );
    case "home":
      return <HomePage />;
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

export function App() {
  const route = useRoute();
  const announce = useAnnounce();
  const [auth, setAuth] = useState<AuthState>("checking");
  const [theme, setTheme] = useState<ThemeChoice>(initialTheme);
  const [collapsed, setCollapsed] = useState(readCollapsed);
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
    if (auth === "loggedIn" && route.view === "login") {
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

  function loggedIn(session: AuthSession) {
    setCSRF(session.csrf);
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
    window.location.hash = "#/setup/unlock";
    announce("Signed out.");
  }

  if (auth === "checking") {
    return <LoginLoading />;
  }

  if (auth !== "loggedIn") {
    return <LoginPage onLoggedIn={loggedIn} />;
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
  return (
    <div className={"app-shell" + (collapsed ? " sidebar-collapsed" : "")}>
      <Sidebar
        route={route}
        collapsed={collapsed}
        onToggle={toggleSidebar}
        onLogout={logout}
        daemon={daemon}
      />
      <div className="app-content">
        {routeContent(route, theme, changeTheme)}
      </div>
    </div>
  );
}
