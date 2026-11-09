package web

// Tests for the Script Parse web routes (spec #44, API-SP-002..005) and the
// revert answer of API-SP-006. They use the fake workflow backend of
// workflows_test.go.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// scriptRouteNode is a minimal script_parse node for the request bodies.
func scriptRouteNode() workflow.Node {
	return workflow.Node{
		ID: "parse", Type: "script_parse", Name: "Parse",
		Params: map[string]any{
			"input":         "{{ input.text }}",
			"goal":          "extract the title",
			"output_schema": map[string]any{"title": "string"},
		},
	}
}

// scriptRequestBody builds a test/generate body; a nil input is left out so the
// handler sees an absent field.
func scriptRequestBody(t *testing.T, node workflow.Node, input any, run int, extra map[string]any) string {
	t.Helper()
	m := map[string]any{"node": node, "run": run}
	if input != nil {
		m["input"] = input
	}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// API-SP-002: the test route answers 200 with the result and forwards the node,
// input and run.
func TestWorkflowScriptTestRoute(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.testScript = workflow.TestResult{
		OK: true, Output: map[string]any{"title": "hello"}, Problems: []workflow.ScriptProblem{},
		Logs: []string{"l1"}, DurationMS: 4, Sample: workflow.Sample{Source: "request"},
	}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	body := scriptRequestBody(t, scriptRouteNode(), "hello", 3, nil)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/test", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got workflow.TestResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !got.OK || got.DurationMS != 4 || got.Sample.Source != "request" {
		t.Fatalf("result = %+v", got)
	}
	out, _ := got.Output.(map[string]any)
	if out["title"] != "hello" {
		t.Fatalf("output = %v", got.Output)
	}
	if !backend.testScriptHasInput || backend.testScriptInput != "hello" || backend.testScriptRun != 3 {
		t.Fatalf("call = %v %v %d", backend.testScriptHasInput, backend.testScriptInput, backend.testScriptRun)
	}
	if backend.testScriptNode.ID != "parse" || backend.testScriptNode.Type != "script_parse" {
		t.Fatalf("node = %+v", backend.testScriptNode)
	}
}

// API-SP-002: an absent input is told from a value.
func TestWorkflowScriptTestAbsentInput(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.testScript = workflow.TestResult{Problems: []workflow.ScriptProblem{}, Logs: []string{}, Sample: workflow.Sample{Source: "run", Run: 2}}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	body := scriptRequestBody(t, scriptRouteNode(), nil, 0, nil)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/test", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if backend.testScriptHasInput {
		t.Fatalf("hasInput = true, want false")
	}
}

// API-SP-002: no sample is 404 no_sample and a non script_parse node is 422.
func TestWorkflowScriptTestErrors(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.testScriptErr = workflow.ErrNoSample
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	body := scriptRequestBody(t, scriptRouteNode(), nil, 0, nil)
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/test", body, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "no_sample")

	backend.mu.Lock()
	backend.testScriptErr = workflow.ErrNotScriptNode
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/test", body, cookie, csrf))
	assertAuthError(t, rr, http.StatusUnprocessableEntity, "validation_failed")
}

// API-SP-003: the generate route answers 200 and forwards the expected object;
// a missing guardian is 503.
func TestWorkflowScriptGenerateRoute(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.generateScript = workflow.GenerateResult{
		Feasible: true, Script: "function parse(input, ctx) { return {title: 'x'}; }",
		Output: map[string]any{"title": "x"}, Problems: []workflow.ScriptProblem{}, Tokens: 9,
	}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	body := scriptRequestBody(t, scriptRouteNode(), "hello", 1, map[string]any{"expected": map[string]any{"title": "x"}})
	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/generate", body, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got workflow.GenerateResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !got.Feasible || got.Script == "" || got.Tokens != 9 {
		t.Fatalf("result = %+v", got)
	}
	if backend.generateScriptExpected["title"] != "x" {
		t.Fatalf("expected = %+v", backend.generateScriptExpected)
	}

	backend.mu.Lock()
	backend.generateScriptErr = workflow.ErrGuardianUnavailable
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/generate", body, cookie, csrf))
	assertAuthError(t, rr, http.StatusServiceUnavailable, "guardian_unavailable")
}

// API-SP-004: the convert route answers 200 with the proposed node and forwards
// the node id; a non-convertible node is 422 and an unknown node is 404.
func TestWorkflowScriptConvertRoute(t *testing.T) {
	backend := newFakeWorkflowBackend()
	node := workflow.Node{ID: "write", Type: "script_parse", Name: "Write"}
	backend.convertResult = workflow.ConvertResult{Feasible: true, Node: &node, Checks: []workflow.ConvertCheck{}, Tokens: 5}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/convert", `{"node_id":"write"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var got workflow.ConvertResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if !got.Feasible || got.Node == nil || got.Node.ID != "write" {
		t.Fatalf("result = %+v", got)
	}
	if backend.convertNodeID != "write" {
		t.Fatalf("node id = %q", backend.convertNodeID)
	}

	backend.mu.Lock()
	backend.convertErr = workflow.ErrNotConvertible
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/convert", `{"node_id":"msg"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusUnprocessableEntity, "validation_failed")

	backend.mu.Lock()
	backend.convertErr = workflow.ErrNotFound
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/script/convert", `{"node_id":"ghost"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// API-SP-005: the suggestions route answers 200 with the list, never null.
func TestWorkflowScriptSuggestionsRoute(t *testing.T) {
	backend := newFakeWorkflowBackend()
	s, cookie, _ := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/workflows/w/script/suggestions", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "{\"suggestions\":[]}\n" {
		t.Fatalf("body = %q want an empty suggestions array", got)
	}

	backend.mu.Lock()
	backend.suggestions = []workflow.Suggestion{{NodeID: "a", Name: "A", Type: "agent", Runs: 3, AvgTokens: 600, EstTokens30d: 1800}}
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/workflows/w/script/suggestions", "", cookie, ""))
	var got struct {
		Suggestions []workflow.Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(got.Suggestions) != 1 || got.Suggestions[0].EstTokens30d != 1800 {
		t.Fatalf("suggestions = %+v", got.Suggestions)
	}
}

// API-SP-006: the revert choice returns the run; a closed decision is 409
// decision_resolved and a bad choice is 400 invalid_choice.
func TestWorkflowScriptRevertAnswer(t *testing.T) {
	backend := newFakeWorkflowBackend()
	backend.answerRun = workflow.Run{ID: "r_1", Number: 1, Status: workflow.RunSucceeded}
	s, cookie, csrf := newWorkflowTestServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/decisions/d_1", `{"choice":"revert"}`, cookie, csrf))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var run workflow.Run
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if run.ID != "r_1" {
		t.Fatalf("run = %+v", run)
	}

	backend.mu.Lock()
	backend.answerErr = workflow.ErrDecisionClosed
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/decisions/d_1", `{"choice":"revert"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusConflict, "decision_resolved")

	backend.mu.Lock()
	backend.answerErr = workflow.ErrInvalidChoice
	backend.mu.Unlock()
	rr = serveAuth(s, sessionTestRequest(http.MethodPost, "/v1/workflows/w/decisions/d_1", `{"choice":"revert"}`, cookie, csrf))
	assertAuthError(t, rr, http.StatusBadRequest, "invalid_choice")
}
