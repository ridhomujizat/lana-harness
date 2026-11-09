package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

type secretSource struct {
	env  func(string) (string, bool)
	file *secretFile
}

// secretFile is the secrets.env layer of a secret source. A lookup re-reads
// the file when its modification time or size changed since the last read, so
// a key the dashboard writes reaches the running daemon without a restart
// (REQ-RTR-008). The environment still wins over the file.
type secretFile struct {
	path string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	values  map[string]string
}

// lookup returns the file value of name, reloading the file first when it
// changed. A file that cannot be read or parsed keeps the values of the last
// good read; a removed file has no values.
func (f *secretFile) lookup(name string) (string, bool) {
	if f == nil {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" {
		f.refreshLocked()
	}
	v, ok := f.values[name]
	return v, ok
}

func (f *secretFile) refreshLocked() {
	info, err := os.Stat(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			f.values = map[string]string{}
			f.modTime, f.size = time.Time{}, 0
		}
		return
	}
	if info.ModTime().Equal(f.modTime) && info.Size() == f.size {
		return
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	values, _ := parseSecrets(f.path, data)
	f.values = values
	f.modTime, f.size = info.ModTime(), info.Size()
}

func (s secretSource) Lookup(name string) (core.Secret, bool) {
	if v, ok := s.env(name); ok && v != "" {
		return core.NewSecret(v), true
	}
	if v, ok := s.file.lookup(name); ok && v != "" {
		return core.NewSecret(v), true
	}
	return core.Secret{}, false
}

func (s secretSource) lookupString(name string) (string, bool) {
	v, ok := s.Lookup(name)
	if !ok {
		return "", false
	}
	return v.Reveal(), true
}

func loadSecretSource(home string, env func(string) (string, bool)) (secretSource, []Warning, []Error) {
	src := secretSource{env: env, file: &secretFile{values: map[string]string{}}}
	var warnings []Warning
	if home == "" {
		return src, nil, nil
	}
	path := filepath.Join(home, "secrets.env")
	src.file.path = path
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return src, nil, nil
		}
		return src, nil, []Error{{File: path, Msg: err.Error()}}
	}
	if info.Mode().Perm() != 0o600 {
		msg := fmt.Sprintf("secrets.env has permission %04o (should be 0600); run: chmod 600 %s", info.Mode().Perm(), path)
		warnings = append(warnings, Warning{File: path, Msg: msg})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return src, warnings, []Error{{File: path, Msg: err.Error()}}
	}
	values, errs := parseSecrets(path, data)
	src.file.values = values
	src.file.modTime, src.file.size = info.ModTime(), info.Size()
	return src, warnings, errs
}

// parseSecrets reads the NAME=value lines of secrets.env. Invalid lines are
// reported and skipped.
func parseSecrets(path string, data []byte) (map[string]string, []Error) {
	values := map[string]string{}
	var errs []Error
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			errs = append(errs, Error{File: path, Line: i + 1, Msg: "invalid line: " + trimmed})
			continue
		}
		name := strings.TrimSpace(line[:eq])
		if name == "" {
			errs = append(errs, Error{File: path, Line: i + 1, Msg: "variable name is empty"})
			continue
		}
		value := strings.TrimSpace(line[eq+1:])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		values[name] = value
	}
	return values, errs
}

func interpolateMCP(value map[string]any, secrets secretSource, positions map[string]pos) []Error {
	var errs []Error
	mcpTable, _ := asMap(value["mcp"])
	for _, name := range sortedKeys(mcpTable) {
		server, _ := asMap(mcpTable[name])
		for _, field := range []string{"env", "headers"} {
			table, _ := asMap(server[field])
			for _, key := range sortedKeys(table) {
				raw, ok := table[key].(string)
				if !ok {
					continue
				}
				out, err := expandVars(raw, secrets)
				path := "mcp." + name + "." + field + "." + key
				if err != nil {
					p := positions[path]
					errs = append(errs, Error{Key: path, Line: p.Line, Col: p.Col, Msg: err.Error()})
					continue
				}
				table[key] = out
			}
		}
	}
	return errs
}

func expandVars(s string, secrets secretSource) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("invalid variable ${%s", s[i+2:])
			}
			name := s[i+2 : i+2+end]
			if !validEnvName(name) {
				return "", fmt.Errorf("invalid variable ${%s}", name)
			}
			v, ok := secrets.lookupString(name)
			if !ok {
				return "", fmt.Errorf("variable ${%s} not found", name)
			}
			b.WriteString(v)
			i += 2 + end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !isAlpha(r) {
				return false
			}
			continue
		}
		if r != '_' && !isAlpha(r) && !isDigit(r) {
			return false
		}
	}
	return true
}

func isAlpha(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') }

func isDigit(r rune) bool { return r >= '0' && r <= '9' }
