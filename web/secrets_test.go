package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// testSettingsStore implements the secrets, agent-file, and skill ports with
// a real temporary home directory.
type testSettingsStore struct {
	home   string
	agents []string
	names  []string
}

func (s *testSettingsStore) SettingsHome() string { return s.home }
func (s *testSettingsStore) AgentNames() []string { return s.agents }
func (s *testSettingsStore) ConfigSecretNames(context.Context) ([]string, error) {
	return s.names, nil
}

func newSettingsTestServer(t *testing.T, store *testSettingsStore) (*Server, *http.Cookie, string) {
	t.Helper()
	token := authTokenFor(t, store.home)
	s := newAuthServer(t, store.home, io.Discard)
	s.SetSecretStore(store)
	s.SetAgentFileStore(store)
	s.SetSkillStore(store)
	cookie, csrf := loginAndSession(t, s, token)
	return s, cookie, csrf
}

func settingsRequest(method, target, body string, cookie *http.Cookie, csrf string) *http.Request {
	req := authRequest(method, target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	return req
}

func decodeSecretList(t *testing.T, s *Server, cookie *http.Cookie) listSecretsResponse {
	t.Helper()
	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/secrets", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/secrets status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got listSecretsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET /v1/secrets body %q: %v", rr.Body.String(), err)
	}
	return got
}

// TestSecretsListCoversACWEB026 covers the read model and AC-WEB-026: a set
// secret shows "set" without a value, and the file has mode 0600.
func TestSecretsListCoversACWEB026(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	if err := os.WriteFile(path, []byte("# comment\nFILE_KEY=file-secret\nEMPTY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FILE_KEY", "from-process")
	store := &testSettingsStore{home: home, names: []string{"OPENAI_API_KEY", "FILE_KEY"}}
	s, cookie, csrf := newSettingsTestServer(t, store)

	got := decodeSecretList(t, s, cookie)
	if got.RestartRequired {
		t.Fatal("restart_required = true before any save")
	}
	if len(got.Secrets) != 3 {
		t.Fatalf("secrets = %+v, want FILE_KEY, EMPTY, OPENAI_API_KEY", got.Secrets)
	}
	names := []string{got.Secrets[0].Name, got.Secrets[1].Name, got.Secrets[2].Name}
	if names[0] != "EMPTY" || names[1] != "FILE_KEY" || names[2] != "OPENAI_API_KEY" {
		t.Fatalf("names = %v, want sorted", names)
	}
	byName := map[string]secretJSON{}
	for _, item := range got.Secrets {
		byName[item.Name] = item
	}
	if item := byName["FILE_KEY"]; item.State != "set" || item.File != path || !item.OverriddenByEnv || item.Source != "env" {
		t.Fatalf("FILE_KEY = %+v", item)
	}
	if item := byName["EMPTY"]; item.State != "unset" || item.File != path {
		t.Fatalf("EMPTY = %+v, want unset with its file path", item)
	}
	if item := byName["OPENAI_API_KEY"]; item.State != "unset" || item.File != "none" || item.OverriddenByEnv || item.UpdatedAt != nil || len(item.UsedBy) != 0 {
		t.Fatalf("OPENAI_API_KEY = %+v", item)
	}

	before := got.Version
	body, err := json.Marshal(map[string]string{"value": "sk-live-secret", "expected_version": before})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/secrets/OPENAI_API_KEY", string(body), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "sk-live-secret") {
		t.Fatalf("PUT response leaked the value: %s", rr.Body.String())
	}
	var written secretWriteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &written); err != nil {
		t.Fatal(err)
	}
	if written.Name != "OPENAI_API_KEY" || written.State != "set" || !written.RestartRequired {
		t.Fatalf("PUT response = %+v", written)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %04o, want 0600", mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY=sk-live-secret\n") {
		t.Fatalf("file = %q", data)
	}

	after := decodeSecretList(t, s, cookie)
	if !after.RestartRequired || after.Version == before {
		t.Fatalf("after PUT: restart_required=%v version %q -> %q", after.RestartRequired, before, after.Version)
	}
	if item := secretByName(after, "OPENAI_API_KEY"); item.State != "set" || item.File != path {
		t.Fatalf("OPENAI_API_KEY after PUT = %+v", item)
	}
	if strings.Contains(mustJSON(t, after), "sk-live-secret") {
		t.Fatal("GET /v1/secrets leaked the value")
	}
}

