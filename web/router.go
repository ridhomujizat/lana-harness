package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/internal/routerdb"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// The Router page API of spec 37 (API-RTR-007..014) and the proxy mount of
// API-RTR-015/016. Config writes (combos, the proxy switch) go through the
// ConfigStore like the provider endpoints; everything that needs the running
// router goes through RouterBackend, which cmd/lana implements over the
// runtime. web cannot import the router package (archtest), so the port uses
// web and internal/routerdb types only.

// RouterProxyPrefix is the path the OpenAI-compatible proxy is mounted at
// (API-RTR-015, API-RTR-016).
const RouterProxyPrefix = "/router/v1"

// routerBodyLimit is the largest accepted Router page request body.
const routerBodyLimit = 64 << 10

// routerModelsTimeout bounds the model-list fetches of one dashboard request
// (choices, models_count). A provider that does not answer in time lists its
// catalog models only.
const routerModelsTimeout = 10 * time.Second

// routerKeyNameMax is the longest accepted proxy key name.
const routerKeyNameMax = 64

// comboNameRE is the combo name rule of API-RTR-002.
var comboNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var (
	// ErrRouterUnavailable is returned by a RouterBackend whose runtime has
	// no router or no router store; the route answers 503.
	ErrRouterUnavailable = errors.New("router is not available")
	// ErrRouterUnknownProvider is returned for a provider the router does
	// not have; the route answers 404.
	ErrRouterUnknownProvider = errors.New("unknown provider")
	// ErrRouterUnknownAddress is returned for an address that names no
	// combo or provider; the route answers 404.
	ErrRouterUnknownAddress = errors.New("unknown provider or combo")
)

// RouterBackend is the daemon port of the Router page. Implementations must
// be safe for concurrent use. Keys are revoked with routerdb.ErrKeyNotFound
// for a missing key.
type RouterBackend interface {
	// RouterStatus returns the live router state: the cooling targets and
	// whether the proxy is enabled.
	RouterStatus(ctx context.Context) (RouterStatus, error)
	// RouterModels lists a provider's models (API-RTR-009). A fetch error
	// of the provider's models endpoint is reported in FetchError, not as
	// an error.
	RouterModels(ctx context.Context, provider string, refresh bool) (RouterModelList, error)
	// RouterTest sends one "ping" with max_tokens 1 to an address or combo
	// with source "test" (API-RTR-010).
	RouterTest(ctx context.Context, address string) (RouterTestResult, error)
	// RouterUsage aggregates call records for range "24h", "7d", or "30d"
	// (API-RTR-011).
	RouterUsage(ctx context.Context, rangeName string) (routerdb.Usage, error)
	// RouterCalls returns a page of call records, newest first (API-RTR-012).
	RouterCalls(ctx context.Context, filter routerdb.CallFilter) (routerdb.CallPage, error)
	// RouterLimits returns the limit snapshots (API-RTR-013).
	RouterLimits(ctx context.Context, refresh bool) ([]RouterLimit, error)
	// RouterKeys lists the live proxy keys (API-RTR-014).
	RouterKeys(ctx context.Context) ([]routerdb.Key, error)
	// CreateRouterKey creates a proxy key; the key value is returned only
	// here.
	CreateRouterKey(ctx context.Context, name string) (RouterKeyCreated, error)
	// RevokeRouterKey revokes a proxy key.
	RevokeRouterKey(ctx context.Context, id string) error
	// RouterProxyHandler returns the OpenAI-compatible proxy handler, or nil
	// when [router] proxy is off. It is read on every proxy request, so the
	// switch applies without a restart.
	RouterProxyHandler() http.Handler
	// ClaudeAgentStatus reports the Claude Agent runtime (spec 38,
	// API-CCR-003).
	ClaudeAgentStatus(ctx context.Context) (ClaudeAgentStatus, error)
}

// RouterStatus is the live router state of GET /v1/router.
type RouterStatus struct {
	Cooling      []RouterCooling
	ProxyEnabled bool
}

// RouterCooling is one target that is cooling down.
type RouterCooling struct {
	Target string    `json:"target"`
	Until  time.Time `json:"until"`
}

// RouterModel is one model of API-RTR-009. UsedBy is filled by the handler.
type RouterModel struct {
	ID            string           `json:"id"`
	Address       string           `json:"address"`
	Name          string           `json:"name"`
	ContextWindow int              `json:"context_window"`
	MaxOutput     int              `json:"max_output"`
	Vision        bool             `json:"vision"`
	Thinking      bool             `json:"thinking"`
	Price         *models.Price    `json:"price"`
	Source        string           `json:"source"`
	UsedBy        RouterModelUsers `json:"used_by"`
}

// RouterModelUsers are the agents and combos that use a model address.
type RouterModelUsers struct {
	Agents []string `json:"agents"`
	Combos []string `json:"combos"`
}

