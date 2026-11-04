package main

// The Telegram side of workflows (design §Wiring, API-WF-022): the owner notice
// for a pending decision, the plain owner message of the message node, and the
// chat command and button callbacks. cmd/lana wires them into the channel in
// daemon.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/channels/telegram"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// workflowNoticeTimeout bounds the send of a workflow heads-up, so a slow
// Telegram call never holds the approval path (design §Wiring).
const workflowNoticeTimeout = 15 * time.Second

// workflowNoticeArgumentMax caps the tool argument shown in a workflow
// heads-up.
const workflowNoticeArgumentMax = 200

// telegramWorkflowNotifier is the workflow.Notifier cmd/lana installs on the
// service. It sends the owner notices and the message node through the
// Telegram channel's owner private chats.
type telegramWorkflowNotifier struct{ ch *telegram.Channel }

// Notify sends one owner notice with decision buttons when it has a decision
// (REQ-WF-005, REQ-WF-008).
func (n telegramWorkflowNotifier) Notify(ctx context.Context, notice workflow.Notice) {
	if n.ch == nil {
		return
	}
	if err := n.ch.SendOwnerButtons(ctx, noticeText(notice), workflowButtons(notice)); err != nil {
		slog.Warn("workflow: telegram notice", "workflow", notice.WorkflowID, "error", err)
	}
}

// SendOwner sends the plain text of a message node (REQ-WF-005).
func (n telegramWorkflowNotifier) SendOwner(ctx context.Context, text string) error {
	if n.ch == nil {
		return errors.New("telegram is not connected")
	}
	return n.ch.SendOwnerButtons(ctx, text, nil)
}

// noticeText joins the non-empty title and body of a notice.
func noticeText(n workflow.Notice) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(n.Title) != "" {
		parts = append(parts, n.Title)
	}
	if strings.TrimSpace(n.Text) != "" {
		parts = append(parts, n.Text)
	}
	return strings.Join(parts, "\n\n")
}

// workflowButtons builds the inline keyboard of a notice: no decision or no
// choices means no keyboard, known choices get their label, and the buttons are
// laid out two per row (design §Wiring).
func workflowButtons(n workflow.Notice) [][]telegram.Button {
	if n.DecisionID == "" || len(n.Choices) == 0 {
		return nil
	}
	buttons := make([]telegram.Button, 0, len(n.Choices))
	for _, choice := range n.Choices {
		label, ok := workflowChoiceLabel(choice)
		if !ok {
			continue
		}
		buttons = append(buttons, telegram.Button{
			Text: label,
			Data: "w:" + n.DecisionID + ":" + choice,
		})
	}
	if len(buttons) == 0 {
		return nil
	}
	rows := make([][]telegram.Button, 0, (len(buttons)+1)/2)
	for i := 0; i < len(buttons); i += 2 {
		end := i + 2
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, buttons[i:end])
	}
	return rows
}

// workflowChoiceLabel maps a decision choice to its button label.
func workflowChoiceLabel(choice string) (string, bool) {
	switch choice {
	case "apply":
		return "Apply and resume", true
	case "run_only":
		return "This run only", true
	case "skip":
		return "Skip", true
	case "stop":
		return "Stop run", true
	case "approve":
		return "Approve", true
	case "deny":
		return "Deny", true
	default:
		return "", false
	}
}

// workflowChoiceWords is the past-tense result line of an answered decision.
func workflowChoiceWords(choice string) string {
	switch choice {
	case "apply":
		return "applied, resuming"
	case "run_only":
		return "retrying this run"
	case "skip":
		return "skipped"
	case "stop":
		return "stopped"
	case "approve":
		return "approved"
	case "deny":
		return "denied"
	default:
		return choice
	}
}

// workflowHooks builds the Telegram hooks of the workflow service
// (API-WF-022): a chat command starts a run, and a "w:..." button answers a
// decision.
func workflowHooks(rt *lana.Runtime) telegram.WorkflowHooks {
	return telegram.WorkflowHooks{
		Command: func(ctx context.Context, text string) (string, bool) {
			result, ok := rt.Workflows().Command(ctx, "telegram", text)
			return result.Reply, ok
		},
		Callback: func(ctx context.Context, decisionID, choice string) (string, error) {
			overview, err := rt.Workflows().Overview(ctx)
			if err != nil {
				return "", err
			}
			for _, attention := range overview.Attention {
				if attention.ID != decisionID {
					continue
				}
				run, err := rt.Workflows().Answer(ctx, attention.WorkflowID, decisionID, choice)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s · Run #%d: %s", attention.WorkflowName, run.Number, workflowChoiceWords(choice)), nil
			}
			return "", errors.New("this decision is no longer open")
		},
	}
}

// telegramWorkflowNotice builds the heads-up sent for a channel "workflow"
// approval: the owner answers on the web, so Telegram only names the workflow,
// the tool and a short argument (design §Wiring, REQ-WF-009).
func telegramWorkflowNotice(ch *telegram.Channel) func(ctx context.Context, req core.ApprovalRequest, info core.SessionInfo) {
	return func(ctx context.Context, req core.ApprovalRequest, info core.SessionInfo) {
		if ch == nil {
			return
		}
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workflowNoticeTimeout)
		defer cancel()
		text := fmt.Sprintf("Workflow %s wants to run %s: %s\nAnswer it on the web: Workflows → Needs attention.",
			info.Address, req.ToolName, shortApprovalArgument(req.ToolName, req.Input))
		if err := ch.SendOwnerButtons(sendCtx, text, nil); err != nil {
			slog.Warn("workflow: telegram notice", "error", err)
		}
	}
}

// shortApprovalArgument is the tool argument shown in a workflow heads-up,
// capped at workflowNoticeArgumentMax characters.
func shortApprovalArgument(tool string, input json.RawMessage) string {
	arg := toolfmt.PrimaryArg(tool, input)
	if arg == "" {
		arg = strings.TrimSpace(string(input))
	}
	if runes := []rune(arg); len(runes) > workflowNoticeArgumentMax {
		return string(runes[:workflowNoticeArgumentMax])
	}
	return arg
}
