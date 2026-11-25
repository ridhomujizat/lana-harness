package web

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// Account order and key pools (spec 75 REQ-PAF-006, design "HTTP"): the
// owner orders the accounts of a subscription pool (Claude Code,
// Antigravity) and the API keys of a provider by drag and drop. Each save is
// one config write.

// poolPriorityStep is the gap between two pool priorities: 10, 20, 30, …
const poolPriorityStep = 10

// apiKeyPoolKinds are the API kinds that may list backup keys
// (REQ-PAF-005).
var apiKeyPoolKinds = map[string]bool{
	"anthropic": true, "commandcode": true, "deepseek": true, "openai": true, "openai_compat": true, "openrouter": true,
}

// ProviderKeyCoolingBackend is the optional router port part that reports the
// API keys of a provider that are cooling down, by secret name (REQ-PAF-006).
type ProviderKeyCoolingBackend interface {
	ProviderKeyCooling(provider string) map[string]time.Time
}

// keyCooling returns the cooling keys of provider, or none without a port.
func (s *Server) keyCooling(provider string) map[string]time.Time {
	if port, ok := s.routerPort().(ProviderKeyCoolingBackend); ok {
		if out := port.ProviderKeyCooling(provider); out != nil {
			return out
		}
	}
	return map[string]time.Time{}
}

// providerKeyNames returns the keys of an API provider in use order:
// api_key_env (or the kind's default <NAME>_API_KEY), then api_key_envs.
func providerKeyNames(name string, record providerRecord) []string {
	var out []string
	if first := providerEffectiveKeyEnv(record, name); first != "" {
		out = append(out, first)
	}
	for _, env := range record.APIKeyEnvs {
		if env != "" && !containsString(out, env) {
			out = append(out, env)
		}
	}
	return out
}

// providerKeys returns the keys of an API provider with whether each secret
// holds a value and the end of its cooldown (spec 75, GET /v1/providers).
func (s *Server) providerKeys(name string, record providerRecord, secretValues map[string]string) []providerKeyJSON {
	cooling := s.keyCooling(name)
	names := providerKeyNames(name, record)
	out := make([]providerKeyJSON, 0, len(names))
	for _, env := range names {
		key := providerKeyJSON{Name: env, Present: providerKeyState(env, secretValues) != "unset"}
		if until, ok := cooling[env]; ok {
			at := until.UTC()
			key.CoolingUntil = &at
		}
		out = append(out, key)
	}
	return out
}

type poolOrderRequest struct {
	Runtime         string   `json:"runtime"`
	Order           []string `json:"order"`
	Unpooled        []string `json:"unpooled"`
	ExpectedVersion string   `json:"expected_version"`
}

type poolOrderResponse struct {
	Version         string   `json:"version"`
	RestartRequired bool     `json:"restart_required"`
	Pool            []string `json:"pool"`
}

// poolAccounts returns every account of runtime in a config document, the
// built-in one included, with its stored table.
func (d providerDocument) poolAccounts(runtime string) map[string]map[string]any {
	out := map[string]map[string]any{}
	switch runtime {
	case core.ClaudeCodeKind:
		out[core.ClaudeCodeProvider] = d.Builtin
	case core.AntigravityKind:
		out[core.AntigravityProvider] = d.AgyBuiltin
	default:
		return nil
	}
	for name, table := range d.Providers {
		if mapProviderString(table, "kind") == runtime {
			out[name] = table
		}
	}
	return out
}

