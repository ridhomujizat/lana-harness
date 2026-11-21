package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// TestAnswerApprovalLineNonHardline covers the unchanged non-hardline prompt.
func TestAnswerApprovalLineNonHardline(t *testing.T) {
	var out bytes.Buffer
	got := answerApprovalLine(&out, strings.NewReader("a\n"), core.ApprovalRequest{ToolName: "bash"}, "1234")
	if got != core.OutcomeAllowedSession {
		t.Fatalf("outcome = %q, want allowed_session", got)
	}
	if !strings.Contains(out.String(), "approve bash [y/a/n]: ") {
		t.Fatalf("prompt = %q", out.String())
	}
}

// TestAnswerApprovalLineEscalate covers REQ-HLF-008: the rule and reason print
// first, the prompt drops the session key, and "a" is treated as "y" (once).
func TestAnswerApprovalLineEscalate(t *testing.T) {
	var out bytes.Buffer
	req := core.ApprovalRequest{
		ToolName: "bash",
		Hardline: &core.HardlineMatch{Rule: "HL-E-002", Tier: core.HardlineEscalate, Reason: "privilege change"},
	}
	got := answerApprovalLine(&out, strings.NewReader("a\n"), req, "1234")
	if got != core.OutcomeAllowedOnce {
		t.Fatalf("outcome = %q, want allowed_once", got)
	}
	text := out.String()
	if !strings.Contains(text, "HL-E-002 · privilege change") {
		t.Fatalf("rule and reason missing: %q", text)
	}
	if !strings.Contains(text, "approve bash [y/n]: ") {
		t.Fatalf("prompt = %q", text)
	}
}

// TestAnswerApprovalLineBlockCorrect covers REQ-HLF-007: "y" prints the typed
// challenge and the exact code returns allowed_verified.
func TestAnswerApprovalLineBlockCorrect(t *testing.T) {
	var out bytes.Buffer
	req := core.ApprovalRequest{
		ToolName: "bash",
		Hardline: &core.HardlineMatch{Rule: "HL-B-001", Tier: core.HardlineBlock, Reason: "recursive delete"},
	}
	got := answerApprovalLine(&out, strings.NewReader("y\n4321\n"), req, "4321")
	if got != core.OutcomeAllowedVerified {
		t.Fatalf("outcome = %q, want allowed_verified", got)
	}
	if !strings.Contains(out.String(), "type 4321 to run it: ") {
		t.Fatalf("challenge prompt missing: %q", out.String())
	}
}

// TestAnswerApprovalLineBlockWrong covers REQ-HLF-007: a wrong challenge rejects.
func TestAnswerApprovalLineBlockWrong(t *testing.T) {
	var out bytes.Buffer
	req := core.ApprovalRequest{
		ToolName: "bash",
		Hardline: &core.HardlineMatch{Rule: "HL-B-001", Tier: core.HardlineBlock, Reason: "recursive delete"},
	}
	got := answerApprovalLine(&out, strings.NewReader("y\n1111\n"), req, "4321")
	if got != core.OutcomeRejected {
		t.Fatalf("outcome = %q, want rejected", got)
	}
}
