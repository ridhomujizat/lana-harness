package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/llmcall"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// Claude Code accounts as provider connections (REQ-CCT-010, API-CCT-010):
// [providers.<name>] kind = "claude-code". lana never reads the account
// folder; it creates the folder, and signs the account in and out only by
// starting the official `claude auth login` / `claude auth logout`
// (REQ-CCT-012, claude_signin.go).

// claudeAccountKeys are the body keys of a Claude Code account save besides
// kind and expected_version.
var claudeAccountKeys = []string{"config_dir", "binary", "auth", "api_key_env", "allow_scheduled", "disabled"}

// claudeOnlyKeys are the body keys only a Claude Code account takes.
var claudeOnlyKeys = []string{"config_dir", "binary", "auth", "allow_scheduled"}

// claudeOnlyProviderKeys reports the Claude Code account keys in the body of
// another kind.
func claudeOnlyProviderKeys(body map[string]json.RawMessage) []ConfigIssue {
	var issues []ConfigIssue
	for _, key := range claudeOnlyKeys {
		if _, ok := body[key]; ok {
			issues = append(issues, ConfigIssue{Key: key, Message: "allowed only for kind claude-code"})
		}
	}
	return issues
}

// claudeConfigDir is the CLAUDE_CONFIG_DIR of a Claude Code account, as the
// runtime sets it.
func (s *Server) claudeConfigDir(name string, record providerRecord) string {
	return llmcall.ClaudeConfigDir(s.providerHome(), name, core.ProviderConfig{ConfigDir: record.ConfigDir})
}

// claudeAccounts returns the Claude Code accounts of a config document: the
// built-in "claude-code" and every provider of kind "claude-code".
func (d providerDocument) claudeAccounts() map[string]bool {
	out := map[string]bool{core.ClaudeCodeProvider: true}
	for name, table := range d.Providers {
		if mapProviderString(table, "kind") == core.ClaudeCodeKind {
			out[name] = true
		}
	}
	return out
}

// claudeAccountsOf is claudeAccounts of raw config.toml; a parse failure
// leaves only the built-in account.
func claudeAccountsOf(raw []byte) map[string]bool {
	document, err := parseProviderDocument(raw)
	if err != nil {
		return map[string]bool{core.ClaudeCodeProvider: true}
	}
	return document.claudeAccounts()
}

// claudeAccountSet returns the Claude Code accounts of the stored config.
func (s *Server) claudeAccountSet(ctx context.Context) map[string]bool {
	if s.configStore != nil {
		if file, err := s.configStore.ConfigFile(ctx); err == nil && file.Exists {
			return claudeAccountsOf(file.Raw)
		}
	}
	return map[string]bool{core.ClaudeCodeProvider: true}
}

// isClaudeAccountModel reports whether model is a "<account>/<model>" address
// of one of accounts.
func isClaudeAccountModel(model string, accounts map[string]bool) bool {
	provider, rest, ok := strings.Cut(model, "/")
	return ok && rest != "" && !strings.Contains(provider, ":") && accounts[provider]
}

// renderClaudeAccountTable renders a [providers.<name>] table of kind
// "claude-code".
func renderClaudeAccountTable(name string, record providerRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[providers.%s]\n", name)
	writeProviderString(&b, "kind", core.ClaudeCodeKind)
	writeProviderString(&b, "config_dir", record.ConfigDir)
	writeProviderString(&b, "binary", record.Binary)
	writeProviderString(&b, "auth", record.Auth)
	writeProviderString(&b, "api_key_env", record.APIKeyEnv)
	if record.AllowScheduled {
		b.WriteString("allow_scheduled = true\n")
	}
	if record.Disabled {
		b.WriteString("disabled = true\n")
	}
	writeProviderString(&b, "label", record.Label)
	return b.String()
}

