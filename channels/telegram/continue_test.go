package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// continueButton reports whether the call's keyboard holds the Continue button
// of the session.
func continueButton(p map[string]any, session string) bool {
	markup, ok := p["reply_markup"].(map[string]any)
	if !ok {
		return false
	}
	rows, _ := markup["inline_keyboard"].([]any)
	for _, row := range rows {
		buttons, _ := row.([]any)
		for _, raw := range buttons {
			button, _ := raw.(map[string]any)
			if button["text"] == "Continue" && button["callback_data"] == "c:"+session {
				return true
			}
		}
	}
	return false
}

// noToast matches an answerCallbackQuery without a toast text.
func noToast(p map[string]any) bool {
	_, ok := p["text"]
	return !ok
}

// AC-TFB-010: an interrupted end shows the reason line and a Continue button;
// the owner press starts the continue turn and a non-owner press is refused
// (REQ-TFB-004, REQ-TFB-005, API-TFB-005).
func TestContinueButtonStartsTurn(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.turn = func(ctx context.Context, session, prompt string) (TurnResult, error) {
		return TurnResult{StopReason: "interrupted"}, nil
	}
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "work")
	api.waitCall("editMessageText", func(p map[string]any) bool {
		text, _ := p["text"].(string)
		return strings.HasSuffix(text, "⚠ lana restarted while working.") && continueButton(p, "S1")
	})

	// The owner press removes the button and starts the continue turn.
	api.callback(ownerChat, "private", ownerID, "c:S1")
	api.waitCall("editMessageReplyMarkup", nil)
	api.waitCall("answerCallbackQuery", noToast)
	api.waitCall("editMessageText", textHas("continued"))

	deadline := time.Now().Add(2 * time.Second)
	for {
		backend.mu.Lock()
		continued := append([]string(nil), backend.continued...)
		backend.mu.Unlock()
		if len(continued) == 1 && continued[0] == "S1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("continue turns = %v want [S1]", continued)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A non-owner press is refused.
	api.callback(groupChat, "supergroup", memberID, "c:S1")
	api.waitCall("answerCallbackQuery", textIs("only the owner can continue"))
}

// A normal end shows no Continue button (API-TFB-005).
func TestNormalEndHasNoContinueButton(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	startChannel(t, api, backend)

	api.text(ownerChat, "private", ownerID, "hi")
	final := api.waitCall("editMessageText", textIs("hello <b>there</b>"))
	if _, ok := final.Params["reply_markup"]; ok {
		t.Fatalf("normal end has a keyboard: %v", final.Params)
	}
}

// A refused press answers the callback with the refusal text (API-TFB-003).
func TestContinueRefusedPressAnswersCallback(t *testing.T) {
	api := newFakeAPI(t)
	backend := newFakeBackend()
	backend.contErr = errors.New("nothing to continue")
	startChannel(t, api, backend)

	api.callback(ownerChat, "private", ownerID, "c:S1")
	api.waitCall("answerCallbackQuery", textIs("nothing to continue"))
}
