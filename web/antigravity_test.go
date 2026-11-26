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
)

// fakeAgyBackend is the Antigravity part of the fake router backend
// (spec 74).
type fakeAgyBackend struct {
	*fakeRouterBackend
	installed bool

	mu      sync.Mutex
	started []AntigravityAccountRef
	codes   []string
	states  map[string]AntigravitySignInStatus
	forgot  []string
	status  AntigravityStatus
	cooling map[string]map[string]time.Time
}

func (f *fakeAgyBackend) AntigravityInstalled(context.Context) bool { return f.installed }

func (f *fakeAgyBackend) AntigravityStatus(context.Context) (AntigravityStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.status
	status.Accounts = append([]AntigravityAccountStatus(nil), f.status.Accounts...)
	return status, nil
}

func (f *fakeAgyBackend) StartAntigravitySignIn(ref AntigravityAccountRef) (AntigravitySignInStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref.Name == "antigravity" {
		return AntigravitySignInStatus{State: AntigravitySignInIdle}, ErrAntigravityBuiltin
	}
	switch f.states[ref.Name].State {
	case "starting", AntigravitySignInWaitingCode, "running":
		return f.states[ref.Name], ErrAntigravitySignInRunning
	}
	f.started = append(f.started, ref)
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	// The fake shows the link at once.
	state := AntigravitySignInStatus{State: AntigravitySignInWaitingCode, URL: "https://accounts.google.com/o/oauth2/auth?x=1", StartedAt: &at}
	f.states[ref.Name] = state
	return state, nil
}

func (f *fakeAgyBackend) AntigravitySignInState(name string) AntigravitySignInStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state, ok := f.states[name]; ok {
		return state
	}
	return AntigravitySignInStatus{State: AntigravitySignInIdle}
}

func (f *fakeAgyBackend) SubmitAntigravityCode(name, code string) (AntigravitySignInStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.states[name]
	if state.State != AntigravitySignInWaitingCode {
		if state.State == "" {
			state.State = AntigravitySignInIdle
		}
		return state, ErrAntigravityNotWaiting
	}
	f.codes = append(f.codes, code)
	state.State, state.URL = "running", ""
	f.states[name] = state
	return state, nil
}

func (f *fakeAgyBackend) CancelAntigravitySignIn(name string) AntigravitySignInStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.states[name]
	switch state.State {
	case "starting", AntigravitySignInWaitingCode, "running":
		state.State, state.URL = "cancelled", ""
		f.states[name] = state
	case "":
		state.State = AntigravitySignInIdle
	}
	return state
}

func (f *fakeAgyBackend) ForgetAntigravityAccount(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgot = append(f.forgot, name)
	delete(f.states, name)
}

func (f *fakeAgyBackend) ProviderKeyCooling(provider string) map[string]time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cooling[provider]
}

func newAgyServer(t *testing.T, raw string) (*Server, *testLiveConfigStore, *fakeAgyBackend, *http.Cookie, string) {
	t.Helper()
	s, store, fake, cookie, csrf := newRouterTestServer(t, raw)
	agy := &fakeAgyBackend{fakeRouterBackend: fake, installed: true, states: map[string]AntigravitySignInStatus{}}
	s.SetRouterBackend(agy)
	return s, store, agy, cookie, csrf
}

