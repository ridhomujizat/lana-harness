package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
)

const (
	// questionTimeout is the time an open question waits for an answer
	// (API-ASK-005).
	questionTimeout = 30 * time.Minute
	// questionClosedRetention is how long a closed request ID is remembered so
	// a late answer gets 409 question_closed instead of 404 not_found.
	questionClosedRetention = time.Hour
	// maxQuestionBody is the largest accepted answer body (API-ASK-005).
	maxQuestionBody = 64 << 10
	// maxQuestionOtherChars is the maximum length of a Something else answer.
	maxQuestionOtherChars = 500
	// questionSubscriberBuffer is the per-stream queue of question events.
	questionSubscriberBuffer = 32
)

var (
	errQuestionNotFound = errors.New("question request not found")
	errQuestionClosed   = errors.New("question request closed")
	// errQuestionInvalidAnswers marks a body that parsed but violates the
	// answer rules of API-ASK-005.
	errQuestionInvalidAnswers = errors.New("invalid question answers")
)

// questionEvent is the data of the question SSE event (API-ASK-004).
type questionEvent struct {
	RequestID string            `json:"request_id"`
	Questions []questionPayload `json:"questions"`
	ExpiresAt int64             `json:"expires_at"`
}

// questionPayload is one question of the SSE event. It carries the tool input
// shape: question, header, multi_select, and options.
type questionPayload struct {
	Question    string          `json:"question"`
	Header      string          `json:"header"`
	MultiSelect bool            `json:"multi_select"`
	Options     []optionPayload `json:"options"`
}

// optionPayload is one option of a question in the SSE event.
type optionPayload struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type pendingQuestion struct {
	id        string
	sessionID string
	questions []core.Question
	event     questionEvent
	reply     chan core.QuestionOutcome
	timer     approvalTimer
}

// Questions is the browser question broker. It opens requests for the web
// channel, publishes them to the session's open streams, and resolves them on
// the first answer, the timeout, or cancellation. It never writes the session
// log: the question service owns question/asked and question/answered.
type Questions struct {
	mu     sync.Mutex
	clock  approvalClock
	open   map[string]*pendingQuestion
	closed map[string]time.Time
	subs   map[string]map[chan questionEvent]struct{}
	// rootOf resolves the root session of a question request. It is nil until
	// a stream installs it, so a broker used alone treats every session as its
	// own root.
	rootOf sessionRootResolver
}

// Questions satisfies the question service the turn runner uses.
var _ core.QuestionService = (*Questions)(nil)

func newQuestions() *Questions {
	return &Questions{
		clock:  systemClock{},
		open:   map[string]*pendingQuestion{},
		closed: map[string]time.Time{},
		subs:   map[string]map[chan questionEvent]struct{}{},
	}
}

// setClock replaces the clock used for expiry. Call it before the first Ask;
// it exists for tests.
func (q *Questions) setClock(clock approvalClock) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if clock != nil {
		q.clock = clock
	}
}

