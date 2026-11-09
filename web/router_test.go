package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/internal/routerdb"
)

// fakeRouterBackend is an in-memory RouterBackend for the handler tests.
type fakeRouterBackend struct {
	mu sync.Mutex

	status  RouterStatus
	lists   map[string]RouterModelList
	refresh []bool
	test    func(address string) (RouterTestResult, error)
	usage   routerdb.Usage
	ranges  []string
	calls   routerdb.CallPage
	filters []routerdb.CallFilter
	limits  []RouterLimit
	keys    []routerdb.Key
	proxy   http.Handler
}

func (f *fakeRouterBackend) RouterStatus(context.Context) (RouterStatus, error) {
	return f.status, nil
}

func (f *fakeRouterBackend) RouterModels(_ context.Context, provider string, refresh bool) (RouterModelList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh = append(f.refresh, refresh)
	list, ok := f.lists[provider]
	if !ok {
		return RouterModelList{}, ErrRouterUnknownProvider
	}
	return list, nil
}

func (f *fakeRouterBackend) RouterTest(_ context.Context, address string) (RouterTestResult, error) {
	return f.test(address)
}

func (f *fakeRouterBackend) RouterUsage(_ context.Context, rangeName string) (routerdb.Usage, error) {
	f.ranges = append(f.ranges, rangeName)
	return f.usage, nil
}

func (f *fakeRouterBackend) RouterCalls(_ context.Context, filter routerdb.CallFilter) (routerdb.CallPage, error) {
	f.filters = append(f.filters, filter)
	return f.calls, nil
}

func (f *fakeRouterBackend) RouterLimits(_ context.Context, refresh bool) ([]RouterLimit, error) {
	f.refresh = append(f.refresh, refresh)
	return f.limits, nil
}

func (f *fakeRouterBackend) RouterKeys(context.Context) ([]routerdb.Key, error) {
	return f.keys, nil
}

func (f *fakeRouterBackend) CreateRouterKey(_ context.Context, name string) (RouterKeyCreated, error) {
	key := routerdb.Key{ID: "k_" + name, Name: name, Prefix: "lrk_ab12", CreatedAt: time.Unix(0, 0).UTC()}
	f.keys = append(f.keys, key)
	return RouterKeyCreated{ID: key.ID, Name: name, Prefix: key.Prefix, Key: "lrk_secret"}, nil
}

func (f *fakeRouterBackend) RevokeRouterKey(_ context.Context, id string) error {
	for i, key := range f.keys {
		if key.ID == id {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			return nil
		}
	}
	return routerdb.ErrKeyNotFound
}

func (f *fakeRouterBackend) RouterProxyHandler() http.Handler {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.proxy
}

func (f *fakeRouterBackend) ClaudeAgentStatus(context.Context) (ClaudeAgentStatus, error) {
	return ClaudeAgentStatus{Error: "not configured"}, nil
}

const routerTestConfig = `[providers.deepseek]
kind = "deepseek"

[providers.local]
kind = "openai_compat"
base_url = "http://127.0.0.1:9/v1"
api_key_env = "LOCAL_KEY"

[providers.spare]
kind = "openai"

[combos.coding]
description = "DeepSeek first"
models = ["deepseek/deepseek-flash", "local/qwen"]

[combos.idle]
models = ["local/qwen"]

[agents.lana]
model = "coding"

[agents.coder]
model = "deepseek/deepseek-flash"
fallback = ["local:qwen"]

[knowledge]
embedding_provider = "local"
embedding_model = "embed"
`

func newRouterTestServer(t *testing.T, raw string) (*Server, *testLiveConfigStore, *fakeRouterBackend, *http.Cookie, string) {
	t.Helper()
	store := &testLiveConfigStore{testConfigStore: newTestConfigStore(t, raw, 0o600, ConfigSettings{}), advance: true}
	s, cookie, csrf := newLiveConfigTestServer(t, store)
	fake := &fakeRouterBackend{
		lists: map[string]RouterModelList{
			"deepseek": {Provider: "deepseek", Models: []RouterModel{
				{ID: "deepseek-flash", Address: "deepseek/deepseek-flash", Name: "DeepSeek Flash", Source: "both",
					Price: &models.Price{Input: 0.1, CachedInput: 0.01, Output: 0.2}},
				{ID: "deepseek-chat", Address: "deepseek/deepseek-chat", Name: "DeepSeek Chat", Source: "catalog"},
			}},
			"local": {Provider: "local", FetchError: "connection refused", Models: []RouterModel{
				{ID: "qwen", Address: "local/qwen", Name: "", Source: "provider"},
			}},
		},
	}
	s.SetRouterBackend(fake)
	return s, store, fake, cookie, csrf
}