// API-AGY-021..023: POST …/antigravity/accounts adds an account in one click
// (agy-2, agy-3, …), creates its home (0700), and starts the sign-in; the
// sign-in shows the link while it waits for the code, takes one code, and
// can be read, refused while running, and cancelled. The built-in account
// signs in with agy in a terminal.
func TestAntigravityAccountCreateAndSignIn(t *testing.T) {
	s, store, agy, cookie, csrf := newAgyServer(t, routerTestConfig)

	created := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", `{"label":"Work"}`, cookie, csrf)
	if created.code != http.StatusAccepted {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	var response antigravityAccountCreateResponse
	created.json(t, &response)
	home := filepath.Join(store.home, "antigravity", "agy-2")
	if response.Name != "agy-2" || response.Home != home || response.SignIn.State != AntigravitySignInWaitingCode ||
		!strings.HasPrefix(response.SignIn.URL, "https://accounts.google.com/") || response.Version == "" {
		t.Fatalf("response = %+v", response)
	}
	if info, err := os.Stat(home); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("home = %v %v", info, err)
	}
	if !strings.Contains(string(mustRead(t, store.path)), "[providers.agy-2]\nkind = 'antigravity'\nlabel = 'Work'\n") {
		t.Fatalf("config:\n%s", mustRead(t, store.path))
	}
	if len(agy.started) != 1 || agy.started[0] != (AntigravityAccountRef{Name: "agy-2", Home: home}) {
		t.Fatalf("started = %+v", agy.started)
	}
	second := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", "", cookie, csrf)
	second.json(t, &response)
	if second.code != http.StatusAccepted || response.Name != "agy-3" {
		t.Fatalf("second = %d %s", second.code, second.body)
	}
	for body, code := range map[string]int{
		`{"name":"agy-2"}`:                            http.StatusConflict,
		`{"name":"antigravity"}`:                      http.StatusConflict,
		`{"name":"deepseek"}`:                         http.StatusConflict,
		`{"name":"Bad Name"}`:                         http.StatusUnprocessableEntity,
		`{"label":"` + strings.Repeat("x", 61) + `"}`: http.StatusUnprocessableEntity,
		`{"other":1}`:                                 http.StatusBadRequest,
	} {
		if got := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", body, cookie, csrf); got.code != code {
			t.Fatalf("create %s = %d %s", body, got.code, got.body)
		}
	}

	base := "/v1/router/antigravity/accounts/agy-2/sign-in"
	busy := serveMCP(t, s, http.MethodPost, base, "", cookie, csrf)
	if busy.code != http.StatusConflict || !strings.Contains(string(busy.body), "sign_in_running") {
		t.Fatalf("second sign-in = %d %s", busy.code, busy.body)
	}
	state := serveMCP(t, s, http.MethodGet, base, "", cookie, csrf)
	if state.code != http.StatusOK || !strings.Contains(string(state.body), `"state":"waiting_code"`) ||
		!strings.Contains(string(state.body), `"url":"https://accounts.google.com/`) {
		t.Fatalf("state = %d %s", state.code, state.body)
	}
	for body, code := range map[string]int{
		`{"code":""}`:     http.StatusUnprocessableEntity,
		`{"code":"  "}`:   http.StatusUnprocessableEntity,
		`{"code":"a\nb"}`: http.StatusUnprocessableEntity,
		`{"code":"` + strings.Repeat("c", 2049) + `"}`: http.StatusUnprocessableEntity,
		`{}`: http.StatusUnprocessableEntity,
	} {
		got := serveMCP(t, s, http.MethodPost, base+"/code", body, cookie, csrf)
		if got.code != code || !strings.Contains(string(got.body), "bad_code") {
			t.Fatalf("code %q = %d %s", body, got.code, got.body)
		}
	}
	sent := serveMCP(t, s, http.MethodPost, base+"/code", `{"code":"  4/abc  "}`, cookie, csrf)
	if sent.code != http.StatusAccepted || !strings.Contains(string(sent.body), `"state":"running"`) || !strings.Contains(string(sent.body), `"url":""`) {
		t.Fatalf("code = %d %s", sent.code, sent.body)
	}
	if len(agy.codes) != 1 || agy.codes[0] != "4/abc" {
		t.Fatalf("codes = %v", agy.codes)
	}
	again := serveMCP(t, s, http.MethodPost, base+"/code", `{"code":"4/abc"}`, cookie, csrf)
	if again.code != http.StatusConflict || !strings.Contains(string(again.body), "not_waiting") {
		t.Fatalf("second code = %d %s", again.code, again.body)
	}
	cancelled := serveMCP(t, s, http.MethodDelete, base, "", cookie, csrf)
	if cancelled.code != http.StatusOK || !strings.Contains(string(cancelled.body), `"state":"cancelled"`) {
		t.Fatalf("cancel = %d %s", cancelled.code, cancelled.body)
	}
	restart := serveMCP(t, s, http.MethodPost, base, "", cookie, csrf)
	if restart.code != http.StatusAccepted || len(agy.started) != 3 {
		t.Fatalf("restart = %d %s (%d started)", restart.code, restart.body, len(agy.started))
	}
	builtin := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts/antigravity/sign-in", "", cookie, csrf)
	if builtin.code != http.StatusConflict || !strings.Contains(string(builtin.body), `"builtin"`) {
		t.Fatalf("built-in = %d %s", builtin.code, builtin.body)
	}
	if got := serveMCP(t, s, http.MethodGet, "/v1/router/antigravity/accounts/antigravity/sign-in", "", cookie, csrf); got.code != http.StatusOK ||
		!strings.Contains(string(got.body), `"state":"idle"`) {
		t.Fatalf("built-in state = %d %s", got.code, got.body)
	}
	for _, name := range []string{"nobody", "deepseek"} {
		if got := serveMCP(t, s, http.MethodGet, "/v1/router/antigravity/accounts/"+name+"/sign-in", "", cookie, csrf); got.code != http.StatusNotFound {
			t.Fatalf("%s = %d %s", name, got.code, got.body)
		}
	}
	// Writes need the CSRF token.
	if got := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", "", cookie, ""); got.code != http.StatusForbidden {
		t.Fatalf("no csrf = %d %s", got.code, got.body)
	}
}