// Ask registers one question request and waits for the first browser answer,
// the timeout, or ctx cancellation. The service sets RequestID and ExpiresAt;
// a request without them gets a random ID and the default timeout. A request
// of a descendant is published to the root stream (API-TEAM-011).
func (q *Questions) Ask(ctx context.Context, req core.QuestionRequest) (core.QuestionOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return questionContextOutcome(err), nil
	}
	id := req.RequestID
	if id == "" {
		generated, err := newQuestionRequestID()
		if err != nil {
			return questionUnavailable(core.QuestionReasonNoResponder), nil
		}
		id = generated
	}
	now := q.clock.Now()
	expiresAt := req.ExpiresAt
	if expiresAt <= now.UnixMilli() {
		expiresAt = now.Add(questionTimeout).UnixMilli()
	}
	wait := time.Duration(expiresAt-now.UnixMilli()) * time.Millisecond

	rootID := req.SessionID
	if resolve := q.sessionRootResolver(); resolve != nil {
		if root := resolve(req.SessionID); root != "" {
			rootID = root
		}
	}

	questions := cloneQuestions(req.Questions)
	pending := &pendingQuestion{
		id:        id,
		sessionID: req.SessionID,
		questions: questions,
		event:     questionEventFrom(id, questions, expiresAt),
		reply:     make(chan core.QuestionOutcome, 1),
		timer:     q.clock.NewTimer(wait),
	}

	q.mu.Lock()
	q.pruneClosedLocked(now)
	q.open[id] = pending
	for ch := range q.subs[rootID] {
		select {
		case ch <- pending.event:
		default:
			// A slow stream gets the event again from the replay of open
			// questions when it reconnects.
		}
	}
	q.mu.Unlock()

	select {
	case outcome := <-pending.reply:
		return outcome, nil
	case <-pending.timer.C():
		return q.resolve(pending, questionUnavailable(core.QuestionReasonTimeout)), nil
	case <-ctx.Done():
		return q.resolve(pending, questionContextOutcome(ctx.Err())), nil
	}
}

// sessionRootResolver returns the current resolver under the broker lock.
func (q *Questions) sessionRootResolver() sessionRootResolver {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.rootOf
}

// resolve closes a pending request if no answer won the race yet. When another
// path already closed it, resolve returns that decision instead.
func (q *Questions) resolve(pending *pendingQuestion, outcome core.QuestionOutcome) core.QuestionOutcome {
	q.mu.Lock()
	if current, ok := q.open[pending.id]; ok && current == pending {
		delete(q.open, pending.id)
		q.markClosedLocked(pending.id)
		pending.timer.Stop()
		pending.reply <- outcome
	}
	q.mu.Unlock()
	return <-pending.reply
}

// answer accepts the first valid submission for an open request. A known but
// closed ID returns errQuestionClosed; an ID that never existed returns
// errQuestionNotFound. A body that violates the answer rules returns
// errQuestionInvalidAnswers and leaves the request open.
func (q *Questions) answer(id string, body questionAnswerRequest) (core.QuestionOutcome, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock.Now()
	q.pruneClosedLocked(now)
	pending, ok := q.open[id]
	if !ok {
		if _, seen := q.closed[id]; seen {
			return core.QuestionOutcome{}, errQuestionClosed
		}
		return core.QuestionOutcome{}, errQuestionNotFound
	}
	answers, err := validateQuestionAnswers(pending.questions, body)
	if err != nil {
		return core.QuestionOutcome{}, err
	}
	outcome := core.QuestionOutcome{Status: core.QuestionStatusAnswered, Answers: answers}
	delete(q.open, id)
	q.closed[id] = now
	pending.timer.Stop()
	pending.reply <- outcome
	return outcome, nil
}

func (q *Questions) markClosedLocked(id string) {
	q.closed[id] = q.clock.Now()
}

func (q *Questions) pruneClosedLocked(now time.Time) {
	for id, closedAt := range q.closed {
		if now.Sub(closedAt) >= questionClosedRetention {
			delete(q.closed, id)
		}
	}
}

// hasOpenQuestion reports whether a request of the session is still waiting
// for an answer. The session list uses it next to the log-derived state.
func (q *Questions) hasOpenQuestion(sessionID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, pending := range q.open {
		if pending.sessionID == sessionID {
			return true
		}
	}
	return false
}

