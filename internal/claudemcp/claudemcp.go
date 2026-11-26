// Package claudemcp mirrors the built-in Claude Code account's MCP servers into
// a named account (spec 81 revision 2, REQ-SMC-004), the way claude-swap does
// (cswap session.py _sync_mcp_servers): the top-level mcpServers of
// $HOME/.claude.json is the single source, and a named account's .claude.json
// gets exactly that value before Lana launches its claude. Adds, edits and
// removals propagate; nothing flows back; projects[...].mcpServers stay
// untouched on both sides; every other key of the account's file keeps its
// value byte for byte.
//
// Only the top-level mcpServers of the built-in config is used; never
// oauthAccount, tokens or the Keychain. Server entries (which may hold secret
// headers or env values) are never logged or printed: errors returned here are
// short fixed reasons without file content.
package claudemcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

const (
	// MaxConfigSize caps a .claude.json that is read.
	MaxConfigSize = 16 << 20
	// MarkerName is the file in an account's config dir that records the
	// account has adopted the mirror (cswap: .cswap-mcp-mirror-v1).
	MarkerName = ".lana-mcp-mirror-v1"
	// DisplacedName holds the account's own entries the first mirror removed
	// or changed (cswap: .cswap-mcp-displaced.json). Written once.
	DisplacedName = ".lana-mcp-displaced.json"

	configName = ".claude.json"
	mcpKey     = "mcpServers"

	takeoverSuffix    = ".takeover"
	takeoverStaleness = 5 * time.Second
	touchInterval     = 3 * time.Second
	declineBackoff    = 50 * time.Millisecond
)

// The lock timings of REQ-SMC-004: a lock older than LockStaleness is taken
// over; a fresh one is waited for at most LockTimeout. Tests shorten them.
var (
	LockTimeout   = 2 * time.Second
	LockStaleness = 10 * time.Second
)

// ErrLockTimeout is returned when the account's config lock stayed held.
var ErrLockTimeout = errors.New("the account's .claude.json is locked")

// Mirror sets the top-level mcpServers of <configDir>/.claude.json to the
// top-level mcpServers of <home>/.claude.json (REQ-SMC-004). It reports
// whether the account file was written. A nil error with false means the
// account was already in sync; a non-nil error is a short reason the account
// was left untouched (fail-open: the caller launches anyway).
func Mirror(home, configDir string) (bool, error) {
	if home == "" || configDir == "" {
		return false, errors.New("no home or config dir")
	}
	sourcePath := filepath.Join(home, configName)
	configPath := filepath.Join(configDir, configName)
	marker := filepath.Join(configDir, MarkerName)

	src, err := readSource(sourcePath)
	if err != nil {
		return false, err
	}
	// The built-in file itself is never a mirror target.
	if sameFile(sourcePath, configPath) {
		return false, errors.New("the account is the built-in account")
	}
	acc, err := readAccount(configPath)
	if err != nil {
		return false, err
	}
	if serversEqual(acc.servers, src.servers) && exists(marker) {
		return false, nil
	}

	// Claude Code's own lock on the file (proper-lockfile: <file>.lock), so the
	// splice never interleaves with a running claude's config writes.
	release, err := lock(configPath+".lock", LockTimeout, LockStaleness)
	if err != nil {
		return false, err
	}
	defer release()

	// Re-read both sides under the lock: a writer that waited here must not
	// clobber a newer mirror with its stale pre-lock snapshot.
	if src, err = readSource(sourcePath); err != nil {
		return false, err
	}
	if acc, err = readAccount(configPath); err != nil {
		return false, err
	}
	if serversEqual(acc.servers, src.servers) {
		return false, ensureMarker(marker)
	}
	if !exists(marker) {
		displaced := map[string]json.RawMessage{}
		for name, value := range acc.servers {
			if v, ok := src.servers[name]; !ok || !rawEqual(v, value) {
				displaced[name] = value
			}
		}
		if len(displaced) > 0 {
			if err := stashDisplaced(filepath.Join(configDir, DisplacedName), displaced); err != nil {
				return false, err // never destroy the only copy
			}
		}
	}
	if len(src.servers) > 0 {
		acc.doc.set(mcpKey, src.raw)
	} else {
		// Claude itself strips default-valued keys; match it.
		acc.doc.remove(mcpKey)
	}
	if err := atomicWrite(configPath, acc.doc.encode(), acc.mode); err != nil {
		return false, errors.New("could not write the account's .claude.json")
	}
	// Only after a successful write: an account whose marker fails to land
	// retries next launch, by then in sync so nothing is mis-stashed.
	_ = ensureMarker(marker)
	return true, nil
}