// RouterModelList is the model list of one provider.
type RouterModelList struct {
	Provider   string        `json:"provider"`
	FetchedAt  *time.Time    `json:"fetched_at"`
	FetchError string        `json:"fetch_error"`
	Models     []RouterModel `json:"models"`
}

// RouterTestResult is the outcome of POST /v1/router/test.
type RouterTestResult struct {
	OK        bool
	LatencyMS int64
	Class     string
	Status    int
	Message   string
}

// RouterLimit is one limit snapshot of API-RTR-013.
type RouterLimit struct {
	Provider  string             `json:"provider"`
	Kind      string             `json:"kind"`
	UpdatedAt *time.Time         `json:"updated_at"`
	Error     string             `json:"error"`
	Rate      *RouterRateLimits  `json:"rate"`
	Credit    *RouterCredit      `json:"credit"`
	Balance   *RouterBalance     `json:"balance"`
	Windows   []RouterPlanWindow `json:"windows"`
}

// RouterRateLimits are the rate limits a provider reports in its headers.
type RouterRateLimits struct {
	Requests     *RouterRateLimit `json:"requests"`
	Tokens       *RouterRateLimit `json:"tokens"`
	InputTokens  *RouterRateLimit `json:"input_tokens"`
	OutputTokens *RouterRateLimit `json:"output_tokens"`
}

// RouterRateLimit is one limit with its remaining amount and reset time.
type RouterRateLimit struct {
	Limit     int64      `json:"limit"`
	Remaining int64      `json:"remaining"`
	ResetAt   *time.Time `json:"reset_at"`
}

// RouterCredit is an OpenRouter-style credit.
type RouterCredit struct {
	LimitUSD     *float64 `json:"limit_usd"`
	UsedUSD      float64  `json:"used_usd"`
	RemainingUSD *float64 `json:"remaining_usd"`
}

// RouterBalance is a DeepSeek-style account balance.
type RouterBalance struct {
	Currency string  `json:"currency"`
	Total    float64 `json:"total"`
	Granted  float64 `json:"granted"`
	ToppedUp float64 `json:"topped_up"`
}

// RouterPlanWindow is one plan usage window (the Claude Agent of spec 38).
type RouterPlanWindow struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	// Utilization is the used fraction, 0..1; null when not reported.
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

// RouterKeyCreated is the answer of POST /v1/router/keys. Key is the only
// place the key value ever appears.
type RouterKeyCreated struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Key    string `json:"key"`
}

// ClaudeAgentStatus is the Claude Agent runtime status of spec 38
// (API-CCR-003).
type ClaudeAgentStatus struct {
	ConfiguredAgents []string `json:"configured_agents"`
	Binary           string   `json:"binary"`
	Version          string   `json:"version"`
	VersionOK        bool     `json:"version_ok"`
	LoggedIn         bool     `json:"logged_in"`
	AuthMethod       string   `json:"auth_method"`
	Error            string   `json:"error"`
	Running          int      `json:"running"`
	Queued           int      `json:"queued"`
	MaxConcurrent    int      `json:"max_concurrent"`
}

// SetRouterBackend installs the Router port directly. SetBackend also
// installs it when the backend implements RouterBackend.
func (s *Server) SetRouterBackend(backend RouterBackend) {
	s.routerMu.Lock()
	s.routerBackend = backend
	s.routerMu.Unlock()
}

func (s *Server) routerPort() RouterBackend {
	s.routerMu.RLock()
	defer s.routerMu.RUnlock()
	return s.routerBackend
}

func (s *Server) routerPortOrError(w http.ResponseWriter) RouterBackend {
	port := s.routerPort()
	if port == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "router_unavailable", "the router is not available")
	}
	return port
}

// registerRouterRoutes installs the Router page API behind the session and
// CSRF middleware, and the proxy mount on the bare mux: the proxy uses proxy
// keys, not the cookie, and keeps the Host check (REQ-RTR-015).
func (s *Server) registerRouterRoutes() {
	s.Handle("GET /v1/router", http.HandlerFunc(s.handleRouterOverview))
	s.Handle("GET /v1/router/models", http.HandlerFunc(s.handleRouterModels))
	s.Handle("POST /v1/router/test", http.HandlerFunc(s.handleRouterTest))
	s.Handle("GET /v1/router/usage", http.HandlerFunc(s.handleRouterUsage))
	s.Handle("GET /v1/router/calls", http.HandlerFunc(s.handleRouterCalls))
	s.Handle("GET /v1/router/limits", http.HandlerFunc(s.handleRouterLimits))
	s.Handle("PUT /v1/router/combos/{name}", http.HandlerFunc(s.handlePutCombo))
	s.Handle("DELETE /v1/router/combos/{name}", http.HandlerFunc(s.handleDeleteCombo))
	s.Handle("PUT /v1/router/proxy", http.HandlerFunc(s.handlePutRouterProxy))
	s.Handle("GET /v1/router/keys", http.HandlerFunc(s.handleListRouterKeys))
	s.Handle("POST /v1/router/keys", http.HandlerFunc(s.handleCreateRouterKey))
	s.Handle("DELETE /v1/router/keys/{id}", http.HandlerFunc(s.handleRevokeRouterKey))
	s.Handle("GET /v1/router/claude-agent", http.HandlerFunc(s.handleClaudeAgentStatus))
	s.mux.Handle(RouterProxyPrefix+"/", s.checkHost(http.HandlerFunc(s.serveRouterProxy)))
}

