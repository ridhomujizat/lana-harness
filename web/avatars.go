package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/avatar"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
)

// avatarBodyLimit is the largest accepted avatar request body: an avatar
// document escaped as JSON with room to spare.
const avatarBodyLimit = 64 << 10

// maxAvatarSeed keeps seeds in a range every JSON client reads exactly.
const maxAvatarSeed = 1<<31 - 1

// maxAvatarPromptRunes is the longest accepted generation prompt (API-PXA-002).
const maxAvatarPromptRunes = 500

// Avatar generation modes (API-PXA-002).
const (
	// avatarModeRandom picks new parts from a seed, locally.
	avatarModeRandom = "random"
	// avatarModeRender renders the body's parts, locally.
	avatarModeRender = "render"
)

// AvatarBackend is an optional Backend extension that draws agent avatars
// through the router (spec 62, API-PXA-002). Without it, the ai modes fall
// back to the local generator.
type AvatarBackend interface {
	// GenerateAvatar draws an avatar for agent with mode (avatar.ModeAIParts
	// or avatar.ModeAIDraw), model ("" is the agent's background chain),
	// prompt, seed, and the agent colour. It returns avatar.ErrUnknownModel
	// for a refused model (Claude Code and Antigravity models are served,
	// spec 78 REQ-RPR-008)
	// and ErrUnknownAgent when the running daemon does not know the agent yet.
	GenerateAvatar(ctx context.Context, agent, mode, model, prompt string, seed int64, color string) (avatar.Result, error)
}

func (s *Server) registerAvatarRoutes() {
	s.Handle("GET /v1/avatars/catalog", http.HandlerFunc(s.handleAvatarCatalog))
	s.Handle("POST /v1/agents/{name}/avatar/generate", http.HandlerFunc(s.handleGenerateAvatar))
	s.Handle("PUT /v1/agents/{name}/avatar", http.HandlerFunc(s.handlePutAvatar))
	s.Handle("DELETE /v1/agents/{name}/avatar", http.HandlerFunc(s.handleDeleteAvatar))
	s.Handle("GET /v1/agents/{name}/avatar.svg", http.HandlerFunc(s.handleAvatarImage))
}

type generateAvatarRequest struct {
	Mode   string        `json:"mode"`
	Seed   *int64        `json:"seed"`
	Parts  *avatar.Parts `json:"parts"`
	Color  *string       `json:"color"`
	Prompt string        `json:"prompt"`
	Model  *string       `json:"model"`
}

type generateAvatarResponse struct {
	Avatar avatar.Document `json:"avatar"`
	Seed   int64           `json:"seed"`
	Model  string          `json:"model"`
	Source string          `json:"source"`
}

type putAvatarRequest struct {
	Avatar avatar.Document `json:"avatar"`
	Color  *string         `json:"color"`
}

type avatarResponse struct {
	Name            string          `json:"name"`
	Avatar          avatar.Document `json:"avatar"`
	AvatarSource    string          `json:"avatar_source"`
	Color           string          `json:"color"`
	RestartRequired bool            `json:"restart_required"`
}

// handleAvatarCatalog implements GET /v1/avatars/catalog (API-PXA-001).
func (s *Server) handleAvatarCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, avatar.Catalog())
}

// avatarAgent reads config.toml and returns the agent's record. It writes the
// error and returns ok false when the store fails or the name is not an
// [agents.<name>] table (a team has no avatar).
func (s *Server) avatarAgent(w http.ResponseWriter, r *http.Request, store ConfigStore, name string) (ConfigFile, agentRecord, bool) {
	if !agenthome.ValidName(name) {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return ConfigFile{}, agentRecord{}, false
	}
	file, err := store.ConfigFile(r.Context())
	if err != nil {
		s.logger.Error("web: read config for avatar", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return ConfigFile{}, agentRecord{}, false
	}
	if !file.Exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return ConfigFile{}, agentRecord{}, false
	}
	tables, err := parseAgentTables(file.Raw)
	if err != nil {
		s.logger.Error("web: parse config for avatar", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return ConfigFile{}, agentRecord{}, false
	}
	table, ok := tables[name]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return ConfigFile{}, agentRecord{}, false
	}
	return file, agentRecordFromMap(table), true
}

