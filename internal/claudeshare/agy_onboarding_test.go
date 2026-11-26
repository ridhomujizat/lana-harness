package claudeshare

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ownerOnboarding is the owner's onboarding.json after agy's first run.
const ownerOnboarding = `{"consumerOnboardingComplete": true, "enterpriseOnboardingComplete": false, "onboardingComplete": true}`

func onboardingPath(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli", "cache", "onboarding.json")
}

func onboardingItem(t *testing.T, r Report) Item {
	t.Helper()
	for _, item := range r.Items {
		if item.Name == agyOnboardingName {
			return item
		}
	}
	t.Fatalf("no %s item in %+v", agyOnboardingName, r.Items)
	return Item{}
}

// Spec 77 REQ-WRA-015: a missing account onboarding.json is created with only
// onboardingComplete true (0600, cache/ 0700), and a second run writes nothing.
func TestLinkAntigravityOnboardingMissing(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	write(t, onboardingPath(owner), ownerOnboarding)

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if item := onboardingItem(t, report); item.Action != Copied {
		t.Fatalf("action = %s", item.Action)
	}
	path := onboardingPath(account)
	if isLink(path) {
		t.Fatal("onboarding.json is a link")
	}
	got := readObject(t, path)
	if len(got) != 1 || string(got["onboardingComplete"]) != "true" {
		t.Fatalf("account onboarding.json = %v", got)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("cache mode: %v %v", info, err)
	}
	if read(t, onboardingPath(owner)) != ownerOnboarding {
		t.Fatal("owner onboarding.json changed")
	}

	// Idempotent: the flag is already true, nothing is written.
	before, _ := os.Stat(path)
	again, err := LinkAntigravity(account, owner, testNow)
	if err != nil || again.Changed() {
		t.Fatalf("second run changed = %v, err %v", again.Changed(), err)
	}
	if item := onboardingItem(t, again); item.Action != Kept {
		t.Fatalf("second run action = %s", item.Action)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("second run rewrote onboarding.json")
	}
}

// Spec 77 REQ-WRA-015: an account file with the flags false gets only
// onboardingComplete set; its own consumer/enterprise flags and other keys stay.
func TestLinkAntigravityOnboardingKeepsAccountFlags(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	write(t, onboardingPath(owner), ownerOnboarding)
	write(t, onboardingPath(account), `{"consumerOnboardingComplete": false, "enterpriseOnboardingComplete": false, "onboardingComplete": false, "other": {"a": 1}}`)

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if item := onboardingItem(t, report); item.Action != Copied {
		t.Fatalf("action = %s", item.Action)
	}
	got := readObject(t, onboardingPath(account))
	want := map[string]string{
		"consumerOnboardingComplete":   "false",
		"enterpriseOnboardingComplete": "false",
		"onboardingComplete":           "true",
	}
	if len(got) != 4 {
		t.Fatalf("account onboarding.json = %v", got)
	}
	for key, value := range want {
		if string(got[key]) != value {
			t.Fatalf("%s = %s, want %s", key, got[key], value)
		}
	}
	var other map[string]int
	if err := json.Unmarshal(got["other"], &other); err != nil || other["a"] != 1 {
		t.Fatalf("other = %s", got["other"])
	}
}

// Spec 77 REQ-WRA-015: without the owner's flag true, the account file is not
// touched or created.
func TestLinkAntigravityOnboardingOwnerNotComplete(t *testing.T) {
	for name, ownerBody := range map[string]string{
		"false":    `{"consumerOnboardingComplete": true, "onboardingComplete": false}`,
		"absent":   `{"consumerOnboardingComplete": true}`,
		"not json": `nope`,
		"missing":  "",
	} {
		t.Run(name, func(t *testing.T) {
			owner, account, fresh := t.TempDir(), t.TempDir(), t.TempDir()
			if ownerBody != "" {
				write(t, onboardingPath(owner), ownerBody)
			}
			const accountBody = `{"onboardingComplete": false}`
			write(t, onboardingPath(account), accountBody)

			for _, home := range []string{account, fresh} {
				report, err := LinkAntigravity(home, owner, testNow)
				if err != nil {
					t.Fatal(err)
				}
				if item := onboardingItem(t, report); item.Action != Skipped {
					t.Fatalf("action = %s", item.Action)
				}
			}
			if read(t, onboardingPath(account)) != accountBody {
				t.Fatal("account onboarding.json changed")
			}
			notExists(t, onboardingPath(fresh))
		})
	}
}

// Spec 77 REQ-WRA-015: an account file already true is not rewritten.
func TestLinkAntigravityOnboardingAlreadyComplete(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	write(t, onboardingPath(owner), ownerOnboarding)
	const accountBody = `{"onboardingComplete":true,"consumerOnboardingComplete":false}`
	write(t, onboardingPath(account), accountBody)

	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if item := onboardingItem(t, report); item.Action != Kept {
		t.Fatalf("action = %s", item.Action)
	}
	if read(t, onboardingPath(account)) != accountBody {
		t.Fatal("account onboarding.json rewritten")
	}
}

// Spec 77 REQ-WRA-015: an account file that is not a JSON object is an error
// and stays as it is; a symlinked one is kept. The other items still run.
func TestLinkAntigravityOnboardingLeavesAlone(t *testing.T) {
	owner, account := t.TempDir(), t.TempDir()
	write(t, onboardingPath(owner), ownerOnboarding)
	for _, body := range []string{"not json", "[1]", "null"} {
		write(t, onboardingPath(account), body)
		report, err := LinkAntigravity(account, owner, testNow)
		if err == nil {
			t.Fatalf("%q: no error", body)
		}
		if read(t, onboardingPath(account)) != body {
			t.Fatalf("%q: account onboarding.json changed", body)
		}
		if got := actions(report); got["conversations"] == "" {
			t.Fatalf("%q: other items not done: %v", body, got)
		}
	}

	other := t.TempDir()
	target := filepath.Join(other, "onboarding.json")
	const linked = `{"onboardingComplete": false}`
	write(t, target, linked)
	if err := os.Remove(onboardingPath(account)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, onboardingPath(account)); err != nil {
		t.Fatal(err)
	}
	report, err := LinkAntigravity(account, owner, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if item := onboardingItem(t, report); item.Action != Kept {
		t.Fatalf("symlink action = %s", item.Action)
	}
	if linkTarget(t, onboardingPath(account)) != target || read(t, target) != linked {
		t.Fatal("symlinked onboarding.json touched")
	}
}