// serveRouterProxy hands a proxy request to the backend's handler, read per
// request so PUT /v1/router/proxy applies at once. Off answers 404 with an
// OpenAI-style body.
func (s *Server) serveRouterProxy(w http.ResponseWriter, r *http.Request) {
	var handler http.Handler
	if port := s.routerPort(); port != nil {
		handler = port.RouterProxyHandler()
	}
	if handler == nil {
		writeOpenAIError(w, http.StatusNotFound, "the router proxy is disabled", "invalid_request_error", "proxy_disabled")
		return
	}
	http.StripPrefix(RouterProxyPrefix, handler).ServeHTTP(w, r)
}

// writeOpenAIError writes {"error":{"message","type","code"}} (API-RTR-015).
func writeOpenAIError(w http.ResponseWriter, status int, message, typ, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message, "type": typ, "code": code}})
}

// routerProviderJSON is one provider row of GET /v1/router.
type routerProviderJSON struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	BaseURL        string   `json:"base_url"`
	DefaultBaseURL string   `json:"default_base_url"`
	KeyState       string   `json:"key_state"`
	UsedBy         []string `json:"used_by"`
}

// routerComboJSON is one combo row of GET /v1/router.
type routerComboJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Models      []string `json:"models"`
	UsedBy      []string `json:"used_by"`
}

// routerChoiceJSON is one model picker entry (REQ-RTR-018).
type routerChoiceJSON struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group"`
}

type routerProxyJSON struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
}

type routerOverviewResponse struct {
	Version   string               `json:"version"`
	Providers []routerProviderJSON `json:"providers"`
	Combos    []routerComboJSON    `json:"combos"`
	Cooling   []RouterCooling      `json:"cooling"`
	Proxy     routerProxyJSON      `json:"proxy"`
	Choices   []routerChoiceJSON   `json:"choices"`
}

// readRouterDocument reads and parses config.toml for a Router handler. On
// failure it writes the error and returns ok false.
func (s *Server) readRouterDocument(w http.ResponseWriter, r *http.Request, store ConfigStore) (ConfigFile, providerDocument, bool) {
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for router", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return ConfigFile{}, providerDocument{}, false
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for router", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return ConfigFile{}, providerDocument{}, false
	}
	return file, document, true
}

