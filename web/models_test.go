package web

import (
	"net/http"
	"strings"
	"testing"
)

const modelsTestConfig = `version = 1

[providers.deepseek]
kind = "openai_compat"
base_url = "https://api.deepseek.com/v1"

[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.com/v1"

[agents.coder]
provider = "deepseek"
model = "deepseek-flash"
`

func serveModels(t *testing.T, s *Server, target string, cookie *http.Cookie) *httptestResponse {
	t.Helper()
	rr := serveAuth(s, settingsRequest(http.MethodGet, target, "", cookie, ""))
	return &httptestResponse{code: rr.Code, body: rr.Body.Bytes()}
}

// TestModelsDeepSeekCatalog covers REQ-MDL-020: the DeepSeek provider exposes
// the catalog and the effort list.
func TestModelsDeepSeekCatalog(t *testing.T) {
	store := newTestConfigStore(t, modelsTestConfig, 0o600, ConfigSettings{})
	s, cookie, _ := newConfigTestServer(t, store)

	response := serveModels(t, s, "/v1/models?provider=deepseek", cookie)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	var body modelsResponse
	response.json(t, &body)

	ids := make([]string, 0, len(body.Models))
	for _, model := range body.Models {
		ids = append(ids, model.ID)
	}
	want := []string{"deepseek-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", ids, want)
	}
	flash := body.Models[0]
	if flash.Name == "" || flash.Description == "" || flash.ContextWindow != 1048576 {
		t.Fatalf("deepseek-flash = %+v", flash)
	}
	if flash.Thinking != "deepseek" || flash.DefaultEffort != "high" || flash.Legacy {
		t.Fatalf("deepseek-flash thinking fields = %+v", flash)
	}
	chat := body.Models[2]
	if !chat.Legacy || chat.Replacement != "deepseek-flash" {
		t.Fatalf("deepseek-chat = %+v", chat)
	}

	if len(body.Efforts) != 4 {
		t.Fatalf("efforts = %+v, want 4", body.Efforts)
	}
	if body.Efforts[0].Value != "off" || body.Efforts[0].Description == "" {
		t.Fatalf("first effort = %+v", body.Efforts[0])
	}

	// The spec names /api/models; both paths answer the same catalog.
	alias := serveModels(t, s, "/api/models?provider=deepseek", cookie)
	if alias.code != http.StatusOK {
		t.Fatalf("alias status = %d, body = %q", alias.code, alias.body)
	}
}

// TestModelsEmptyCatalogAndUnknownProvider covers the empty list for a provider
// with no catalog and the 404 for an unknown provider.
func TestModelsEmptyCatalogAndUnknownProvider(t *testing.T) {
	store := newTestConfigStore(t, modelsTestConfig, 0o600, ConfigSettings{})
	s, cookie, _ := newConfigTestServer(t, store)

	response := serveModels(t, s, "/v1/models?provider=openai", cookie)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	if strings.Contains(string(response.body), `"models":null`) {
		t.Fatalf("models is null: %s", response.body)
	}
	var body modelsResponse
	response.json(t, &body)
	if body.Models == nil || len(body.Models) != 0 {
		t.Fatalf("models = %+v, want an empty list", body.Models)
	}

	missing := serveModels(t, s, "/v1/models?provider=nope", cookie)
	if missing.code != http.StatusNotFound {
		t.Fatalf("unknown provider status = %d, body = %q", missing.code, missing.body)
	}
	envelope := decodeError(t, missing)
	if envelope.Error.Code != "not_found" {
		t.Fatalf("unknown provider error = %+v", envelope.Error)
	}
}