func TestRouterOverview(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	s, store, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	if err := os.WriteFile(filepath.Join(store.home, "secrets.env"), []byte("DEEPSEEK_API_KEY=sk-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 11, 8, 10, 5, 0, 0, time.UTC)
	fake.status = RouterStatus{ProxyEnabled: true, Cooling: []RouterCooling{{Target: "local/qwen", Until: until}}}

	response := serveMCP(t, s, http.MethodGet, "/v1/router", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body routerOverviewResponse
	response.json(t, &body)
	if body.Version != shaHex([]byte(routerTestConfig)) {
		t.Fatalf("version = %q", body.Version)
	}
	if len(body.Providers) != 3 {
		t.Fatalf("providers = %+v", body.Providers)
	}
	ds := body.Providers[0]
	if ds.Name != "deepseek" || ds.Kind != "deepseek" || ds.BaseURL != "" || ds.DefaultBaseURL != "https://api.deepseek.com/v1" ||
		ds.KeyState != "set" || strings.Join(ds.UsedBy, ",") != "coder,lana" {
		t.Fatalf("deepseek row = %+v", ds)
	}
	if local := body.Providers[1]; local.KeyState != "unset" || local.DefaultBaseURL != "" || strings.Join(local.UsedBy, ",") != "coder,lana" {
		t.Fatalf("local row = %+v", local)
	}
	if spare := body.Providers[2]; len(spare.UsedBy) != 0 || spare.UsedBy == nil {
		t.Fatalf("spare row = %+v", spare)
	}
	if len(body.Combos) != 2 || body.Combos[0].Name != "coding" || body.Combos[0].Description != "DeepSeek first" ||
		strings.Join(body.Combos[0].UsedBy, ",") != "lana" || len(body.Combos[1].UsedBy) != 0 {
		t.Fatalf("combos = %+v", body.Combos)
	}
	if len(body.Cooling) != 1 || body.Cooling[0].Target != "local/qwen" || !body.Cooling[0].Until.Equal(until) {
		t.Fatalf("cooling = %+v", body.Cooling)
	}
	if !body.Proxy.Enabled || body.Proxy.BaseURL != "http://"+authTestAddr+"/router/v1" {
		t.Fatalf("proxy = %+v", body.Proxy)
	}
	// AC-RTR-024: combos first, then the models grouped by provider; a
	// provider the router does not know yet lists its catalog.
	choices := body.Choices
	if len(choices) < 6 || choices[0] != (routerChoiceJSON{Value: "coding", Label: "coding", Group: "Combos"}) ||
		choices[1].Group != "Combos" {
		t.Fatalf("choices = %+v", choices)
	}
	if choices[2] != (routerChoiceJSON{Value: "deepseek/deepseek-flash", Label: "DeepSeek Flash", Group: "deepseek"}) {
		t.Fatalf("first model choice = %+v", choices[2])
	}
	if choices[4] != (routerChoiceJSON{Value: "local/qwen", Label: "qwen", Group: "local"}) {
		t.Fatalf("local choice = %+v", choices[4])
	}
	if choices[5].Group != "spare" || !strings.HasPrefix(choices[5].Value, "spare/") {
		t.Fatalf("catalog fallback choice = %+v", choices[5])
	}
	if strings.Contains(string(response.body), "sk-file") {
		t.Fatalf("overview leaked a key: %s", response.body)
	}
}

func TestRouterModelsEndpoint(t *testing.T) {
	s, _, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)

	response := serveMCP(t, s, http.MethodGet, "/v1/router/models?provider=deepseek&refresh=1", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var list RouterModelList
	response.json(t, &list)
	if list.Provider != "deepseek" || len(list.Models) != 2 {
		t.Fatalf("list = %+v", list)
	}
	flash := list.Models[0]
	if strings.Join(flash.UsedBy.Agents, ",") != "coder" || strings.Join(flash.UsedBy.Combos, ",") != "coding" ||
		flash.Price == nil || flash.Price.Output != 0.2 {
		t.Fatalf("flash = %+v", flash)
	}
	if chat := list.Models[1]; chat.UsedBy.Agents == nil || len(chat.UsedBy.Agents) != 0 || chat.Price != nil {
		t.Fatalf("chat = %+v", chat)
	}
	if !strings.Contains(string(response.body), `"price":null`) || !strings.Contains(string(response.body), `"fetched_at":null`) {
		t.Fatalf("null fields missing: %s", response.body)
	}
	if len(fake.refresh) != 1 || !fake.refresh[0] {
		t.Fatalf("refresh = %v", fake.refresh)
	}

	local := serveMCP(t, s, http.MethodGet, "/v1/router/models?provider=local", "", cookie, csrf)
	var localList RouterModelList
	local.json(t, &localList)
	// The fallback "local:qwen" counts as the same address.
	if localList.FetchError != "connection refused" || strings.Join(localList.Models[0].UsedBy.Agents, ",") != "coder" ||
		strings.Join(localList.Models[0].UsedBy.Combos, ",") != "coding,idle" {
		t.Fatalf("local list = %+v", localList)
	}

	unknown := serveMCP(t, s, http.MethodGet, "/v1/router/models?provider=nope", "", cookie, csrf)
	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown provider status = %d, body = %q", unknown.code, unknown.body)
	}

	all := serveMCP(t, s, http.MethodGet, "/v1/router/models", "", cookie, csrf)
	var allBody routerModelsAllResponse
	all.json(t, &allBody)
	if all.code != http.StatusOK || len(allBody.Providers) != 2 || allBody.Providers[0].Provider != "deepseek" {
		t.Fatalf("all = %d %+v", all.code, allBody)
	}
}

func TestRouterTestEndpoint(t *testing.T) {
	s, _, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	fake.test = func(address string) (RouterTestResult, error) {
		switch address {
		case "deepseek/deepseek-flash":
			return RouterTestResult{OK: true, LatencyMS: 812}, nil
		case "local/qwen":
			return RouterTestResult{Class: "auth", Status: 401, Message: "401 Unauthorized"}, nil
		}
		return RouterTestResult{}, ErrRouterUnknownAddress
	}
	ok := serveMCP(t, s, http.MethodPost, "/v1/router/test", `{"address":"deepseek/deepseek-flash"}`, cookie, csrf)
	if ok.code != http.StatusOK || strings.TrimSpace(string(ok.body)) != `{"ok":true,"latency_ms":812}` {
		t.Fatalf("ok = %d %s", ok.code, ok.body)
	}
	fail := serveMCP(t, s, http.MethodPost, "/v1/router/test", `{"address":"local/qwen"}`, cookie, csrf)
	if fail.code != http.StatusOK || strings.TrimSpace(string(fail.body)) != `{"ok":false,"class":"auth","status":401,"message":"401 Unauthorized"}` {
		t.Fatalf("fail = %d %s", fail.code, fail.body)
	}
	unknown := serveMCP(t, s, http.MethodPost, "/v1/router/test", `{"address":"nope/x"}`, cookie, csrf)
	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown = %d %s", unknown.code, unknown.body)
	}
	empty := serveMCP(t, s, http.MethodPost, "/v1/router/test", `{"address":" "}`, cookie, csrf)
	if empty.code != http.StatusBadRequest {
		t.Fatalf("empty = %d %s", empty.code, empty.body)
	}
	// The session and CSRF rules apply.
	noCSRF := serveMCP(t, s, http.MethodPost, "/v1/router/test", `{"address":"deepseek/deepseek-flash"}`, cookie, "")
	if noCSRF.code != http.StatusForbidden {
		t.Fatalf("no CSRF = %d %s", noCSRF.code, noCSRF.body)
	}
	noCookie := serveMCP(t, s, http.MethodGet, "/v1/router", "", nil, "")
	if noCookie.code != http.StatusUnauthorized {
		t.Fatalf("no cookie = %d %s", noCookie.code, noCookie.body)
	}
}

func TestRouterUsageEndpoint(t *testing.T) {
	s, _, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	fake.usage = routerdb.Usage{
		Totals:     routerdb.Stats{Calls: 3, Errors: 1, CostUSD: 1.5, CostPartial: true, TTFTP50Ms: 900},
		ByProvider: []routerdb.ProviderStats{{Provider: "deepseek", Stats: routerdb.Stats{Calls: 3}}},
	}
	response := serveMCP(t, s, http.MethodGet, "/v1/router/usage", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body map[string]any
	response.json(t, &body)
	if body["range"] != "24h" || fake.ranges[0] != "24h" {
		t.Fatalf("range = %v, backend got %v", body["range"], fake.ranges)
	}
	totals, _ := body["totals"].(map[string]any)
	if totals["calls"] != float64(3) || totals["cost_partial"] != true || totals["ttft_p50_ms"] != float64(900) {
		t.Fatalf("totals = %v", totals)
	}
	for _, key := range []string{"series", "by_model", "by_source"} {
		if list, ok := body[key].([]any); !ok || len(list) != 0 {
			t.Fatalf("%s = %#v, want []", key, body[key])
		}
	}
	if rows, _ := body["by_provider"].([]any); len(rows) != 1 {
		t.Fatalf("by_provider = %v", body["by_provider"])
	}
	week := serveMCP(t, s, http.MethodGet, "/v1/router/usage?range=7d", "", cookie, csrf)
	if week.code != http.StatusOK || fake.ranges[1] != "7d" {
		t.Fatalf("7d = %d %v", week.code, fake.ranges)
	}
	bad := serveMCP(t, s, http.MethodGet, "/v1/router/usage?range=1y", "", cookie, csrf)
	if bad.code != http.StatusBadRequest {
		t.Fatalf("bad range = %d", bad.code)
	}
}

func TestRouterCallsEndpoint(t *testing.T) {
	s, _, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	cost := 0.001
	fake.calls = routerdb.CallPage{
		Calls: []routerdb.Call{{ID: 123, Source: "turn", Agent: "lana", Requested: "coding", Provider: "deepseek",
			Model: "deepseek-flash", Attempt: 1, Outcome: "ok", CostUSD: &cost}},
		NextBefore: 123,
	}
	response := serveMCP(t, s, http.MethodGet, "/v1/router/calls?limit=500&before=200&provider=deepseek&status=error&source=turn", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	want := routerdb.CallFilter{Limit: 200, Before: 200, Provider: "deepseek", Outcome: "error", Source: "turn"}
	if len(fake.filters) != 1 || fake.filters[0] != want {
		t.Fatalf("filter = %+v", fake.filters)
	}
	var page routerdb.CallPage
	response.json(t, &page)
	if page.NextBefore != 123 || len(page.Calls) != 1 || page.Calls[0].Requested != "coding" {
		t.Fatalf("page = %+v", page)
	}
	fake.calls = routerdb.CallPage{}
	empty := serveMCP(t, s, http.MethodGet, "/v1/router/calls", "", cookie, csrf)
	if strings.TrimSpace(string(empty.body)) != `{"calls":[],"next_before":0}` {
		t.Fatalf("empty page = %s", empty.body)
	}
	for _, query := range []string{"status=maybe", "limit=0", "before=x"} {
		bad := serveMCP(t, s, http.MethodGet, "/v1/router/calls?"+query, "", cookie, csrf)
		if bad.code != http.StatusBadRequest {
			t.Fatalf("%s = %d", query, bad.code)
		}
	}
}

func TestRouterLimitsEndpoint(t *testing.T) {
	s, _, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	reset := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	limit, used, left := 10.0, 2.5, 7.5
	fake.limits = []RouterLimit{
		{Provider: "openai", Kind: "openai", UpdatedAt: &reset, Rate: &RouterRateLimits{
			Tokens: &RouterRateLimit{Limit: 150000, Remaining: 149000, ResetAt: &reset},
		}},
		{Provider: "openrouter", Kind: "openrouter", Credit: &RouterCredit{LimitUSD: &limit, UsedUSD: used, RemainingUSD: &left}},
		{Provider: "spare", Kind: "openai"},
	}
	response := serveMCP(t, s, http.MethodGet, "/v1/router/limits?refresh=1", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	if len(fake.refresh) != 1 || !fake.refresh[0] {
		t.Fatalf("refresh = %v", fake.refresh)
	}
	text := string(response.body)
	for _, want := range []string{
		`"tokens":{"limit":150000,"remaining":149000,"reset_at":"2026-11-08T10:00:00Z"}`,
		`"credit":{"limit_usd":10,"used_usd":2.5,"remaining_usd":7.5}`,
		`{"provider":"spare","kind":"openai","updated_at":null,"error":"","rate":null,"credit":null,"balance":null,"windows":[]}`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("limits body lacks %s:\n%s", want, text)
		}
	}
}

// Spec 39 Revision 4: POST /v1/router/limits/refresh needs the session and
// the CSRF token, refreshes, and answers like GET ?refresh=1, with the label
// and source of each plan window.
func TestRouterLimitsRefreshEndpoint(t *testing.T) {
	s, _, fake, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	at := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	used := 0.0
	fake.limits = []RouterLimit{{Provider: "claude-code", Kind: "claude-code", UpdatedAt: &at, Error: "probe failed",
		Windows: []RouterPlanWindow{{Type: "seven_day_fable", Status: "allowed", Utilization: &used, Label: "Weekly · Fable", Source: "usage_probe"}}}}
	if denied := serveMCP(t, s, http.MethodPost, "/v1/router/limits/refresh", "", cookie, ""); denied.code != http.StatusForbidden {
		t.Fatalf("without CSRF = %d", denied.code)
	}
	if denied := serveMCP(t, s, http.MethodPost, "/v1/router/limits/refresh", "", nil, ""); denied.code != http.StatusUnauthorized && denied.code != http.StatusForbidden {
		t.Fatalf("without a session = %d", denied.code)
	}
	if len(fake.refresh) != 0 {
		t.Fatalf("refresh ran without auth: %v", fake.refresh)
	}
	response := serveMCP(t, s, http.MethodPost, "/v1/router/limits/refresh", "", cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	if len(fake.refresh) != 1 || !fake.refresh[0] {
		t.Fatalf("refresh = %v", fake.refresh)
	}
	want := `"windows":[{"type":"seven_day_fable","status":"allowed","utilization":0,"resets_at":null,"label":"Weekly · Fable","source":"usage_probe"}]`
	if !strings.Contains(string(response.body), want) || !strings.Contains(string(response.body), `"error":"probe failed"`) {
		t.Fatalf("body lacks %s:\n%s", want, response.body)
	}
}

// AC-RTR-019 and API-RTR-007: combo save and delete.
func TestRouterCombos(t *testing.T) {
	s, store, _, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	version := shaHex([]byte(routerTestConfig))

	body := `{"description":"fast","models":["local/qwen","deepseek/deepseek-chat"],"expected_version":"` + version + `"}`
	saved := serveMCP(t, s, http.MethodPut, "/v1/router/combos/fast", body, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save status = %d, body = %q", saved.code, saved.body)
	}
	var response routerSaveResponse
	saved.json(t, &response)
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if response.RestartRequired || response.Version != shaHex(data) {
		t.Fatalf("save response = %+v", response)
	}
	if !strings.Contains(string(data), "[combos.fast]\ndescription = 'fast'\nmodels = ['local/qwen', 'deepseek/deepseek-chat']\n") {
		t.Fatalf("combo table not written:\n%s", data)
	}
	if !strings.HasPrefix(string(data), routerTestConfig) {
		t.Fatalf("save changed other tables:\n%s", data)
	}
	if len(store.applied) != 1 {
		t.Fatalf("ApplyLive calls = %d", len(store.applied))
	}

	// A stale version is a version conflict.
	stale := serveMCP(t, s, http.MethodPut, "/v1/router/combos/fast", body, cookie, csrf)
	if stale.code != http.StatusConflict || decodeError(t, stale).Error.Code != "version_conflict" {
		t.Fatalf("stale = %d %s", stale.code, stale.body)
	}

	// Validation failures are 422 invalid with the config path.
	version = response.Version
	invalid := serveMCP(t, s, http.MethodPut, "/v1/router/combos/bad",
		`{"models":["nope/x"],"expected_version":"`+version+`"}`, cookie, csrf)
	if invalid.code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid = %d %s", invalid.code, invalid.body)
	}
	envelope := decodeError(t, invalid)
	if envelope.Error.Code != "invalid" || len(envelope.Error.Details) == 0 ||
		!strings.HasPrefix(envelope.Error.Details[0]["path"].(string), "combos.bad.models") {
		t.Fatalf("invalid envelope = %+v", envelope.Error)
	}
	badName := serveMCP(t, s, http.MethodPut, "/v1/router/combos/Bad%20Name",
		`{"models":["local/qwen"],"expected_version":"`+version+`"}`, cookie, csrf)
	if badName.code != http.StatusUnprocessableEntity || decodeError(t, badName).Error.Code != "invalid" {
		t.Fatalf("bad name = %d %s", badName.code, badName.body)
	}
	if after, _ := os.ReadFile(store.path); string(after) != string(data) {
		t.Fatal("a refused save changed config.toml")
	}

	// AC-RTR-019: a combo in use cannot be deleted; the error names the users.
	used := serveMCP(t, s, http.MethodDelete, "/v1/router/combos/coding", `{"expected_version":"`+version+`"}`, cookie, csrf)
	if used.code != http.StatusConflict {
		t.Fatalf("used delete = %d %s", used.code, used.body)
	}
	if envelope := decodeError(t, used); envelope.Error.Code != "in_use" || envelope.Error.Message != "used by agents: lana" {
		t.Fatalf("used delete error = %+v", envelope.Error)
	}
	missing := serveMCP(t, s, http.MethodDelete, "/v1/router/combos/nope", `{"expected_version":"`+version+`"}`, cookie, csrf)
	if missing.code != http.StatusNotFound {
		t.Fatalf("missing delete = %d %s", missing.code, missing.body)
	}
	deleted := serveMCP(t, s, http.MethodDelete, "/v1/router/combos/fast", `{"expected_version":"`+version+`"}`, cookie, csrf)
	if deleted.code != http.StatusOK {
		t.Fatalf("delete = %d %s", deleted.code, deleted.body)
	}
	data, _ = os.ReadFile(store.path)
	if strings.Contains(string(data), "[combos.fast]") || !strings.Contains(string(data), "[combos.coding]") {
		t.Fatalf("delete result:\n%s", data)
	}
	deleted.json(t, &response)
	if response.RestartRequired {
		t.Fatal("combo delete asked for a restart")
	}
}

// API-RTR-014: the proxy switch writes [router] proxy and applies live.
func TestRouterProxySwitch(t *testing.T) {
	s, store, _, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	version := shaHex([]byte(routerTestConfig))
	on := serveMCP(t, s, http.MethodPut, "/v1/router/proxy", `{"enabled":true,"expected_version":"`+version+`"}`, cookie, csrf)
	if on.code != http.StatusOK {
		t.Fatalf("on = %d %s", on.code, on.body)
	}
	var response routerProxyResponse
	on.json(t, &response)
	data, _ := os.ReadFile(store.path)
	if !response.Enabled || response.RestartRequired || response.BaseURL != "http://"+authTestAddr+"/router/v1" ||
		!strings.HasSuffix(string(data), "[router]\nproxy = true\n") {
		t.Fatalf("on response = %+v, config:\n%s", response, data)
	}
	off := serveMCP(t, s, http.MethodPut, "/v1/router/proxy", `{"enabled":false,"expected_version":"`+response.Version+`"}`, cookie, csrf)
	if off.code != http.StatusOK {
		t.Fatalf("off = %d %s", off.code, off.body)
	}
	data, _ = os.ReadFile(store.path)
	if !strings.HasSuffix(string(data), "[router]\nproxy = false\n") || strings.Count(string(data), "[router]") != 1 {
		t.Fatalf("off config:\n%s", data)
	}
	if len(store.applied) != 2 {
		t.Fatalf("ApplyLive calls = %d", len(store.applied))
	}
	missing := serveMCP(t, s, http.MethodPut, "/v1/router/proxy", `{"expected_version":"x"}`, cookie, csrf)
	if missing.code != http.StatusUnprocessableEntity {
		t.Fatalf("missing enabled = %d", missing.code)
	}
}

func TestRouterKeysEndpoints(t *testing.T) {
	s, _, _, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	created := serveMCP(t, s, http.MethodPost, "/v1/router/keys", `{"name":"cursor"}`, cookie, csrf)
	if created.code != http.StatusCreated ||
		strings.TrimSpace(string(created.body)) != `{"id":"k_cursor","name":"cursor","prefix":"lrk_ab12","key":"lrk_secret"}` {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	list := serveMCP(t, s, http.MethodGet, "/v1/router/keys", "", cookie, csrf)
	if list.code != http.StatusOK || strings.Contains(string(list.body), "lrk_secret") ||
		!strings.Contains(string(list.body), `"id":"k_cursor"`) || !strings.Contains(string(list.body), `"last_used_at":null`) {
		t.Fatalf("list = %d %s", list.code, list.body)
	}
	for _, body := range []string{`{"name":""}`, `{"name":"a\nb"}`, `{"name":"` + strings.Repeat("x", 65) + `"}`} {
		if bad := serveMCP(t, s, http.MethodPost, "/v1/router/keys", body, cookie, csrf); bad.code != http.StatusBadRequest {
			t.Fatalf("bad name %s = %d", body, bad.code)
		}
	}
	revoked := serveMCP(t, s, http.MethodDelete, "/v1/router/keys/k_cursor", "", cookie, csrf)
	if revoked.code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", revoked.code, revoked.body)
	}
	again := serveMCP(t, s, http.MethodDelete, "/v1/router/keys/k_cursor", "", cookie, csrf)
	if again.code != http.StatusNotFound {
		t.Fatalf("revoke again = %d %s", again.code, again.body)
	}
	empty := serveMCP(t, s, http.MethodGet, "/v1/router/keys", "", cookie, csrf)
	if strings.TrimSpace(string(empty.body)) != `{"keys":[]}` {
		t.Fatalf("empty list = %s", empty.body)
	}
}

func TestClaudeAgentStatusEndpoint(t *testing.T) {
	s, _, _, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	response := serveMCP(t, s, http.MethodGet, "/v1/router/claude-agent", "", cookie, csrf)
	if response.code != http.StatusOK || !strings.Contains(string(response.body), `"configured_agents":[]`) ||
		!strings.Contains(string(response.body), `"error":"not configured"`) {
		t.Fatalf("claude agent = %d %s", response.code, response.body)
	}
}

// REQ-RTR-015: /router/v1/* answers 404 while the proxy is off, reaches the
// backend's handler (prefix stripped, no cookie needed) once it is on, and
// keeps the Host check.
func TestRouterProxyMount(t *testing.T) {
	s, _, fake, _, _ := newRouterTestServer(t, routerTestConfig)
	off := serveAuth(s, authRequest(http.MethodGet, "/router/v1/models", ""))
	if off.Code != http.StatusNotFound || !strings.Contains(off.Body.String(), `"code":"proxy_disabled"`) ||
		!strings.Contains(off.Body.String(), `"type":"invalid_request_error"`) {
		t.Fatalf("off = %d %s", off.Code, off.Body.String())
	}
	var seen string
	fake.mu.Lock()
	fake.proxy = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusTeapot)
	})
	fake.mu.Unlock()
	on := serveAuth(s, authRequest(http.MethodPost, "/router/v1/chat/completions", "{}"))
	if on.Code != http.StatusTeapot || seen != "POST /chat/completions" {
		t.Fatalf("on = %d, handler saw %q", on.Code, seen)
	}
	wrongHost := authRequest(http.MethodGet, "/router/v1/models", "")
	wrongHost.Host = "evil.example.com"
	if rr := serveAuth(s, wrongHost); rr.Code != http.StatusForbidden {
		t.Fatalf("wrong host = %d", rr.Code)
	}
	fake.mu.Lock()
	fake.proxy = nil
	fake.mu.Unlock()
	if again := serveAuth(s, authRequest(http.MethodGet, "/router/v1/models", "")); again.Code != http.StatusNotFound {
		t.Fatalf("off again = %d", again.Code)
	}
}

