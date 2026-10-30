import {
  html,
  render,
  useState,
  useEffect,
} from "/assets/vendor/preact-htm.js";
import {
  api,
  setCSRF,
  setUnauthorizedHandler,
} from "/assets/api.js";
import { AnnouncerProvider, useAnnounce } from "/assets/ui.js";
import {
  applyTheme,
  readTheme,
  saveTheme,
  themeLabel,
  watchSystemTheme,
} from "/assets/theme.js";
import { LoginPage, LoginLoading } from "/assets/views/login.js";
import { HomePage } from "/assets/views/home.js";
import { NotFoundPage, PlaceholderPage } from "/assets/views/placeholder.js";
import { SettingsGeneralPage } from "/assets/views/settings-general.js";
import { SessionsWorkspace } from "/assets/views/sessions.js";
import {
  SettingsProvidersPage,
  SettingsSecretsPage,
  SettingsConfigPage,
  SettingsAuditPage,
} from "/assets/views/settings-pages.js";
import { AgentsPage } from "/assets/views/agents.js";
import { AgentDetailPage } from "/assets/views/agent-detail.js";
import { SkillsPage, SkillEditorPage } from "/assets/views/skills.js";
import { McpPage, McpDetailPage } from "/assets/views/mcp.js";
import { SetupWizard, setupNeeded } from "/assets/views/setup.js";
import { ChannelsPage, TelegramPage } from "/assets/views/channels.js";

const DEFAULT_ROUTE = "#/sessions";
const SIDEBAR_KEY = "lana.sidebar";

/* The route table of REQ-WEB-030. {name} segments match any value; tab and
   step segments are limited by the lists on each entry. */
const AGENT_TABS = ["general", "persona", "tools", "skills", "mcp", "rules", "workspace"];
const SETTINGS_TABS = ["general", "providers", "secrets", "config", "audit"];
const LEGACY_SETTINGS_TABS = ["agents", "skills"];
const SETUP_STEPS = ["unlock", "provider", "connect", "agent", "persona", "ready"];

const ROUTE_TABLE = [
  { path: "#/home", view: "home", section: "home" },
  { path: "#/sessions", view: "sessions", section: "sessions" },
  { path: "#/sessions/new", view: "new-session", section: "sessions" },
  { path: "#/sessions/{id}", view: "session", section: "sessions" },
  { path: "#/agents", view: "agents", section: "agents" },
  { path: "#/agents/{name}/{tab}", view: "agent", section: "agents", tabs: AGENT_TABS },
  { path: "#/skills", view: "skills", section: "skills" },
  { path: "#/skills/new", view: "new-skill", section: "skills" },
  { path: "#/skills/{scope}/{name}", view: "skill", section: "skills" },
  { path: "#/mcp", view: "mcp", section: "mcp" },
  { path: "#/mcp/{name}", view: "mcp-detail", section: "mcp" },
  { path: "#/channels", view: "channels", section: "channels" },
  { path: "#/channels/telegram", view: "telegram", section: "channels" },
  {
    path: "#/settings/{tab}",
    view: "settings",
    section: "settings",
    tabs: SETTINGS_TABS.concat(LEGACY_SETTINGS_TABS),
  },
  { path: "#/setup/{step}", view: "setup", section: "", steps: SETUP_STEPS },
];

const NAV_ITEMS = [
  { key: "home", label: "Home", icon: "home", href: "#/home" },
  { key: "sessions", label: "Sessions", icon: "sessions", href: "#/sessions" },
  { key: "agents", label: "Agents", icon: "agents", href: "#/agents" },
  { key: "skills", label: "Skills", icon: "skills", href: "#/skills" },
  { key: "mcp", label: "MCP", icon: "mcp", href: "#/mcp" },
  { key: "channels", label: "Channels", icon: "channels", href: "#/channels" },
  { key: "settings", label: "Settings", icon: "settings", href: "#/settings/general" },
];

const SETUP_LABELS = {
  unlock: "Unlock",
  provider: "Choose provider",
  connect: "Connect model",
  agent: "Set up agent",
  persona: "Persona",
  ready: "Ready",
};

const initialTheme = readTheme();
applyTheme(initialTheme);

function readCollapsed() {
  try {
    return window.localStorage.getItem(SIDEBAR_KEY) === "collapsed";
  } catch (cause) {
    return false;
  }
}

function saveCollapsed(collapsed) {
  try {
    window.localStorage.setItem(SIDEBAR_KEY, collapsed ? "collapsed" : "expanded");
  } catch (cause) {
    /* The sidebar still collapses for this page when storage is disabled. */
  }
}

function safeDecode(part) {
  try {
    return decodeURIComponent(part);
  } catch (cause) {
    return part;
  }
}

