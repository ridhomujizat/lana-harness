package web

import (
	"net/http"
	"strings"
	"testing"
)

// Spec 80: API connections of one kind and base URL are one ordered pool.
const apiPoolTestConfig = routerTestConfig + `
[providers.commandcode]
kind = "commandcode"
api_key_envs = ["CC_BACKUP"]

[providers.commandcode-2]
kind = "commandcode"
base_url = "https://API.commandcode.ai/"
timeout = "30s"

[providers.commandcode-3]
kind = "commandcode"
disabled = true
`

// REQ-ACP-003: PUT /v1/router/pool-order with runtime "api" writes priority
// 10, 20, 30 in the new order and pool = false for unpooled connections, in
// one write; their other keys stay.
func TestPutAPIPoolOrder(t *testing.T) {
	s, store, _, cookie, csrf := newRouterTestServer(t, apiPoolTestConfig)
	saved := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"api","order":["commandcode-2","commandcode"],"unpooled":["commandcode-3"],"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	var response poolOrderResponse
	saved.json(t, &response)
	if strings.Join(response.Pool, ",") != "commandcode-2,commandcode" || response.Version != configVersion(t, store) {
		t.Fatalf("response = %+v", response)
	}
	config := string(mustRead(t, store.path))
	for _, want := range []string{
		"[providers.commandcode-2]\nkind = 'commandcode'\nbase_url = 'https://API.commandcode.ai/'\ntimeout = '30s'\npriority = 10\n",
		"[providers.commandcode]\nkind = 'commandcode'\napi_key_envs = ['CC_BACKUP']\npriority = 20\n",
		"[providers.commandcode-3]\nkind = 'commandcode'\ndisabled = true\npriority = 30\npool = false\n",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config lacks %q:\n%s", want, config)
		}
	}
	// Other connections are not touched.
	if !strings.Contains(config, "[providers.deepseek]\nkind = \"deepseek\"\n") {
		t.Fatalf("deepseek changed:\n%s", config)
	}

	// Back into the pool: pool = false goes.
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"api","order":["commandcode","commandcode-3","commandcode-2"]}`, cookie, csrf); got.code != http.StatusOK ||
		strings.Contains(string(mustRead(t, store.path)), "pool = false") ||
		!strings.Contains(string(mustRead(t, store.path)), "[providers.commandcode-3]\nkind = 'commandcode'\ndisabled = true\npriority = 20\n") {
		t.Fatalf("repool = %d %s\n%s", got.code, got.body, mustRead(t, store.path))
	}

	before := string(mustRead(t, store.path))
	for body, code := range map[string]int{
		`{"runtime":"api","order":["commandcode","commandcode-2"]}`:                                            http.StatusUnprocessableEntity, // commandcode-3 missing
		`{"runtime":"api","order":["commandcode","commandcode-2","commandcode-3","nobody"]}`:                   http.StatusUnprocessableEntity, // unknown
		`{"runtime":"api","order":["commandcode","commandcode-2","commandcode-3","commandcode"]}`:              http.StatusUnprocessableEntity, // duplicate
		`{"runtime":"api","order":["commandcode","commandcode-2","commandcode-3","deepseek"]}`:                 http.StatusUnprocessableEntity, // another pool
		`{"runtime":"api","order":["commandcode","commandcode-2"],"unpooled":["commandcode-3","local"]}`:       http.StatusUnprocessableEntity, // another pool
		`{"runtime":"api","order":["local","spare"]}`:                                                          http.StatusUnprocessableEntity, // two kinds
		`{"runtime":"api","order":["antigravity"]}`:                                                            http.StatusUnprocessableEntity, // not an API connection
		`{"runtime":"api","order":[]}`:                                                                         http.StatusUnprocessableEntity,
		`{"runtime":"api","order":["commandcode","commandcode-2","commandcode-3"],"expected_version":"stale"}`: http.StatusConflict,
	} {
		got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order", body, cookie, csrf)
		if got.code != code {
			t.Fatalf("pool order %s = %d %s", body, got.code, got.body)
		}
		if code == http.StatusConflict && !strings.Contains(string(got.body), "version_conflict") {
			t.Fatalf("conflict = %s", got.body)
		}
	}
	if string(mustRead(t, store.path)) != before {
		t.Fatal("a refused pool order changed the config")
	}

	// One issue per problem.
	got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"api","order":["commandcode","deepseek","nobody"]}`, cookie, csrf)
	for _, want := range []string{"order[1]", "deepseek is not in the pool of commandcode", "order[2]", "no API connection named nobody",
		"commandcode-2 is missing", "commandcode-3 is missing"} {
		if !strings.Contains(string(got.body), want) {
			t.Fatalf("issues lack %q: %s", want, got.body)
		}
	}
}