// handleGenerateAvatar implements POST /v1/agents/{name}/avatar/generate
// (API-PXA-002). Nothing is stored.
func (s *Server) handleGenerateAvatar(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	_, record, ok := s.avatarAgent(w, r, store, name)
	if !ok {
		return
	}
	var body generateAvatarRequest
	if !decodeJSONObject(w, r, avatarBodyLimit, &body) {
		return
	}
	if utf8.RuneCountInString(body.Prompt) > maxAvatarPromptRunes {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "prompt must be at most 500 characters")
		return
	}
	// The colour the sprite renders with: the body colour when given, else the
	// agent's colour (API-PXA-002).
	color := core.ResolveAgentColor(name, record.Color)
	if body.Color != nil {
		if !core.ValidAgentColor(*body.Color) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", core.AgentColorMessage)
			return
		}
		color = core.ResolveAgentColor(name, *body.Color)
	}
	seed := randomAvatarSeed()
	if body.Seed != nil {
		if *body.Seed < 1 || *body.Seed > maxAvatarSeed {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "seed must be between 1 and 2147483647")
			return
		}
		seed = *body.Seed
	}

	switch body.Mode {
	case avatarModeRandom:
		result, err := localPartsResult(seed, color)
		if err != nil {
			s.writeBackendError(w, "generate avatar", err)
			return
		}
		writeJSON(w, http.StatusOK, newGenerateAvatarResponse(result))
		return
	case avatarModeRender:
		if body.Parts == nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "render requires parts")
			return
		}
		doc, err := avatar.Normalize(avatar.Document{Kind: avatar.KindParts, Parts: body.Parts}, color)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, generateAvatarResponse{Avatar: doc, Seed: seed, Source: avatar.SourceLocal})
		return
	case avatar.ModeAIParts, avatar.ModeAIDraw:
		// Handled below.
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "mode must be random, render, ai_parts, or ai_draw")
		return
	}

	model := ""
	if body.Model != nil {
		model = strings.TrimSpace(*body.Model)
	}
	if port, ok := s.backend.(AvatarBackend); ok {
		generated, err := port.GenerateAvatar(r.Context(), name, body.Mode, model, body.Prompt, seed, color)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, newGenerateAvatarResponse(generated))
			return
		case errors.Is(err, avatar.ErrUnknownModel):
			writeAPIError(w, http.StatusBadRequest, "invalid_model", "unknown model or combo")
			return
		case errors.Is(err, ErrUnknownAgent):
			// The agent is in config.toml but the running daemon has not
			// loaded it yet: draw locally.
		default:
			s.writeBackendError(w, "generate avatar", err)
			return
		}
	}
	result, err := localPartsResult(seed, color)
	if err != nil {
		s.writeBackendError(w, "generate avatar", err)
		return
	}
	writeJSON(w, http.StatusOK, newGenerateAvatarResponse(result))
}

// newGenerateAvatarResponse converts a generation result to the wire shape.
func newGenerateAvatarResponse(result avatar.Result) generateAvatarResponse {
	return generateAvatarResponse{
		Avatar: result.Avatar,
		Seed:   result.Seed,
		Model:  result.Model,
		Source: result.Source,
	}
}

// localPartsResult renders the local fallback of a generation: RandomParts(seed)
// with the agent colour, source local.
func localPartsResult(seed int64, color string) (avatar.Result, error) {
	parts := avatar.RandomParts(seed)
	if color != "" {
		parts.Color = color
	}
	sprite, err := avatar.Render(parts)
	if err != nil {
		return avatar.Result{}, err
	}
	return avatar.Result{
		Avatar: avatar.Document{Kind: avatar.KindParts, Parts: &parts, Sprite: sprite},
		Seed:   seed,
		Source: avatar.SourceLocal,
	}, nil
}