// source is the built-in account's top-level mcpServers: raw bytes (copied
// as they are) and the decoded entries.
type source struct {
	raw     json.RawMessage
	servers map[string]json.RawMessage
}

// readSource reads the built-in config. A readable object without mcpServers
// is "no user servers" ({}), which propagates; anything unusable is an error
// so the account is left untouched.
func readSource(path string) (source, error) {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return source{}, errors.New("built-in .claude.json not found")
		}
		return source{}, errors.New("built-in .claude.json is not readable")
	}
	// Reading a FIFO would hang the launch.
	if !st.Mode().IsRegular() {
		return source{}, errors.New("built-in .claude.json is not a regular file")
	}
	doc, err := readObject(path)
	if err != nil {
		return source{}, fmt.Errorf("built-in %w", err)
	}
	raw, ok := doc.get(mcpKey)
	if !ok {
		return source{raw: json.RawMessage("{}"), servers: map[string]json.RawMessage{}}, nil
	}
	servers, ok := decodeServers(raw)
	if !ok {
		return source{}, errors.New("built-in .claude.json mcpServers is not an object")
	}
	return source{raw: raw, servers: servers}, nil
}

// account is the named account's config: the whole document, its mcpServers
// ({} when absent) and the file's mode.
type account struct {
	doc     *object
	servers map[string]json.RawMessage
	mode    fs.FileMode
}

// readAccount reads the account's config: it must exist and be a regular file
// (not a symlink) holding a JSON object whose mcpServers, if any, is an object.
func readAccount(path string) (account, error) {
	st, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return account{}, errors.New("the account has no .claude.json yet")
		}
		return account{}, errors.New("the account's .claude.json is not readable")
	}
	if !st.Mode().IsRegular() {
		return account{}, errors.New("the account's .claude.json is not a regular file")
	}
	doc, err := readObject(path)
	if err != nil {
		return account{}, fmt.Errorf("the account's %w", err)
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := doc.get(mcpKey); ok {
		if servers, ok = decodeServers(raw); !ok {
			return account{}, errors.New("the account's mcpServers is not an object")
		}
	}
	return account{doc: doc, servers: servers, mode: st.Mode().Perm()}, nil
}

// readObject reads a JSON object of at most MaxConfigSize bytes.
func readObject(path string) (*object, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New(".claude.json is not readable")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigSize+1))
	if err != nil {
		return nil, errors.New(".claude.json is not readable")
	}
	if len(data) > MaxConfigSize {
		return nil, errors.New(".claude.json is larger than 16 MiB")
	}
	doc, err := decodeObject(data)
	if err != nil {
		// The decoder error may quote file content; it is not passed on.
		return nil, errors.New(".claude.json is not a JSON object")
	}
	return doc, nil
}

// decodeServers decodes an mcpServers value; ok is false unless it is a JSON
// object (null included).
func decodeServers(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &out); err != nil || out == nil {
		return nil, false
	}
	return out, true
}

// serversEqual compares two mcpServers maps by JSON value.
func serversEqual(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for name, av := range a {
		bv, ok := b[name]
		if !ok || !rawEqual(av, bv) {
			return false
		}
	}
	return true
}