// API-AGY-020: GET /v1/router/antigravity answers the accounts with their
// label from config.toml, display name, and sign-in state; empty lists are
// [] and the min version is set.
func TestAntigravityStatus(t *testing.T) {
	s, store, agy, cookie, csrf := newAgyServer(t, routerTestConfig+`
[providers.agy-2]
kind = "antigravity"
label = "Work"
`)
	_ = store
	remaining := 0.75
	agy.status = AntigravityStatus{
		Installed: true, Binary: "/bin/agy", Version: "1.5.17", VersionOK: true, PoolWarning: true,
		Accounts: []AntigravityAccountStatus{
			{Name: "agy-2", SignedIn: true, Tier: "Google AI Pro", Email: "second@example.com", Priority: 10, Pool: true,
				Windows: []AntigravityWindow{{Group: "Gemini Models", Type: "5h", Remaining: &remaining}}},
			{Name: "antigravity", Builtin: true, Priority: 20, Pool: true},
		},
	}
	agy.states["agy-2"] = AntigravitySignInStatus{State: "succeeded"}
	got := serveMCP(t, s, http.MethodGet, "/v1/router/antigravity", "", cookie, csrf)
	if got.code != http.StatusOK {
		t.Fatalf("status = %d %s", got.code, got.body)
	}
	var body AntigravityStatus
	got.json(t, &body)
	if body.MinVersion != AntigravityMinVersion || !body.PoolWarning || len(body.Accounts) != 2 {
		t.Fatalf("body = %s", got.body)
	}
	work, builtin := body.Accounts[0], body.Accounts[1]
	if work.Label != "Work" || work.DisplayName != "Work" || work.SignIn == nil || work.SignIn.State != "succeeded" ||
		work.Tier != "Google AI Pro" || len(work.Windows) != 1 || work.Agents == nil || work.Combos == nil {
		t.Fatalf("agy-2 = %+v", work)
	}
	if builtin.DisplayName != "antigravity" || builtin.SignIn.State != "idle" || builtin.Windows == nil {
		t.Fatalf("built-in = %+v", builtin)
	}
	// Spec 77 REQ-WRA-014: each account has email, "" when unknown.
	if work.Email != "second@example.com" || !strings.Contains(string(got.body), `"email":""`) {
		t.Fatalf("email: %s", got.body)
	}
	for _, want := range []string{`"cooling_until":null`, `"windows_updated_at":null`, `"usage_24h":{"calls":0`} {
		if !strings.Contains(string(got.body), want) {
			t.Fatalf("body lacks %s: %s", want, got.body)
		}
	}
}

