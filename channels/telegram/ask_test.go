package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

// startQuestionChannel starts a channel and binds a session to a chat worker.
func startQuestionChannel(t *testing.T, chatID int64, session string) (*Channel, *fakeAPI, *fakeBackend, *chat) {
	t.Helper()
	api := newFakeAPI(t)
	backend := newFakeBackend()
	ch, _, _ := startChannel(t, api, backend)
	workerCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := ch.worker(workerCtx, chatID, "helper")
	ch.bindSession(session, w)
	return ch, api, backend, w
}

// askInBackground runs Ask and returns its outcome channel.
func askInBackground(ch *Channel, session string, questions []core.Question) <-chan core.QuestionOutcome {
	outcomes := make(chan core.QuestionOutcome, 1)
	go func() {
		outcomes <- ch.Ask(context.Background(), core.QuestionRequest{SessionID: session, Questions: questions})
	}()
	return outcomes
}

func waitQuestionOutcome(t *testing.T, outcomes <-chan core.QuestionOutcome) core.QuestionOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the question outcome")
		return core.QuestionOutcome{}
	}
}

func noQuestionOutcome(t *testing.T, outcomes <-chan core.QuestionOutcome) {
	t.Helper()
	select {
	case outcome := <-outcomes:
		t.Fatalf("unexpected outcome %+v", outcome)
	case <-time.After(100 * time.Millisecond):
	}
}

// keyboardButton returns the button with the given text.
func keyboardButton(t *testing.T, call apiCall, text string) map[string]any {
	t.Helper()
	markup, ok := call.Params["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("%s call has no keyboard: %v", call.Method, call.Params)
	}
	rows, ok := markup["inline_keyboard"].([]any)
	if !ok {
		t.Fatalf("inline_keyboard = %T", markup["inline_keyboard"])
	}
	for _, row := range rows {
		buttons, _ := row.([]any)
		for _, button := range buttons {
			item, _ := button.(map[string]any)
			if item["text"] == text {
				return item
			}
		}
	}
	t.Fatalf("no button %q in %v", text, markup)
	return nil
}

func buttonData(t *testing.T, call apiCall, text string) string {
	t.Helper()
	data, _ := keyboardButton(t, call, text)["callback_data"].(string)
	if data == "" {
		t.Fatalf("button %q has no callback_data", text)
	}
	return data
}

func singleSelectQuestions() []core.Question {
	return []core.Question{{
		Text: "Where should I save the weekly digest?",
		Options: []core.QuestionOption{
			{Label: "research/digests/", Description: "Next to the notes, one file per week."},
			{Label: "Inbox/", Description: "Your inbox folder, to sort later."},
		},
	}}
}

// AC-ASK-028: the owner taps option 2, the request is submitted, the message is
// edited to the answer, and the keyboard is removed.
func TestQuestionSingleSelectTapAC028(t *testing.T) {
	ch, api, _, _ := startQuestionChannel(t, ownerChat, "S1")
	outcomes := askInBackground(ch, "S1", singleSelectQuestions())

	ask := api.waitCall("sendMessage", textHas("❓ Question 1 of 1"))
	if text, _ := ask.Params["text"].(string); !strings.Contains(text, "1. research/digests/ — Next to the notes") {
		t.Fatalf("question text = %q", ask.Params["text"])
	}
	data := buttonData(t, ask, "2 Inbox/")
	parts := strings.Split(data, ":")
	if len(parts) != 5 || parts[0] != "q" || len(parts[1]) != 12 || parts[2] != "0" || parts[3] != "s" || parts[4] != "1" {
		t.Fatalf("callback data = %q", data)
	}
	if len(data) >= 64 {
		t.Fatalf("callback data is %d bytes, want under 64", len(data))
	}

	api.callback(ownerChat, "private", ownerID, data)
	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	answer := outcome.Answers[0]
	if len(answer.Selected) != 1 || answer.Selected[0] != "Inbox/" || answer.Skipped || answer.Other != "" {
		t.Fatalf("answer = %+v", answer)
	}
	edit := api.waitCall("editMessageText", textIs("✓ Answered: Inbox/"))
	if _, ok := edit.Params["reply_markup"]; ok {
		t.Fatalf("keyboard was not removed: %v", edit.Params)
	}
}

