package permission

import (
	"encoding/json"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// EffectivePolicy mengembalikan approval/policy terakhir atau default.
func EffectivePolicy(events []core.SessionEvent, def core.Policy) core.Policy {
	policy := def
	if policy == "" {
		policy = core.PolicyAsk
	}
	for _, event := range events {
		if event.Kind != core.KindApprovalPolicy {
			continue
		}
		var payload core.ApprovalPolicy
		if json.Unmarshal(event.Payload, &payload) == nil && validPolicy(payload.Policy) {
			policy = payload.Policy
		}
	}
	return policy
}

func validPolicy(p core.Policy) bool {
	return p == core.PolicyTrust || p == core.PolicyAsk || p == core.PolicyNever
}

// Grant adalah izin sesi yang cocok dengan tool dan argumen literal.
type Grant struct {
	Tool   string
	Arg    string
	HasArg bool
}

// Grants membaca approval/decided allowed_session yang merujuk tool/call.
func Grants(events []core.SessionEvent) []Grant {
	calls := map[string]core.ToolCallPayload{}
	for _, event := range events {
		if event.Kind != core.KindToolCall {
			continue
		}
		var payload core.ToolCallPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			calls[payload.CallID] = payload
		}
	}
	var grants []Grant
	for _, event := range events {
		if event.Kind != core.KindApprovalDecided {
			continue
		}
		var decided core.ApprovalDecided
		if json.Unmarshal(event.Payload, &decided) != nil || decided.Outcome != core.OutcomeAllowedSession {
			continue
		}
		call, ok := calls[decided.CallID]
		if !ok {
			continue
		}
		grant := Grant{Tool: call.Name}
		if value, ok := primaryArg(call.Input); ok {
			grant.Arg = value
			grant.HasArg = true
		}
		grants = append(grants, grant)
	}
	return grants
}

func primaryArg(input json.RawMessage) (string, bool) {
	var value map[string]any
	if json.Unmarshal(input, &value) != nil {
		return "", false
	}
	for _, key := range []string{"command", "path", "url", "agent"} {
		if raw, ok := value[key]; ok {
			if s, ok := raw.(string); ok {
				return s, true
			}
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// ChildPolicy memilih policy paling ketat.
func ChildPolicy(parent, child core.Policy) core.Policy {
	rank := func(p core.Policy) int {
		switch p {
		case core.PolicyNever:
			return 0
		case core.PolicyAsk:
			return 1
		case core.PolicyTrust:
			return 2
		}
		return 1
	}
	if rank(child) < rank(parent) {
		return child
	}
	return parent
}

// MergeDeny menggabungkan daftar deny induk dan child.
func MergeDeny(parent, child []pattern.Pattern) []pattern.Pattern {
	out := make([]pattern.Pattern, 0, len(parent)+len(child))
	out = append(out, parent...)
	out = append(out, child...)
	return out
}
