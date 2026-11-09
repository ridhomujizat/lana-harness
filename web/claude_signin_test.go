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

// fakeClaudeAuth is the Claude Code part of the fake backend with sign-in
// (REQ-CCT-012).
type fakeClaudeAuth struct {
	fakeClaudeRouterBackend

	mu       sync.Mutex
	started  []ClaudeAccountRef
	signOuts []ClaudeAccountRef
	states   map[string]ClaudeSignInStatus
	forgot   []string
	signOut  error
	accounts []ClaudeCodeAccountStatus
}

func (f *fakeClaudeAuth) StartClaudeSignIn(ref ClaudeAccountRef) (ClaudeSignInStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states[ref.Name].State == ClaudeSignInRunning {
		return f.states[ref.Name], ErrClaudeSignInRunning
	}
	f.started = append(f.started, ref)
	at := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	state := ClaudeSignInStatus{State: ClaudeSignInRunning, StartedAt: &at}
	f.states[ref.Name] = state
	return state, nil
}

func (f *fakeClaudeAuth) CancelClaudeSignIn(name string) ClaudeSignInStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.states[name]
	if state.State == ClaudeSignInRunning {
		state.State = "cancelled"
		f.states[name] = state
	}
	if state.State == "" {
		state.State = ClaudeSignInIdle
	}
	return state
}

func (f *fakeClaudeAuth) ClaudeSignInState(name string) ClaudeSignInStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state, ok := f.states[name]; ok {
		return state
	}
	return ClaudeSignInStatus{State: ClaudeSignInIdle}
}

func (f *fakeClaudeAuth) ForgetClaudeSignIn(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgot = append(f.forgot, name)
	delete(f.states, name)
}

func (f *fakeClaudeAuth) ClaudeSignOut(_ context.Context, ref ClaudeAccountRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signOuts = append(f.signOuts, ref)
	return f.signOut
}

func (f *fakeClaudeAuth) ClaudeAgentStatus(context.Context) (ClaudeAgentStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ClaudeAgentStatus{Accounts: append([]ClaudeCodeAccountStatus(nil), f.accounts...)}, nil
}

func newClaudeAuthServer(t *testing.T) (*Server, *testLiveConfigStore, *fakeClaudeAuth, *http.Cookie, string) {
	t.Helper()
	s, store, fake, cookie, csrf := newClaudeRouterServer(t, true)
	auth := &fakeClaudeAuth{
		fakeClaudeRouterBackend: fakeClaudeRouterBackend{fakeRouterBackend: fake, installed: true},
		states:                  map[string]ClaudeSignInStatus{},
	}
	s.SetRouterBackend(auth)
	return s, store, auth, cookie, csrf
}

func configVersion(t *testing.T, store *testLiveConfigStore) string {
	t.Helper()
	return shaHex(mustRead(t, store.path))
}