// AC-ASK-029: while Something else is armed the owner's text is the answer and
// is not added to the session inbox; other text still reaches the inbox.
func TestQuestionSomethingElseAC029(t *testing.T) {
	ch, api, backend, w := startQuestionChannel(t, ownerChat, "S1")
	w.mu.Lock()
	w.session = "S1"
	w.running = true
	w.mu.Unlock()
	outcomes := askInBackground(ch, "S1", singleSelectQuestions())
	ask := api.waitCall("sendMessage", textHas("❓ Question 1 of 1"))

	api.text(ownerChat, "private", ownerID, "just a note")
	waitSent(t, backend, "just a note")
	noQuestionOutcome(t, outcomes)

	api.callback(ownerChat, "private", ownerID, buttonData(t, ask, "Something else"))
	api.waitCall("answerCallbackQuery", textIs("Type your answer in the next message."))
	api.text(ownerChat, "private", ownerID, "custom wording")

	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	answer := outcome.Answers[0]
	if answer.Other != "custom wording" || len(answer.Selected) != 0 || answer.Skipped {
		t.Fatalf("answer = %+v", answer)
	}
	api.waitCall("editMessageText", textIs("✓ Answered: custom wording"))

	backend.mu.Lock()
	sent := append([]string(nil), backend.sent...)
	prompts := append([]string(nil), backend.prompts...)
	backend.mu.Unlock()
	for _, text := range append(sent, prompts...) {
		if strings.Contains(text, "custom wording") {
			t.Fatalf("armed text reached the inbox: %q", text)
		}
	}
}

func waitSent(t *testing.T, backend *fakeBackend, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		backend.mu.Lock()
		sent := append([]string(nil), backend.sent...)
		backend.mu.Unlock()
		for _, text := range sent {
			if text == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("inbox never got %q", want)
}

// AC-ASK-030: a group member's tap is refused with the owner text and nothing is
// recorded.
func TestQuestionNonOwnerAC030(t *testing.T) {
	ch, api, _, _ := startQuestionChannel(t, groupChat, "S1")
	outcomes := askInBackground(ch, "S1", singleSelectQuestions())
	ask := api.waitCall("sendMessage", textHas("❓ Question 1 of 1"))
	data := buttonData(t, ask, "2 Inbox/")

	api.callback(groupChat, "supergroup", memberID, data)
	api.waitCall("answerCallbackQuery", textIs("only the owner can answer"))
	noQuestionOutcome(t, outcomes)
	if n := api.count("editMessageText", textHas("✓ Answered")); n != 0 {
		t.Fatalf("non-owner tap edited %d messages", n)
	}

	api.callback(groupChat, "supergroup", ownerID, data)
	if outcome := waitQuestionOutcome(t, outcomes); outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("owner outcome = %+v", outcome)
	}
}

// AC-ASK-034: a callback whose token is closed is answered with the closed text
// and records nothing.
func TestQuestionClosedTokenAC034(t *testing.T) {
	ch, api, _, _ := startQuestionChannel(t, ownerChat, "S1")
	outcomes := askInBackground(ch, "S1", singleSelectQuestions())
	ask := api.waitCall("sendMessage", textHas("❓ Question 1 of 1"))
	data := buttonData(t, ask, "2 Inbox/")

	api.callback(ownerChat, "private", ownerID, data)
	if outcome := waitQuestionOutcome(t, outcomes); outcome.Status != core.QuestionStatusAnswered {
		t.Fatalf("first outcome = %+v", outcome)
	}
	api.waitCall("editMessageText", textIs("✓ Answered: Inbox/"))
	edits := api.count("editMessageText", textHas("✓ Answered"))

	api.callback(ownerChat, "private", ownerID, data)
	api.waitCall("answerCallbackQuery", textIs("this question is no longer open"))
	noQuestionOutcome(t, outcomes)
	if n := api.count("editMessageText", textHas("✓ Answered")); n != edits {
		t.Fatalf("closed token recorded an answer (%d edits, want %d)", n, edits)
	}
}

