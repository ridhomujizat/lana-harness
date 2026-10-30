package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/pelletier/go-toml/v2"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// secretBodyLimit is the largest accepted PUT/DELETE /v1/secrets body.
const secretBodyLimit = 64 << 10

// SecretStore supplies the home directory and the names referenced by
// config.toml. The web package never imports config or lana.
type SecretStore interface {
	// SettingsHome is the $LANA_HOME directory.
	SettingsHome() string
	// ConfigSecretNames returns providers.*.api_key_env,
	// channels.telegram.token_env, and MCP ${VAR} names.
	ConfigSecretNames(ctx context.Context) ([]string, error)
}

// SetSecretStore installs the secrets port directly. SetBackend also calls it
// when the backend implements SecretStore.
func (s *Server) SetSecretStore(store SecretStore) {
	s.secretStore = store
}

func (s *Server) registerSecretRoutes() {
	s.Handle("GET /v1/secrets", http.HandlerFunc(s.handleListSecrets))
	s.Handle("PUT /v1/secrets/{NAME}", http.HandlerFunc(s.handlePutSecret))
	s.Handle("DELETE /v1/secrets/{NAME}", http.HandlerFunc(s.handleDeleteSecret))
}

func (s *Server) secretStoreOrError(w http.ResponseWriter) SecretStore {
	if s.secretStore == nil {
		s.logger.Error("web: secret store is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return nil
	}
	return s.secretStore
}

type secretJSON struct {
	Name            string   `json:"name"`
	State           string   `json:"state"`
	Source          string   `json:"source"`
	File            string   `json:"file"`
	OverriddenByEnv bool     `json:"overridden_by_env"`
	UpdatedAt       *int64   `json:"updated_at"`
	UsedBy          []string `json:"used_by"`
}

type listSecretsResponse struct {
	Version         string       `json:"version"`
	Secrets         []secretJSON `json:"secrets"`
	RestartRequired bool         `json:"restart_required"`
}

type secretValueRequest struct {
	Value           string `json:"value"`
	ExpectedVersion string `json:"expected_version"`
}

type secretVersionRequest struct {
	ExpectedVersion string `json:"expected_version"`
}

type secretWriteResponse struct {
	Name            string `json:"name"`
	State           string `json:"state"`
	RestartRequired bool   `json:"restart_required"`
}

// handleListSecrets returns the union of secrets.env names and config
// references. A name is "set" when secrets.env has a non-empty value for it or
// the process environment defines a non-empty value; the process environment
// wins at runtime, which overridden_by_env reports. Values are never returned.
func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	store := s.secretStoreOrError(w)
	if store == nil {
		return
	}
	path := secretsPath(store.SettingsHome())
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		s.logger.Error("web: read secrets version", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	fileValues, err := webfiles.ReadSecrets(path)
	if err != nil {
		s.logger.Error("web: read secrets", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	// Update times are best-effort: a missing, unreadable, or corrupt meta file
	// must not stop the list, it only leaves updated_at null.
	meta, err := webfiles.ReadSecretsMeta(secretsMetaPath(store.SettingsHome()))
	if err != nil {
		s.logger.Warn("web: read secrets meta", "error", err)
		meta = webfiles.SecretsMeta{}
	}

	names := make(map[string]struct{}, len(fileValues))
	for name := range fileValues {
		names[name] = struct{}{}
	}
	configNames, err := store.ConfigSecretNames(r.Context())
	if err != nil {
		// The file names are still useful when config.toml cannot be read.
		s.logger.Debug("web: secret names from config", "error", err)
	}
	for _, name := range configNames {
		if webfiles.ValidateSecretName(name) == nil {
			names[name] = struct{}{}
		}
	}
	usage := s.secretUsage(r.Context())

	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	secrets := make([]secretJSON, 0, len(sorted))
	for _, name := range sorted {
		fileValue, inFile := fileValues[name]
		envValue, inEnv := os.LookupEnv(name)
		overridden := inEnv && envValue != ""
		state := "unset"
		if (inFile && fileValue != "") || overridden {
			state = "set"
		}
		file := "none"
		if inFile {
			file = path
		}
		var updatedAt *int64
		if ms, ok := meta.UpdatedMS[name]; ok {
			value := ms
			updatedAt = &value
		}
		usedBy := usage[name]
		if usedBy == nil {
			usedBy = []string{}
		}
		secrets = append(secrets, secretJSON{
			Name:            name,
			State:           state,
			Source:          "env",
			File:            file,
			OverriddenByEnv: overridden,
			UpdatedAt:       updatedAt,
			UsedBy:          usedBy,
		})
	}
	writeJSON(w, http.StatusOK, listSecretsResponse{
		Version:         version,
		Secrets:         secrets,
		RestartRequired: s.secretsRestartRequired(),
	})
}

func (s *Server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	store := s.secretStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("NAME")
	var body secretValueRequest
	if !decodeJSONObject(w, r, secretBodyLimit, &body) {
		return
	}
	if err := webfiles.ValidateSecretName(name); err != nil {
		writeSecretValidation(w, err)
		return
	}
	if err := webfiles.ValidateSecretValue(body.Value); err != nil {
		writeSecretValidation(w, err)
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	path := secretsPath(store.SettingsHome())
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		s.logger.Error("web: read secrets version before save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if body.ExpectedVersion != version {
		writeAPIError(w, http.StatusConflict, "stale_version", "secrets.env changed since it was loaded")
		return
	}
	if err := webfiles.SetSecret(path, name, body.Value); err != nil {
		writeSecretWriteError(w, err)
		return
	}
	if err := webfiles.SetSecretMeta(secretsMetaPath(store.SettingsHome()), name, s.nowTime().UnixMilli()); err != nil {
		// The secret itself is already written; a meta failure only loses the
		// update time.
		s.logger.Warn("web: write secrets meta", "error", err)
	}
	s.setSecretsChanged()
	s.auditSave("secret", "", name, nil)
	writeJSON(w, http.StatusOK, secretWriteResponse{
		Name:            name,
		State:           "set",
		RestartRequired: true,
	})
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	store := s.secretStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("NAME")
	var body secretVersionRequest
	if !decodeJSONObject(w, r, secretBodyLimit, &body) {
		return
	}
	if err := webfiles.ValidateSecretName(name); err != nil {
		writeSecretValidation(w, err)
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	path := secretsPath(store.SettingsHome())
	version, err := webfiles.SecretsVersion(path)
	if err != nil {
		s.logger.Error("web: read secrets version before delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if body.ExpectedVersion != version {
		writeAPIError(w, http.StatusConflict, "stale_version", "secrets.env changed since it was loaded")
		return
	}
	if err := webfiles.RemoveSecret(path, name); err != nil {
		if errors.Is(err, webfiles.ErrSecretNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "secret is not set")
			return
		}
		writeSecretWriteError(w, err)
		return
	}
	if err := webfiles.RemoveSecretMeta(secretsMetaPath(store.SettingsHome()), name); err != nil {
		// The secret itself is already removed; a meta failure only leaves a
		// stale update time behind.
		s.logger.Warn("web: write secrets meta", "error", err)
	}
	s.setSecretsChanged()
	s.auditSave("secret", "", name, nil)
	writeJSON(w, http.StatusOK, secretWriteResponse{
		Name:            name,
		State:           "unset",
		RestartRequired: true,
	})
}

func secretsPath(home string) string {
	return filepath.Join(home, "secrets.env")
}

func secretsMetaPath(home string) string {
	return filepath.Join(home, webfiles.SecretsMetaFileName)
}

// secretRefRE matches an MCP ${VAR} reference in raw config.toml.
var secretRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// secretUsage returns the config table paths that name each secret. It reads
// the raw config.toml through the configured ConfigStore, because a loaded
// config would have MCP ${VAR} references already interpolated. Only names are
// read from the file; every value is ignored. A config read or parse failure is
// not fatal: the list is returned with empty used_by arrays.
func (s *Server) secretUsage(ctx context.Context) map[string][]string {
	if s.configStore == nil {
		return nil
	}
	file, err := s.configStore.ConfigFile(ctx)
	if err != nil {
		s.logger.Debug("web: secret usage from config", "error", err)
		return nil
	}
	if !file.Exists {
		return nil
	}
	usage, err := secretUsageFromConfig(file.Raw)
	if err != nil {
		s.logger.Debug("web: secret usage from config", "error", err)
		return nil
	}
	return usage
}

// secretUsageFromConfig maps each secret name to the config tables that
// reference it: providers.<name> for api_key_env, channels.telegram for
// token_env, and mcp.<name> for MCP ${VAR} references.
func secretUsageFromConfig(data []byte) (map[string][]string, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	usage := make(map[string][]string)
	add := func(name, table string) {
		if webfiles.ValidateSecretName(name) != nil {
			return
		}
		for _, existing := range usage[name] {
			if existing == table {
				return
			}
		}
		usage[name] = append(usage[name], table)
	}
	if providers, ok := root["providers"].(map[string]any); ok {
		for name, raw := range providers {
			table, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if env, ok := table["api_key_env"].(string); ok {
				add(env, "providers."+name)
			}
		}
	}
	if channels, ok := root["channels"].(map[string]any); ok {
		if telegram, ok := channels["telegram"].(map[string]any); ok {
			if env, ok := telegram["token_env"].(string); ok {
				add(env, "channels.telegram")
			}
		}
	}
	if mcp, ok := root["mcp"].(map[string]any); ok {
		for name, raw := range mcp {
			server, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range []string{"env", "headers"} {
				table, ok := server[field].(map[string]any)
				if !ok {
					continue
				}
				for _, value := range table {
					text, ok := value.(string)
					if !ok {
						continue
					}
					for _, match := range secretRefRE.FindAllStringSubmatch(text, -1) {
						add(match[1], "mcp."+name)
					}
				}
			}
		}
	}
	for name := range usage {
		sort.Strings(usage[name])
	}
	return usage, nil
}

func writeSecretValidation(w http.ResponseWriter, err error) {
	field := "value"
	message := err.Error()
	var validation *webfiles.SecretValidationError
	if errors.As(err, &validation) {
		field = validation.Field
		message = validation.Message
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "secret is not valid", []map[string]any{
		{"field": field, "message": message},
	})
}

func writeSecretWriteError(w http.ResponseWriter, err error) {
	var validation *webfiles.SecretValidationError
	if errors.As(err, &validation) {
		writeSecretValidation(w, err)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func (s *Server) setSecretsChanged() {
	s.mu.Lock()
	s.secretsChanged = true
	s.mu.Unlock()
}

func (s *Server) secretsRestartRequired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secretsChanged
}
