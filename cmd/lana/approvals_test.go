package main

import (
	"context"
	"errors"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestApprovalRouterRoutesByChannel(t *testing.T) {
	sessions := map[string]core.SessionInfo{
		"telegram-session": {ID: "telegram-session", Channel: "telegram"},
		"web-session":      {ID: "web-session", Channel: "web"},
		"cli-session":      {ID: "cli-session", Channel: "cli"},
	}
	router := &ApprovalRouter{
		sessionInfo: func(_ context.Context, sessionID string) (core.SessionInfo, error) {
			info, ok := sessions[sessionID]
			if !ok {
				return core.SessionInfo{}, errors.New("session not found")
			}
			return info, nil
		},
		telegram: func(_ context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
			if req.SessionID != "telegram-session" {
				t.Errorf("telegram route got session %q", req.SessionID)
			}
			return core.OutcomeAllowedOnce
		},
		web: func(_ context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
			if req.SessionID != "web-session" {
				t.Errorf("web route got session %q", req.SessionID)
			}
			return core.OutcomeAllowedSession
		},
	}

	tests := []struct {
		name      string
		sessionID string
		want      core.ApprovalOutcome
	}{
		{name: "telegram", sessionID: "telegram-session", want: core.OutcomeAllowedOnce},
		{name: "web", sessionID: "web-session", want: core.OutcomeAllowedSession},
		{name: "unknown channel", sessionID: "cli-session", want: core.OutcomeUnavailable},
		{name: "lookup failure", sessionID: "missing", want: core.OutcomeUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := router.Approve(context.Background(), core.ApprovalRequest{SessionID: tt.sessionID}); got != tt.want {
				t.Fatalf("outcome = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApprovalRouterAbsentChannel(t *testing.T) {
	router := &ApprovalRouter{
		sessionInfo: func(_ context.Context, sessionID string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: sessionID, Channel: sessionID}, nil
		},
	}
	for _, channel := range []string{"telegram", "web"} {
		t.Run("no "+channel, func(t *testing.T) {
			if got := router.Approve(context.Background(), core.ApprovalRequest{SessionID: channel}); got != core.OutcomeUnavailable {
				t.Fatalf("outcome = %q, want unavailable", got)
			}
		})
	}

	// A web-only daemon still routes web approvals; telegram stays unavailable.
	webOnly := &ApprovalRouter{
		sessionInfo: func(_ context.Context, sessionID string) (core.SessionInfo, error) {
			return core.SessionInfo{ID: sessionID, Channel: sessionID}, nil
		},
		web: func(context.Context, core.ApprovalRequest) core.ApprovalOutcome { return core.OutcomeAllowedOnce },
	}
	if got := webOnly.Approve(context.Background(), core.ApprovalRequest{SessionID: "web"}); got != core.OutcomeAllowedOnce {
		t.Fatalf("web-only outcome = %q", got)
	}
	if got := webOnly.Approve(context.Background(), core.ApprovalRequest{SessionID: "telegram"}); got != core.OutcomeUnavailable {
		t.Fatalf("web-only telegram outcome = %q", got)
	}
}
