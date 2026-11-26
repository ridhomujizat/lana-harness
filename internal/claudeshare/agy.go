package claudeshare

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Antigravity (agy) conversations shared across accounts (spec 77
// REQ-WRA-010, experimental). agy keeps its conversations under
// <HOME>/.gemini/antigravity-cli/; an added Router account runs with its own
// HOME, so its conversations are linked to the owner's. Only the items below
// are touched: agy keeps its file-based login elsewhere in the account HOME,
// and nothing else there is read, listed or moved.

// AgyDataDir is the agy data folder of a HOME:
// <home>/.gemini/antigravity-cli.
func AgyDataDir(home string) string {
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

// agyDirNames are the folders linked to the owner's, created when missing
// and adopted (merged) when the account has a real one.
var agyDirNames = []string{"conversations", "brain", "annotations", "implicit"}

// agyHistoryName is the JSON-lines prompt history, adopted by append.
const agyHistoryName = "history.jsonl"

// agyOptionalNames are linked only when the owner has them and the account has
// nothing at the name: SQLite and JSON files are never merged, so an account's
// own copy is kept.
var agyOptionalNames = []string{
	"conversation_summaries.db",
	"cache/last_conversations.json",
	"cache/conversation_metadata.json",
}

// agySettingsNames are the agy settings, relative to the HOME, that an added
// account shares with the owner (spec 77 REQ-WRA-011): linked when the owner
// has them. agy writes defaults at the first sign-in, so a real account item
// is a default: it is renamed <name>.before-share-<unix> and linked.
// .gemini/config/hooks.json is not listed: the Workbench hook installer
// manages it per folder (REQ-WRA-009). Nothing of agy's login or state
// (antigravity-oauth-token, cache/acp-auth-state.json,
// cache/default_project_id.txt, installation_id, jetski_state.pbtxt) is ever
// read, listed, linked or moved. cache/onboarding.json is touched only for its
// onboardingComplete flag (REQ-WRA-015, see agyOnboardingName).
var agySettingsNames = []string{
	".gemini/antigravity-cli/settings.json",
	".gemini/config/config.json",
	".gemini/config/mcp_config.json",
	".gemini/config/skills",
}

// agyOnboardingName is agy's first-run onboarding state, relative to the agy
// data folder (spec 77 REQ-WRA-015). Only its onboardingComplete flag is
// copied from the owner; consumerOnboardingComplete and
// enterpriseOnboardingComplete may be per Google account and are never copied.
const agyOnboardingName = "cache/onboarding.json"

// agyOnboardingKey is the onboarding.json flag copied from the owner.
const agyOnboardingKey = "onboardingComplete"

// LinkAntigravity links the conversation items of an Antigravity account HOME
// to the owner's HOME (spec 77 REQ-WRA-010), and its settings (REQ-WRA-011):
//
//   - conversations/, brain/, annotations/, implicit/: the owner folder is
//     created (0700) when missing; a real account folder is merged into it
//     (a name the owner already has stays behind, the rest renamed
//     <name>.before-share-<unix>), then linked.
//   - history.jsonl: the owner file is created empty (0600) when missing; a
//     real account file is appended to it and renamed, then linked.
//   - conversation_summaries.db, cache/last_conversations.json,
//     cache/conversation_metadata.json: linked only when the owner's exists
//     and the account has nothing at the name.
//   - .gemini/antigravity-cli/settings.json, .gemini/config/config.json,
//     .gemini/config/mcp_config.json, .gemini/config/skills/: linked when the
//     owner's exists; a real account item (agy's first-sign-in defaults) is
//     renamed <name>.before-share-<unix>, then linked.
//   - cache/onboarding.json (REQ-WRA-015): when the owner's has
//     "onboardingComplete": true and the account's is missing or does not, the
//     account's file (0600, atomic write) gets onboardingComplete true and keeps
//     every other key it has. This copies one flag; it is not a link. A
//     symlinked account file is kept; one that is not a JSON object is an
//     error and stays as it is.
//
// The built-in account (accountHome "" or the owner's HOME) and an account
// HOME that does not exist are a no-op. The account's
// .gemini/antigravity-cli folder is created (0700) when missing. Each item is
// done on its own; the errors of failed items are joined.
func LinkAntigravity(accountHome, ownerHome string, now time.Time) (Report, error) {
	if accountHome == "" || ownerHome == "" {
		return Report{}, nil
	}
	accountRoot, err := filepath.Abs(accountHome)
	if err != nil {
		return Report{}, err
	}
	ownerRoot, err := filepath.Abs(ownerHome)
	if err != nil {
		return Report{}, err
	}
	if sameDir(accountRoot, ownerRoot) {
		return Report{}, nil
	}
	if info, err := os.Stat(accountRoot); errors.Is(err, os.ErrNotExist) {
		return Report{}, nil
	} else if err != nil {
		return Report{}, err
	} else if !info.IsDir() {
		return Report{}, fmt.Errorf("claudeshare: account HOME %s is not a folder", accountRoot)
	}
	account, shared := AgyDataDir(accountRoot), AgyDataDir(ownerRoot)
	if sameDir(account, shared) {
		return Report{}, nil
	}

	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(account, dirMode); err != nil {
		return Report{}, err
	}
	stamp := strconv.FormatInt(now.Unix(), 10)
	var report Report
	var errs []error
	add := func(item Item, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("claudeshare: antigravity %s: %w", item.Name, err))
			return
		}
		report.Items = append(report.Items, item)
	}
	for _, name := range agyDirNames {
		add(linkDir(account, shared, name, stamp))
	}
	add(linkAppendFile(account, shared, agyHistoryName, stamp))
	for _, name := range agyOptionalNames {
		add(linkOptional(account, shared, name))
	}
	for _, name := range agySettingsNames {
		add(linkReplace(accountRoot, ownerRoot, name, stamp))
	}
	add(copyOnboardingComplete(account, shared))
	return report, errors.Join(errs...)
}