// API-AGY-024: PUT …/label stores the label of an added account; the
// built-in account has none.
func TestAntigravityAccountLabel(t *testing.T) {
	s, store, _, cookie, csrf := newAgyServer(t, routerTestConfig+`
[providers.agy-2]
kind = "antigravity"
allow_scheduled = true
priority = 30
`)
	path := "/v1/router/antigravity/accounts/agy-2/label"
	saved := serveMCP(t, s, http.MethodPut, path, `{"label":"  Second  ","expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if saved.code != http.StatusOK || !strings.Contains(string(saved.body), `"label":"Second"`) {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	if !strings.Contains(string(mustRead(t, store.path)), "[providers.agy-2]\nkind = 'antigravity'\nallow_scheduled = true\nlabel = 'Second'\npriority = 30\n") {
		t.Fatalf("config:\n%s", mustRead(t, store.path))
	}
	for body, code := range map[string]int{
		`{"label":"` + strings.Repeat("x", 61) + `"}`: http.StatusUnprocessableEntity,
		`{"label":"a\nb"}`:                            http.StatusUnprocessableEntity,
		`{}`:                                          http.StatusUnprocessableEntity,
		`{"label":"x","expected_version":"stale"}`: http.StatusConflict,
	} {
		if got := serveMCP(t, s, http.MethodPut, path, body, cookie, csrf); got.code != code {
			t.Fatalf("label %s = %d %s", body, got.code, got.body)
		}
	}
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/antigravity/accounts/antigravity/label", `{"label":"Home"}`, cookie, csrf); got.code != http.StatusConflict {
		t.Fatalf("built-in label = %d %s", got.code, got.body)
	}
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/antigravity/accounts/deepseek/label", `{"label":"x"}`, cookie, csrf); got.code != http.StatusNotFound {
		t.Fatalf("other kind = %d %s", got.code, got.body)
	}
}

// API-AGY-002, API-AGY-003: PUT /v1/providers/{name} saves an Antigravity
// account (home created 0700, pool keys kept); the built-in account takes
// only its settings, and API keys are refused.
func TestAntigravityProviderPut(t *testing.T) {
	s, store, _, cookie, csrf := newAgyServer(t, routerTestConfig+`
[providers.agy-2]
kind = "antigravity"
priority = 5
`)
	saved := serveMCP(t, s, http.MethodPut, "/v1/providers/agy-2",
		`{"kind":"antigravity","label":"Work","allow_scheduled":true,"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if saved.code != http.StatusOK || !strings.Contains(string(saved.body), `"home":"`+filepath.Join(store.home, "antigravity", "agy-2")+`"`) {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	if !strings.Contains(string(mustRead(t, store.path)), "[providers.agy-2]\nkind = 'antigravity'\nallow_scheduled = true\nlabel = 'Work'\npriority = 5\n") {
		t.Fatalf("config:\n%s", mustRead(t, store.path))
	}
	builtin := serveMCP(t, s, http.MethodPut, "/v1/providers/antigravity",
		`{"kind":"antigravity","allow_scheduled":true,"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if builtin.code != http.StatusOK || !strings.Contains(string(mustRead(t, store.path)), "[providers.antigravity]\nkind = 'antigravity'\nallow_scheduled = true\n") {
		t.Fatalf("built-in = %d %s\n%s", builtin.code, builtin.body, mustRead(t, store.path))
	}
	for path, body := range map[string]string{
		"/v1/providers/antigravity":  `{"kind":"antigravity","home":"/tmp/x","expected_version":"v"}`,
		"/v1/providers/agy-2":        `{"kind":"antigravity","api_key_env":"X","expected_version":"v"}`,
		"/v1/providers/agy-9":        `{"kind":"antigravity","label":"` + strings.Repeat("y", 61) + `","expected_version":"v"}`,
		"/v1/providers/antigravity?": `{"kind":"openai","expected_version":"v"}`,
	} {
		if got := serveMCP(t, s, http.MethodPut, path, body, cookie, csrf); got.code != http.StatusUnprocessableEntity {
			t.Fatalf("%s %s = %d %s", path, body, got.code, got.body)
		}
	}
	// The built-in account is not deleted.
	if got := serveMCP(t, s, http.MethodDelete, "/v1/providers/antigravity", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf); got.code != http.StatusConflict {
		t.Fatalf("delete built-in = %d %s", got.code, got.body)
	}
}

// AC-AGY-014 at the HTTP level (API-AGY-025): DELETE …?remove_folder=1 cancels
// the sign-in, deletes the home, then the table; a home outside
// $LANA_HOME/antigravity/ answers 409 folder_outside_home and deletes
// nothing; in-use rules come first. Without the flag the home stays.
func TestAntigravityAccountRemoveFolder(t *testing.T) {
	s, store, agy, cookie, csrf := newAgyServer(t, routerTestConfig)
	if created := serveMCP(t, s, http.MethodPost, "/v1/router/antigravity/accounts", "", cookie, csrf); created.code != http.StatusAccepted {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	home := filepath.Join(store.home, "antigravity", "agy-2")
	if err := os.WriteFile(filepath.Join(home, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := "\n[agents.worker]\nmodel = \"agy-2/gemini-4-neon-high\"\n"
	if err := os.WriteFile(store.path, append(mustRead(t, store.path), agent...), 0o600); err != nil {
		t.Fatal(err)
	}
	inUse := serveMCP(t, s, http.MethodDelete, "/v1/providers/agy-2?remove_folder=1", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if inUse.code != http.StatusConflict || !strings.Contains(string(inUse.body), "in_use") || len(agy.forgot) != 0 {
		t.Fatalf("in use = %d %s", inUse.code, inUse.body)
	}
	if err := os.WriteFile(store.path, []byte(strings.Replace(string(mustRead(t, store.path)), agent, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	removed := serveMCP(t, s, http.MethodDelete, "/v1/providers/agy-2?remove_folder=1", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	var response providerSaveResponse
	removed.json(t, &response)
	if removed.code != http.StatusOK || !response.FolderRemoved || response.Home != home {
		t.Fatalf("remove = %d %s", removed.code, removed.body)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("home still there: %v", err)
	}
	if strings.Contains(string(mustRead(t, store.path)), "agy-2") {
		t.Fatal("table still there")
	}
	if len(agy.forgot) != 1 || agy.forgot[0] != "agy-2" {
		t.Fatalf("forgot = %v", agy.forgot)
	}

	outside := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, folder := range map[string]string{"agy-x": outside, "agy-y": "$LANA_HOME/antigravity/../escape"} {
		saved := serveMCP(t, s, http.MethodPut, "/v1/providers/"+name,
			`{"kind":"antigravity","home":"`+folder+`","expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
		if saved.code != http.StatusOK {
			t.Fatalf("save %s = %d %s", name, saved.code, saved.body)
		}
		before := string(mustRead(t, store.path))
		refused := serveMCP(t, s, http.MethodDelete, "/v1/providers/"+name+"?remove_folder=1", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
		if refused.code != http.StatusConflict || !strings.Contains(string(refused.body), "folder_outside_home") {
			t.Fatalf("outside %s = %d %s", name, refused.code, refused.body)
		}
		if string(mustRead(t, store.path)) != before {
			t.Fatalf("config changed for %s", name)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside folder: %v", err)
	}
	if len(agy.forgot) != 1 {
		t.Fatalf("forgot = %v", agy.forgot)
	}
	kept := serveMCP(t, s, http.MethodDelete, "/v1/providers/agy-x", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if kept.code != http.StatusOK || !strings.Contains(string(kept.body), "was kept") {
		t.Fatalf("keep = %d %s", kept.code, kept.body)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside folder: %v", err)
	}
}

func TestAntigravityFolderRemovable(t *testing.T) {
	s, store, _, _, _ := newAgyServer(t, routerTestConfig)
	root := filepath.Join(store.home, "antigravity")
	cases := map[string]bool{
		filepath.Join(root, "agy-2"):   true,
		root:                           false,
		filepath.Join(root, "..", "x"): false,
		filepath.Join(store.home, "antigravityX", "y"): false,
		"relative/agy-2": false,
		"":               false,
		filepath.Join(t.TempDir(), "antigravity", "a-2"): false,
	}
	for dir, want := range cases {
		if got := s.antigravityFolderRemovable(dir); got != want {
			t.Errorf("removable(%q) = %v, want %v", dir, got, want)
		}
	}
}

// API-AGY-026: GET /v1/router lists Antigravity models in one group
// "Antigravity" with one enabled account, one group per account with
// several, and nothing when agy is not installed.
func TestRouterAntigravityChoices(t *testing.T) {
	builtinModels := RouterModelList{Provider: "antigravity", Models: []RouterModel{
		{ID: "gemini-4-neon-high", Address: "antigravity/gemini-4-neon-high", Name: "Antigravity · Gemini 4 Neon (High)", Source: "runtime"},
		{ID: "gemini-4-neon", Address: "antigravity/gemini-4-neon", Name: "Antigravity · Gemini 4 Neon", Source: "runtime",
			Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "high"},
	}}
	work := RouterModelList{Provider: "agy-2", Models: []RouterModel{
		{ID: "gemini-3.8-flash-low", Address: "agy-2/gemini-3.8-flash-low", Name: "Antigravity · Gemini 3.8 Flash (Low)", Source: "runtime"},
	}}
	groups := func(t *testing.T, raw string, installed bool) map[string][]routerChoiceJSON {
		t.Helper()
		s, _, agy, cookie, csrf := newAgyServer(t, raw)
		agy.installed = installed
		agy.lists["antigravity"] = builtinModels
		agy.lists["agy-2"] = work
		response := serveMCP(t, s, http.MethodGet, "/v1/router", "", cookie, csrf)
		if response.code != http.StatusOK {
			t.Fatalf("status = %d %s", response.code, response.body)
		}
		var body routerOverviewResponse
		response.json(t, &body)
		out := map[string][]routerChoiceJSON{}
		for _, choice := range body.Choices {
			if strings.HasPrefix(choice.Group, "Antigravity") {
				out[choice.Group] = append(out[choice.Group], choice)
			}
		}
		return out
	}
	one := groups(t, routerTestConfig, true)
	if len(one) != 1 || len(one["Antigravity"]) != 2 || one["Antigravity"][0].Value != "antigravity/gemini-4-neon-high" ||
		one["Antigravity"][0].Label != "Antigravity · Gemini 4 Neon (High)" || one["Antigravity"][0].Efforts != nil {
		t.Fatalf("one account = %+v", one)
	}
	// Spec 76 REQ-MES-002: a choice carries the model's effort levels.
	if base := one["Antigravity"][1]; base.Value != "antigravity/gemini-4-neon" ||
		strings.Join(base.Efforts, ",") != "low,medium,high,xhigh" || base.DefaultEffort != "high" {
		t.Fatalf("base choice = %+v", base)
	}
	two := groups(t, routerTestConfig+"\n[providers.agy-2]\nkind = \"antigravity\"\nlabel = \"Work\"\n", true)
	if len(two) != 2 || len(two["Antigravity · antigravity"]) != 2 || len(two["Antigravity · Work"]) != 1 ||
		two["Antigravity · Work"][0].Value != "agy-2/gemini-3.8-flash-low" {
		t.Fatalf("two accounts = %+v", two)
	}
	disabled := groups(t, routerTestConfig+"\n[providers.antigravity]\nkind = \"antigravity\"\ndisabled = true\n\n[providers.agy-2]\nkind = \"antigravity\"\n", true)
	if len(disabled) != 1 || len(disabled["Antigravity"]) != 1 || disabled["Antigravity"][0].Value != "agy-2/gemini-3.8-flash-low" {
		t.Fatalf("built-in disabled = %+v", disabled)
	}
	if none := groups(t, routerTestConfig, false); len(none) != 0 {
		t.Fatalf("not installed = %+v", none)
	}
}

const poolTestConfig = routerTestConfig + `
[providers.agy-2]
kind = "antigravity"

[providers.agy-3]
kind = "antigravity"
label = "Third"

[providers.claude-2]
kind = "claude-code"
`

// AC-PAF-007: PUT /v1/router/pool-order with agy-3 dragged to the top writes
// priority 10 for agy-3, 20 for antigravity (in [providers.antigravity]), 30
// for agy-2 in one write; unpooled accounts get pool = false; unknown,
// missing, and duplicate names are 422.
func TestPutPoolOrder(t *testing.T) {
	s, store, _, cookie, csrf := newAgyServer(t, poolTestConfig)
	saved := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"antigravity","order":["agy-3","antigravity","agy-2"],"unpooled":[],"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	var response poolOrderResponse
	saved.json(t, &response)
	if strings.Join(response.Pool, ",") != "agy-3,antigravity,agy-2" || response.Version != configVersion(t, store) {
		t.Fatalf("response = %+v", response)
	}
	config := string(mustRead(t, store.path))
	for _, want := range []string{
		"[providers.agy-3]\nkind = 'antigravity'\nlabel = 'Third'\npriority = 10\n",
		"[providers.antigravity]\nkind = 'antigravity'\npriority = 20\n",
		"[providers.agy-2]\nkind = 'antigravity'\npriority = 30\n",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config lacks %q:\n%s", want, config)
		}
	}

	// agy-2 leaves the pool; agy-3 goes back below the built-in account.
	saved = serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"antigravity","order":["antigravity","agy-3"],"unpooled":["agy-2"]}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("unpool = %d %s", saved.code, saved.body)
	}
	config = string(mustRead(t, store.path))
	for _, want := range []string{
		"[providers.antigravity]\nkind = 'antigravity'\npriority = 10\n",
		"[providers.agy-3]\nkind = 'antigravity'\nlabel = 'Third'\npriority = 20\n",
		"[providers.agy-2]\nkind = 'antigravity'\npriority = 30\npool = false\n",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config lacks %q:\n%s", want, config)
		}
	}
	// Back into the pool: pool = false goes.
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"antigravity","order":["agy-2","antigravity","agy-3"],"unpooled":[]}`, cookie, csrf); got.code != http.StatusOK ||
		strings.Contains(string(mustRead(t, store.path)), "pool = false") {
		t.Fatalf("repool = %d %s\n%s", got.code, got.body, mustRead(t, store.path))
	}

	// Claude Code: the built-in table is written with its priority.
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order",
		`{"runtime":"claude-code","order":["claude-2","claude-code"]}`, cookie, csrf); got.code != http.StatusOK ||
		!strings.Contains(string(mustRead(t, store.path)), "[providers.claude-code]\nkind = 'claude-code'\npriority = 20\n") ||
		!strings.Contains(string(mustRead(t, store.path)), "[providers.claude-2]\nkind = 'claude-code'\npriority = 10\n") {
		t.Fatalf("claude = %d %s\n%s", got.code, got.body, mustRead(t, store.path))
	}

	before := string(mustRead(t, store.path))
	for body, code := range map[string]int{
		`{"runtime":"antigravity","order":["agy-3","antigravity"]}`:                                    http.StatusUnprocessableEntity, // agy-2 missing
		`{"runtime":"antigravity","order":["agy-3","antigravity","agy-2","nobody"]}`:                   http.StatusUnprocessableEntity, // unknown
		`{"runtime":"antigravity","order":["agy-3","antigravity","agy-2","agy-3"]}`:                    http.StatusUnprocessableEntity, // duplicate
		`{"runtime":"antigravity","order":["agy-3","antigravity"],"unpooled":["agy-2","agy-3"]}`:       http.StatusUnprocessableEntity,
		`{"runtime":"antigravity","order":["agy-3","antigravity","claude-2","agy-2"]}`:                 http.StatusUnprocessableEntity, // another runtime
		`{"runtime":"deepseek","order":["deepseek"]}`:                                                  http.StatusUnprocessableEntity,
		`{"runtime":"antigravity","order":[]}`:                                                         http.StatusUnprocessableEntity,
		`{"runtime":"antigravity","order":["agy-3","antigravity","agy-2"],"expected_version":"stale"}`: http.StatusConflict,
		`{"runtime":"antigravity","order":"agy-3"}`:                                                    http.StatusBadRequest,
	} {
		if got := serveMCP(t, s, http.MethodPut, "/v1/router/pool-order", body, cookie, csrf); got.code != code {
			t.Fatalf("pool order %s = %d %s", body, got.code, got.body)
		}
	}
	if string(mustRead(t, store.path)) != before {
		t.Fatal("a refused pool order changed the config")
	}
}

// AC-PAF-008: PUT /v1/providers/{name}/keys with K2 dragged above K1 writes
// api_key_env = "K2", api_key_envs = ["K1"]; one key removes api_key_envs.
// GET /v1/providers lists the keys with presence and cooldown.
func TestPutProviderKeys(t *testing.T) {
	t.Setenv("K1", "")
	t.Setenv("K2", "")
	s, store, agy, cookie, csrf := newAgyServer(t, routerTestConfig+`
[providers.ds]
kind = "deepseek"
api_key_env = "K1"
api_key_envs = ["K2"]
`)
	if err := os.WriteFile(filepath.Join(store.home, "secrets.env"), []byte("K1=sk-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	agy.cooling = map[string]map[string]time.Time{"ds": {"K1": until}}

	list := serveMCP(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
	if !strings.Contains(string(list.body), `"keys":[{"name":"K1","present":true,"cooling_until":"2026-11-25T12:00:00Z"},{"name":"K2","present":false,"cooling_until":null}]`) {
		t.Fatalf("providers = %s", list.body)
	}

	saved := serveMCP(t, s, http.MethodPut, "/v1/providers/ds/keys", `{"keys":["K2","K1"],"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if saved.code != http.StatusOK {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	if !strings.Contains(string(mustRead(t, store.path)), "[providers.ds]\nkind = 'deepseek'\napi_key_env = 'K2'\napi_key_envs = ['K1']\n") {
		t.Fatalf("config:\n%s", mustRead(t, store.path))
	}
	if !strings.Contains(string(saved.body), `"keys":[{"name":"K2","present":false,"cooling_until":null},{"name":"K1","present":true,"cooling_until":"2026-11-25T12:00:00Z"}]`) {
		t.Fatalf("answer = %s", saved.body)
	}
	one := serveMCP(t, s, http.MethodPut, "/v1/providers/ds/keys", `{"keys":["K1"]}`, cookie, csrf)
	if one.code != http.StatusOK || strings.Contains(string(mustRead(t, store.path)), "api_key_envs") ||
		!strings.Contains(string(mustRead(t, store.path)), "api_key_env = 'K1'") {
		t.Fatalf("one key = %d %s\n%s", one.code, one.body, mustRead(t, store.path))
	}
	// A provider save keeps the backup keys.
	if got := serveMCP(t, s, http.MethodPut, "/v1/providers/ds/keys", `{"keys":["K1","K2"]}`, cookie, csrf); got.code != http.StatusOK {
		t.Fatalf("two keys = %d %s", got.code, got.body)
	}
	if got := serveMCP(t, s, http.MethodPut, "/v1/providers/ds", `{"kind":"deepseek","api_key_env":"K1","disabled":true,"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf); got.code != http.StatusOK ||
		!strings.Contains(string(mustRead(t, store.path)), "api_key_envs = ['K2']") {
		t.Fatalf("provider save = %d %s\n%s", got.code, got.body, mustRead(t, store.path))
	}

	if err := os.WriteFile(store.path, append(mustRead(t, store.path), "\n[providers.agy-2]\nkind = \"antigravity\"\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	before := string(mustRead(t, store.path))
	for path, want := range map[string][2]any{
		`{"keys":[]}`:                              {"/v1/providers/ds/keys", http.StatusUnprocessableEntity},
		`{"keys":["bad name"]}`:                    {"/v1/providers/ds/keys", http.StatusUnprocessableEntity},
		`{"keys":["K1","K1"]}`:                     {"/v1/providers/ds/keys", http.StatusUnprocessableEntity},
		`{"keys":["K1"],"expected_version":"old"}`: {"/v1/providers/ds/keys", http.StatusConflict},
		`{"keys":["K1"]} `:                         {"/v1/providers/agy-2/keys", http.StatusUnprocessableEntity},
		` {"keys":["K1"]}`:                         {"/v1/providers/nobody/keys", http.StatusNotFound},
	} {
		if got := serveMCP(t, s, http.MethodPut, want[0].(string), path, cookie, csrf); got.code != want[1].(int) {
			t.Fatalf("keys %s %s = %d %s", want[0], path, got.code, got.body)
		}
	}
	if string(mustRead(t, store.path)) != before {
		t.Fatal("a refused keys save changed the config")
	}
}
