package claudeshare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec 77 REQ-WRA-010: a new account HOME gets every conversation item linked
// to the owner's; the SQLite and JSON files only when the owner has them.
func TestLinkAntigravityNewAccount(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	ownerData := AgyDataDir(owner)
	write(t, filepath.Join(ownerData, "conversation_summaries.db"), "sqlite")
	write(t, filepath.Join(ownerData, "cache", "last_conversations.json"), "{}")
	// The account's login lives elsewhere in its HOME and is never touched.
	write(t, filepath.Join(account, ".gemini", "oauth_creds.json"), "secret")

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed() {
		t.Fatal("report.Changed() = false")
	}
	data := AgyDataDir(account)
	for _, name := range []string{"conversations", "brain", "annotations", "implicit", "history.jsonl", "conversation_summaries.db", "cache/last_conversations.json"} {
		if got := linkTarget(t, filepath.Join(data, name)); got != filepath.Join(ownerData, name) {
			t.Fatalf("%s -> %s", name, got)
		}
	}
	notExists(t, filepath.Join(data, "cache", "conversation_metadata.json"))
	if got := actions(report)["cache/conversation_metadata.json"]; got != Skipped {
		t.Fatalf("conversation_metadata action = %s", got)
	}
	for _, name := range []string{"conversations", "brain", "annotations", "implicit"} {
		info, err := os.Stat(filepath.Join(ownerData, name))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("owner %s: %v %v", name, info, err)
		}
	}
	if read(t, filepath.Join(account, ".gemini", "oauth_creds.json")) != "secret" {
		t.Fatal("login file touched")
	}

	// A second run keeps every link.
	again, err := LinkAntigravity(account, owner, testNow)
	if err != nil || again.Changed() {
		t.Fatalf("second run changed = %v, err %v", again.Changed(), err)
	}
}

// An account that already ran agy has its folders merged and its history
// appended; its own SQLite and JSON files are kept, not merged.
func TestLinkAntigravityAdoptsExisting(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	ownerData, data := AgyDataDir(owner), AgyDataDir(account)
	write(t, filepath.Join(ownerData, "conversations", "a.pb"), "owner-a")
	write(t, filepath.Join(ownerData, "history.jsonl"), `{"o":1}`)
	write(t, filepath.Join(ownerData, "conversation_summaries.db"), "owner-db")
	write(t, filepath.Join(ownerData, "cache", "conversation_metadata.json"), "owner-meta")
	write(t, filepath.Join(data, "conversations", "a.pb"), "account-a")
	write(t, filepath.Join(data, "conversations", "b.pb"), "account-b")
	write(t, filepath.Join(data, "brain", "x", "plan.md"), "plan")
	write(t, filepath.Join(data, "history.jsonl"), `{"a":1}`+"\n")
	write(t, filepath.Join(data, "conversation_summaries.db"), "account-db")
	write(t, filepath.Join(data, "cache", "conversation_metadata.json"), "account-meta")

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	got := actions(report)
	if got["conversations"] != Adopted || got["brain"] != Adopted || got["history.jsonl"] != Adopted || got["implicit"] != Linked {
		t.Fatalf("actions = %v", got)
	}
	if got["conversation_summaries.db"] != Kept || got["cache/conversation_metadata.json"] != Kept {
		t.Fatalf("sqlite/json actions = %v", got)
	}
	if read(t, filepath.Join(ownerData, "conversations", "b.pb")) != "account-b" ||
		read(t, filepath.Join(ownerData, "conversations", "a.pb")) != "owner-a" ||
		read(t, filepath.Join(ownerData, "brain", "x", "plan.md")) != "plan" {
		t.Fatal("folders not merged")
	}
	stamp := ".before-share-1700000000"
	if read(t, filepath.Join(data, "conversations"+stamp, "a.pb")) != "account-a" {
		t.Fatal("clashing conversation not kept aside")
	}
	notExists(t, filepath.Join(data, "brain"+stamp))
	if h := read(t, filepath.Join(ownerData, "history.jsonl")); h != `{"o":1}`+"\n"+`{"a":1}`+"\n" {
		t.Fatalf("history = %q", h)
	}
	if read(t, filepath.Join(data, "history.jsonl"+stamp)) != `{"a":1}`+"\n" {
		t.Fatal("history leftover missing")
	}
	if read(t, filepath.Join(data, "conversation_summaries.db")) != "account-db" ||
		read(t, filepath.Join(data, "cache", "conversation_metadata.json")) != "account-meta" ||
		read(t, filepath.Join(ownerData, "conversation_summaries.db")) != "owner-db" {
		t.Fatal("sqlite/json files changed")
	}
}

// The built-in account (no HOME or the owner's HOME) and a missing HOME are
// a no-op.
func TestLinkAntigravityNoOp(t *testing.T) {
	owner := t.TempDir()
	for _, account := range []string{"", owner, filepath.Join(owner, "..", filepath.Base(owner))} {
		report, err := LinkAntigravity(account, owner, testNow)
		if err != nil || len(report.Items) != 0 {
			t.Fatalf("account %q: %+v %v", account, report, err)
		}
	}
	missing := filepath.Join(t.TempDir(), "gone")
	if report, err := LinkAntigravity(missing, owner, testNow); err != nil || len(report.Items) != 0 {
		t.Fatalf("missing HOME: %+v %v", report, err)
	}
	notExists(t, missing)
	entries, _ := os.ReadDir(owner)
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("owner HOME touched: %s", strings.Join(names, ","))
	}
}