// REQ-CCT-012: POST /v1/router/claude-code/accounts adds an account in one
// click (name claude-2, claude-3, …), creates its folder (0700), and starts
// the sign-in; the sign-in can be read, refused while running, and
// cancelled.
func TestClaudeAccountCreateAndSignIn(t *testing.T) {
	s, store, auth, cookie, csrf := newClaudeAuthServer(t)

	created := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts", "", cookie, csrf)
	if created.code != http.StatusAccepted {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	var response claudeAccountCreateResponse
	created.json(t, &response)
	dir := filepath.Join(store.home, "claude", "claude-2")
	if response.Name != "claude-2" || response.ConfigDir != dir || response.SignIn.State != "running" || response.SignIn.StartedAt == nil {
		t.Fatalf("response = %+v", response)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("folder = %v %v", info, err)
	}
	if !strings.Contains(string(mustRead(t, store.path)), "[providers.claude-2]\nkind = 'claude-code'\n") {
		t.Fatalf("config:\n%s", mustRead(t, store.path))
	}
	if len(auth.started) != 1 || auth.started[0] != (ClaudeAccountRef{Name: "claude-2", ConfigDir: dir}) {
		t.Fatalf("started = %+v", auth.started)
	}
	for _, want := range []string{`"started_at":"2026-11-08T10:00:00Z"`, `"finished_at":null`, `"error":""`} {
		if !strings.Contains(string(created.body), want) {
			t.Fatalf("create body lacks %s: %s", want, created.body)
		}
	}

	second := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts", `{}`, cookie, csrf)
	second.json(t, &response)
	if second.code != http.StatusAccepted || response.Name != "claude-3" {
		t.Fatalf("second = %d %s", second.code, second.body)
	}
	named := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts", `{"name":"claude-work"}`, cookie, csrf)
	named.json(t, &response)
	if named.code != http.StatusAccepted || response.Name != "claude-work" {
		t.Fatalf("named = %d %s", named.code, named.body)
	}
	for body, code := range map[string]int{
		`{"name":"claude-2"}`:    http.StatusConflict,
		`{"name":"claude-code"}`: http.StatusConflict,
		`{"name":"Bad Name"}`:    http.StatusUnprocessableEntity,
		`{"other":1}`:            http.StatusBadRequest,
	} {
		if got := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts", body, cookie, csrf); got.code != code {
			t.Fatalf("create %s = %d %s", body, got.code, got.body)
		}
	}

	base := "/v1/router/claude-code/accounts/claude-2/sign-in"
	busy := serveMCP(t, s, http.MethodPost, base, "", cookie, csrf)
	if busy.code != http.StatusConflict || !strings.Contains(string(busy.body), "sign_in_running") {
		t.Fatalf("second sign-in = %d %s", busy.code, busy.body)
	}
	state := serveMCP(t, s, http.MethodGet, base, "", cookie, csrf)
	if state.code != http.StatusOK || !strings.Contains(string(state.body), `"state":"running"`) {
		t.Fatalf("state = %d %s", state.code, state.body)
	}
	cancelled := serveMCP(t, s, http.MethodDelete, base, "", cookie, csrf)
	if cancelled.code != http.StatusOK || !strings.Contains(string(cancelled.body), `"state":"cancelled"`) {
		t.Fatalf("cancel = %d %s", cancelled.code, cancelled.body)
	}
	again := serveMCP(t, s, http.MethodPost, base, "", cookie, csrf)
	if again.code != http.StatusAccepted || len(auth.started) != 4 {
		t.Fatalf("restart = %d %s (%d started)", again.code, again.body, len(auth.started))
	}
	// The built-in account signs in to the owner's own login.
	builtin := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts/claude-code/sign-in", "", cookie, csrf)
	if builtin.code != http.StatusAccepted || auth.started[4] != (ClaudeAccountRef{Name: "claude-code"}) {
		t.Fatalf("built-in = %d %s %+v", builtin.code, builtin.body, auth.started)
	}
	// An unknown account and a provider of another kind are not accounts.
	for _, name := range []string{"nobody", "deepseek"} {
		got := serveMCP(t, s, http.MethodGet, "/v1/router/claude-code/accounts/"+name+"/sign-in", "", cookie, csrf)
		if got.code != http.StatusNotFound {
			t.Fatalf("%s = %d %s", name, got.code, got.body)
		}
	}

	// GET /v1/router/claude-agent carries the sign-in state per account.
	auth.accounts = []ClaudeCodeAccountStatus{{Name: "claude-code", Builtin: true, Email: "me@home.dev"}, {Name: "claude-2"}}
	status := serveMCP(t, s, http.MethodGet, "/v1/router/claude-agent", "", cookie, csrf)
	var body ClaudeAgentStatus
	status.json(t, &body)
	if len(body.Accounts) != 2 || body.Accounts[0].SignIn == nil || body.Accounts[0].SignIn.State != "running" ||
		body.Accounts[1].SignIn.State != "running" || body.Accounts[0].DisplayName != "me@home.dev" || body.Accounts[1].DisplayName != "claude-2" {
		t.Fatalf("status = %s", status.body)
	}
}

