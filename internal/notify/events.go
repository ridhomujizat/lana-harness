package notify

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Length limits of the banner text (spec #42).
const (
	// ReplyMax caps the reply excerpt.
	ReplyMax = 140
	// ApprovalMax caps "<tool>: <argument>".
	ApprovalMax = 120
	// QuestionMax caps the question text.
	QuestionMax = 140
	// ErrorMax caps a workflow error excerpt.
	ErrorMax = 140
	// AgentMax caps a workbench agent banner body.
	AgentMax = 140
	// titlePartMax caps a session title or workflow name inside a title.
	titlePartMax = 60
)

// NewChatTitle stands in for a session without a title.
const NewChatTitle = "New chat"

// SessionURL is the dashboard hash of a session page.
func SessionURL(sessionID string) string {
	return "#/sessions/" + sessionID
}

// ChatURL is the dashboard hash of a chat page: "#/chats/{name}" where name is
// the agent name. An empty agent falls back to the chats list.
func ChatURL(agent string) string {
	if strings.TrimSpace(agent) == "" {
		return "#/chats"
	}
	return "#/chats/" + url.PathEscape(agent)
}

// WorkflowURL is the dashboard hash of a workflow page.
func WorkflowURL(workflowID string) string {
	return "#/workflows/" + workflowID
}

// WorkbenchURL is the dashboard hash of the Workbench page.
const WorkbenchURL = "#/workbench"

// WorkflowRunURL is the dashboard hash of one workflow run. number is the run
// number; 0 falls back to the run id.
func WorkflowRunURL(workflowID, runID string, number int) string {
	ref := runID
	if number > 0 {
		ref = strconv.Itoa(number)
	}
	if ref == "" {
		return WorkflowURL(workflowID)
	}
	return WorkflowURL(workflowID) + "/runs/" + ref
}

// WorkflowThread is the thread of every banner of one workflow run.
func WorkflowThread(workflowID, runID string) string {
	if runID == "" {
		return "workflow:" + workflowID
	}
	return "workflow:" + workflowID + ":" + runID
}

// Reply is the event of an agent reply: "<agent> · <session title>" over the
// excerpt of the final assistant text. It opens the agent's chat page. turnID
// dedupes the publish.
func Reply(agent, sessionTitle, sessionID, turnID, text string) Event {
	title := strings.TrimSpace(sessionTitle)
	if title == "" {
		title = NewChatTitle
	}
	return Event{
		Kind:      KindReply,
		SessionID: sessionID,
		Agent:     agent,
		Title:     joinTitle(agentLabel(agent), OneLine(title, titlePartMax)),
		Body:      Excerpt(text, ReplyMax),
		URL:       ChatURL(agent),
		Thread:    sessionID,
		Key:       "reply:" + sessionID + ":" + turnID,
	}
}

// ApprovalTarget says where an approval banner leads: the root session of a
// chat, or the workflow of a workflow session.
type ApprovalTarget struct {
	// SessionID is the session that asked; RootID its root session.
	SessionID, RootID string
	// WorkflowID is set for a session on channel "workflow".
	WorkflowID string
}

func (t ApprovalTarget) url() string {
	if t.WorkflowID != "" {
		return WorkflowURL(t.WorkflowID)
	}
	return SessionURL(t.root())
}

func (t ApprovalTarget) thread() string {
	if t.WorkflowID != "" {
		return WorkflowThread(t.WorkflowID, "")
	}
	return t.root()
}

func (t ApprovalTarget) root() string {
	if t.RootID != "" {
		return t.RootID
	}
	return t.SessionID
}

// ApprovalAsked is the event of a tool approval answerable through the web
// broker: "Approval needed · <agent>" over "<tool>: <argument>". It is time
// sensitive and carries the broker id for the Allow once and Reject actions.
//
// When stepUp is true the approval is a Block-tier hardline match that the
// macOS banner must not allow: ApprovalID is left empty so the app posts a
// plain banner without actions, and the body ends with " Open Lana to
// confirm." The suffix is appended after the ApprovalMax cut so it is never
// cut, and the key still dedupes on the approval id (REQ-HLF-007, spec 63).
func ApprovalAsked(approvalID, agent, tool, argument string, target ApprovalTarget, stepUp bool) Event {
	body := tool
	if arg := strings.TrimSpace(argument); arg != "" {
		body = tool + ": " + arg
	}
	body = OneLine(body, ApprovalMax)
	if stepUp {
		body += " Open Lana to confirm."
	}
	e := Event{
		Kind:          KindApprovalAsked,
		SessionID:     target.SessionID,
		Agent:         agent,
		Title:         joinTitle("Approval needed", agentLabel(agent)),
		Body:          body,
		URL:           target.url(),
		WorkflowID:    target.WorkflowID,
		TimeSensitive: true,
		Thread:        target.thread(),
		Key:           "approval_asked:" + approvalID,
	}
	if !stepUp {
		e.ApprovalID = approvalID
	}
	return e
}

// ApprovalDecided closes the banner of approvalID.
func ApprovalDecided(approvalID, outcome string, target ApprovalTarget) Event {
	return Event{
		Kind:       KindApprovalDecided,
		SessionID:  target.SessionID,
		Title:      "Approval closed",
		Body:       outcome,
		URL:        target.url(),
		ApprovalID: approvalID,
		WorkflowID: target.WorkflowID,
		Outcome:    outcome,
		Silent:     true,
		Thread:     target.thread(),
		Key:        "approval_decided:" + approvalID,
	}
}

// QuestionAsked is the event of an ask_user question answerable on the web:
// "Question from <agent>" over the first question. more counts the other
// questions of the same call.
func QuestionAsked(questionID, agent, question string, more int, target ApprovalTarget) Event {
	body := OneLine(question, QuestionMax)
	if more > 0 {
		suffix := fmt.Sprintf(" (+%d more)", more)
		body = OneLine(question, QuestionMax-len([]rune(suffix))) + suffix
	}
	return Event{
		Kind:       KindQuestionAsked,
		SessionID:  target.SessionID,
		Agent:      agent,
		Title:      "Question from " + agentLabel(agent),
		Body:       body,
		URL:        target.url(),
		QuestionID: questionID,
		WorkflowID: target.WorkflowID,
		Thread:     target.thread(),
		Key:        "question_asked:" + questionID,
	}
}

// QuestionAnswered closes the banner of questionID.
func QuestionAnswered(questionID string, target ApprovalTarget) Event {
	return Event{
		Kind:       KindQuestionAnswered,
		SessionID:  target.SessionID,
		Title:      "Question closed",
		URL:        target.url(),
		QuestionID: questionID,
		WorkflowID: target.WorkflowID,
		Silent:     true,
		Thread:     target.thread(),
		Key:        "question_answered:" + questionID,
	}
}

// AgentFinished is the banner of a workbench coding agent that finished a
// turn: "<name> finished" over the session title. occurrence makes the key
// unique per transition, so the bus does not dedupe a real repeat.
func AgentFinished(name, body, sessionID, occurrence string) Event {
	return agentEvent(KindAgentFinished, name, "finished", body, sessionID, occurrence)
}

// AgentNeedsYou is the banner of a workbench coding agent that waits for the
// owner: "<name> needs you" over the session title.
func AgentNeedsYou(name, body, sessionID, occurrence string) Event {
	return agentEvent(KindAgentNeedsYou, name, "needs you", body, sessionID, occurrence)
}

// agentEvent builds a workbench agent banner. The title is "<name> <verb>",
// name being the readable agent label, and the body the session title excerpt.
func agentEvent(kind Kind, name, verb, body, sessionID, occurrence string) Event {
	name = OneLine(name, titlePartMax)
	return Event{
		Kind:      kind,
		SessionID: sessionID,
		Agent:     name,
		Title:     strings.TrimSpace(name + " " + verb),
		Body:      OneLine(body, AgentMax),
		URL:       WorkbenchURL,
		Thread:    sessionID,
		Key:       string(kind) + ":" + sessionID + ":" + occurrence,
	}
}

// WorkflowRun is the part of a workflow run the workflow events need.
type WorkflowRun struct {
	WorkflowID, WorkflowName string
	RunID                    string
	Number                   int
	Trigger                  string
	Summary                  string
	Error                    string
	StartedAt, EndedAt       int64 // Unix ms
}

func (r WorkflowRun) name() string {
	name := strings.TrimSpace(r.WorkflowName)
	if name == "" {
		name = r.WorkflowID
	}
	return OneLine(name, titlePartMax)
}

func (r WorkflowRun) event(kind Kind, title, body string) Event {
	return Event{
		Kind:          kind,
		Title:         title,
		Body:          body,
		URL:           WorkflowRunURL(r.WorkflowID, r.RunID, r.Number),
		WorkflowID:    r.WorkflowID,
		WorkflowRunID: r.RunID,
		Thread:        WorkflowThread(r.WorkflowID, r.RunID),
		Key:           string(kind) + ":" + r.RunID,
	}
}

// WorkflowStarted is "Workflow started · <name>" over the trigger. It is
// silent: no sound.
func WorkflowStarted(r WorkflowRun) Event {
	e := r.event(KindWorkflowStarted, joinTitle("Workflow started", r.name()), triggerLabel(r.Trigger))
	e.Silent = true
	return e
}

// WorkflowFinished is "Workflow done · <name> · <duration>" over the run
// summary, when the run has one.
func WorkflowFinished(r WorkflowRun) Event {
	title := joinTitle("Workflow done", r.name())
	if d := runDuration(r); d != "" {
		title = joinTitle(title, d)
	}
	body := Excerpt(r.Summary, ReplyMax)
	if body == "" {
		body = "Run #" + strconv.Itoa(r.Number) + " succeeded"
	}
	return r.event(KindWorkflowFinished, title, body)
}

// WorkflowFailed is "Workflow failed · <name>" over the error excerpt.
func WorkflowFailed(r WorkflowRun) Event {
	body := OneLine(r.Error, ErrorMax)
	if body == "" {
		body = "Run #" + strconv.Itoa(r.Number) + " failed"
	}
	return r.event(KindWorkflowFailed, joinTitle("Workflow failed", r.name()), body)
}

// WorkflowWaiting is a run that waits for the owner's decision: "Approval
// needed · <name>" over the reason. approvalID is set when the decision is an
// approve or deny choice the Allow once and Reject actions can answer.
func WorkflowWaiting(r WorkflowRun, decisionID, reason, approvalID string) Event {
	body := OneLine(reason, ApprovalMax)
	if body == "" {
		body = "Run #" + strconv.Itoa(r.Number) + " is waiting for you"
	}
	e := r.event(KindWorkflowWaiting, joinTitle("Approval needed", r.name()), body)
	e.ApprovalID = approvalID
	e.TimeSensitive = true
	e.Key = "workflow_waiting:" + decisionID
	return e
}

// WorkflowDecided closes the banner of a workflow decision.
func WorkflowDecided(r WorkflowRun, decisionID, approvalID, outcome string) Event {
	e := r.event(KindApprovalDecided, "Approval closed", outcome)
	e.ApprovalID = approvalID
	e.Outcome = outcome
	e.Silent = true
	e.Key = "workflow_decided:" + decisionID
	return e
}

// WorkflowApprovalID is the local approval id of a workflow decision:
// "wf:<workflow id>:<decision id>".
func WorkflowApprovalID(workflowID, decisionID string) string {
	return "wf:" + workflowID + ":" + decisionID
}

// ParseWorkflowApprovalID splits an id made by WorkflowApprovalID.
func ParseWorkflowApprovalID(id string) (workflowID, decisionID string, ok bool) {
	rest, found := strings.CutPrefix(id, "wf:")
	if !found {
		return "", "", false
	}
	workflowID, decisionID, ok = strings.Cut(rest, ":")
	if !ok || workflowID == "" || decisionID == "" {
		return "", "", false
	}
	return workflowID, decisionID, true
}

// FormatDuration renders a run length: "45s", "3m 05s", "1h 02m".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return "<1s"
	}
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return strconv.Itoa(s) + "s"
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh %02dm", s/3600, (s%3600)/60)
	}
}

func runDuration(r WorkflowRun) string {
	if r.StartedAt <= 0 || r.EndedAt < r.StartedAt {
		return ""
	}
	return FormatDuration(time.Duration(r.EndedAt-r.StartedAt) * time.Millisecond)
}

// triggerLabel names the trigger of a run for the started banner.
func triggerLabel(trigger string) string {
	switch t := strings.TrimSpace(trigger); t {
	case "":
		return "Started"
	case "schedule":
		return "Trigger: schedule"
	case "manual":
		return "Trigger: manual"
	case "calendar":
		return "Trigger: calendar"
	case "webhook":
		return "Trigger: webhook"
	default:
		return "Trigger: " + OneLine(t, 60)
	}
}

func agentLabel(agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "lana"
	}
	return OneLine(agent, titlePartMax)
}

func joinTitle(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}
