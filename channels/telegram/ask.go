package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	// questionEditTimeout bounds an edit made after the request closed, as
	// approvals do.
	questionEditTimeout = 5 * time.Second
	// questionLabelLimit is the Telegram button text limit of API-ASK-006.
	questionLabelLimit = 60
	// questionArmedNone marks a request without an armed Something else
	// capture.
	questionArmedNone = -1
)

// question is the per-request state of one Telegram question message set.
// The chat worker handles callbacks and text, while Ask waits on reply; the
// mutex guards the state shared by both.
type question struct {
	mu       sync.Mutex
	token    string
	req      core.QuestionRequest
	chat     *chat
	reply    chan core.QuestionOutcome
	messages []int64
	answers  []core.QuestionAnswer
	answered []bool
	selected [][]int
	armed    int
	index    int
	closed   bool
	outcome  core.QuestionOutcome
}

// newQuestionToken returns 12 lowercase hex characters (API-ASK-006).
func newQuestionToken() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func newQuestion(token string, req core.QuestionRequest, w *chat) *question {
	n := len(req.Questions)
	selected := make([][]int, n)
	for i := range selected {
		selected[i] = []int{}
	}
	return &question{
		token:    token,
		req:      req,
		chat:     w,
		reply:    make(chan core.QuestionOutcome, 1),
		answers:  make([]core.QuestionAnswer, n),
		answered: make([]bool, n),
		selected: selected,
		armed:    questionArmedNone,
	}
}

// Ask asks the owner through one inline keyboard message per question
// (API-ASK-006). It is the runtime's question answerer; sessions without a
// bound chat are unavailable.
func (c *Channel) Ask(ctx context.Context, req core.QuestionRequest) core.QuestionOutcome {
	if err := ctx.Err(); err != nil {
		return questionContextOutcome(err)
	}
	w := c.chatForSession(req.SessionID)
	if w == nil {
		return questionUnavailable(core.QuestionReasonNoResponder)
	}
	token, err := newQuestionToken()
	if err != nil {
		return questionUnavailable(core.QuestionReasonNoResponder)
	}
	q := newQuestion(token, req, w)
	c.mu.Lock()
	c.questions[token] = q
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.questions[token] == q {
			delete(c.questions, token)
		}
		c.mu.Unlock()
	}()

	if !c.sendQuestion(ctx, q, 0) {
		return questionUnavailable(core.QuestionReasonNoResponder)
	}

	timeout := c.QuestionTimeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case outcome := <-q.reply:
		return outcome
	case <-timer.C:
		outcome, won := q.close(questionUnavailable(core.QuestionReasonTimeout))
		if won {
			c.editQuestionMessages(ctx, q, questionExpiredText)
		}
		return outcome
	case <-ctx.Done():
		outcome, won := q.close(questionContextOutcome(ctx.Err()))
		if won {
			c.editQuestionMessages(ctx, q, questionContextText(ctx.Err()))
		}
		return outcome
	}
}

// sendQuestion sends the message of one question. A send error ends the
// request as no_responder.
func (c *Channel) sendQuestion(ctx context.Context, q *question, index int) bool {
	q.mu.Lock()
	if q.closed || index < 0 || index >= len(q.req.Questions) {
		q.mu.Unlock()
		return true
	}
	payload := q.req.Questions[index]
	token := q.token
	chatID := q.chat.id
	selected := append([]int(nil), q.selected[index]...)
	q.mu.Unlock()

	text, keyboard := questionMessage(token, payload, index, len(q.req.Questions), selected)
	msgID, err := c.api.SendMessage(ctx, chatID, text, SendOptions{Keyboard: keyboard})
	if err != nil {
		c.apiErr("sendMessage", err)
		return false
	}

	q.mu.Lock()
	q.messages = append(q.messages, msgID)
	closed := q.closed
	outcome := q.outcome
	q.mu.Unlock()
	// A timeout or cancellation may close the request while the message is in
	// flight; its keyboard must not stay active.
	if closed {
		c.editQuestionMessageAt(ctx, chatID, msgID, questionOutcomeText(outcome), nil)
	}
	return true
}

// sendNextQuestion sends the next question after an answer. A send error ends
// the request as no_responder.
func (c *Channel) sendNextQuestion(ctx context.Context, q *question, index int) {
	if c.sendQuestion(ctx, q, index) {
		return
	}
	q.fail(questionUnavailable(core.QuestionReasonNoResponder))
}

