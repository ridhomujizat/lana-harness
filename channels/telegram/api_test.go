package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestGetUpdatesPeekParams covers the read-only getUpdates request: offset 0,
// timeout 0, only message updates, and no confirming follow-up call.
func TestGetUpdatesPeekParams(t *testing.T) {
	const token = "123456:test-token"
	var mu sync.Mutex
	var calls []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/getUpdates" {
			t.Errorf("path = %q, want the getUpdates path", r.URL.Path)
		}
		var params map[string]any
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			t.Errorf("decode params: %v", err)
		}
		mu.Lock()
		calls = append(calls, params)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
	}))
	defer server.Close()

	updates, err := NewClient(server.URL, token).GetUpdatesPeek(context.Background())
	if err != nil {
		t.Fatalf("GetUpdatesPeek: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("updates = %+v, want empty", updates)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("getUpdates calls = %d, want exactly one (never confirm)", len(calls))
	}
	if got := calls[0]["offset"]; got != float64(0) {
		t.Fatalf("offset = %v, want 0", got)
	}
	if got := calls[0]["timeout"]; got != float64(0) {
		t.Fatalf("timeout = %v, want 0", got)
	}
	allowed, ok := calls[0]["allowed_updates"].([]any)
	if !ok || len(allowed) != 1 || allowed[0] != "message" {
		t.Fatalf("allowed_updates = %v, want [message]", calls[0]["allowed_updates"])
	}
}

// TestGetUpdatesPeekConflict covers AC-TGC-006: a 409 from getUpdates maps to
// ErrConflict and the error text never carries the token.
func TestGetUpdatesPeekConflict(t *testing.T) {
	const token = "123456:test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, token).GetUpdatesPeek(context.Background())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked the token: %q", err)
	}
}
