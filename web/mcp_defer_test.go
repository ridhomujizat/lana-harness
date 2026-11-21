package web

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// API-DTL-002: a dashboard save keeps a stored defer, because the editor does
// not carry the key.
func TestMCPPutKeepsDefer(t *testing.T) {
	raw := "[mcp.docs]\ncommand = \"docs-server\"\ndefer = false\n"
	store := newTestConfigStore(t, raw, 0o600, ConfigSettings{})
	s, cookie, csrf, _ := newMCPTestServer(t, store, nil)

	body := `{"type":"command","command":"docs-server","expected_version":"` + shaHex([]byte(raw)) + `"}`
	response := serveMCP(t, s, http.MethodPut, "/v1/mcp/docs", body, cookie, csrf)
	if response.code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.code, response.body)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "defer = false") {
		t.Fatalf("stored defer was dropped:\n%s", data)
	}
}