// putClaudeAccount saves a Claude Code account (API-CCT-010): it writes the
// table, creates the account folder (0700), and answers the command that logs
// the account in once. An absent key keeps its stored value. The built-in
// account "claude-code" stores only its settings (API-CCT-022): no
// config_dir or label, no folder is created, and its login command stays
// `claude auth login`.
func (s *Server) putClaudeAccount(w http.ResponseWriter, r *http.Request, store ConfigStore, name string, body map[string]json.RawMessage) {
	path := "providers." + name
	builtin := name == core.ClaudeCodeProvider
	var issues []ConfigIssue
	for _, key := range []string{"base_url", "headers", "api_key"} {
		if _, ok := body[key]; ok {
			issues = append(issues, ConfigIssue{Key: path + "." + key, Message: "not allowed for kind claude-code"})
		}
	}
	if builtin {
		for _, key := range []string{"config_dir", "label"} {
			if _, ok := body[key]; ok {
				issues = append(issues, ConfigIssue{Key: path + "." + key, Message: "not allowed for the built-in account: it always uses your normal Claude Code login"})
			}
		}
	}
	if len(issues) > 0 {
		writeProviderIssues(w, issues)
		return
	}
	expected, present, err := providerStringField(body, "expected_version")
	if err != nil || !present || expected == "" {
		writeProviderIssue(w, path+".expected_version", "expected_version is required")
		return
	}
	strs := map[string]*string{}
	bools := map[string]*bool{}
	for _, key := range claudeAccountKeys {
		raw, ok := body[key]
		if !ok {
			continue
		}
		switch key {
		case "allow_scheduled", "disabled":
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				writeProviderIssue(w, path+"."+key, key+" must be a boolean")
				return
			}
			bools[key] = &value
		default:
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				writeProviderIssue(w, path+"."+key, key+" must be a string")
				return
			}
			value = strings.TrimSpace(value)
			if strings.ContainsAny(value, "\x00\r\n") {
				writeProviderIssue(w, path+"."+key, key+" must be one line")
				return
			}
			strs[key] = &value
		}
	}
	if v := strs["auth"]; v != nil && *v != "" && *v != core.RuntimeAuthSubscription && *v != core.RuntimeAuthAPIKey {
		writeProviderIssue(w, path+".auth", "auth must be subscription or api_key")
		return
	}
	if v := strs["api_key_env"]; v != nil && *v != "" && !providerEnvRE.MatchString(*v) {
		writeProviderIssue(w, path+".api_key_env", "api_key_env must match [A-Za-z_][A-Za-z0-9_]*")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before claude account save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before claude account save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	record := providerRecord{}
	storedTable := document.Providers[name]
	if builtin {
		storedTable = document.Builtin
	}
	if stored := providerRecordFromMap(storedTable); stored.Kind == core.ClaudeCodeKind {
		record = providerRecord{
			ConfigDir: stored.ConfigDir, Binary: stored.Binary, Auth: stored.Auth, APIKeyEnv: stored.APIKeyEnv,
			AllowScheduled: stored.AllowScheduled, Disabled: stored.Disabled, Label: stored.Label,
		}
	}
	record.Kind = core.ClaudeCodeKind
	for key, value := range strs {
		switch key {
		case "config_dir":
			record.ConfigDir = *value
		case "binary":
			record.Binary = *value
		case "auth":
			record.Auth = *value
		case "api_key_env":
			record.APIKeyEnv = *value
		}
	}
	if v := bools["allow_scheduled"]; v != nil {
		record.AllowScheduled = *v
	}
	if v := bools["disabled"]; v != nil {
		record.Disabled = *v
	}

	if builtin {
		record.ConfigDir, record.Label = "", ""
	}
	table := renderClaudeAccountTable(name, record)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw, "[providers."+name+"]", table)
	if err != nil {
		s.writeProviderSaveError(w, "save claude account", err)
		return
	}
	// The folder is created before the table is written, so a saved account
	// always has one. An existing folder is kept as it is. The built-in
	// account uses the owner's own Claude Code folder: nothing is created.
	dir := s.claudeConfigDir(name, record)
	if !builtin {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			writeProviderIssue(w, path+".config_dir", "cannot create the account folder: "+err.Error())
			return
		}
	}
	configChanged := !bytes.Equal(candidate, file.Raw)
	restartRequired := !isLiveStore(store)
	if configChanged {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for claude account save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, changed)
		restartRequired = s.applyLiveRestartRequired(r.Context(), store, candidate)
	}
	writeJSON(w, http.StatusOK, providerSaveResponse{
		Name:            name,
		Version:         webfiles.ConfigVersion(candidate),
		RestartRequired: restartRequired,
		ConfigDir:       dir,
		LoginCommand:    llmcall.ClaudeLoginCommand(dir),
	})
}

// testClaudeAccount tests a saved Claude Code account with the same tiny turn
// as POST /v1/router/test on "<name>/<model>" (model default haiku). An empty
// name tests the built-in account.
func (s *Server) testClaudeAccount(w http.ResponseWriter, r *http.Request, body providerTestRequest) {
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = core.ClaudeCodeProvider
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = claudeTestModel
	}
	if !providerNameRE.MatchString(name) || strings.ContainsAny(model, "\r\n") || !llmcall.ValidClaudeCodeModel(model) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "name must be a Claude Code account and model one line not starting with '-'")
		return
	}
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	result, err := port.RouterTest(r.Context(), name+"/"+model)
	if err != nil {
		s.writeRouterError(w, "claude account test", err)
		return
	}
	if result.OK {
		writeJSON(w, http.StatusOK, providerTestOKResponse{OK: true, LatencyMS: result.LatencyMS})
		return
	}
	writeJSON(w, http.StatusOK, providerTestFailResponse{OK: false, Status: result.Status, Message: result.Message})
}