// REQ-CCT-012: signing out the built-in account needs {"confirm": true};
// an added account signs out without it. The answer is the refreshed row.
func TestClaudeSignOut(t *testing.T) {
	s, store, auth, cookie, csrf := newClaudeAuthServer(t)
	auth.accounts = []ClaudeCodeAccountStatus{{Name: "claude-code", Builtin: true}, {Name: "claude-work", Email: "w@example.com"}}
	refused := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts/claude-code/sign-out", "", cookie, csrf)
	if refused.code != http.StatusBadRequest || !strings.Contains(string(refused.body), "confirm_required") || len(auth.signOuts) != 0 {
		t.Fatalf("unconfirmed = %d %s", refused.code, refused.body)
	}
	refused = serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts/claude-code/sign-out", `{"confirm":false}`, cookie, csrf)
	if refused.code != http.StatusBadRequest || len(auth.signOuts) != 0 {
		t.Fatalf("confirm false = %d %s", refused.code, refused.body)
	}
	done := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts/claude-code/sign-out", `{"confirm":true}`, cookie, csrf)
	var row ClaudeCodeAccountStatus
	done.json(t, &row)
	if done.code != http.StatusOK || row.Name != "claude-code" || !row.Builtin || row.SignIn == nil || row.SignIn.State != "idle" ||
		len(auth.signOuts) != 1 || auth.signOuts[0] != (ClaudeAccountRef{Name: "claude-code"}) {
		t.Fatalf("confirmed = %d %s %+v", done.code, done.body, auth.signOuts)
	}

	if err := os.WriteFile(store.path, append(mustRead(t, store.path), "\n[providers.claude-work]\nkind = \"claude-code\"\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	done = serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts/claude-work/sign-out", "", cookie, csrf)
	done.json(t, &row)
	dir := filepath.Join(store.home, "claude", "claude-work")
	if done.code != http.StatusOK || row.Name != "claude-work" || row.DisplayName != "w@example.com" ||
		auth.signOuts[1] != (ClaudeAccountRef{Name: "claude-work", ConfigDir: dir}) {
		t.Fatalf("named = %d %s %+v", done.code, done.body, auth.signOuts)
	}
	auth.signOut = ErrClaudeSignInRunning
	busy := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts/claude-work/sign-out", "", cookie, csrf)
	if busy.code != http.StatusConflict {
		t.Fatalf("busy = %d %s", busy.code, busy.body)
	}
}

// REQ-CCT-012: PUT …/label stores label in the account's table (at most 60
// characters, one line); the display name is the label, else the e-mail,
// else the name. The built-in account has no label.
func TestClaudeAccountLabel(t *testing.T) {
	s, store, auth, cookie, csrf := newClaudeAuthServer(t)
	if err := os.WriteFile(store.path, append(mustRead(t, store.path),
		"\n[providers.claude-work]\nkind = \"claude-code\"\nallow_scheduled = true\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	auth.accounts = []ClaudeCodeAccountStatus{{Name: "claude-code", Builtin: true}, {Name: "claude-work", Email: "w@example.com"}}
	path := "/v1/router/claude-code/accounts/claude-work/label"
	saved := serveMCP(t, s, http.MethodPut, path, `{"label":"  Work  ","expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if saved.code != http.StatusOK || !strings.Contains(string(saved.body), `"label":"Work"`) {
		t.Fatalf("save = %d %s", saved.code, saved.body)
	}
	if !strings.Contains(string(mustRead(t, store.path)), "[providers.claude-work]\nkind = 'claude-code'\nallow_scheduled = true\nlabel = 'Work'\n") {
		t.Fatalf("config:\n%s", mustRead(t, store.path))
	}
	status := serveMCP(t, s, http.MethodGet, "/v1/router/claude-agent", "", cookie, csrf)
	var body ClaudeAgentStatus
	status.json(t, &body)
	if body.Accounts[1].Label != "Work" || body.Accounts[1].DisplayName != "Work" || body.Accounts[0].DisplayName != "claude-code" {
		t.Fatalf("status = %s", status.body)
	}
	list := serveMCP(t, s, http.MethodGet, "/v1/providers", "", cookie, csrf)
	if !strings.Contains(string(list.body), `"label":"Work"`) {
		t.Fatalf("providers = %s", list.body)
	}
	// A PUT /v1/providers save keeps the label.
	kept := serveMCP(t, s, http.MethodPut, "/v1/providers/claude-work", `{"kind":"claude-code","disabled":true,"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if kept.code != http.StatusOK || !strings.Contains(string(mustRead(t, store.path)), "label = 'Work'") {
		t.Fatalf("provider save = %d %s\n%s", kept.code, kept.body, mustRead(t, store.path))
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
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/accounts/claude-code/label", `{"label":"Home"}`, cookie, csrf); got.code != http.StatusConflict {
		t.Fatalf("built-in label = %d %s", got.code, got.body)
	}
	if got := serveMCP(t, s, http.MethodPut, "/v1/router/claude-code/accounts/deepseek/label", `{"label":"x"}`, cookie, csrf); got.code != http.StatusNotFound {
		t.Fatalf("other kind = %d %s", got.code, got.body)
	}
	cleared := serveMCP(t, s, http.MethodPut, path, `{"label":""}`, cookie, csrf)
	if cleared.code != http.StatusOK || strings.Contains(string(mustRead(t, store.path)), "label =") {
		t.Fatalf("clear = %d %s\n%s", cleared.code, cleared.body, mustRead(t, store.path))
	}
	status = serveMCP(t, s, http.MethodGet, "/v1/router/claude-agent", "", cookie, csrf)
	status.json(t, &body)
	if body.Accounts[1].DisplayName != "w@example.com" {
		t.Fatalf("display after clear = %s", status.body)
	}
}

// REQ-CCT-012: DELETE /v1/providers/{name}?remove_folder=1 signs the account
// out, deletes its folder, then the table, but only for a folder inside
// $LANA_HOME/claude/; in-use rules are unchanged.
func TestClaudeAccountRemoveFolder(t *testing.T) {
	s, store, auth, cookie, csrf := newClaudeAuthServer(t)
	created := serveMCP(t, s, http.MethodPost, "/v1/router/claude-code/accounts", "", cookie, csrf)
	if created.code != http.StatusAccepted {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	dir := filepath.Join(store.home, "claude", "claude-2")
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// In use: refused before any sign-out.
	agent := "\n[agents.worker]\nmodel = \"claude-2/opus\"\n"
	if err := os.WriteFile(store.path, append(mustRead(t, store.path), agent...), 0o600); err != nil {
		t.Fatal(err)
	}
	inUse := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-2?remove_folder=1", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if inUse.code != http.StatusConflict || !strings.Contains(string(inUse.body), "in_use") || len(auth.signOuts) != 0 {
		t.Fatalf("in use = %d %s", inUse.code, inUse.body)
	}
	if err := os.WriteFile(store.path, []byte(strings.Replace(string(mustRead(t, store.path)), agent, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	removed := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-2?remove_folder=1", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	var response providerSaveResponse
	removed.json(t, &response)
	if removed.code != http.StatusOK || !response.FolderRemoved || response.ConfigDir != dir {
		t.Fatalf("remove = %d %s", removed.code, removed.body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	if strings.Contains(string(mustRead(t, store.path)), "claude-2") {
		t.Fatal("table still there")
	}
	if len(auth.signOuts) != 1 || auth.signOuts[0] != (ClaudeAccountRef{Name: "claude-2", ConfigDir: dir}) {
		t.Fatalf("sign-outs = %+v", auth.signOuts)
	}
	if len(auth.forgot) != 1 || auth.forgot[0] != "claude-2" {
		t.Fatalf("forgot = %v", auth.forgot)
	}

	// A folder outside $LANA_HOME/claude/ is never deleted: the whole delete
	// is refused before the sign-out.
	outside := filepath.Join(t.TempDir(), "mine")
	escape := "$LANA_HOME/claude/../escape"
	for name, folder := range map[string]string{"claude-x": outside, "claude-y": escape} {
		saved := serveMCP(t, s, http.MethodPut, "/v1/providers/"+name,
			`{"kind":"claude-code","config_dir":"`+folder+`","expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
		if saved.code != http.StatusOK {
			t.Fatalf("save %s = %d %s", name, saved.code, saved.body)
		}
		refused := serveMCP(t, s, http.MethodDelete, "/v1/providers/"+name+"?remove_folder=1", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
		if refused.code != http.StatusConflict || !strings.Contains(string(refused.body), "folder_outside_home") {
			t.Fatalf("outside %s = %d %s", name, refused.code, refused.body)
		}
		if !strings.Contains(string(mustRead(t, store.path)), "[providers."+name+"]") {
			t.Fatalf("table %s removed", name)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.home, "escape")); err != nil {
		t.Fatalf("escape folder: %v", err)
	}
	if len(auth.signOuts) != 1 {
		t.Fatalf("sign-outs = %+v", auth.signOuts)
	}
	// Without remove_folder the folder stays, as before.
	kept := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-x", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf)
	if kept.code != http.StatusOK || !strings.Contains(string(kept.body), "was kept") {
		t.Fatalf("keep = %d %s", kept.code, kept.body)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside folder: %v", err)
	}
	if bad := serveMCP(t, s, http.MethodDelete, "/v1/providers/claude-y?remove_folder=yes", `{"expected_version":"`+configVersion(t, store)+`"}`, cookie, csrf); bad.code != http.StatusBadRequest {
		t.Fatalf("bad flag = %d %s", bad.code, bad.body)
	}
}

func TestClaudeFolderRemovable(t *testing.T) {
	s, store, _, _, _ := newClaudeAuthServer(t)
	root := filepath.Join(store.home, "claude")
	cases := map[string]bool{
		filepath.Join(root, "claude-2"): true,
		filepath.Join(root, "a", "b"):   true,
		root:                            false,
		store.home:                      false,
		filepath.Join(root, "..", "x"):  false,
		filepath.Join(store.home, "claudeX", "y"): false,
		"relative/claude-2":                       false,
		"":                                        false,
		filepath.Join(t.TempDir(), "claude", "c-2"): false,
	}
	for dir, want := range cases {
		if got := s.claudeFolderRemovable(dir); got != want {
			t.Errorf("removable(%q) = %v, want %v", dir, got, want)
		}
	}
}
