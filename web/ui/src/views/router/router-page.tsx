import { Tabs } from "../../ui/gr8r";
import { RouterOverview } from "./overview";
import { RouterProviders } from "./providers";
import { RouterModels } from "./models";
import { RouterCombos } from "./combos";
import { RouterLogs } from "./logs";
import { RouterProxy } from "./proxy";

/* The Router page (spec 37, API-RTR-017): #/router is the Overview, the other
   tabs live at #/router/<tab>. Switching tabs changes the hash; the App keeps
   the page mounted (pageKey "router"). */

export const ROUTER_TAB_LABELS: Record<string, string> = {
  overview: "Overview",
  providers: "Providers",
  models: "Models",
  combos: "Combos",
  logs: "Logs",
  proxy: "Proxy",
};

const TAB_ORDER = ["overview", "providers", "models", "combos", "logs", "proxy"];

export function routerHref(tab: string): string {
  return tab === "overview" ? "#/router" : "#/router/" + tab;
}

export function RouterPage({ tab }: { tab: string }) {
  const active = TAB_ORDER.includes(tab) ? tab : "overview";
  return (
    <main id="main" className="page home-page router-page">
      <header className="ph">
        <div>
          <h1>Router</h1>
          <p>Every model call goes through here: providers, fallback combos, cost, and limits.</p>
        </div>
      </header>
      <Tabs
        className="rt-tabs"
        aria-label="Router sections"
        value={active}
        onChange={(value) => {
          window.location.hash = routerHref(value);
        }}
        items={TAB_ORDER.map((value) => ({ value, label: ROUTER_TAB_LABELS[value] || value }))}
      />
      <section className="rt-panel" role="tabpanel" aria-label={ROUTER_TAB_LABELS[active]}>
        {active === "overview" ? <RouterOverview /> : null}
        {active === "providers" ? <RouterProviders /> : null}
        {active === "models" ? <RouterModels /> : null}
        {active === "combos" ? <RouterCombos /> : null}
        {active === "logs" ? <RouterLogs /> : null}
        {active === "proxy" ? <RouterProxy /> : null}
      </section>
    </main>
  );
}
