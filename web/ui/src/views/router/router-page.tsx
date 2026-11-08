import { RouterUsagePage } from "./overview";
import { RouterProviders } from "./providers";
import { ProviderDetail } from "./provider-detail";
import { RouterCombos } from "./combos";
import { RouterLogs } from "./logs";
import { RouterProxy } from "./proxy";
import { RouterQuota } from "./quota";
import { CLAUDE_CODE_ID, presetById } from "./presets";

/* The Router pages (spec 37, API-RTR-017 revision 2). Each page is its own
   sidebar item under the Router group, like 9router's dashboard:
   #/router/<page>, and #/router/providers/<preset or name> for one
   provider. #/router opens Providers. */

interface PageInfo {
  title: string;
  lead: string;
}

export const ROUTER_PAGES: Record<string, PageInfo> = {
  providers: {
    title: "Providers",
    lead: "Connect the services your agents call. Open one to add a key, switch it off, or test its models.",
  },
  combos: {
    title: "Combos",
    lead: "Fallback chains. The router tries each model in order until one answers.",
  },
  usage: {
    title: "Usage",
    lead: "Calls, tokens, and cost across every provider.",
  },
  quota: {
    title: "Quota",
    lead: "What each provider has left: rate limits, credit, balance, and Claude plan windows.",
  },
  logs: {
    title: "Logs",
    lead: "Every model call the router made, newest first.",
  },
  endpoint: {
    title: "Endpoint",
    lead: "An OpenAI-compatible URL that Cursor, scripts, and other tools can call through the router.",
  },
};

export function routerHref(tab: string): string {
  return "#/router/" + tab;
}

/* The display name of a provider page: the preset label, Claude Code, or the
   custom provider's own name. */
export function providerPageLabel(id: string): string {
  if (id === CLAUDE_CODE_ID) {
    return "Claude Code";
  }
  const preset = presetById(id);
  return preset ? preset.label : id;
}

export function routerCrumbs(tab: string, provider?: string): Array<{ label: string; href?: string }> {
  const page = ROUTER_PAGES[tab] || ROUTER_PAGES.providers;
  if (tab === "providers" && provider) {
    return [
      { label: "Router", href: routerHref("providers") },
      { label: "Providers", href: routerHref("providers") },
      { label: providerPageLabel(provider) },
    ];
  }
  return [{ label: "Router", href: routerHref("providers") }, { label: page ? page.title : "Providers" }];
}

export function RouterPage({ tab, provider }: { tab: string; provider?: string }) {
  const active = ROUTER_PAGES[tab] ? tab : "providers";
  if (active === "providers" && provider) {
    return (
      <main id="main" className="page home-page router-page">
        <ProviderDetail key={provider} id={provider} />
      </main>
    );
  }
  const page = ROUTER_PAGES[active] as PageInfo;
  return (
    <main id="main" className="page home-page router-page" aria-labelledby="rt-page-title">
      <header className="ph">
        <div>
          <h1 id="rt-page-title">{page.title}</h1>
          <p>{page.lead}</p>
        </div>
      </header>
      <div className="rt-panel">
        {active === "providers" ? <RouterProviders /> : null}
        {active === "combos" ? <RouterCombos /> : null}
        {active === "usage" ? <RouterUsagePage /> : null}
        {active === "quota" ? <RouterQuota /> : null}
        {active === "logs" ? <RouterLogs /> : null}
        {active === "endpoint" ? <RouterProxy /> : null}
      </div>
    </main>
  );
}