func TestSecretsPutValidationAndStale(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	if err := os.WriteFile(path, []byte("A=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, cookie, csrf := newSettingsTestServer(t, &testSettingsStore{home: home})
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		body string
		code int
	}{
		{name: "invalid name", path: "/v1/secrets/1BAD", body: `{"value":"x","expected_version":"` + version + `"}`, code: http.StatusUnprocessableEntity},
		{name: "newline value", path: "/v1/secrets/A", body: `{"value":"a\nb","expected_version":"` + version + `"}`, code: http.StatusUnprocessableEntity},
		{name: "too long", path: "/v1/secrets/A", body: `{"value":"` + strings.Repeat("x", webfiles.MaxSecretValueBytes+1) + `","expected_version":"` + version + `"}`, code: http.StatusUnprocessableEntity},
		{name: "stale version", path: "/v1/secrets/A", body: `{"value":"two","expected_version":"stale"}`, code: http.StatusConflict},
		{name: "malformed json", path: "/v1/secrets/A", body: `{`, code: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := serveAuth(s, settingsRequest(http.MethodPut, tc.path, tc.body, cookie, csrf))
			if rr.Code != tc.code {
				t.Fatalf("status = %d, want %d, body = %q", rr.Code, tc.code, rr.Body.String())
			}
		})
	}

	// The file must not change on any rejected save.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "A=one\n" {
		t.Fatalf("file changed by rejected saves: %q", data)
	}

	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/secrets/A", `{"value":"two","expected_version":"`+version+`"}`, cookie, ""))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF header status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestSecretsDelete(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	if err := os.WriteFile(path, []byte("A=one\n# keep\nB=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, cookie, csrf := newSettingsTestServer(t, &testSettingsStore{home: home})
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodDelete, "/v1/secrets/A", `{"expected_version":"`+version+`"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var deleted secretWriteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Name != "A" || deleted.State != "unset" || !deleted.RestartRequired {
		t.Fatalf("DELETE response = %+v", deleted)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# keep\nB=two\n" {
		t.Fatalf("file after delete = %q", data)
	}
	version, err = webfiles.SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/secrets/A", `{"expected_version":"`+version+`"}`, cookie, csrf))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown status = %d, body = %q", rr.Code, rr.Body.String())
	}
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/secrets/A", `{"expected_version":"stale"}`, cookie, csrf))
	if rr.Code != http.StatusConflict {
		t.Fatalf("DELETE stale status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestSecretRoundTripQuotesValues(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	s, cookie, csrf := newSettingsTestServer(t, &testSettingsStore{home: home})
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	value := "two words # and 'quotes'"
	body, err := json.Marshal(map[string]string{"value": value, "expected_version": version})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/secrets/QUOTED", string(body), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	values, err := webfiles.ReadSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if values["QUOTED"] != value {
		t.Fatalf("stored value = %q, want %q", values["QUOTED"], value)
	}
}

// TestSecretsUpdateTimeAndUsageCoversACWEB058 covers AC-WEB-058: a PUT records
// the set time, the list shows it with the config table that uses the name, the
// value never appears in a response, and secrets.meta.json holds the name and
// time only.
func TestSecretsUpdateTimeAndUsageCoversACWEB058(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	const configRaw = `[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
`
	if err := os.WriteFile(configPath, []byte(configRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &testSettingsStore{home: home, names: []string{"OPENAI_API_KEY"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	s.SetConfigStore(&testConfigStore{home: home, path: configPath})
	fixed := time.Date(2026, 10, 30, 12, 34, 56, 789_000_000, time.UTC)
	s.now = func() time.Time { return fixed }

	path := filepath.Join(home, "secrets.env")
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	const value = "sk-live-secret"
	body, err := json.Marshal(map[string]string{"value": value, "expected_version": version})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/secrets/OPENAI_API_KEY", string(body), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), value) {
		t.Fatalf("PUT response leaked the value: %s", rr.Body.String())
	}

	metaPath := filepath.Join(home, webfiles.SecretsMetaFileName)
	info, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("meta mode = %04o, want 0600", mode)
	}
	meta, err := webfiles.ReadSecretsMeta(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.UpdatedMS) != 1 || meta.UpdatedMS["OPENAI_API_KEY"] != fixed.UnixMilli() {
		t.Fatalf("meta times = %+v, want only OPENAI_API_KEY at %d", meta.UpdatedMS, fixed.UnixMilli())
	}
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metaData), value) || strings.Contains(string(metaData), "sk-") {
		t.Fatalf("meta file leaked the value: %q", metaData)
	}
	if !strings.Contains(string(metaData), `"OPENAI_API_KEY"`) {
		t.Fatalf("meta file = %q, want the name", metaData)
	}

	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/secrets", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), value) {
		t.Fatalf("GET /v1/secrets leaked the value: %s", rr.Body.String())
	}
	var got listSecretsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	item := secretByName(got, "OPENAI_API_KEY")
	if item.State != "set" {
		t.Fatalf("OPENAI_API_KEY state = %q, want set", item.State)
	}
	if item.UpdatedAt == nil || *item.UpdatedAt != fixed.UnixMilli() {
		t.Fatalf("OPENAI_API_KEY updated_at = %v, want %d", item.UpdatedAt, fixed.UnixMilli())
	}
	if !slices.Equal(item.UsedBy, []string{"providers.openai"}) {
		t.Fatalf("OPENAI_API_KEY used_by = %v, want [providers.openai]", item.UsedBy)
	}
}

func TestSecretsUsedByConfigTables(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	const configRaw = `[providers.openai]
api_key_env = "OPENAI_API_KEY"

[providers.copy]
api_key_env = "SHARED"

[channels.telegram]
token_env = "TELEGRAM_BOT_TOKEN"

[mcp.github]
command = "gh"

[mcp.github.env]
TOKEN = "${SHARED}"

[mcp.slack]
command = "slack"

[mcp.slack.headers]
Authorization = "Bearer ${SHARED}"
`
	if err := os.WriteFile(configPath, []byte(configRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &testSettingsStore{home: home, names: []string{"OPENAI_API_KEY", "SHARED", "TELEGRAM_BOT_TOKEN", "UNUSED"}}
	s, cookie, _ := newSettingsTestServer(t, store)
	s.SetConfigStore(&testConfigStore{home: home, path: configPath})

	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/secrets", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got listSecretsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"OPENAI_API_KEY":     {"providers.openai"},
		"SHARED":             {"mcp.github", "mcp.slack", "providers.copy"},
		"TELEGRAM_BOT_TOKEN": {"channels.telegram"},
		"UNUSED":             {},
	}
	for name, paths := range want {
		item := secretByName(got, name)
		if item.Name != name {
			t.Fatalf("%s is missing from %+v", name, got.Secrets)
		}
		if !slices.Equal(item.UsedBy, paths) {
			t.Fatalf("%s used_by = %v, want %v", name, item.UsedBy, paths)
		}
	}
	if !strings.Contains(rr.Body.String(), `"used_by":[]`) {
		t.Fatalf("GET body = %s, want an empty used_by array", rr.Body.String())
	}
}

func TestSecretsUpdatedAtMissingAndCorruptMeta(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	if err := os.WriteFile(path, []byte("A=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &testSettingsStore{home: home}
	s, cookie, _ := newSettingsTestServer(t, store)
	metaPath := filepath.Join(home, webfiles.SecretsMetaFileName)

	// A missing file is treated as empty: the name still lists, with no time.
	item := secretByName(decodeSecretList(t, s, cookie), "A")
	if item.Name != "A" || item.UpdatedAt != nil {
		t.Fatalf("missing meta: A = %+v, want updated_at null", item)
	}

	// A name with no entry shows null while another name keeps its time.
	if err := os.WriteFile(metaPath, []byte(`{"version":1,"updated_ms":{"B":42}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	item = secretByName(decodeSecretList(t, s, cookie), "A")
	if item.UpdatedAt != nil {
		t.Fatalf("A updated_at = %v, want null", item.UpdatedAt)
	}

	// A corrupt file does not fail the list and still shows null.
	if err := os.WriteFile(metaPath, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	item = secretByName(decodeSecretList(t, s, cookie), "A")
	if item.UpdatedAt != nil {
		t.Fatalf("corrupt meta: A updated_at = %v, want null", item.UpdatedAt)
	}
}

func TestSecretsDeleteClearsUpdateTime(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "secrets.env")
	if err := os.WriteFile(path, []byte("A=one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(home, webfiles.SecretsMetaFileName)
	if err := webfiles.SetSecretMeta(metaPath, "A", 42); err != nil {
		t.Fatal(err)
	}
	store := &testSettingsStore{home: home, names: []string{"A"}}
	s, cookie, csrf := newSettingsTestServer(t, store)
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodDelete, "/v1/secrets/A", `{"expected_version":"`+version+`"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %q", rr.Code, rr.Body.String())
	}
	meta, err := webfiles.ReadSecretsMeta(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := meta.UpdatedMS["A"]; ok {
		t.Fatalf("DELETE kept the time entry: %+v", meta.UpdatedMS)
	}
	item := secretByName(decodeSecretList(t, s, cookie), "A")
	if item.State != "unset" || item.UpdatedAt != nil {
		t.Fatalf("A after delete = %+v, want unset with updated_at null", item)
	}
}

func secretByName(list listSecretsResponse, name string) secretJSON {
	for _, item := range list.Secrets {
		if item.Name == name {
			return item
		}
	}
	return secretJSON{}
}