// copyOnboardingComplete sets onboardingComplete true in the account's
// cache/onboarding.json when the owner's has it true (spec 77 REQ-WRA-015),
// so agy skips its first-run onboarding. The account's other keys, its own
// consumerOnboardingComplete and enterpriseOnboardingComplete included, are
// kept; a missing account file is created with that one key. The owner file
// missing, unreadable, not a JSON object or without the flag true is Skipped.
// An account file already true, a symlink or not a regular file is Kept. An
// account file that is not a JSON object is an error and is left alone.
func copyOnboardingComplete(account, shared string) (Item, error) {
	item := Item{Name: agyOnboardingName}
	src := filepath.Join(account, filepath.FromSlash(agyOnboardingName))
	dst := filepath.Join(shared, filepath.FromSlash(agyOnboardingName))
	owner, err := readJSONObject(dst)
	if err != nil || !jsonTrue(owner[agyOnboardingKey]) {
		item.Action = Skipped
		return item, nil
	}
	current := map[string]json.RawMessage{}
	if info, err := os.Lstat(src); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			item.Action = Kept
			return item, nil
		}
		current, err = readJSONObject(src)
		if err != nil {
			return item, err
		}
		if jsonTrue(current[agyOnboardingKey]) {
			item.Action = Kept
			return item, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return item, err
	}
	current[agyOnboardingKey] = json.RawMessage("true")
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return item, err
	}
	if err := os.MkdirAll(filepath.Dir(src), dirMode); err != nil {
		return item, err
	}
	if err := writeFileAtomic(src, append(data, '\n')); err != nil {
		return item, err
	}
	item.Action = Copied
	return item, nil
}

// jsonTrue reports whether raw is the JSON value true.
func jsonTrue(raw json.RawMessage) bool {
	var value bool
	return raw != nil && json.Unmarshal(raw, &value) == nil && value
}

// linkReplace links name (relative to the roots, it may hold sub-folders) when
// the shared item exists: an existing link is kept, a real account item is
// renamed <name>.before-share-<stamp> (Adopted, the owner's item replaces it),
// and nothing at the name is Linked. The account's parent folders are created
// (0700) when missing. A missing shared item is Skipped and the account item,
// if any, stays as it is.
func linkReplace(account, shared, name, stamp string) (Item, error) {
	item := Item{Name: name}
	src := filepath.Join(account, filepath.FromSlash(name))
	dst := filepath.Join(shared, filepath.FromSlash(name))
	if isLink(src) {
		item.Action = Kept
		return item, nil
	}
	if _, err := os.Stat(dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			item.Action = Skipped
			return item, nil
		}
		return item, err
	}
	if err := os.MkdirAll(filepath.Dir(src), dirMode); err != nil {
		return item, err
	}
	item.Action = Linked
	if exists(src) {
		backup, err := moveAside(src, stamp)
		if err != nil {
			return item, err
		}
		item.Action, item.Backup = Adopted, backup
	}
	return item, os.Symlink(dst, src)
}
