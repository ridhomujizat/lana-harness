package tui

import (
	"context"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ridhomujizat/lana-harness/core"
)

// Approver menjembatani approval runtime ke TUI.
type Approver struct {
	program atomic.Pointer[tea.Program]
}

// NewApprover membuat Approver.
func NewApprover() *Approver { return &Approver{} }

// SetProgram memasang program yang menerima prompt approval.
func (a *Approver) SetProgram(p *tea.Program) { a.program.Store(p) }

// Ask mengirim approval ke TUI dan menunggu jawaban.
func (a *Approver) Ask(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
	program := a.program.Load()
	if program == nil {
		return core.OutcomeUnavailable
	}
	reply := make(chan core.ApprovalOutcome, 1)
	program.Send(approvalMsg{req: req, reply: reply})
	select {
	case out := <-reply:
		return out
	case <-ctx.Done():
		return core.OutcomeCancelled
	}
}