func TestRouterUnavailable(t *testing.T) {
	store := &testLiveConfigStore{testConfigStore: newTestConfigStore(t, routerTestConfig, 0o600, ConfigSettings{}), advance: true}
	s, cookie, csrf := newLiveConfigTestServer(t, store)
	response := serveMCP(t, s, http.MethodGet, "/v1/router/usage", "", cookie, csrf)
	if response.code != http.StatusServiceUnavailable || decodeError(t, response).Error.Code != "router_unavailable" {
		t.Fatalf("usage without router = %d %s", response.code, response.body)
	}
	// The overview still works from config.toml, with catalog choices.
	overview := serveMCP(t, s, http.MethodGet, "/v1/router", "", cookie, csrf)
	if overview.code != http.StatusOK || !strings.Contains(string(overview.body), `"group":"deepseek"`) {
		t.Fatalf("overview without router = %d %s", overview.code, overview.body)
	}
	if proxy := serveAuth(s, authRequest(http.MethodGet, "/router/v1/models", "")); proxy.Code != http.StatusNotFound {
		t.Fatalf("proxy without router = %d", proxy.Code)
	}
}

// API-RTR-006: preset kinds without base_url, effective key env, the
// extended GET fields, live apply, and the in-use refusal.
func TestProvidersPresetKindsAndInUse(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	s, store, _, cookie, csrf := newRouterTestServer(t, routerTestConfig)
	var tested []ProviderTest
	s.SetBackend(&testProviderTesterBackend{fakeWebBackend: newFakeWebBackend(), test: func(_ context.Context, test ProviderTest) (TestResult, error) {
		tested = append(tested, test)
		return TestResult{OK: true, LatencyMS: 3}, nil
	}})

	list := serveMCP(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
	if list.code != http.StatusOK {
		t.Fatalf("list = %d %s", list.code, list.body)
	}
	var listed listProvidersResponse
	list.json(t, &listed)
	byName := map[string]providerJSON{}
	for _, row := range listed.Providers {
		byName[row.Name] = row
	}
	ds := byName["deepseek"]
	if ds.DefaultBaseURL != "https://api.deepseek.com/v1" || ds.APIKeyEnv != "DEEPSEEK_API_KEY" || ds.KeyState != "unset" ||
		ds.ModelsCount != 2 || strings.Join(ds.UsedBy, ",") != "agents.coder,combos.coding" {
		t.Fatalf("deepseek = %+v", ds)
	}
	if local := byName["local"]; strings.Join(local.UsedBy, ",") != "agents.coder,combos.coding,combos.idle,knowledge.embedding_provider" ||
		local.ModelsCount != 1 || local.APIKeyEnv != "LOCAL_KEY" {
		t.Fatalf("local = %+v", local)
	}
	if spare := byName["spare"]; spare.ModelsCount == 0 || spare.APIKeyEnv != "SPARE_API_KEY" {
		t.Fatalf("spare (catalog fallback) = %+v", spare)
	}

	// A preset kind saves without base_url and tests with the kind's model.
	body := `{"kind":"anthropic","api_key":"sk-ant-new","expected_version":"` + listed.Version + `"}`
	saved := serveMCP(t, s, http.MethodPut, "/v1/providers/claude", body, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	var savedBody providerSaveResponse
	saved.json(t, &savedBody)
	if savedBody.RestartRequired {
		t.Fatal("restart_required = true for a live provider save (AC-RTR-011)")
	}
	if len(tested) != 1 || tested[0].Kind != "anthropic" || tested[0].BaseURL != "" || tested[0].Model != "claude-haiku-5-5" {
		t.Fatalf("tested = %+v", tested)
	}
	data, _ := os.ReadFile(store.path)
	if !strings.Contains(string(data), "[providers.claude]\nkind = 'anthropic'\napi_key_env = 'CLAUDE_API_KEY'\n") {
		t.Fatalf("saved table:\n%s", data)
	}
	secrets, _ := os.ReadFile(filepath.Join(store.home, "secrets.env"))
	if !strings.Contains(string(secrets), "CLAUDE_API_KEY=sk-ant-new") {
		t.Fatalf("secrets.env = %q", secrets)
	}
	// The key write needs no restart: secrets.env is read live.
	if s.secretsRestartRequired() {
		t.Fatal("provider key write raised the secrets restart flag")
	}

	// openai_compat still needs base_url; config validation says so.
	compat := serveMCP(t, s, http.MethodPut, "/v1/providers/other",
		`{"kind":"openai_compat","expected_version":"`+savedBody.Version+`"}`, cookie, csrf)
	if compat.code != http.StatusUnprocessableEntity || !strings.Contains(string(compat.body), "providers.other.base_url") {
		t.Fatalf("compat without base_url = %d %s", compat.code, compat.body)
	}

	// Delete in use names agents, combos, and the embedding provider.
	used := serveMCP(t, s, http.MethodDelete, "/v1/providers/local", `{"expected_version":"`+savedBody.Version+`"}`, cookie, csrf)
	if used.code != http.StatusConflict {
		t.Fatalf("delete local = %d %s", used.code, used.body)
	}
	if envelope := decodeError(t, used); envelope.Error.Code != "in_use" ||
		envelope.Error.Message != "used by agents: coder; combos: coding, idle; knowledge.embedding_provider" {
		t.Fatalf("delete local error = %+v", envelope.Error)
	}
	deleted := serveMCP(t, s, http.MethodDelete, "/v1/providers/spare", `{"expected_version":"`+savedBody.Version+`"}`, cookie, csrf)
	if deleted.code != http.StatusOK {
		t.Fatalf("delete spare = %d %s", deleted.code, deleted.body)
	}
	deleted.json(t, &savedBody)
	if savedBody.RestartRequired {
		t.Fatal("provider delete asked for a restart")
	}

	// The test endpoint takes a preset kind without base_url.
	test := serveMCP(t, s, http.MethodPost, "/v1/providers/test", `{"kind":"deepseek","model":"deepseek-flash","api_key":"sk-x"}`, cookie, csrf)
	if test.code != http.StatusOK || len(tested) != 2 || tested[1].BaseURL != "" || tested[1].Kind != "deepseek" {
		t.Fatalf("test = %d %s %+v", test.code, test.body, tested)
	}
	noURL := serveMCP(t, s, http.MethodPost, "/v1/providers/test", `{"kind":"openai_compat","model":"m","api_key":"sk-x"}`, cookie, csrf)
	if noURL.code != http.StatusBadRequest {
		t.Fatalf("compat test without base_url = %d", noURL.code)
	}
}

func TestProviderTestModelDefaults(t *testing.T) {
	document, err := parseProviderDocument([]byte(routerTestConfig))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		kind string
		want string
	}{
		{"deepseek", "deepseek", "deepseek-flash"}, // agent coder's address
		{"local", "openai_compat", "qwen"},         // combo entry
		{"oa", "openai", "gpt-5-mini"},
		{"ant", "anthropic", "claude-haiku-5-5"},
		{"or", "openrouter", "openai/gpt-5-mini"},
		{"ds2", "deepseek", "deepseek-flash"},
		{"custom", "openai_compat", "gpt-5-mini"},
	}
	for _, tc := range cases {
		if got := providerTestModel(tc.name, providerRecord{Kind: tc.kind}, document); got != tc.want {
			t.Fatalf("providerTestModel(%s, %s) = %q, want %q", tc.name, tc.kind, got, tc.want)
		}
	}
	if got := providerTestModel("x", providerRecord{Kind: "openai_compat", BaseURL: "https://api.deepseek.com/v1"}, providerDocument{}); got != "deepseek-flash" {
		t.Fatalf("deepseek host = %q", got)
	}
}