// REQ-ACP-004: GET /v1/providers and GET /v1/router report priority, pool,
// and pool_group of each API connection; REQ-ACP-002: a save from the
// provider dialog keeps priority and pool.
func TestAPIConnectionPoolFields(t *testing.T) {
	s, store, _, cookie, csrf := newRouterTestServer(t, apiPoolTestConfig+"\n[providers.local-2]\nkind = \"openai_compat\"\nbase_url = \"http://127.0.0.1:8092/v1\"\n")
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"api","order":["commandcode-2","commandcode"],"unpooled":["commandcode-3"]}`, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("order = %d %s", got.code, got.body)
	}
	type row struct {
		Name      string `json:"name"`
		Priority  *int   `json:"priority"`
		Pool      *bool  `json:"pool"`
		PoolGroup string `json:"pool_group"`
	}
	check := func(path string, rows []row) {
		t.Helper()
		byName := map[string]row{}
		for _, r := range rows {
			byName[r.Name] = r
		}
		cc, cc2, cc3 := byName["commandcode"], byName["commandcode-2"], byName["commandcode-3"]
		if cc2.Priority == nil || *cc2.Priority != 10 || cc2.Pool == nil || !*cc2.Pool ||
			cc.Priority == nil || *cc.Priority != 20 || cc3.Pool == nil || *cc3.Pool {
			t.Fatalf("%s rows = %+v", path, rows)
		}
		if cc.PoolGroup == "" || cc.PoolGroup != cc2.PoolGroup || cc.PoolGroup != cc3.PoolGroup {
			t.Fatalf("%s commandcode groups = %q %q %q", path, cc.PoolGroup, cc2.PoolGroup, cc3.PoolGroup)
		}
		ds, local, local2 := byName["deepseek"], byName["local"], byName["local-2"]
		if ds.Priority == nil || *ds.Priority != 100 || ds.Pool == nil || !*ds.Pool || ds.PoolGroup == cc.PoolGroup {
			t.Fatalf("%s deepseek = %+v", path, ds)
		}
		if local.PoolGroup == "" || local.PoolGroup == local2.PoolGroup {
			t.Fatalf("%s local groups = %q %q", path, local.PoolGroup, local2.PoolGroup)
		}
	}
	var providers struct {
		Providers []row `json:"providers"`
	}
	serveMCP(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf).json(t, &providers)
	check("/v1/providers", providers.Providers)
	for _, r := range providers.Providers {
		if r.Name == "claude-code" && (r.Priority != nil || r.PoolGroup != "") {
			t.Fatalf("claude-code row = %+v", r)
		}
	}
	var overview struct {
		Providers []row `json:"providers"`
	}
	serveMCP(t, s, http.MethodGet, "/v1/router", "", cookie, csrf).json(t, &overview)
	check("/v1/router", overview.Providers)

	// Turning a connection on from the provider page keeps its order.
	if got := serveMCP(t, s, http.MethodPut, "/v1/providers/commandcode-3",
		`{"kind":"commandcode","disabled":false,"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("save = %d %s", got.code, got.body)
	}
	if config := string(mustRead(t, store.path)); !strings.Contains(config, "[providers.commandcode-3]\nkind = 'commandcode'\napi_key_env = 'COMMANDCODE_3_API_KEY'\npriority = 30\npool = false\n") {
		t.Fatalf("config:\n%s", config)
	}
	// An edit that changes the base URL keeps them too.
	if got := serveMCP(t, s, http.MethodPut, "/v1/providers/commandcode",
		`{"kind":"commandcode","base_url":"https://api.commandcode.ai","api_key_env":"COMMANDCODE_API_KEY","expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("edit = %d %s", got.code, got.body)
	}
	if config := string(mustRead(t, store.path)); !strings.Contains(config, "[providers.commandcode]\nkind = 'commandcode'\nbase_url = 'https://api.commandcode.ai'\napi_key_env = 'COMMANDCODE_API_KEY'\napi_key_envs = ['CC_BACKUP']\npriority = 20\n") {
		t.Fatalf("config:\n%s", config)
	}
}