// handlePutPoolOrder rewrites the pool order of a subscription runtime
// (REQ-PAF-006): priority 10, 20, … for order then unpooled, pool removed for
// order and pool = false for unpooled, in one config write. Every account of
// the runtime appears exactly once; anything else is 422.
func (s *Server) handlePutPoolOrder(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body poolOrderRequest
	if !decodeJSONObject(w, r, routerBodyLimit, &body) {
		return
	}
	if body.Runtime != core.ClaudeCodeKind && body.Runtime != core.AntigravityKind {
		writeRouterInvalid(w, []ConfigIssue{{Key: "runtime", Message: "runtime must be claude-code or antigravity"}})
		return
	}
	if len(body.Order)+len(body.Unpooled) == 0 {
		writeRouterInvalid(w, []ConfigIssue{{Key: "order", Message: "order must list the accounts"}})
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before pool order save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if body.ExpectedVersion != "" && file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "version_conflict", "config.toml changed since it was loaded")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before pool order save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	accounts := document.poolAccounts(body.Runtime)
	var issues []ConfigIssue
	seen := map[string]bool{}
	check := func(field string, names []string) {
		for i, name := range names {
			key := field + "[" + strconv.Itoa(i) + "]"
			switch {
			case seen[name]:
				issues = append(issues, ConfigIssue{Key: key, Message: name + " is listed more than once"})
			case !providerNameRE.MatchString(name):
				issues = append(issues, ConfigIssue{Key: key, Message: "name must match [a-z0-9-]+"})
			default:
				if _, ok := accounts[name]; !ok {
					issues = append(issues, ConfigIssue{Key: key, Message: "no " + body.Runtime + " account named " + name})
				}
			}
			seen[name] = true
		}
	}
	check("order", body.Order)
	check("unpooled", body.Unpooled)
	missing := []string{}
	for name := range accounts {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		issues = append(issues, ConfigIssue{Key: "order", Message: name + " is missing; every account must appear once"})
	}
	if len(issues) > 0 {
		writeRouterInvalid(w, issues)
		return
	}

	candidate := file.Raw
	noCheck := func(context.Context, []byte) error { return nil }
	all := append(append([]string{}, body.Order...), body.Unpooled...)
	for i, name := range all {
		record := providerRecordFromMap(accounts[name])
		record.Kind = body.Runtime
		priority := (i + 1) * poolPriorityStep
		record.Priority = &priority
		record.Pool = nil
		if i >= len(body.Order) {
			unpooled := false
			record.Pool = &unpooled
		}
		var table string
		if body.Runtime == core.ClaudeCodeKind {
			table = renderClaudeAccountTable(name, record)
		} else {
			table = renderAntigravityAccountTable(name, record)
		}
		next, _, err := webfiles.RewriteTable(r.Context(), noCheck, candidate, "[providers."+name+"]", table)
		if err != nil {
			s.writeRouterSaveError(w, "save pool order", err)
			return
		}
		candidate = next
	}
	if err := providerChecker(store)(r.Context(), candidate); err != nil {
		s.writeRouterSaveError(w, "save pool order", err)
		return
	}
	restartRequired := false
	if string(candidate) != string(file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for pool order save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		keys := make([]string, 0, len(all))
		for _, name := range all {
			keys = append(keys, "providers."+name+".priority")
		}
		s.auditSave("router.pool_order", providerConfigPath(file, s.providerHome()), body.Runtime, keys)
		restartRequired = s.applyLiveRestartRequired(r.Context(), store, candidate)
	}
	pool := body.Order
	if pool == nil {
		pool = []string{}
	}
	writeJSON(w, http.StatusOK, poolOrderResponse{
		Version: webfiles.ConfigVersion(candidate), RestartRequired: restartRequired, Pool: pool,
	})
}

type providerKeysRequest struct {
	Keys            []string `json:"keys"`
	ExpectedVersion string   `json:"expected_version"`
}

type providerKeysResponse struct {
	Version         string            `json:"version"`
	RestartRequired bool              `json:"restart_required"`
	Keys            []providerKeyJSON `json:"keys"`
}

// handlePutProviderKeys sets an API provider's keys in use order
// (REQ-PAF-006): api_key_env = the first, api_key_envs = the rest (removed
// when empty). Secret names only; values are set through the secrets API.
func (s *Server) handlePutProviderKeys(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !providerNameRE.MatchString(name) {
		writeProviderIssue(w, "name", "name must match [a-z0-9-]+")
		return
	}
	var body providerKeysRequest
	if !decodeJSONObject(w, r, providerBodyLimit, &body) {
		return
	}
	path := "providers." + name
	if len(body.Keys) == 0 {
		writeProviderIssue(w, path+".keys", "list at least one key")
		return
	}
	var issues []ConfigIssue
	seen := map[string]bool{}
	keys := make([]string, 0, len(body.Keys))
	for i, key := range body.Keys {
		key = strings.TrimSpace(key)
		field := path + ".keys." + strconv.Itoa(i)
		switch {
		case !providerEnvRE.MatchString(key):
			issues = append(issues, ConfigIssue{Key: field, Message: "a key must be a secret name matching [A-Za-z_][A-Za-z0-9_]*"})
		case seen[key]:
			issues = append(issues, ConfigIssue{Key: field, Message: key + " is listed more than once"})
		}
		seen[key] = true
		keys = append(keys, key)
	}
	if len(issues) > 0 {
		writeProviderIssues(w, issues)
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before provider keys save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return
	}
	if body.ExpectedVersion != "" && file.Version != body.ExpectedVersion {
		writeAPIError(w, http.StatusConflict, "stale_version", "config.toml changed since it was loaded")
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before provider keys save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	table, ok := document.Providers[name]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "provider is not configured")
		return
	}
	record := providerRecordFromMap(table)
	if !apiKeyPoolKinds[record.Kind] {
		writeProviderIssue(w, path+".kind", "kind "+record.Kind+" has no API keys")
		return
	}
	record.APIKeyEnv = keys[0]
	record.APIKeyEnvs = append([]string(nil), keys[1:]...)
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw,
		"[providers."+name+"]", renderProviderTable(name, record))
	if err != nil {
		s.writeProviderSaveError(w, "save provider keys", err)
		return
	}
	restartRequired := !isLiveStore(store)
	if string(candidate) != string(file.Raw) {
		if err := store.ConfigWrite(r.Context(), candidate); err != nil {
			s.logger.Error("web: write config for provider keys save", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		s.auditSave("provider.save", providerConfigPath(file, s.providerHome()), name, changed)
		restartRequired = s.applyLiveRestartRequired(r.Context(), store, candidate)
	}
	secretValues := map[string]string{}
	if home := s.providerHome(); home != "" {
		if values, err := webfiles.ReadSecrets(secretsPath(home)); err == nil {
			secretValues = values
		} else if !os.IsNotExist(err) {
			s.logger.Warn("web: read secrets for provider keys", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, providerKeysResponse{
		Version: webfiles.ConfigVersion(candidate), RestartRequired: restartRequired,
		Keys: s.providerKeys(name, record, secretValues),
	})
}