func (s *Server) handleRouterOverview(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	file, document, ok := s.readRouterDocument(w, r, store)
	if !ok {
		return
	}
	secretValues := map[string]string{}
	if home := s.providerHome(); home != "" {
		if values, err := webfiles.ReadSecrets(secretsPath(home)); err == nil {
			secretValues = values
		} else {
			s.logger.Warn("web: read secrets for router overview", "error", err)
		}
	}
	response := routerOverviewResponse{
		Version:   file.Version,
		Providers: []routerProviderJSON{},
		Combos:    []routerComboJSON{},
		Cooling:   []RouterCooling{},
		Choices:   []routerChoiceJSON{},
		Proxy:     routerProxyJSON{BaseURL: "http://" + s.Addr() + RouterProxyPrefix},
	}
	if enabled, ok := document.Router["proxy"].(bool); ok {
		response.Proxy.Enabled = enabled
	}
	if port := s.routerPort(); port != nil {
		status, err := port.RouterStatus(r.Context())
		if err != nil && !errors.Is(err, ErrRouterUnavailable) {
			s.logger.Error("web: router status", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if err == nil {
			response.Proxy.Enabled = status.ProxyEnabled
			if status.Cooling != nil {
				response.Cooling = status.Cooling
			}
		}
	}
	providers := sortedMapKeys(document.Providers)
	for _, name := range providers {
		record := providerRecordFromMap(document.Providers[name])
		response.Providers = append(response.Providers, routerProviderJSON{
			Name:           name,
			Kind:           record.Kind,
			BaseURL:        record.BaseURL,
			DefaultBaseURL: models.DefaultBaseURL(record.Kind),
			KeyState:       providerKeyState(providerEffectiveKeyEnv(record, name), secretValues),
			UsedBy:         document.agentsUsingProvider(name),
		})
	}
	for _, name := range sortedMapKeys(document.Combos) {
		table := document.Combos[name]
		response.Combos = append(response.Combos, routerComboJSON{
			Name:        name,
			Description: mapProviderString(table, "description"),
			Models:      nonNilStrings(mapProviderStrings(table, "models")),
			UsedBy:      document.comboUsers(name),
		})
		response.Choices = append(response.Choices, routerChoiceJSON{Value: name, Label: name, Group: "Combos"})
	}
	lists := s.routerProviderModels(r.Context(), document, providers)
	for _, name := range providers {
		for _, model := range lists[name] {
			label := model.Name
			if label == "" {
				label = model.ID
			}
			response.Choices = append(response.Choices, routerChoiceJSON{Value: model.Address, Label: label, Group: name})
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// routerProviderModels lists the models of each provider, in parallel and
// bounded by routerModelsTimeout. Without a router backend, or for a
// provider the router does not know yet, the catalog of the kind is used.
func (s *Server) routerProviderModels(ctx context.Context, document providerDocument, names []string) map[string][]RouterModel {
	out := make(map[string][]RouterModel, len(names))
	port := s.routerPort()
	if port == nil {
		for _, name := range names {
			out[name] = catalogRouterModels(name, providerRecordFromMap(document.Providers[name]))
		}
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, routerModelsTimeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := port.RouterModels(ctx, name, false)
			found := list.Models
			if err != nil {
				found = catalogRouterModels(name, providerRecordFromMap(document.Providers[name]))
			}
			mu.Lock()
			out[name] = found
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// catalogRouterModels returns the built-in catalog models of a provider.
func catalogRouterModels(name string, record providerRecord) []RouterModel {
	catalog := models.ForKind(record.Kind)
	if record.Kind == "openai_compat" {
		catalog = models.ForProviderKind(record.BaseURL)
	}
	out := make([]RouterModel, 0, len(catalog))
	for _, m := range catalog {
		out = append(out, RouterModel{
			ID: m.ID, Address: name + "/" + m.ID, Name: m.Name,
			ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutput,
			Vision: m.Vision, Thinking: m.Thinking != "", Price: m.Price, Source: "catalog",
		})
	}
	return out
}

type routerModelsAllResponse struct {
	Providers []RouterModelList `json:"providers"`
}

func (s *Server) handleRouterModels(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	_, document, ok := s.readRouterDocument(w, r, store)
	if !ok {
		return
	}
	refresh := queryFlag(r, "refresh")
	if provider := r.URL.Query().Get("provider"); provider != "" {
		list, err := port.RouterModels(r.Context(), provider, refresh)
		if err != nil {
			s.writeRouterError(w, "router models", err)
			return
		}
		writeJSON(w, http.StatusOK, document.withModelUsers(list))
		return
	}
	names := sortedMapKeys(document.Providers)
	response := routerModelsAllResponse{Providers: make([]RouterModelList, 0, len(names))}
	for _, name := range names {
		list, err := port.RouterModels(r.Context(), name, refresh)
		if errors.Is(err, ErrRouterUnknownProvider) {
			continue
		}
		if err != nil {
			s.writeRouterError(w, "router models", err)
			return
		}
		response.Providers = append(response.Providers, document.withModelUsers(list))
	}
	writeJSON(w, http.StatusOK, response)
}

// withModelUsers fills the used_by of every model and makes the lists
// non-null.
func (d providerDocument) withModelUsers(list RouterModelList) RouterModelList {
	if list.Models == nil {
		list.Models = []RouterModel{}
	}
	for i := range list.Models {
		list.Models[i].UsedBy = d.modelUsers(list.Models[i].Address)
	}
	return list
}

type routerTestRequest struct {
	Address string `json:"address"`
}

type routerTestFailResponse struct {
	OK      bool   `json:"ok"`
	Class   string `json:"class"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (s *Server) handleRouterTest(w http.ResponseWriter, r *http.Request) {
	var body routerTestRequest
	if !decodeJSONObject(w, r, routerBodyLimit, &body) {
		return
	}
	address := strings.TrimSpace(body.Address)
	if address == "" || strings.ContainsAny(address, "\r\n") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "address is required and must be one line")
		return
	}
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	result, err := port.RouterTest(r.Context(), address)
	if err != nil {
		s.writeRouterError(w, "router test", err)
		return
	}
	if result.OK {
		writeJSON(w, http.StatusOK, providerTestOKResponse{OK: true, LatencyMS: result.LatencyMS})
		return
	}
	writeJSON(w, http.StatusOK, routerTestFailResponse{Class: result.Class, Status: result.Status, Message: result.Message})
}

type routerUsageResponse struct {
	Range string `json:"range"`
	routerdb.Usage
}

func (s *Server) handleRouterUsage(w http.ResponseWriter, r *http.Request) {
	rangeName := r.URL.Query().Get("range")
	if rangeName == "" {
		rangeName = "24h"
	}
	switch rangeName {
	case "24h", "7d", "30d":
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "range must be 24h, 7d, or 30d")
		return
	}
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	usage, err := port.RouterUsage(r.Context(), rangeName)
	if err != nil {
		s.writeRouterError(w, "router usage", err)
		return
	}
	if usage.Series == nil {
		usage.Series = []routerdb.Bucket{}
	}
	if usage.ByProvider == nil {
		usage.ByProvider = []routerdb.ProviderStats{}
	}
	if usage.ByModel == nil {
		usage.ByModel = []routerdb.ModelStats{}
	}
	if usage.BySource == nil {
		usage.BySource = []routerdb.SourceStats{}
	}
	writeJSON(w, http.StatusOK, routerUsageResponse{Range: rangeName, Usage: usage})
}

func (s *Server) handleRouterCalls(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := routerdb.CallFilter{
		Provider: query.Get("provider"),
		Source:   query.Get("source"),
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return
		}
		filter.Limit = min(limit, routerdb.MaxCallsLimit)
	}
	if raw := query.Get("before"); raw != "" {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "before must be a call id")
			return
		}
		filter.Before = before
	}
	switch status := query.Get("status"); status {
	case "", "ok", "error":
		filter.Outcome = status
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "status must be ok or error")
		return
	}
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	page, err := port.RouterCalls(r.Context(), filter)
	if err != nil {
		if errors.Is(err, routerdb.ErrInvalidFilter) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid call filter")
			return
		}
		s.writeRouterError(w, "router calls", err)
		return
	}
	if page.Calls == nil {
		page.Calls = []routerdb.Call{}
	}
	writeJSON(w, http.StatusOK, page)
}

type routerLimitsResponse struct {
	Limits []RouterLimit `json:"limits"`
}

func (s *Server) handleRouterLimits(w http.ResponseWriter, r *http.Request) {
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	limits, err := port.RouterLimits(r.Context(), queryFlag(r, "refresh"))
	if err != nil {
		s.writeRouterError(w, "router limits", err)
		return
	}
	if limits == nil {
		limits = []RouterLimit{}
	}
	for i := range limits {
		if limits[i].Windows == nil {
			limits[i].Windows = []RouterPlanWindow{}
		}
	}
	writeJSON(w, http.StatusOK, routerLimitsResponse{Limits: limits})
}

type comboPutRequest struct {
	Description     string   `json:"description"`
	Models          []string `json:"models"`
	ExpectedVersion string   `json:"expected_version"`
}

type routerSaveResponse struct {
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

func (s *Server) handlePutCombo(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	if !comboNameRE.MatchString(name) {
		writeRouterInvalid(w, []ConfigIssue{{Key: "combos." + name, Message: "name must match [a-z0-9][a-z0-9_-]{0,63}"}})
		return
	}
	var body comboPutRequest
	if !decodeJSONObject(w, r, routerBodyLimit, &body) {
		return
	}
	if body.ExpectedVersion == "" {
		writeRouterInvalid(w, []ConfigIssue{{Key: "expected_version", Message: "expected_version is required"}})
		return
	}
	if strings.ContainsAny(body.Description, "\r\n") {
		writeRouterInvalid(w, []ConfigIssue{{Key: "combos." + name + ".description", Message: "description must be one line"}})
		return
	}
	for i, model := range body.Models {
		if strings.ContainsAny(model, "\r\n") {
			writeRouterInvalid(w, []ConfigIssue{{Key: "combos." + name + ".models[" + strconv.Itoa(i) + "]", Message: "model must be one line"}})
			return
		}
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, ok := s.readConfigForRouterWrite(w, r, store, body.ExpectedVersion)
	if !ok {
		return
	}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw, "[combos."+name+"]", renderComboTable(name, body.Description, body.Models))
	if err != nil {
		s.writeRouterSaveError(w, "save combo", err)
		return
	}
	s.finishRouterWrite(w, r, store, file, candidate, "combo.save", name, changed)
}

func (s *Server) handleDeleteCombo(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	var body struct {
		ExpectedVersion string `json:"expected_version"`
	}
	if !decodeJSONObject(w, r, routerBodyLimit, &body) {
		return
	}
	if body.ExpectedVersion == "" {
		writeRouterInvalid(w, []ConfigIssue{{Key: "expected_version", Message: "expected_version is required"}})
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, ok := s.readConfigForRouterWrite(w, r, store, body.ExpectedVersion)
	if !ok {
		return
	}
	document, err := parseProviderDocument(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config before combo delete", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if _, exists := document.Combos[name]; !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "combo is not configured")
		return
	}
	if users := document.comboUsers(name); len(users) > 0 {
		writeAPIError(w, http.StatusConflict, "in_use", "used by agents: "+strings.Join(users, ", "))
		return
	}
	candidate, changed, err := webfiles.RewriteTable(r.Context(), providerChecker(store), file.Raw, "[combos."+name+"]", "")
	if err != nil {
		s.writeRouterSaveError(w, "delete combo", err)
		return
	}
	s.finishRouterWrite(w, r, store, file, candidate, "combo.delete", name, changed)
}

type routerProxyRequest struct {
	Enabled         *bool  `json:"enabled"`
	ExpectedVersion string `json:"expected_version"`
}

type routerProxyResponse struct {
	Enabled         bool   `json:"enabled"`
	BaseURL         string `json:"base_url"`
	Version         string `json:"version"`
	RestartRequired bool   `json:"restart_required"`
}

func (s *Server) handlePutRouterProxy(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	var body routerProxyRequest
	if !decodeJSONObject(w, r, routerBodyLimit, &body) {
		return
	}
	if body.Enabled == nil {
		writeRouterInvalid(w, []ConfigIssue{{Key: "enabled", Message: "enabled is required"}})
		return
	}
	if body.ExpectedVersion == "" {
		writeRouterInvalid(w, []ConfigIssue{{Key: "expected_version", Message: "expected_version is required"}})
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, ok := s.readConfigForRouterWrite(w, r, store, body.ExpectedVersion)
	if !ok {
		return
	}
	value := strconv.FormatBool(*body.Enabled)
	candidate, err := webfiles.SetTableKey(file.Raw, "[router]", "proxy", value)
	if errors.Is(err, webfiles.ErrTableNotFound) {
		candidate, _, err = webfiles.RewriteTable(r.Context(), func(context.Context, []byte) error { return nil },
			file.Raw, "[router]", "[router]\nproxy = "+value+"\n")
	}
	if err != nil {
		s.logger.Error("web: set router proxy", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := providerChecker(store)(r.Context(), candidate); err != nil {
		s.writeRouterSaveError(w, "save router proxy", err)
		return
	}
	if !s.writeRouterCandidate(w, r, store, file, candidate) {
		return
	}
	s.auditSave("router.proxy", providerConfigPath(file, s.providerHome()), "", []string{"router.proxy"})
	writeJSON(w, http.StatusOK, routerProxyResponse{
		Enabled:         *body.Enabled,
		BaseURL:         "http://" + s.Addr() + RouterProxyPrefix,
		Version:         webfiles.ConfigVersion(candidate),
		RestartRequired: s.applyLiveRestartRequired(r.Context(), store, candidate),
	})
}

// readConfigForRouterWrite reads config.toml under the write lock and checks
// expected_version (409 version_conflict on a mismatch).
func (s *Server) readConfigForRouterWrite(w http.ResponseWriter, r *http.Request, store ConfigStore, expected string) (ConfigFile, bool) {
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config before router save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return ConfigFile{}, false
	}
	if !file.Exists {
		writeAPIError(w, http.StatusConflict, "config_missing", "config.toml does not exist")
		return ConfigFile{}, false
	}
	if file.Version != expected {
		writeAPIError(w, http.StatusConflict, "version_conflict", "config.toml changed since it was loaded")
		return ConfigFile{}, false
	}
	return file, true
}

// writeRouterCandidate writes a checked candidate when it differs from the
// file.
func (s *Server) writeRouterCandidate(w http.ResponseWriter, r *http.Request, store ConfigStore, file ConfigFile, candidate []byte) bool {
	if string(candidate) == string(file.Raw) {
		return true
	}
	if err := store.ConfigWrite(r.Context(), candidate); err != nil {
		s.logger.Error("web: write config for router save", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return false
	}
	return true
}

// finishRouterWrite writes the candidate, audits it, applies it live, and
// answers {"version","restart_required"}.
func (s *Server) finishRouterWrite(w http.ResponseWriter, r *http.Request, store ConfigStore, file ConfigFile, candidate []byte, kind, name string, changed []string) {
	if !s.writeRouterCandidate(w, r, store, file, candidate) {
		return
	}
	if len(changed) > 0 {
		s.auditSave(kind, providerConfigPath(file, s.providerHome()), name, changed)
	}
	writeJSON(w, http.StatusOK, routerSaveResponse{
		Version:         webfiles.ConfigVersion(candidate),
		RestartRequired: s.applyLiveRestartRequired(r.Context(), store, candidate),
	})
}

// writeRouterSaveError maps a checker rejection to 422 invalid.
func (s *Server) writeRouterSaveError(w http.ResponseWriter, operation string, err error) {
	var validation *providerValidationError
	if errors.As(err, &validation) {
		writeRouterInvalid(w, validation.issues)
		return
	}
	s.logger.Error("web: "+operation, "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// writeRouterInvalid answers 422 invalid with one detail per issue
// (API-RTR-007): path and message, plus the file and key of the shared
// config issue shape.
func writeRouterInvalid(w http.ResponseWriter, issues []ConfigIssue) {
	details := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		details = append(details, map[string]any{
			"path":    issue.Key,
			"key":     issue.Key,
			"file":    issue.File,
			"message": issue.Message,
		})
	}
	writeAPIErrorDetails(w, http.StatusUnprocessableEntity, "invalid", "config.toml is not valid", details)
}

// renderComboTable renders one [combos.<name>] table.
func renderComboTable(name, description string, modelList []string) string {
	var b strings.Builder
	b.WriteString("[combos." + name + "]\n")
	writeProviderString(&b, "description", description)
	quoted := make([]string, 0, len(modelList))
	for _, model := range modelList {
		quoted = append(quoted, providerTOMLString(model))
	}
	b.WriteString("models = [" + strings.Join(quoted, ", ") + "]\n")
	return b.String()
}

type routerKeysResponse struct {
	Keys []routerdb.Key `json:"keys"`
}

func (s *Server) handleListRouterKeys(w http.ResponseWriter, r *http.Request) {
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	keys, err := port.RouterKeys(r.Context())
	if err != nil {
		s.writeRouterError(w, "router keys", err)
		return
	}
	if keys == nil {
		keys = []routerdb.Key{}
	}
	writeJSON(w, http.StatusOK, routerKeysResponse{Keys: keys})
}

func (s *Server) handleCreateRouterKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSONObject(w, r, routerBodyLimit, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > routerKeyNameMax || strings.ContainsAny(name, "\r\n") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "name is required, one line, at most 64 characters")
		return
	}
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	created, err := port.CreateRouterKey(r.Context(), name)
	if err != nil {
		s.writeRouterError(w, "create router key", err)
		return
	}
	s.auditSave("router.key.create", "", created.ID, nil)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleRevokeRouterKey(w http.ResponseWriter, r *http.Request) {
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	id := r.PathValue("id")
	if err := port.RevokeRouterKey(r.Context(), id); err != nil {
		if errors.Is(err, routerdb.ErrKeyNotFound) || errors.Is(err, core.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "proxy key not found")
			return
		}
		s.writeRouterError(w, "revoke router key", err)
		return
	}
	s.auditSave("router.key.revoke", "", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClaudeAgentStatus(w http.ResponseWriter, r *http.Request) {
	port := s.routerPortOrError(w)
	if port == nil {
		return
	}
	status, err := port.ClaudeAgentStatus(r.Context())
	if err != nil {
		s.writeRouterError(w, "claude agent status", err)
		return
	}
	if status.ConfiguredAgents == nil {
		status.ConfiguredAgents = []string{}
	}
	writeJSON(w, http.StatusOK, status)
}

// writeRouterError maps a RouterBackend error to the API error envelope.
func (s *Server) writeRouterError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, ErrRouterUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "router_unavailable", "the router is not available")
	case errors.Is(err, ErrRouterUnknownProvider):
		writeAPIError(w, http.StatusNotFound, "not_found", "provider is not configured")
	case errors.Is(err, ErrRouterUnknownAddress):
		writeAPIError(w, http.StatusNotFound, "not_found", "unknown provider or combo")
	default:
		s.logger.Error("web: "+operation, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// isLiveStore reports whether store applies a written config to the running
// daemon.
func isLiveStore(store ConfigStore) bool {
	_, ok := store.(LiveConfigStore)
	return ok
}

// applyLiveRestartRequired applies a written candidate and reports whether a
// restart is still needed: the apply failed or is not supported, or another
// pending change still needs one (API-RTR-006).
func (s *Server) applyLiveRestartRequired(ctx context.Context, store ConfigStore, candidate []byte) bool {
	if !s.applyLiveConfig(ctx, store, candidate) {
		return true
	}
	return webfiles.ConfigVersion(candidate) != store.ConfigLoadedVersion()
}

func queryFlag(r *http.Request, name string) bool {
	switch r.URL.Query().Get(name) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// parseModelAddress splits "provider/model" or the legacy "provider:model"
// at the first "/" or ":", like router.ParseAddress.
func parseModelAddress(value string) (provider, model string, ok bool) {
	i := strings.IndexAny(value, "/:")
	if i <= 0 || i == len(value)-1 {
		return "", "", false
	}
	return value[:i], value[i+1:], true
}

// sameAddress reports whether two model values name the same target.
func sameAddress(a, b string) bool {
	pa, ma, okA := parseModelAddress(a)
	pb, mb, okB := parseModelAddress(b)
	return okA && okB && pa == pb && ma == mb
}

// agentDirectProviders returns the providers an agent names itself: the
// legacy provider, a model address (not a combo), and fallback entries.
func (d providerDocument) agentDirectProviders(table map[string]any) map[string]bool {
	out := map[string]bool{}
	legacy := mapProviderString(table, "provider")
	model := mapProviderString(table, "model")
	switch {
	case legacy != "":
		out[legacy] = true
	case d.Combos[model] != nil:
	default:
		if provider, _, ok := parseModelAddress(model); ok {
			out[provider] = true
		}
	}
	for _, entry := range mapProviderStrings(table, "fallback") {
		if provider, _, ok := parseModelAddress(entry); ok {
			out[provider] = true
		} else if entry != "" {
			out[entry] = true
		}
	}
	return out
}

// combosUsingProvider returns the combos that list a provider, sorted.
func (d providerDocument) combosUsingProvider(name string) []string {
	var out []string
	for _, combo := range sortedMapKeys(d.Combos) {
		for _, address := range mapProviderStrings(d.Combos[combo], "models") {
			if provider, _, ok := parseModelAddress(address); ok && provider == name {
				out = append(out, combo)
				break
			}
		}
	}
	return out
}

// agentsDirectlyUsingProvider returns the agents that name a provider
// themselves, sorted.
func (d providerDocument) agentsDirectlyUsingProvider(name string) []string {
	var out []string
	for _, agent := range sortedMapKeys(d.Agents) {
		if d.agentDirectProviders(d.Agents[agent])[name] {
			out = append(out, agent)
		}
	}
	return out
}

// agentsUsingProvider returns the agents that use a provider directly or
// through their combo, sorted (GET /v1/router used_by).
func (d providerDocument) agentsUsingProvider(name string) []string {
	out := []string{}
	combos := map[string]bool{}
	for _, combo := range d.combosUsingProvider(name) {
		combos[combo] = true
	}
	for _, agent := range sortedMapKeys(d.Agents) {
		table := d.Agents[agent]
		model := mapProviderString(table, "model")
		if d.agentDirectProviders(table)[name] || (mapProviderString(table, "provider") == "" && combos[model]) {
			out = append(out, agent)
		}
	}
	return out
}

// embeddingUsesProvider reports whether knowledge.embedding_provider names
// the provider.
func (d providerDocument) embeddingUsesProvider(name string) bool {
	value, _ := d.Knowledge["embedding_provider"].(string)
	return value == name
}

// providerUsedBy is the used_by list of GET /v1/providers: "agents.<name>",
// "combos.<name>", and "knowledge.embedding_provider".
func (d providerDocument) providerUsedBy(name string) []string {
	out := []string{}
	for _, agent := range d.agentsDirectlyUsingProvider(name) {
		out = append(out, "agents."+agent)
	}
	for _, combo := range d.combosUsingProvider(name) {
		out = append(out, "combos."+combo)
	}
	if d.embeddingUsesProvider(name) {
		out = append(out, "knowledge.embedding_provider")
	}
	return out
}

// providerInUse returns the 409 in_use message of a provider delete, or ""
// when nothing uses it (API-RTR-006).
func (d providerDocument) providerInUse(name string) string {
	var parts []string
	if agents := d.agentsDirectlyUsingProvider(name); len(agents) > 0 {
		parts = append(parts, "agents: "+strings.Join(agents, ", "))
	}
	if combos := d.combosUsingProvider(name); len(combos) > 0 {
		parts = append(parts, "combos: "+strings.Join(combos, ", "))
	}
	if d.embeddingUsesProvider(name) {
		parts = append(parts, "knowledge.embedding_provider")
	}
	if len(parts) == 0 {
		return ""
	}
	return "used by " + strings.Join(parts, "; ")
}

// comboUsers returns the agents whose model is the combo, sorted.
func (d providerDocument) comboUsers(name string) []string {
	out := []string{}
	for _, agent := range sortedMapKeys(d.Agents) {
		table := d.Agents[agent]
		if mapProviderString(table, "provider") == "" && mapProviderString(table, "model") == name {
			out = append(out, agent)
		}
	}
	return out
}

// modelUsers returns the agents and combos that use a model address.
func (d providerDocument) modelUsers(address string) RouterModelUsers {
	users := RouterModelUsers{Agents: []string{}, Combos: []string{}}
	for _, agent := range sortedMapKeys(d.Agents) {
		table := d.Agents[agent]
		legacy := mapProviderString(table, "provider")
		model := mapProviderString(table, "model")
		uses := false
		if legacy != "" {
			uses = sameAddress(legacy+"/"+model, address)
		} else if d.Combos[model] == nil {
			uses = sameAddress(model, address)
		}
		for _, entry := range mapProviderStrings(table, "fallback") {
			uses = uses || sameAddress(entry, address)
		}
		if uses {
			users.Agents = append(users.Agents, agent)
		}
	}
	for _, combo := range sortedMapKeys(d.Combos) {
		for _, entry := range mapProviderStrings(d.Combos[combo], "models") {
			if sameAddress(entry, address) {
				users.Combos = append(users.Combos, combo)
				break
			}
		}
	}
	return users
}

// mapProviderStrings returns a string array value of a TOML table.
func mapProviderStrings(table map[string]any, key string) []string {
	raw, ok := table[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
