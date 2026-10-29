package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

// Events adalah satu-satunya penulis event turn, dengan payload v:1.
type Events struct {
	Store     core.SessionStore
	SessionID string
}

// Append menulis draft dan memastikan setiap payload punya v = 1.
func (e *Events) Append(ctx context.Context, drafts ...core.EventDraft) ([]core.SessionEvent, error) {
	out := make([]core.EventDraft, 0, len(drafts))
	for _, draft := range drafts {
		copy := draft
		if copy.Payload != nil {
			payload, err := withVersion(copy.Payload)
			if err != nil {
				return nil, err
			}
			copy.Payload = payload
		}
		out = append(out, copy)
	}
	return e.Store.Append(ctx, e.SessionID, out)
}

func withVersion(payload any) (any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("payload event bukan object: %w", err)
	}
	value["v"] = core.PayloadVersion
	return value, nil
}