// fail closes the request and wakes Ask without editing messages.
func (q *question) fail(outcome core.QuestionOutcome) {
	if _, won := q.close(outcome); !won {
		return
	}
	select {
	case q.reply <- outcome:
	default:
	}
}

// close records the first closing outcome. It returns the winning outcome and
// whether this call closed the request.
func (q *question) close(outcome core.QuestionOutcome) (core.QuestionOutcome, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return q.outcome, false
	}
	q.closed = true
	q.outcome = outcome
	return outcome, true
}

// submit closes the request once every question has an answer.
func (c *Channel) submitQuestion(q *question) {
	q.mu.Lock()
	if q.closed || !q.completeLocked() {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.outcome = core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: append([]core.QuestionAnswer(nil), q.answers...)}
	outcome := q.outcome
	q.mu.Unlock()
	select {
	case q.reply <- outcome:
	default:
	}
}

func (q *question) completeLocked() bool {
	for _, answered := range q.answered {
		if !answered {
			return false
		}
	}
	return true
}

// advanceLocked records an answer and moves to the next question. It must be
// called with the request mutex held; it returns the next index, or -1 when
// the request is complete.
func (q *question) advanceLocked(index int, answer core.QuestionAnswer) int {
	q.answers[index] = answer
	q.answered[index] = true
	q.armed = questionArmedNone
	if q.completeLocked() {
		return -1
	}
	q.index = index + 1
	return q.index
}

// continueQuestion sends the next question, or submits the request after the
// last one.
func (c *Channel) continueQuestion(ctx context.Context, q *question, next int) {
	if next < 0 {
		c.submitQuestion(q)
		return
	}
	c.sendNextQuestion(ctx, q, next)
}

// answerQuestion handles a press on a question button. data is
// "<tok>:<qi>:<act>[:<oi>]" after the "q:" prefix.
func (c *Channel) answerQuestion(ctx context.Context, q *CallbackQuery, data string, owner bool) {
	if !owner {
		c.answerCallback(ctx, q.ID, "only the owner can answer")
		return
	}
	token, index, act, option, ok := parseQuestionCallback(data)
	if !ok {
		c.answerCallback(ctx, q.ID, questionClosedText)
		return
	}
	c.mu.Lock()
	request := c.questions[token]
	c.mu.Unlock()
	if request == nil {
		c.answerCallback(ctx, q.ID, questionClosedText)
		return
	}

	request.mu.Lock()
	if request.closed || index != request.index || index < 0 || index >= len(request.req.Questions) {
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, questionClosedText)
		return
	}
	payload := request.req.Questions[index]
	if request.answered[index] {
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, questionClosedText)
		return
	}

	switch act {
	case "s":
		if payload.MultiSelect || option < 0 || option >= len(payload.Options) {
			request.mu.Unlock()
			c.answerCallback(ctx, q.ID, questionClosedText)
			return
		}
		answer := core.QuestionAnswer{Question: payload.Text, Selected: []string{payload.Options[option].Label}}
		next := request.advanceLocked(index, answer)
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, "")
		c.editQuestionMessage(ctx, request, index, questionAnswerText(answer), nil)
		c.continueQuestion(ctx, request, next)
	case "t":
		if !payload.MultiSelect || option < 0 || option >= len(payload.Options) {
			request.mu.Unlock()
			c.answerCallback(ctx, q.ID, questionClosedText)
			return
		}
		request.selected[index] = toggleOption(request.selected[index], option)
		selected := append([]int(nil), request.selected[index]...)
		request.mu.Unlock()
		text, keyboard := questionMessage(request.token, payload, index, len(request.req.Questions), selected)
		c.answerCallback(ctx, q.ID, "")
		c.editQuestionMessage(ctx, request, index, text, keyboard)
	case "d":
		if !payload.MultiSelect {
			request.mu.Unlock()
			c.answerCallback(ctx, q.ID, questionClosedText)
			return
		}
		labels := optionLabels(payload, request.selected[index])
		if len(labels) == 0 {
			request.mu.Unlock()
			c.answerCallback(ctx, q.ID, "Pick at least one option, or Skip.")
			return
		}
		answer := core.QuestionAnswer{Question: payload.Text, Selected: labels}
		next := request.advanceLocked(index, answer)
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, "")
		c.editQuestionMessage(ctx, request, index, questionAnswerText(answer), nil)
		c.continueQuestion(ctx, request, next)
	case "o":
		request.armed = index
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, "Type your answer in the next message.")
	case "k":
		answer := core.QuestionAnswer{Question: payload.Text, Skipped: true}
		next := request.advanceLocked(index, answer)
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, "")
		c.editQuestionMessage(ctx, request, index, questionSkippedText, nil)
		c.continueQuestion(ctx, request, next)
	default:
		request.mu.Unlock()
		c.answerCallback(ctx, q.ID, questionClosedText)
	}
}