// subscribe atomically returns the open questions of a session and registers
// a stream for new ones, so an answer racing a connection is not lost. The
// replay includes the open questions of the session's descendants
// (API-TEAM-011, API-ASK-004).
func (q *Questions) subscribe(sessionID string, resolvers ...sessionRootResolver) ([]questionEvent, <-chan questionEvent, func()) {
	var resolve sessionRootResolver
	if len(resolvers) > 0 {
		resolve = resolvers[0]
	}

	q.mu.Lock()
	// The resolver runs under the same lock as the replay so a request that
	// opens during the handshake is either in the replay or in the channel.
	if resolve != nil {
		q.rootOf = resolve
	}
	now := q.clock.Now().UnixMilli()
	replay := make([]questionEvent, 0)
	for _, request := range q.open {
		rootID := request.sessionID
		if resolve != nil && request.sessionID != sessionID {
			if root := resolve(request.sessionID); root != "" {
				rootID = root
			}
		}
		if rootID == sessionID && request.event.ExpiresAt > now {
			replay = append(replay, request.event)
		}
	}
	sort.Slice(replay, func(i, j int) bool {
		if replay[i].ExpiresAt != replay[j].ExpiresAt {
			return replay[i].ExpiresAt < replay[j].ExpiresAt
		}
		return replay[i].RequestID < replay[j].RequestID
	})
	ch := make(chan questionEvent, questionSubscriberBuffer)
	if q.subs[sessionID] == nil {
		q.subs[sessionID] = map[chan questionEvent]struct{}{}
	}
	q.subs[sessionID][ch] = struct{}{}
	q.mu.Unlock()

	unsubscribe := func() {
		q.mu.Lock()
		delete(q.subs[sessionID], ch)
		if len(q.subs[sessionID]) == 0 {
			delete(q.subs, sessionID)
		}
		q.mu.Unlock()
	}
	return replay, ch, unsubscribe
}

// registerQuestionRoutes registers API-ASK-005 through Handle so the auth,
// Host, Origin, and CSRF checks apply exactly as for the other endpoints.
func (s *Server) registerQuestionRoutes() {
	s.Handle("POST /v1/questions/{request_id}", http.HandlerFunc(s.handleAnswerQuestion))
}

type questionAnswerRequest struct {
	Answers []questionAnswerEntry `json:"answers"`
	SkipAll bool                  `json:"skip_all"`
}

type questionAnswerEntry struct {
	Question int     `json:"question"`
	Selected []int   `json:"selected"`
	Other    *string `json:"other"`
	Skipped  bool    `json:"skipped"`
}

type questionAnswerResponse struct {
	Outcome string `json:"outcome"`
}

func (s *Server) handleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	var body questionAnswerRequest
	if !decodeJSONObject(w, r, maxQuestionBody, &body) {
		return
	}
	outcome, err := s.questions.answer(r.PathValue("request_id"), body)
	switch {
	case errors.Is(err, errQuestionClosed):
		writeAPIError(w, http.StatusConflict, "question_closed", "question closed")
	case errors.Is(err, errQuestionNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "question request not found")
	case errors.Is(err, errQuestionInvalidAnswers):
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_answers", "check your answers")
	case err != nil:
		s.logger.Error("web: answer question", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	default:
		writeJSON(w, http.StatusOK, questionAnswerResponse{Outcome: outcome.Status})
	}
}