// rawEqual compares two JSON values by value, not by bytes.
func rawEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// stashDisplaced saves the entries the first mirror displaces, write-once: a
// valid stash left by an earlier interrupted adoption is kept as it is; a
// path holding anything else blocks the mirror.
func stashDisplaced(path string, displaced map[string]json.RawMessage) error {
	if _, err := os.Lstat(path); err == nil {
		if validStash(path) {
			return nil
		}
		return errors.New(DisplacedName + " exists but is not a valid stash")
	}
	data, err := json.MarshalIndent(struct {
		SchemaVersion int                        `json:"schemaVersion"`
		MCPServers    map[string]json.RawMessage `json:"mcpServers"`
	}{1, displaced}, "", "  ")
	if err != nil {
		return errors.New("could not encode the displaced MCP servers")
	}
	if err := atomicWrite(path, append(data, '\n'), 0o600); err != nil {
		return errors.New("could not save the account's MCP servers")
	}
	return nil
}

// validStash reports whether path is a regular file holding a JSON object
// whose mcpServers is an object.
func validStash(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	doc, err := readObject(path)
	if err != nil {
		return false
	}
	raw, ok := doc.get(mcpKey)
	if !ok {
		return false
	}
	_, ok = decodeServers(raw)
	return ok
}

// ensureMarker creates the adoption marker when it is missing.
func ensureMarker(path string) error {
	if exists(path) {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("could not write " + MarkerName)
	}
	return f.Close()
}

// atomicWrite writes data to a temp file in path's directory, fsyncs it, sets
// mode, and renames it over path.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".lana-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// lock takes the proper-lockfile directory lock lockDir (cswap
// claude_locks.py proper_lockfile): mkdir; a lock whose mtime is older than
// staleness is taken over; a fresh one is waited for at most timeout. While
// held, its mtime is refreshed so other holders do not deem it stale. The
// returned func removes it.
func lock(lockDir string, timeout, staleness time.Duration) (func(), error) {
	start := time.Now()
	for {
		err := os.Mkdir(lockDir, 0o777)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, errors.New("could not lock the account's .claude.json")
		}
		if time.Since(start) > timeout {
			return nil, ErrLockTimeout
		}
		st, err := os.Stat(lockDir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// A dangling symlink answers "exists" to mkdir and "not
				// found" to stat; back off so this cannot spin hot.
				nap(declineBackoff, start, timeout)
				continue
			}
			return nil, errors.New("could not lock the account's .claude.json")
		}
		if time.Since(st.ModTime()) > staleness {
			// Dead holder per the protocol: remove and retake.
			if !removeIfStillStale(lockDir, staleness) {
				nap(declineBackoff, start, timeout)
			}
			continue
		}
		nap(250*time.Millisecond+rand.N(250*time.Millisecond), start, timeout)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(touchInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				now := time.Now()
				if err := os.Chtimes(lockDir, now, now); errors.Is(err, fs.ErrNotExist) {
					return
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		_ = os.Remove(lockDir)
	}, nil
}

// nap sleeps want, but never past what is left of the timeout.
func nap(want time.Duration, start time.Time, timeout time.Duration) {
	left := timeout - time.Since(start)
	if left > 0 {
		time.Sleep(min(want, left))
	}
}

// removeIfStillStale removes lockDir when it is still stale, serialized by the
// takeover directory <lockDir>.takeover so two waiters that saw the same dead
// holder cannot remove each other's fresh lock. It reports whether this call
// removed it.
func removeIfStillStale(lockDir string, staleness time.Duration) bool {
	takeover := lockDir + takeoverSuffix
	if err := os.Mkdir(takeover, 0o777); err != nil {
		if errors.Is(err, fs.ErrExist) {
			if st, err := os.Lstat(takeover); err == nil && time.Since(st.ModTime()) > takeoverStaleness {
				_ = os.Remove(takeover) // its holder died mid-takeover
			}
		}
		return false
	}
	defer func() { _ = os.Remove(takeover) }()
	// Re-check inside: the waiter before us may already have removed the dead
	// lock and taken a fresh one at the same path.
	st, err := os.Stat(lockDir)
	if err != nil || time.Since(st.ModTime()) <= staleness {
		return false
	}
	return os.Remove(lockDir) == nil
}