// handlePutAvatar implements PUT /v1/agents/{name}/avatar (API-PXA-003): it
// validates the avatar document, writes <agent home>/avatar.json, and writes
// the color key when the body carries one.
func (s *Server) handlePutAvatar(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	var body putAvatarRequest
	if !decodeJSONObject(w, r, avatarBodyLimit, &body) {
		return
	}
	if body.Color != nil && !core.ValidAgentColor(*body.Color) {
		writeAgentIssue(w, "agents."+name+".color", core.AgentColorMessage)
		return
	}
	home := s.agentHome()
	if home == "" {
		s.logger.Error("web: agent home is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	file, record, ok := s.avatarAgent(w, r, store, name)
	if !ok {
		return
	}
	// The colour the sprite renders with: the new colour when given, else the
	// agent's (API-PXA-003).
	color := core.ResolveAgentColor(name, record.Color)
	if body.Color != nil {
		color = core.ResolveAgentColor(name, *body.Color)
	}
	doc, err := avatar.Normalize(body.Avatar, color)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_avatar", err.Error())
		return
	}
	data, err := json.Marshal(doc)
	if err != nil {
		s.logger.Error("web: encode avatar", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if len(data) > avatar.MaxDocumentSize {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_avatar", "avatar document is too large")
		return
	}

	restart := false
	if body.Color != nil && *body.Color != record.Color {
		record.Color = *body.Color
		candidate, changed, err := webfiles.RewriteTable(r.Context(), agentChecker(store), file.Raw, "[agents."+name+"]", renderAgentTable(name, record))
		if err != nil {
			s.writeAgentSaveError(w, "save avatar color", err)
			return
		}
		if !bytes.Equal(candidate, file.Raw) {
			if err := store.ConfigWrite(r.Context(), candidate); err != nil {
				s.logger.Error("web: write config for avatar color", "error", err)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				return
			}
			s.auditSave("agent.save", agentConfigPath(file, home), name, changed)
			restart = !s.applyLiveConfig(r.Context(), store, candidate)
		}
	}
	path := agenthome.Paths(home, name).Avatar
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.logger.Error("web: create agent home for avatar", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if err := webfiles.AtomicWriteMode(path, data, 0o600); err != nil {
		s.logger.Error("web: write avatar", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSave("agent_avatar", path, name, nil)
	writeJSON(w, http.StatusOK, avatarResponse{
		Name:            name,
		Avatar:          doc,
		AvatarSource:    avatar.Saved,
		Color:           color,
		RestartRequired: restart,
	})
}

// handleDeleteAvatar implements DELETE /v1/agents/{name}/avatar
// (API-PXA-004): the agent goes back to its default avatar, seeded from the
// name. It is idempotent.
func (s *Server) handleDeleteAvatar(w http.ResponseWriter, r *http.Request) {
	store := s.configStoreOrError(w)
	if store == nil {
		return
	}
	name := r.PathValue("name")
	home := s.agentHome()
	if home == "" {
		s.logger.Error("web: agent home is not configured")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	_, record, ok := s.avatarAgent(w, r, store, name)
	if !ok {
		return
	}
	path := agenthome.Paths(home, name).Avatar
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Error("web: remove avatar", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	} else if err == nil {
		s.auditSave("agent_avatar.delete", path, name, nil)
	}
	color := core.ResolveAgentColor(name, record.Color)
	writeJSON(w, http.StatusOK, avatarResponse{
		Name:         name,
		Avatar:       avatar.DefaultFor(name, color),
		AvatarSource: avatar.Default,
		Color:        color,
	})
}

// agentAvatar returns the avatar an agent shows and its source: the saved
// avatar.json, normalized so a saved parts avatar follows the agent's current
// colour, or the default one seeded from the name when there is none or the
// file is not a valid avatar (REQ-PXA-009, API-PXA-005).
func (s *Server) agentAvatar(name, color string) (avatar.Document, string) {
	if doc, ok := s.savedAvatar(name, color); ok {
		return doc, avatar.Saved
	}
	return avatar.DefaultFor(name, color), avatar.Default
}

// savedAvatar returns the stored avatar of an agent, normalized, and ok false
// when there is none or the file is not a valid avatar.
func (s *Server) savedAvatar(name, color string) (avatar.Document, bool) {
	home := s.agentHome()
	if home == "" || !agenthome.ValidName(name) {
		return avatar.Document{}, false
	}
	path := agenthome.Paths(home, name).Avatar
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > avatar.MaxDocumentSize {
		return avatar.Document{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return avatar.Document{}, false
	}
	var doc avatar.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		s.logger.Debug("web: stored avatar is not valid", "agent", name, "error", err)
		return avatar.Document{}, false
	}
	normalized, err := avatar.Normalize(doc, color)
	if err != nil {
		s.logger.Debug("web: stored avatar is not valid", "agent", name, "error", err)
		return avatar.Document{}, false
	}
	return normalized, true
}

// handleAvatarImage implements GET /v1/agents/{name}/avatar.svg (API-PXA-006):
// frame 0 of the avatar an agent or team shows, with the computed outline. The
// ?view=full query adds the ground bar; any other view is the bust. The theme
// query is accepted and ignored.
func (s *Server) handleAvatarImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	color, ok := s.avatarImageColor(r.Context(), name)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "agent is not configured")
		return
	}
	doc, _ := s.agentAvatar(name, color)
	body := []byte(avatar.SVG(doc.Sprite, r.URL.Query().Get("view")))
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, max-age=60")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// avatarImageColor returns the resolved color of a configured agent or team,
// ok false for an unknown name.
func (s *Server) avatarImageColor(ctx context.Context, name string) (string, bool) {
	if !agenthome.ValidName(name) {
		return "", false
	}
	if s.configStore != nil {
		if file, err := s.configStore.ConfigFile(ctx); err == nil && file.Exists {
			if tables, err := parseAgentTables(file.Raw); err == nil {
				if table, ok := tables[name]; ok {
					return core.ResolveAgentColor(name, agentRecordFromMap(table).Color), true
				}
			}
		}
	}
	if s.backend != nil {
		for _, agent := range s.backend.Agents() {
			if agent.Name != name {
				continue
			}
			if agent.Kind == "team" {
				return "brand", true
			}
			return core.ResolveAgentColor(name, ""), true
		}
	}
	return "", false
}

// randomAvatarSeed returns a seed in 1..maxAvatarSeed.
func randomAvatarSeed() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 1
	}
	return int64(binary.LittleEndian.Uint64(b[:])%maxAvatarSeed) + 1
}