// validateQuestionAnswers applies the rules of API-ASK-005 in contract order
// and maps option indexes to labels. A skip_all submission marks every
// question skipped.
func validateQuestionAnswers(questions []core.Question, body questionAnswerRequest) ([]core.QuestionAnswer, error) {
	if body.SkipAll {
		if body.Answers != nil {
			return nil, questionInvalidAnswers("skip_all cannot be combined with answers")
		}
		answers := make([]core.QuestionAnswer, len(questions))
		for i, question := range questions {
			answers[i] = core.QuestionAnswer{Question: question.Text, Skipped: true}
		}
		return answers, nil
	}
	if len(body.Answers) == 0 {
		return nil, questionInvalidAnswers("answers is required")
	}
	answers := make([]core.QuestionAnswer, len(questions))
	seen := make([]bool, len(questions))
	for _, entry := range body.Answers {
		if entry.Question < 0 || entry.Question >= len(questions) {
			return nil, questionInvalidAnswers("question out of range")
		}
		if seen[entry.Question] {
			return nil, questionInvalidAnswers("question index given twice")
		}
		seen[entry.Question] = true
		question := questions[entry.Question]
		if entry.Skipped {
			if len(entry.Selected) > 0 || entry.Other != nil {
				return nil, questionInvalidAnswers("a skipped entry cannot select an option or set other")
			}
			answers[entry.Question] = core.QuestionAnswer{Question: question.Text, Skipped: true}
			continue
		}
		if len(entry.Selected) == 0 && entry.Other == nil {
			return nil, questionInvalidAnswers("an answer needs a selected option or other text")
		}
		if !question.MultiSelect && len(entry.Selected) > 1 {
			return nil, questionInvalidAnswers("a single-select question takes at most one option")
		}
		if !question.MultiSelect && len(entry.Selected) > 0 && entry.Other != nil {
			return nil, questionInvalidAnswers("a single-select question cannot combine an option and other")
		}
		selected, err := selectedLabels(question.Options, entry.Selected)
		if err != nil {
			return nil, err
		}
		other := ""
		if entry.Other != nil {
			other = strings.TrimSpace(*entry.Other)
			if other == "" || utf8.RuneCountInString(other) > maxQuestionOtherChars {
				return nil, questionInvalidAnswers("other must be 1 to 500 characters")
			}
		}
		answers[entry.Question] = core.QuestionAnswer{Question: question.Text, Selected: selected, Other: other}
	}
	for _, ok := range seen {
		if !ok {
			return nil, questionInvalidAnswers("question missing from answers")
		}
	}
	return answers, nil
}

// selectedLabels maps option indexes to labels in option order. Out-of-range
// and repeated indexes are invalid.
func selectedLabels(options []core.QuestionOption, indexes []int) ([]string, error) {
	if len(indexes) == 0 {
		return nil, nil
	}
	unique := make(map[int]bool, len(indexes))
	ordered := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(options) {
			return nil, questionInvalidAnswers("option out of range")
		}
		if unique[index] {
			return nil, questionInvalidAnswers("option index repeated")
		}
		unique[index] = true
		ordered = append(ordered, index)
	}
	sort.Ints(ordered)
	labels := make([]string, len(ordered))
	for i, index := range ordered {
		labels[i] = options[index].Label
	}
	return labels, nil
}

func questionInvalidAnswers(message string) error {
	return errors.Join(errQuestionInvalidAnswers, errors.New(message))
}

func questionUnavailable(reason string) core.QuestionOutcome {
	return core.QuestionOutcome{Status: core.QuestionStatusUnavailable, Reason: reason}
}

func questionContextOutcome(err error) core.QuestionOutcome {
	if errors.Is(err, context.DeadlineExceeded) {
		return questionUnavailable(core.QuestionReasonTimeout)
	}
	return questionUnavailable(core.QuestionReasonCancelled)
}

// cloneQuestions copies the questions so a caller mutation cannot race the
// answer validation.
func cloneQuestions(questions []core.Question) []core.Question {
	if len(questions) == 0 {
		return nil
	}
	out := make([]core.Question, len(questions))
	for i, question := range questions {
		out[i] = question
		if question.Options != nil {
			out[i].Options = append([]core.QuestionOption(nil), question.Options...)
		}
	}
	return out
}

// questionEventFrom builds the SSE payload with the API-ASK-004 field names.
func questionEventFrom(id string, questions []core.Question, expiresAt int64) questionEvent {
	payload := make([]questionPayload, len(questions))
	for i, question := range questions {
		options := make([]optionPayload, len(question.Options))
		for j, option := range question.Options {
			options[j] = optionPayload{Label: option.Label, Description: option.Description}
		}
		payload[i] = questionPayload{
			Question:    question.Text,
			Header:      question.Header,
			MultiSelect: question.MultiSelect,
			Options:     options,
		}
	}
	return questionEvent{RequestID: id, Questions: payload, ExpiresAt: expiresAt}
}

func newQuestionRequestID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
