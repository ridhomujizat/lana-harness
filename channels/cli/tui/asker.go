package tui

import (
	"context"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ridhomujizat/lana-harness/core"
)

// Asker bridges runtime questions to the TUI.
type Asker struct {
	program atomic.Pointer[tea.Program]
}

// NewAsker creates an Asker.
func NewAsker() *Asker { return &Asker{} }

// SetProgram installs the program that receives question prompts.
func (a *Asker) SetProgram(p *tea.Program) { a.program.Store(p) }

// Ask sends the question to the TUI and waits for the answer.
func (a *Asker) Ask(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome {
	program := a.program.Load()
	if program == nil {
		return questionOutcomeUnavailable(core.QuestionReasonNoResponder)
	}
	reply := make(chan core.QuestionOutcome, 1)
	program.Send(questionMsg{req: req, reply: reply})
	select {
	case out := <-reply:
		return out
	case <-ctx.Done():
		return questionOutcomeUnavailable(core.QuestionReasonCancelled)
	}
}

func questionOutcomeUnavailable(reason string) core.QuestionOutcome {
	return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: reason}
}