// The request timeout edits every message with the expired text.
func TestQuestionTimeoutEditsExpired(t *testing.T) {
	ch, api, _, _ := startQuestionChannel(t, ownerChat, "S1")
	ch.QuestionTimeout = 30 * time.Millisecond
	outcomes := askInBackground(ch, "S1", singleSelectQuestions())
	api.waitCall("sendMessage", textHas("❓ Question 1 of 1"))

	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusUnavailable || outcome.Reason != core.QuestionReasonTimeout {
		t.Fatalf("outcome = %+v", outcome)
	}
	edit := api.waitCall("editMessageText", textIs("⊘ Question expired"))
	if _, ok := edit.Params["reply_markup"]; ok {
		t.Fatalf("expired keyboard was not removed: %v", edit.Params)
	}
}

// A single-select tap sends the next question; the next question is not sent
// before the answer.
func TestQuestionSendsNextAfterAnswer(t *testing.T) {
	questions := []core.Question{
		{Text: "Where?", Options: []core.QuestionOption{{Label: "a"}, {Label: "b"}}},
		{Text: "When?", Options: []core.QuestionOption{{Label: "today"}, {Label: "tomorrow"}}},
	}
	ch, api, _, _ := startQuestionChannel(t, ownerChat, "S1")
	outcomes := askInBackground(ch, "S1", questions)
	ask := api.waitCall("sendMessage", textHas("❓ Question 1 of 2"))
	if n := api.count("sendMessage", textHas("❓ Question 2 of 2")); n != 0 {
		t.Fatalf("next question was sent early (%d)", n)
	}

	api.callback(ownerChat, "private", ownerID, buttonData(t, ask, "1 a"))
	next := api.waitCall("sendMessage", textHas("❓ Question 2 of 2"))
	noQuestionOutcome(t, outcomes)
	api.callback(ownerChat, "private", ownerID, buttonData(t, next, "2 tomorrow"))

	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 2 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.Answers[0].Selected[0] != "a" || outcome.Answers[1].Selected[0] != "tomorrow" {
		t.Fatalf("answers = %+v", outcome.Answers)
	}
}

// A multi-select toggle edits the keyboard and Done records the selection.
func TestQuestionMultiSelectToggleAndDone(t *testing.T) {
	questions := []core.Question{{
		Text:        "Which sections?",
		MultiSelect: true,
		Options:     []core.QuestionOption{{Label: "Findings"}, {Label: "Actions"}},
	}}
	ch, api, _, _ := startQuestionChannel(t, ownerChat, "S1")
	outcomes := askInBackground(ch, "S1", questions)
	ask := api.waitCall("sendMessage", textHas("Tap options to select, then Done."))

	api.callback(ownerChat, "private", ownerID, buttonData(t, ask, "☐ Findings"))
	api.waitCall("editMessageText", func(p map[string]any) bool {
		text, _ := p["text"].(string)
		return strings.Contains(text, "Tap options to select") && hasKeyboardButton(p, "☑ Findings")
	})
	api.callback(ownerChat, "private", ownerID, buttonData(t, ask, "Done"))

	outcome := waitQuestionOutcome(t, outcomes)
	if outcome.Status != core.QuestionStatusAnswered || len(outcome.Answers) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Answers[0].Selected) != 1 || outcome.Answers[0].Selected[0] != "Findings" {
		t.Fatalf("selected = %+v", outcome.Answers[0].Selected)
	}
}

func hasKeyboardButton(params map[string]any, text string) bool {
	markup, _ := params["reply_markup"].(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	for _, row := range rows {
		buttons, _ := row.([]any)
		for _, button := range buttons {
			item, _ := button.(map[string]any)
			if item["text"] == text {
				return true
			}
		}
	}
	return false
}
