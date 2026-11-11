package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/secretreq"
)

var secretRequestSpec = json.RawMessage(`{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":128,"description":"Secret name, an env var name such as GITHUB_TOKEN: letters, digits and _, not starting with a digit."},` +
	`"description":{"type":"string","minLength":1,"maxLength":500,"description":"Why it is needed and where the owner gets it, shown on the card."},` +
	`"link":{"type":"string","maxLength":500,"description":"Optional http(s) page where the owner can create or copy the secret."}},` +
	`"required":["name","description"],"additionalProperties":false}`)

type secretRequestInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Link        string `json:"link"`
}

// secretRequestOutput keeps the contract key order: status, name, reason.
// It never carries the value (spec 53 API-SEC-001).
type secretRequestOutput struct {
	Status string `json:"status"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

type secretRequestTool struct{}

// secretRequestGuidance is the head-prompt note shown when secret_request is
// offered (spec 53 REQ-SEC-008).
const secretRequestGuidance = "When a workflow, MCP server or tool needs a credential (API key, token, password) that is not among the owner's secret names, call secret_request instead of asking the owner to paste it in chat. " +
	"Never ask for a secret value in plain chat, and never repeat a value the owner pastes. " +
	"Reuse an existing secret name; ask to replace one only when the owner wants to rotate it."

// NewSecretRequest creates the secret_request tool.
func NewSecretRequest() core.Tool { return &secretRequestTool{} }

func (t *secretRequestTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name: "secret_request",
		Description: "Asks the owner to type a secret (API key, token, password) into a secure input card in the web chat and waits for it. " +
			"The value is saved straight into the owner's secrets (Settings > Secrets) under name and is never shown to you: the result is only {\"status\":\"saved\"|\"cancelled\"|\"expired\",\"name\":...}. " +
			"Use the name afterwards, for example in an MCP server's ${NAME} or a workflow. " +
			"Prefer a name that already exists; if the name already has a value the card warns the owner that saving replaces it, so ask for an existing name only to rotate it. " +
			"Deleting secrets is not possible here. Works only in a web chat; on Telegram ask the owner to add it on the web.",
		InputSchema: secretRequestSpec,
	}
}

func (t *secretRequestTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *secretRequestTool) PromptGuidance() string { return secretRequestGuidance }

func (t *secretRequestTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in secretRequestInput
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Link = strings.TrimSpace(in.Link)
	if err := secretreq.Validate(in.Name, in.Description, in.Link); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Secrets == nil {
		return errorOutput(core.ErrSecretNoWeb.Error())
	}
	outcome, err := tc.Secrets.RequestSecret(ctx, core.SecretRequest{
		SessionID:   tc.SessionID,
		TurnID:      tc.TurnID,
		CallID:      tc.CallID,
		AgentName:   tc.AgentName,
		Name:        in.Name,
		Description: in.Description,
		Link:        in.Link,
	})
	if err != nil {
		return errorOutput(err.Error())
	}
	out := secretRequestOutput{Status: outcome.Status, Name: in.Name}
	switch outcome.Status {
	case core.SecretStatusSaved:
	case core.SecretStatusCancelled, core.SecretStatusExpired, core.SecretStatusUnavailable:
		out.Reason = outcome.Reason
	default:
		out.Status = core.SecretStatusUnavailable
		out.Reason = outcome.Reason
	}
	data, err := json.Marshal(out)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(string(data))
}