function matchRoute(parts, entry) {
  const segments = entry.path.slice(2).split("/");
  if (segments.length !== parts.length) {
    return null;
  }
  const params = {};
  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index];
    const value = parts[index];
    if (segment.startsWith("{") && segment.endsWith("}")) {
      const name = segment.slice(1, -1);
      if (name === "tab" && entry.tabs && !entry.tabs.includes(value)) {
        return null;
      }
      if (name === "step" && entry.steps && !entry.steps.includes(value)) {
        return null;
      }
      params[name] = value;
      continue;
    }
    if (segment !== value) {
      return null;
    }
  }
  return params;
}

function parseRoute(hash) {
  const raw = String(hash || "").replace(/^#/, "");
  const parts = raw.split("/").filter(Boolean).map(safeDecode);
  if (parts.length === 0) {
    return { view: "sessions", section: "sessions", path: DEFAULT_ROUTE };
  }
  if (parts.length === 1 && parts[0] === "login") {
    /* Rev 1 login alias; the setup wizard owns #/setup/unlock. */
    return { view: "login", section: "", path: "#/login" };
  }
  if (parts.length === 1 && parts[0] === "new") {
    /* Rev 1 new-session alias kept so the sessions list keeps working. */
    return { view: "new-session", section: "sessions", params: {}, path: "#/sessions/new" };
  }
  for (const entry of ROUTE_TABLE) {
    const params = matchRoute(parts, entry);
    if (params) {
      return {
        view: entry.view,
        section: entry.section,
        params,
        path: "#/" + parts.join("/"),
      };
    }
  }
  return { view: "not-found", section: "", params: {}, path: "#/" + parts.join("/") };
}

function useRoute() {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));
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

function Icon({ name, className = "icon-20" }) {
  return html`<img
    class=${className}
    src=${"/assets/icons/" + name + ".svg"}
    alt=""
    aria-hidden="true"
  />`;
}

function SidebarToggle({ collapsed, onToggle }) {
  return html`<button
    type="button"
    class="icon-button sidebar-toggle"
    aria-label=${collapsed ? "Expand sidebar" : "Collapse sidebar"}
    aria-expanded=${!collapsed}
    aria-controls="sidebar-nav"
    title=${collapsed ? "Expand sidebar" : "Collapse sidebar"}
    onClick=${onToggle}
  >
    <${Icon} name=${collapsed ? "sidebar-expand" : "sidebar-collapse"} />
  </button>`;
}

function Sidebar({ route, collapsed, onToggle, onLogout, daemon }) {
  return html`<aside class="sidebar" aria-label="Sidebar">
    <div class="sidebar-top">
      <div class="sidebar-logo">
        <a class="logo-lockup" href="#/home" aria-label="lana">
          <img
            class="logo-mark"
            src="/assets/icons/logo-mark.svg"
            alt=""
            aria-hidden="true"
          />
          ${collapsed ? null : html`<span class="logo-word">LΛNΛ</span>`}
        </a>
        ${collapsed ? null : html`<${SidebarToggle} collapsed=${collapsed} onToggle=${onToggle} />`}
      </div>

      <nav id="sidebar-nav" class="sidebar-nav" aria-label="Main">
        ${NAV_ITEMS.map((item) => {
          const active = route.section === item.key;
          return html`<a
            key=${item.key}
            class="nav-item"
            href=${item.href}
            aria-current=${active ? "page" : undefined}
            aria-label=${collapsed ? item.label : undefined}
            title=${collapsed ? item.label : undefined}
          >
            <${Icon} name=${item.icon} />
            <span class="nav-label">${item.label}</span>
          </a>`;
        })}
      </nav>
    </div>

    <div class="sidebar-bottom">
      ${collapsed ? html`<${SidebarToggle} collapsed=${collapsed} onToggle=${onToggle} />` : null}

      <div
        class=${"daemon-card" + (daemon.state === "unavailable" ? " is-unavailable" : "")}
        title=${collapsed ? daemon.title + " · " + daemon.meta : undefined}
      >
        <div class="daemon-title">
          <span
            class=${"status-dot" + (daemon.state === "unavailable" ? " is-unavailable" : "")}
            aria-hidden="true"
          ></span>
          <span class="daemon-label">${daemon.title}</span>
        </div>
        <p class="daemon-meta">${daemon.meta}</p>
      </div>

      ${collapsed ? null : html`<hr class="sidebar-divider" />`}

      <div class="owner-row">
        ${collapsed
          ? html`<button
              type="button"
              class="owner-avatar avatar-button"
              aria-label="Log out"
              title="Log out"
              onClick=${onLogout}
            >
              OW
            </button>`
          : html`<span class="owner-avatar" aria-hidden="true">OW</span>
              <div class="owner-text">
                <div class="owner-name">Owner</div>
                <div class="owner-meta">This browser</div>
              </div>
              <button
                type="button"
                class="icon-button owner-logout"
                aria-label="Log out"
                title="Log out"
                onClick=${onLogout}
              >
                <${Icon} name="logout" />
              </button>`}
      </div>
    </div>
  </aside>`;
}

