package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// TestAuditCoversACWEB029 covers AC-WEB-029: one info line per save names the
// file and changed key paths or the secret name, and no value appears.
func TestAuditCoversACWEB029(t *testing.T) {
	store := newTestConfigStore(t, configMaskRaw, 0o600, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)
	secrets := &testSettingsStore{home: t.TempDir()}
	s.SetSecretStore(secrets)

	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	before, err := store.ConfigFile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(configMaskRaw, `base_url = "mock://local"`, `base_url = "mock://remote"`, 1)
	changed = strings.Replace(changed, `model = "coder"`, `model = "coder-two"`, 1)
	payload, err := json.Marshal(map[string]string{"raw": changed, "expected_version": before.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/config", string(payload), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("config save status = %d, body = %q", rr.Code, rr.Body.String())
	}

	list := decodeSecretList(t, s, cookie)
	secretBody, err := json.Marshal(map[string]string{"value": "sk-audit-secret", "expected_version": list.Version})
	if err != nil {
		t.Fatal(err)
	}
	rr = serveAuth(s, settingsRequest(http.MethodPut, "/v1/secrets/OPENAI_API_KEY", string(secretBody), cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("secret save status = %d, body = %q", rr.Code, rr.Body.String())
	}

	configLines, secretLines := 0, 0
	for _, line := range strings.Split(logs.String(), "\n") {
		switch {
		case strings.Contains(line, "kind=config"):
			configLines++
			for _, want := range []string{"config.toml", "agents.coder.model", "providers.mock.base_url"} {
				if !strings.Contains(line, want) {
					t.Fatalf("config audit line %q does not name %s", line, want)
				}
			}
		case strings.Contains(line, "kind=secret"):
			secretLines++
			if !strings.Contains(line, "name=OPENAI_API_KEY") {
				t.Fatalf("secret audit line %q does not name the secret", line)
			}
		}
	}
	if configLines != 1 || secretLines != 1 {
		t.Fatalf("config audit lines = %d, secret audit lines = %d, log:\n%s", configLines, secretLines, logs.String())
	}
	for _, forbidden := range []string{
		"sk-audit-secret",
		"coder-two",
		"mock://remote",
		"Bearer x",
		"Authorization",
		"Run the checks",
	} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("audit log contains a value or content %q:\n%s", forbidden, logs.String())
		}
	}
}

const auditConfigRaw = `[providers.mock]
kind = "openai_compat"
base_url = "mock://local"

[agents.coder]
provider = "mock"
model = "coder"
permission = "ask"
`

// TestAuditCoversACWEB059 covers AC-WEB-059: saving model and permission appends
// one JSON line with kind config.save, target config.toml, and both changed key
// paths, never a value, and the file is mode 0600.
func TestAuditCoversACWEB059(t *testing.T) {
	store := newTestConfigStore(t, auditConfigRaw, 0o600, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)

	before, err := store.ConfigFile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(auditConfigRaw, `model = "coder"`, `model = "coder-two"`, 1)
	changed = strings.Replace(changed, `permission = "ask"`, `permission = "trust"`, 1)
	body := mustJSON(t, map[string]string{"raw": changed, "expected_version": before.Version})
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/config", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("config save status = %d, body = %q", rr.Code, rr.Body.String())
	}

	path := webfiles.AuditPath(store.home)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat audit file: %v", err)
	}
	if got := info.Mode().Perm(); got != webfiles.AuditFileMode {
		t.Fatalf("audit file mode = %04o, want %04o", got, webfiles.AuditFileMode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1:\n%s", len(lines), data)
	}
	var entry webfiles.AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode audit line %q: %v", lines[0], err)
	}
	if entry.Kind != "config.save" {
		t.Fatalf("kind = %q, want config.save", entry.Kind)
	}
	if entry.Target != "config.toml" {
		t.Fatalf("target = %q, want config.toml", entry.Target)
	}
	if got := strings.Join(entry.ChangedKeys, ","); got != "agents.coder.model,agents.coder.permission" {
		t.Fatalf("changed_keys = %q", got)
	}
	if entry.Time <= 0 {
		t.Fatalf("time = %d, want Unix ms", entry.Time)
	}
	for _, forbidden := range []string{"coder-two", "trust"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("audit file leaked a value %q:\n%s", forbidden, data)
		}
	}

	// The audit page returns the same entry newest first.
	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/audit", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var page webfiles.AuditPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode audit page %q: %v", rr.Body.String(), err)
	}
	if len(page.Entries) != 1 || page.NextBefore != nil {
		t.Fatalf("audit page = %+v", page)
	}
	if page.Entries[0].Kind != "config.save" || page.Entries[0].Target != "config.toml" {
		t.Fatalf("audit page entry = %+v", page.Entries[0])
	}
	if got := strings.Join(page.Entries[0].ChangedKeys, ","); got != "agents.coder.model,agents.coder.permission" {
		t.Fatalf("audit page changed_keys = %q", got)
	}
}

