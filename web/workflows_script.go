package web

// This file holds the Script Parse web routes of spec #44 (API-SP-002..005):
// Test, Generate, Convert and Suggestions, behind the existing workflow port.
// The body limit of test and generate is raised because of pasted samples; the
// other routes use the workflow body limit.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// workflowScriptBodyLimit is the largest accepted body of the script test and
// generate routes (API-SP-002, API-SP-003): a pasted sample can be large, so it
// is raised above workflowBodyLimit.
const workflowScriptBodyLimit = 4 << 20

// registerWorkflowScriptRoutes registers the Script Parse routes (API-SP-002..005).
func (s *Server) registerWorkflowScriptRoutes() {
	s.Handle("POST /v1/workflows/{id}/script/test", http.HandlerFunc(s.handleWorkflowScriptTest))
	s.Handle("POST /v1/workflows/{id}/script/generate", http.HandlerFunc(s.handleWorkflowScriptGenerate))
	s.Handle("POST /v1/workflows/{id}/script/convert", http.HandlerFunc(s.handleWorkflowScriptConvert))
	s.Handle("GET /v1/workflows/{id}/script/suggestions", http.HandlerFunc(s.handleWorkflowScriptSuggestions))
}

// scriptNodeBody is the body of the test and generate routes (API-SP-002,
// API-SP-003). Input and Expected are RawMessage so an absent value is told from
// an explicit null; the expected output is an object.
type scriptNodeBody struct {
	Node     workflow.Node   `json:"node"`
	Input    json.RawMessage `json:"input"`
	Run      int             `json:"run"`
	Expected json.RawMessage `json:"expected"`
}

// scriptConvertBody is the body of the convert route (API-SP-004).
type scriptConvertBody struct {
	NodeID string `json:"node_id"`
}

// handleWorkflowScriptTest serves API-SP-002 (REQ-SP-015).
func (s *Server) handleWorkflowScriptTest(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body scriptNodeBody
	if !decodeJSONObject(w, r, workflowScriptBodyLimit, &body) {
		return
	}
	input, hasInput := decodeScriptValue(body.Input)
	result, err := backend.TestScript(r.Context(), r.PathValue("id"), body.Node, input, hasInput, body.Run)
	if err != nil {
		s.writeScriptError(w, "workflow script test", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleWorkflowScriptGenerate serves API-SP-003 (REQ-SP-016).
func (s *Server) handleWorkflowScriptGenerate(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body scriptNodeBody
	if !decodeJSONObject(w, r, workflowScriptBodyLimit, &body) {
		return
	}
	input, hasInput := decodeScriptValue(body.Input)
	result, err := backend.GenerateScript(r.Context(), r.PathValue("id"), body.Node, input, hasInput, body.Run, decodeScriptObject(body.Expected))
	if err != nil {
		s.writeScriptError(w, "workflow script generate", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleWorkflowScriptConvert serves API-SP-004 (REQ-SP-017).
func (s *Server) handleWorkflowScriptConvert(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	var body scriptConvertBody
	if !decodeJSONObject(w, r, workflowBodyLimit, &body) {
		return
	}
	result, err := backend.ConvertNode(r.Context(), r.PathValue("id"), body.NodeID)
	if err != nil {
		s.writeScriptError(w, "workflow script convert", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleWorkflowScriptSuggestions serves API-SP-005 (REQ-SP-018).
func (s *Server) handleWorkflowScriptSuggestions(w http.ResponseWriter, r *http.Request) {
	backend := s.workflowsOrError(w)
	if backend == nil {
		return
	}
	suggestions, err := backend.ScriptSuggestions(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeScriptError(w, "workflow script suggestions", err)
		return
	}
	if suggestions == nil {
		suggestions = []workflow.Suggestion{}
	}
	writeJSON(w, http.StatusOK, struct {
		Suggestions []workflow.Suggestion `json:"suggestions"`
	}{Suggestions: suggestions})
}

// decodeScriptValue decodes the optional sample input of test and generate
// (API-SP-002, API-SP-003). It reports whether the client sent a value, so an
// absent body field is told from an explicit null.
func decodeScriptValue(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return v, true
}

// decodeScriptObject decodes the optional expected output of generate
// (API-SP-003); an absent or null field yields nil.
func decodeScriptObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// writeScriptError maps the Script Parse service errors to the contract codes
// (API-SP-002..004). Everything else goes through the shared workflow mapper.
func (s *Server) writeScriptError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, workflow.ErrNoSample):
		writeAPIError(w, http.StatusNotFound, "no_sample", "no sample input for this node")
	case errors.Is(err, workflow.ErrNotScriptNode), errors.Is(err, workflow.ErrNotConvertible):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, workflow.ErrGuardianUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "guardian_unavailable", "the guardian is not available")
	case errors.Is(err, workflow.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "not found")
	default:
		s.writeWorkflowError(w, operation, err)
	}
}