function routeContent(route, theme, onThemeChange) {
  const params = route.params || {};
  switch (route.view) {
    case "login":
      return html`<main id="main" class="page"><p class="muted">Opening sessions.</p></main>`;
    case "home":
      return html`<${HomePage} />`;
    case "sessions":
      return html`<${SessionsWorkspace} />`;
    case "new-session":
      return html`<${SessionsWorkspace} newSession=${true} />`;
    case "session":
      return html`<${SessionsWorkspace} selectedId=${params.id} />`;
    case "agents":
      return html`<${AgentsPage} />`;
    case "agent":
      return html`<${AgentDetailPage} name=${params.name} tab=${params.tab} />`;
    case "skills":
      return html`<${SkillsPage} />`;
    case "new-skill":
      return html`<${SkillEditorPage} isNew=${true} />`;
    case "skill":
      return html`<${SkillEditorPage} key=${params.scope + "/" + params.name} scope=${params.scope} name=${params.name} />`;
    case "mcp":
      return html`<${McpPage} />`;
    case "mcp-detail":
      return html`<${McpDetailPage} key=${params.name} name=${params.name} />`;
    case "channels":
      return html`<${ChannelsPage} />`;
    case "telegram":
      return html`<${TelegramPage} />`;
    case "settings":
      switch (params.tab) {
        case "general":
          return html`<${SettingsGeneralPage} theme=${theme} onThemeChange=${onThemeChange} />`;
        case "providers":
          return html`<${SettingsProvidersPage} />`;
        case "secrets":
          return html`<${SettingsSecretsPage} />`;
        case "config":
          return html`<${SettingsConfigPage} />`;
        case "audit":
          return html`<${SettingsAuditPage} />`;
        default:
          return html`<${NotFoundPage} path=${route.path} />`;
      }
    case "setup":
      return html`<${SetupWizard} key=${params.step} step=${params.step} />`;
    default:
      return html`<${NotFoundPage} path=${route.path} />`;
  }
}

function formatUptime(milliseconds) {
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

function daemonCard(info) {
  if (!info) {
    return {
      state: "loading",
      title: "Checking daemon…",
      meta: window.location.host,
    };
  }
  if (info.unavailable) {
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

function Root() {
  const route = useRoute();
  const announce = useAnnounce();
  const [auth, setAuth] = useState("checking");
  const [theme, setTheme] = useState(initialTheme);
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const [daemonInfo, setDaemonInfo] = useState(null);

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

    api
      .authSession()
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
    api
      .daemonInfo()
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

  function changeTheme(choice) {
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

  function loggedIn(session) {
    setCSRF(session.csrf);
    setAuth("loggedIn");
    if (window.location.hash !== DEFAULT_ROUTE) {
      window.location.hash = DEFAULT_ROUTE;
    }
  }

  async function logout() {
    try {
      await api.logout();
    } catch (failure) {
      /* The cookie may already be gone; clear the page either way. */
    }
    setCSRF("");
    setAuth("loggedOut");
    window.location.hash = "#/setup/unlock";
    announce("Signed out.");
  }

  if (auth === "checking") {
    return html`<${LoginLoading} />`;
  }

  if (auth !== "loggedIn") {
    return html`<${LoginPage} onLoggedIn=${loggedIn} />`;
  }

  if (route.view === "setup" && route.params.step === "unlock") {
    /* Already unlocked: step 1 is done. */
    window.location.hash = DEFAULT_ROUTE;
    return html`<${LoginLoading} />`;
  }

  if (route.view === "setup") {
    /* The wizard is a full page with its own progress rail (mockup 02-05). */
    return html`<${SetupWizard} key=${route.params.step} step=${route.params.step} />`;
  }

  const daemon = daemonCard(daemonInfo);
  return html`<div class=${"app-shell" + (collapsed ? " sidebar-collapsed" : "")}>
    <${Sidebar}
      route=${route}
      collapsed=${collapsed}
      onToggle=${toggleSidebar}
      onLogout=${logout}
      daemon=${daemon}
    />
    <div class="app-content">${routeContent(route, theme, changeTheme)}</div>
  </div>`;
}

const mount = document.getElementById("app");
if (mount) {
  /* Preact appends to a non-empty container, so drop the static loading copy
     before the first render. */
  mount.textContent = "";
  const root = html`<${Root} />`;
  render(html`<${AnnouncerProvider} children=${root} />`, mount);
}
