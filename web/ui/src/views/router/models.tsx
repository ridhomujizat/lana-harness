import { useCallback, useEffect, useState } from "react";
import { listRouterModels, testRouterModel } from "../../api/router";
import type { ModelTestResponse, ProviderModels, RouterModel } from "../../api/router";
import { Badge, Button, EmptyState } from "../../ui/gr8r";
import { Ic } from "../../ui/icons";
import { DASH, agoText, compact, failureOf, ms } from "./router-common";

/* The models of one provider connection (REQ-RTR-013, API-RTR-009/010), shown
   on the provider's page: the catalog and the provider's own model list, with
   prices, who uses each model, and a Test per model. */

const SOURCE_LABELS: Record<string, string> = {
  catalog: "Catalog",
  provider: "Provider",
  both: "Catalog + provider",
};

function price(value: number | undefined | null): string {
  if (value === null || value === undefined) {
    return DASH;
  }
  return "$" + (Number.isInteger(value) ? String(value) : String(Number(value.toFixed(4))));
}

function priceText(model: RouterModel): string {
  if (!model.price) {
    return DASH;
  }
  return (
    price(model.price.input) + " / " + price(model.price.cached_input) + " / " + price(model.price.output)
  );
}

export function usedByText(model: RouterModel): string {
  const agents = model.used_by?.agents || [];
  const combos = model.used_by?.combos || [];
  const parts = agents.slice();
  for (const combo of combos) {
    parts.push("combo " + combo);
  }
  return parts.join(", ");
}

export type TestState = { busy: boolean; result?: ModelTestResponse; error?: string };

export function TestResult({ state }: { state: TestState | undefined }) {
  if (!state) {
    return null;
  }
  if (state.busy) {
    return <span className="hint">Testing…</span>;
  }
  if (state.error) {
    return (
      <span className="rt-test bad" role="status">
        <Ic name="circle-alert" size={12} /> {state.error}
      </span>
    );
  }
  const result = state.result;
  if (result && result.ok) {
    return (
      <span className="rt-test ok" role="status">
        <Ic name="circle-check" size={12} /> OK · {ms(result.latency_ms)}
      </span>
    );
  }
  const parts = [result?.class, result?.status ? "HTTP " + result.status : "", result?.message].filter(Boolean);
  return (
    <span className="rt-test bad" role="status">
      <Ic name="circle-alert" size={12} /> {parts.join(" · ") || "Failed"}
    </span>
  );
}

export function ProviderModelsPanel({ provider, title }: { provider: string; title?: string }) {
  const [list, setList] = useState<ProviderModels | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [tests, setTests] = useState<Record<string, TestState>>({});

  const load = useCallback(
    async (refresh: boolean) => {
      setLoading(true);
      try {
        const result = await listRouterModels(provider, refresh);
        setList(result.find((entry) => entry.provider === provider) || result[0] || null);
        setError("");
      } catch (failure) {
        setError(failureOf(failure).message);
      } finally {
        setLoading(false);
      }
    },
    [provider]
  );

  useEffect(() => {
    void load(false);
  }, [load]);

  async function runTest(address: string) {
    setTests((current) => ({ ...current, [address]: { busy: true } }));
    try {
      const result = await testRouterModel(address);
      setTests((current) => ({ ...current, [address]: { busy: false, result } }));
    } catch (failure) {
      setTests((current) => ({ ...current, [address]: { busy: false, error: failureOf(failure).message } }));
    }
  }

  const models = list ? list.models : [];

  return (
    <section className="panel rt-model-list" aria-label={"Models of " + provider}>
      <div className="panel-h">
        <h2>{title || "Models"}</h2>
        <div className="acts">
          <span className="hint">
            {list ? models.length + " models" : ""}
            {list && list.fetched_at ? " · fetched " + agoText(list.fetched_at) : ""}
          </span>
          <Button
            variant="ghost"
            size="sm"
            icon="refresh-cw"
            loading={loading}
            disabled={loading}
            onClick={() => void load(true)}
          >
            Refresh
          </Button>
        </div>
      </div>
      <div className="panel-b">
        {error ? (
          <p className="rt-limit-err" role="alert">
            <Ic name="circle-alert" size={12} />
            <span>Could not load models: {error}</span>
          </p>
        ) : null}
        {list === null && !error ? <div className="home-empty">Loading models…</div> : null}
        {list && list.fetch_error ? (
          <div className="alert warn rt-fetch-error" role="status">
            <Ic name="triangle-alert" size={15} />
            <p className="grow">
              Could not fetch the provider's model list: {list.fetch_error}. Showing the built-in
              catalog.
            </p>
          </div>
        ) : null}
        {list && models.length === 0 ? (
          <EmptyState size="sm" icon="boxes" text="This provider lists no models. Refresh after adding a key." />
        ) : null}
        {models.length > 0 ? (
          <div className="home-table-wrap">
            <table className="perm-t home-table rt-models-table">
              <thead>
                <tr>
                  <th scope="col">Model</th>
                  <th scope="col" className="is-num">Context</th>
                  <th scope="col" className="is-num">Max output</th>
                  <th scope="col">Vision</th>
                  <th scope="col">Thinking</th>
                  <th scope="col" className="is-num" title="Input / cached input / output, per 1M tokens">
                    Price per 1M (in / cached / out)
                  </th>
                  <th scope="col">Source</th>
                  <th scope="col">Used by</th>
                  <th scope="col">
                    <span className="sr">Test</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {models.map((model) => (
                  <tr key={model.address || model.id}>
                    <td className="rt-model-cell">
                      <span className="rt-model-name trunc">{model.name || model.id}</span>
                      <span className="rt-model-addr mono trunc">{model.address}</span>
                    </td>
                    <td className="is-num num">{model.context_window ? compact(model.context_window) : DASH}</td>
                    <td className="is-num num">{model.max_output ? compact(model.max_output) : DASH}</td>
                    <td>{model.vision ? "Yes" : "No"}</td>
                    <td>{model.thinking ? "Yes" : "No"}</td>
                    <td className="is-num num">{priceText(model)}</td>
                    <td>
                      <Badge tone={model.source === "provider" ? "accent" : "gray"}>
                        {SOURCE_LABELS[model.source] || model.source || DASH}
                      </Badge>
                    </td>
                    <td className="rt-used-cell trunc" title={usedByText(model)}>
                      {usedByText(model) || <span className="faint">{DASH}</span>}
                    </td>
                    <td className="rt-test-cell">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={tests[model.address]?.busy}
                        aria-label={"Test " + model.address}
                        onClick={() => void runTest(model.address)}
                      >
                        Test
                      </Button>
                      <TestResult state={tests[model.address]} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </div>
    </section>
  );
}
