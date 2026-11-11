package permission

import (
	"encoding/json"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/pattern"
)

// EffectivePolicy returns the latest approval/policy, or the default.
func EffectivePolicy(events []core.SessionEvent, def core.Policy) core.Policy {
	policy := def
	if policy == "" {
		policy = core.DefaultPolicy
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
	return p == core.PolicyTrust || p == core.PolicyAsk || p == core.PolicyNever || p == core.PolicyAuto
}

// Grant is a session permission matching a whole tool.
type Grant struct {
	Tool string
}

// Grants reads approval/decided events with allowed_session that refer to a tool call.
// Each matching event grants the whole tool, regardless of its arguments.
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
		grants = append(grants, Grant{Tool: call.Name})
	}
	return grants
}

// ChildPolicy picks the stricter policy.
func ChildPolicy(parent, child core.Policy) core.Policy {
	rank := func(p core.Policy) int {
		switch p {
		case core.PolicyNever:
			return 0
		case core.PolicyAsk:
			return 1
		case core.PolicyAuto:
			return 2
		case core.PolicyTrust:
			return 3
		}
		return 1
	}
	if rank(child) < rank(parent) {
		return child
	}
	return parent
}

// MergeDeny merges the parent and child deny lists.
func MergeDeny(parent, child []pattern.Pattern) []pattern.Pattern {
	out := make([]pattern.Pattern, 0, len(parent)+len(child))
	out = append(out, parent...)
	out = append(out, child...)
	return out
}
