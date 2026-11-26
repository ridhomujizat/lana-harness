package claudeshare

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Claude Code settings like cswap (spec 77 REQ-WRA-012). A Router Claude Code
// account folder that never signed in gets a copy of the default account's
// .claude.json without its login keys, so a new account starts with the
// owner's theme, onboarding, projects, MCP servers and the rest instead of a
// first run.

// claudeJSONName is Claude Code's global config file name.
const claudeJSONName = ".claude.json"

// claudeLoginKeys are the .claude.json keys of the signed-in account, removed
// from the copy: the new account signs in on its own.
var claudeLoginKeys = []string{"oauthAccount", "primaryApiKey", "customApiKeyResponses"}

// ClaudeJSONPath returns the default account's .claude.json for the default
// Claude Code folder defaultDir: <defaultDir>/.claude.json when the daemon has
// CLAUDE_CONFIG_DIR set (explicitDir), else the file beside the folder
// (~/.claude → ~/.claude.json), where Claude Code keeps it.
func ClaudeJSONPath(defaultDir string, explicitDir bool) string {
	if defaultDir == "" {
		return ""
	}
	if explicitDir {
		return filepath.Join(defaultDir, claudeJSONName)
	}
	return filepath.Join(filepath.Dir(filepath.Clean(defaultDir)), claudeJSONName)
}

// SeedClaudeJSON writes <accountDir>/.claude.json (0600) as a copy of
// defaultJSON without oauthAccount, primaryApiKey and customApiKeyResponses
// when the account has none, or has one without oauthAccount (it never signed
// in). Every other key is kept. It reports whether it wrote the file.
//
// Nothing happens when accountDir or defaultJSON is empty, the account folder
// does not exist, the default file does not exist, the two paths are the same
// file, or the account file is a symlink or holds oauthAccount (a signed-in
// account is never overwritten). A default or account file that is not a JSON
// object is an error and nothing is written.
func SeedClaudeJSON(accountDir, defaultJSON string) (bool, error) {
	if accountDir == "" || defaultJSON == "" {
		return false, nil
	}
	account, err := filepath.Abs(accountDir)
	if err != nil {
		return false, err
	}
	source, err := filepath.Abs(defaultJSON)
	if err != nil {
		return false, err
	}
	if info, err := os.Stat(account); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !info.IsDir() {
		return false, fmt.Errorf("claudeshare: account folder %s is not a folder", account)
	}
	target := filepath.Join(account, claudeJSONName)
	if sameFile(target, source) {
		return false, nil
	}

	mu.Lock()
	defer mu.Unlock()

	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, nil
		}
		current, err := readJSONObject(target)
		if err != nil {
			return false, fmt.Errorf("claudeshare: account %s: %w", claudeJSONName, err)
		}
		if _, signedIn := current["oauthAccount"]; signedIn {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	settings, err := readJSONObject(source)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claudeshare: default %s: %w", claudeJSONName, err)
	}
	for _, key := range claudeLoginKeys {
		delete(settings, key)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(target, append(data, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// readJSONObject reads a JSON object, keeping each value as it is.
func readJSONObject(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("not a JSON object")
	}
	return object, nil
}

// writeFileAtomic writes data to path (0600) through a temporary file in the
// same folder renamed over it.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".lana-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// sameFile reports whether a and b are the same file (both must exist), or the
// same cleaned path.
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}