// parseQuestionCallback splits "<tok>:<qi>:<act>[:<oi>]".
func parseQuestionCallback(data string) (token string, index int, act string, option int, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) < 3 {
		return "", 0, "", 0, false
	}
	token = parts[0]
	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 0 {
		return "", 0, "", 0, false
	}
	act = parts[2]
	switch act {
	case "s", "t":
		if len(parts) != 4 {
			return "", 0, "", 0, false
		}
		option, err = strconv.Atoi(parts[3])
		if err != nil || option < 0 {
			return "", 0, "", 0, false
		}
	case "d", "o", "k":
		if len(parts) != 3 {
			return "", 0, "", 0, false
		}
		option = questionArmedNone
	default:
		return "", 0, "", 0, false
	}
	return token, index, act, option, true
}

func toggleOption(selected []int, option int) []int {
	for i, current := range selected {
		if current == option {
			return append(selected[:i], selected[i+1:]...)
		}
	}
	return append(selected, option)
}

// optionLabels maps option indexes to labels in option order, dropping
// out-of-range and repeated indexes.
func optionLabels(payload core.Question, indexes []int) []string {
	unique := make(map[int]bool, len(indexes))
	for _, index := range indexes {
		if index >= 0 && index < len(payload.Options) {
			unique[index] = true
		}
	}
	labels := make([]string, 0, len(unique))
	for index := 0; index < len(payload.Options); index++ {
		if unique[index] {
			labels = append(labels, payload.Options[index].Label)
		}
	}
	return labels
}

// questionAnswerText is the edited text of one answered question.
func questionAnswerText(answer core.QuestionAnswer) string {
	if answer.Skipped {
		return questionSkippedText
	}
	if len(answer.Selected) == 0 {
		return "✓ Answered: " + answer.Other
	}
	labels := strings.Join(answer.Selected, ", ")
	if answer.Other == "" {
		return "✓ Answered: " + labels
	}
	return "✓ Answered: " + labels + ". Other: " + answer.Other
}

// questionMessage builds the text and inline keyboard of one question
// (API-ASK-006). selected holds the toggled option indexes of a multi-select
// question.
func questionMessage(token string, payload core.Question, index, total int, selected []int) (string, *Keyboard) {
	var body strings.Builder
	fmt.Fprintf(&body, "❓ Question %d of %d\n", index+1, total)
	if payload.Header != "" {
		fmt.Fprintf(&body, "[%s] ", payload.Header)
	}
	body.WriteString(payload.Text)
	body.WriteString("\n")
	for i, option := range payload.Options {
		fmt.Fprintf(&body, "%d. %s", i+1, option.Label)
		if option.Description != "" {
			body.WriteString(" — ")
			body.WriteString(option.Description)
		}
		body.WriteString("\n")
	}
	if payload.MultiSelect {
		body.WriteString("Tap options to select, then Done.")
	} else {
		body.WriteString("Tap an option, or Something else, or Skip.")
	}

	rows := make([][]Button, 0, len(payload.Options)+3)
	for i, option := range payload.Options {
		action := "s"
		label := fmt.Sprintf("%d %s", i+1, option.Label)
		if payload.MultiSelect {
			action = "t"
			label = checkbox(selected, i) + " " + option.Label
		}
		rows = append(rows, []Button{{
			Text: truncateQuestionLabel(label),
			Data: fmt.Sprintf("q:%s:%d:%s:%d", token, index, action, i),
		}})
	}
	rows = append(rows, []Button{
		{Text: "Something else", Data: fmt.Sprintf("q:%s:%d:o", token, index)},
		{Text: "Skip", Data: fmt.Sprintf("q:%s:%d:k", token, index)},
	})
	if payload.MultiSelect {
		rows = append(rows, []Button{{Text: "Done", Data: fmt.Sprintf("q:%s:%d:d", token, index)}})
	}
	return body.String(), &Keyboard{Rows: rows}
}

func checkbox(selected []int, option int) string {
	for _, current := range selected {
		if current == option {
			return "☑"
		}
	}
	return "☐"
}