// TestAuditListPaging covers the before and limit query of API-WEB-030 and the
// 400 invalid_request on an out-of-range limit.
func TestAuditListPaging(t *testing.T) {
	store := newTestConfigStore(t, configMaskRaw, 0o600, ConfigSettings{})
	s, cookie, _ := newConfigTestServer(t, store)

	path := webfiles.AuditPath(store.home)
	const base = int64(1_700_000_000_000)
	for i := int64(0); i < 3; i++ {
		entry := webfiles.AuditEntry{Time: base + i, Kind: "config.save", Target: "config.toml"}
		if err := webfiles.AppendAudit(path, entry); err != nil {
			t.Fatal(err)
		}
	}

	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/audit?limit=2", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit?limit=2 status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var page webfiles.AuditPage
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.Entries[0].Time != base+2 || page.NextBefore == nil || *page.NextBefore != base+1 {
		t.Fatalf("first page = %+v", page)
	}

	rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/audit?limit=2&before="+strconv.FormatInt(*page.NextBefore, 10), "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit?before status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Time != base || page.NextBefore != nil {
		t.Fatalf("older page = %+v", page)
	}

	for _, query := range []string{"limit=0", "limit=101", "limit=nope", "before=-1", "before=nope"} {
		rr = serveAuth(s, settingsRequest(http.MethodGet, "/v1/audit?"+query, "", cookie, ""))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("GET /v1/audit?%s status = %d, want 400", query, rr.Code)
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error.Code != "invalid_request" {
			t.Fatalf("GET /v1/audit?%s code = %q", query, envelope.Error.Code)
		}
	}
}

// TestAuditListMissingFile covers a fresh home: the audit page is empty and
// next_before is null.
func TestAuditListMissingFile(t *testing.T) {
	store := newTestConfigStore(t, configMaskRaw, 0o600, ConfigSettings{})
	s, cookie, _ := newConfigTestServer(t, store)

	rr := serveAuth(s, settingsRequest(http.MethodGet, "/v1/audit", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != `{"entries":[],"next_before":null}` {
		t.Fatalf("missing file body = %q", got)
	}
}

// TestAuditSecretKinds covers the two secret kinds of API-WEB-030: a set and a
// remove share the stderr kind, but the audit file tells them apart.
func TestAuditSecretKinds(t *testing.T) {
	store := newTestConfigStore(t, configMaskRaw, 0o600, ConfigSettings{})
	s, cookie, csrf := newConfigTestServer(t, store)
	secrets := &testSettingsStore{home: store.home}
	s.SetSecretStore(secrets)

	version, err := webfiles.SecretsVersion(secretsPath(store.home))
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, map[string]string{"value": "sk-audit-secret", "expected_version": version})
	rr := serveAuth(s, settingsRequest(http.MethodPut, "/v1/secrets/OPENAI_API_KEY", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("secret set status = %d, body = %q", rr.Code, rr.Body.String())
	}

	version, err = webfiles.SecretsVersion(secretsPath(store.home))
	if err != nil {
		t.Fatal(err)
	}
	body = mustJSON(t, map[string]string{"expected_version": version})
	rr = serveAuth(s, settingsRequest(http.MethodDelete, "/v1/secrets/OPENAI_API_KEY", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("secret remove status = %d, body = %q", rr.Code, rr.Body.String())
	}

	data, err := os.ReadFile(webfiles.AuditPath(store.home))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d, want 2:\n%s", len(lines), data)
	}
	want := []string{"secret.set", "secret.remove"}
	for i, line := range lines {
		var entry webfiles.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode audit line %q: %v", line, err)
		}
		if entry.Kind != want[i] || entry.Target != "OPENAI_API_KEY" {
			t.Fatalf("audit line %d = %+v, want kind %s target OPENAI_API_KEY", i, entry, want[i])
		}
	}
	if strings.Contains(string(data), "sk-audit-secret") {
		t.Fatalf("audit file leaked the secret value:\n%s", data)
	}
}
