package main

import (
	"context"
	"errors"
	"testing"

	"github.com/ridhomujizat/lana-harness/channels/telegram"
)

// TestTelegramResetMessage checks the exact reset text of REQ-WLG-004.
func TestTelegramResetMessage(t *testing.T) {
	got := telegramResetMessage("123456")
	want := "🔐 Your lana reset code: 123456. It expires in 10 minutes. If you did not ask for it, ignore this message."
	if got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// TestTelegramResetSenderAvailability covers the availability rule of
// REQ-WLG-004: a running channel with at least one owner that has a
// private-chat route.
func TestTelegramResetSenderAvailability(t *testing.T) {
	cases := []struct {
		name   string
		sender *telegramResetSender
		want   bool
	}{
		{
			name:   "owner with a route",
			sender: &telegramResetSender{channel: &telegram.Channel{}, owners: map[int64]bool{42: true}, routes: map[int64]string{42: "assistant"}},
			want:   true,
		},
		{
			name:   "owner without a route",
			sender: &telegramResetSender{channel: &telegram.Channel{}, owners: map[int64]bool{42: true}, routes: map[int64]string{7: "assistant"}},
			want:   false,
		},
		{
			name:   "no owners",
			sender: &telegramResetSender{channel: &telegram.Channel{}, owners: map[int64]bool{}, routes: map[int64]string{42: "assistant"}},
			want:   false,
		},
		{
			name:   "channel not running",
			sender: &telegramResetSender{owners: map[int64]bool{42: true}, routes: map[int64]string{42: "assistant"}},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sender.ResetAvailable(); got != tc.want {
				t.Fatalf("ResetAvailable = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTelegramResetSenderNoOwnersSendsNothing covers the empty send.
func TestTelegramResetSenderNoOwnersSendsNothing(t *testing.T) {
	sender := &telegramResetSender{channel: &telegram.Channel{}, owners: map[int64]bool{}, routes: map[int64]string{}}
	sent, err := sender.SendOwnerCode(context.Background(), "123456")
	if err != nil {
		t.Fatalf("SendOwnerCode: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}
}

// TestTelegramResetSenderSendsToRoutedOwners covers REQ-WLG-004: the code is
// sent as the reset text to each owner that has a private-chat route, owners
// without a route are skipped, and the count is the number of successful sends.
func TestTelegramResetSenderSendsToRoutedOwners(t *testing.T) {
	type message struct {
		chatID int64
		text   string
	}
	var sent []message
	sender := &telegramResetSender{
		channel: &telegram.Channel{},
		owners:  map[int64]bool{42: true, 7: true, 9: true},
		routes:  map[int64]string{42: "assistant", 7: "assistant"},
		send: func(_ context.Context, chatID int64, text string) error {
			sent = append(sent, message{chatID: chatID, text: text})
			return nil
		},
	}
	count, err := sender.SendOwnerCode(context.Background(), "123456")
	if err != nil {
		t.Fatalf("SendOwnerCode: %v", err)
	}
	if count != 2 {
		t.Fatalf("sent = %d, want 2", count)
	}
	want := telegramResetMessage("123456")
	seen := map[int64]bool{}
	for _, m := range sent {
		if m.text != want {
			t.Fatalf("text = %q, want %q", m.text, want)
		}
		if m.chatID != 42 && m.chatID != 7 {
			t.Fatalf("message sent to owner %d, which has no route", m.chatID)
		}
		seen[m.chatID] = true
	}
	if !seen[42] || !seen[7] {
		t.Fatalf("owners with a route were not both sent: %v", seen)
	}
}

// TestTelegramResetSenderErrorNotCounted covers the count rule: a send error is
// not counted and is reported back to the caller.
func TestTelegramResetSenderErrorNotCounted(t *testing.T) {
	sentinel := errors.New("telegram is unreachable")
	sender := &telegramResetSender{
		channel: &telegram.Channel{},
		owners:  map[int64]bool{42: true, 7: true},
		routes:  map[int64]string{42: "assistant", 7: "assistant"},
		send: func(_ context.Context, chatID int64, _ string) error {
			if chatID == 42 {
				return sentinel
			}
			return nil
		},
	}
	count, err := sender.SendOwnerCode(context.Background(), "123456")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the send error", err)
	}
	if count != 1 {
		t.Fatalf("sent = %d, want 1", count)
	}
}