// truncateQuestionLabel cuts a button label to the Telegram limit with an
// ellipsis.
func truncateQuestionLabel(label string) string {
	runes := []rune(label)
	if len(runes) <= questionLabelLimit {
		return label
	}
	return string(runes[:questionLabelLimit-1]) + "…"
}

// editQuestionMessage edits one message and passes keyboard nil to remove the
// buttons. The edit runs after the request may have closed.
func (c *Channel) editQuestionMessage(ctx context.Context, q *question, index int, text string, keyboard *Keyboard) {
	q.mu.Lock()
	if index < 0 || index >= len(q.messages) {
		q.mu.Unlock()
		return
	}
	msgID := q.messages[index]
	chatID := q.chat.id
	q.mu.Unlock()
	c.editQuestionMessageAt(ctx, chatID, msgID, text, keyboard)
}

// editQuestionMessageAt edits one message and passes keyboard nil to remove
// the buttons.
func (c *Channel) editQuestionMessageAt(ctx context.Context, chatID, msgID int64, text string, keyboard *Keyboard) {
	editCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), questionEditTimeout)
	defer cancel()
	c.apiErr("editMessageText", c.api.EditMessageText(editCtx, chatID, msgID, text, SendOptions{Keyboard: keyboard}))
}

// editQuestionMessages rewrites every message of a closed request and removes
// its keyboard.
func (c *Channel) editQuestionMessages(ctx context.Context, q *question, text string) {
	q.mu.Lock()
	messages := append([]int64(nil), q.messages...)
	chatID := q.chat.id
	q.mu.Unlock()
	for _, msgID := range messages {
		editCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), questionEditTimeout)
		c.apiErr("editMessageText", c.api.EditMessageText(editCtx, chatID, msgID, text, SendOptions{}))
		cancel()
	}
}

func (c *Channel) answerCallback(ctx context.Context, id, text string) {
	c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, id, text))
}

// armedQuestion returns the question of the chat with Something else armed,
// or nil.
func (c *Channel) armedQuestion(w *chat) *question {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, request := range c.questions {
		request.mu.Lock()
		armed := request.chat == w && !request.closed && request.armed >= 0 && !request.answered[request.armed]
		request.mu.Unlock()
		if armed {
			return request
		}
	}
	return nil
}

// captureQuestionText records the owner's text as the answer while Something
// else is armed (Q-007). It returns false when no capture applies, so the text
// follows the normal inbox path.
func (w *chat) captureQuestionText(ctx context.Context, m *Message, text string) bool {
	if text == "" || m.From == nil || !w.ch.isOwner(m.Chat.Type, m.From.ID) {
		return false
	}
	request := w.ch.armedQuestion(w)
	if request == nil {
		return false
	}
	request.mu.Lock()
	index := request.armed
	if request.closed || index != request.index || index < 0 || index >= len(request.req.Questions) || request.answered[index] {
		request.mu.Unlock()
		return false
	}
	payload := request.req.Questions[index]
	answer := core.QuestionAnswer{Question: payload.Text, Other: text}
	if payload.MultiSelect {
		answer.Selected = optionLabels(payload, request.selected[index])
	}
	next := request.advanceLocked(index, answer)
	request.mu.Unlock()

	w.ch.editQuestionMessage(ctx, request, index, questionAnswerText(answer), nil)
	w.ch.continueQuestion(ctx, request, next)
	return true
}

func questionUnavailable(reason string) core.QuestionOutcome {
	return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: reason}
}

// questionContextOutcome maps a context error to the API-ASK-006 reason.
func questionContextOutcome(err error) core.QuestionOutcome {
	if errors.Is(err, context.DeadlineExceeded) {
		return questionUnavailable(core.QuestionReasonTimeout)
	}
	return questionUnavailable(core.QuestionReasonCancelled)
}

// questionContextText is the edited text of a request closed by its context.
func questionContextText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return questionExpiredText
	}
	return questionCancelledText
}

// questionOutcomeText is the edited text of a request closed while a question
// message was in flight.
func questionOutcomeText(outcome core.QuestionOutcome) string {
	switch outcome.Reason {
	case core.QuestionReasonTimeout:
		return questionExpiredText
	case core.QuestionReasonCancelled:
		return questionCancelledText
	}
	if outcome.Status == core.QuestionStatusAnswered {
		return "⊘ Answered elsewhere"
	}
	return questionCancelledText
}

const (
	questionClosedText    = "this question is no longer open"
	questionSkippedText   = "⊘ Skipped"
	questionExpiredText   = "⊘ Question expired"
	questionCancelledText = "⊘ Question cancelled"
)
