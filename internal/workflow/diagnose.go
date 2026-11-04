package workflow

// This file holds the guardian diagnosis prompt and the parsing of its answer
// (REQ-WF-008, design §"Guardian prompts"). The engine builds a prompt from the
// failing node, sends it to the workflow guardian and expects one JSON decision
// back. It also holds the transient-error test used by the retry policy.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// diagnosis limits keep the prompt small (design §"Guardian prompts").
const (
	diagnoseInputMaxBytes = 4 << 10 // rendered input, 4 KiB
	diagnoseLogsMaxBytes  = 2 << 10 // last logs, 2 KiB
	diagnoseRecentRuns    = 5       // last runs shown
)

// DiagnosisInput is everything the guardian sees when it diagnoses one node
// failure (REQ-WF-008).
type DiagnosisInput struct {
	Workflow Workflow // the workflow being run
	Node     Node     // the node that failed
	Input    any      // the rendered input of the failing attempt
	Error    string   // the last error text
	Logs     []string // the node logs so far
	Recent   []Run    // the last runs of the workflow
	Lock     []string // the locked tool names of the workflow
}

// diagnosisActions is the set of actions the guardian may answer with.
var diagnosisActions = []string{"retry_fix", "skip", "fallback", "ask", "stop"}

// BuildDiagnosisPrompt renders the diagnosis prompt of the design: the role, the
// workflow goal, the node, the input and error, the logs, the recent runs, the
// locked tools, the five actions, and the exact reply shape (REQ-WF-008).
func BuildDiagnosisPrompt(in DiagnosisInput) string {
	var b strings.Builder
	b.WriteString("You are the guardian of a lana workflow. A node failed during a run and you must choose what to do next.\n\n")

	goal := in.Workflow.Description
	if strings.TrimSpace(goal) == "" {
		goal = in.Workflow.Name
	}
	b.WriteString("Workflow goal: " + goal + "\n\n")

	b.WriteString("Node:\n" + compactJSON(in.Node) + "\n\n")

	input := compactJSON(in.Input)
	if len(input) > diagnoseInputMaxBytes {
		input = input[:diagnoseInputMaxBytes] + "…"
	}
	b.WriteString("Node input:\n" + input + "\n\n")

	b.WriteString("Error:\n" + in.Error + "\n\n")

	logs := strings.Join(in.Logs, "\n")
	if len(logs) > diagnoseLogsMaxBytes {
		logs = logs[len(logs)-diagnoseLogsMaxBytes:]
	}
	if logs == "" {
		logs = "(none)"
	}
	b.WriteString("Last logs:\n" + logs + "\n\n")

	b.WriteString("Recent runs:\n")
	recent := in.Recent
	if len(recent) > diagnoseRecentRuns {
		recent = recent[:diagnoseRecentRuns]
	}
	if len(recent) == 0 {
		b.WriteString("(none)\n")
	}
	for _, r := range recent {
		b.WriteString(fmt.Sprintf("#%d %s %s\n", r.Number, r.Status, r.Error))
	}
	b.WriteString("\n")

	lock := strings.Join(in.Lock, ", ")
	if lock == "" {
		lock = "(none)"
	}
	b.WriteString("Locked tools: " + lock + "\n\n")

	b.WriteString("Choose one action:\n")
	b.WriteString("- retry_fix: a setting you may change, this run only\n")
	b.WriteString("- skip: one loop item or optional node\n")
	b.WriteString("- fallback: the node has a fallback\n")
	b.WriteString("- ask: the fix changes the saved workflow, needs a tool outside the lock, costs more, or you are unsure\n")
	b.WriteString("- stop: nothing safe is left\n\n")

	b.WriteString("Reply with ONE JSON object only: " +
		"{\"action\":\"retry_fix|skip|fallback|ask|stop\",\"reason\":\"...\"," +
		"\"fix\":{\"<param>\":<value>}|null,\"persistent\":true|false,\"confidence\":0..1}")
	return b.String()
}

// Diagnosis is a parsed guardian decision (REQ-WF-008).
type Diagnosis struct {
	Action     string
	Reason     string
	Fix        map[string]any
	Persistent bool
	Confidence float64
}

// ParseDiagnosis extracts the guardian's JSON decision from text and validates
// the action (REQ-WF-008). An unknown or missing action is an error.
func ParseDiagnosis(text string) (Diagnosis, error) {
	v, err := ExtractJSON(text)
	if err != nil {
		return Diagnosis{}, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return Diagnosis{}, errors.New("workflow: diagnosis is not a JSON object")
	}
	action, _ := m["action"].(string)
	if !validDiagnosisAction(action) {
		return Diagnosis{}, fmt.Errorf("workflow: unknown diagnosis action %q", action)
	}
	d := Diagnosis{Action: action}
	d.Reason, _ = m["reason"].(string)
	if fix, ok := m["fix"].(map[string]any); ok {
		d.Fix = fix
	}
	if p, ok := m["persistent"].(bool); ok {
		d.Persistent = p
	}
	if c, ok := asFloat(m["confidence"]); ok {
		d.Confidence = c
	}
	return d, nil
}

// validDiagnosisAction reports whether action is one of the five choices.
func validDiagnosisAction(action string) bool {
	for _, a := range diagnosisActions {
		if a == action {
			return true
		}
	}
	return false
}

// transient5xx matches an HTTP 5xx status code as a whole word.
var transient5xx = regexp.MustCompile(`\b5\d\d\b`)

// transientMarkers are substrings that mark an error as retryable
// (design §"Error ladder").
var transientMarkers = []string{
	"timeout", "timed out", "429", "rate limit", "too many requests",
	"connection reset", "connection refused", "eof", "no such host", "temporar",
}

// IsTransient reports whether err is worth retrying (REQ-WF-008): a context
// deadline, a timeout or rate-limit wording, a 5xx status, or a network error. A
// context cancellation is never transient.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range transientMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return transient5xx.MatchString(text)
}

// compactJSON marshals v to a compact JSON string; a marshal failure yields "".
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
