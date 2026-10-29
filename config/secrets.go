package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

type secretSource struct {
	env  func(string) (string, bool)
	file map[string]string
}

func (s secretSource) Lookup(name string) (core.Secret, bool) {
	if v, ok := s.env(name); ok && v != "" {
		return core.NewSecret(v), true
	}
	if v, ok := s.file[name]; ok && v != "" {
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
	src := secretSource{env: env, file: map[string]string{}}
	var warnings []Warning
	var errs []Error
	if home == "" {
		return src, nil, nil
	}
	path := filepath.Join(home, "secrets.env")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return src, nil, nil
		}
		return src, nil, []Error{{File: path, Msg: err.Error()}}
	}
	if info.Mode().Perm() != 0o600 {
		msg := fmt.Sprintf("secrets.env punya permission %04o (seharusnya 0600); jalankan: chmod 600 %s", info.Mode().Perm(), path)
		warnings = append(warnings, Warning{File: path, Msg: msg})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return src, warnings, []Error{{File: path, Msg: err.Error()}}
	}
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			errs = append(errs, Error{File: path, Line: i + 1, Msg: "baris tidak valid: " + trimmed})
			continue
		}
		name := strings.TrimSpace(line[:eq])
		if name == "" {
			errs = append(errs, Error{File: path, Line: i + 1, Msg: "nama variabel kosong"})
			continue
		}
		value := strings.TrimSpace(line[eq+1:])
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		src.file[name] = value
	}
	return src, warnings, errs
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
				return "", fmt.Errorf("variabel ${%s tidak valid", s[i+2:])
			}
			name := s[i+2 : i+2+end]
			if !validEnvName(name) {
				return "", fmt.Errorf("variabel ${%s} tidak valid", name)
			}
			v, ok := secrets.lookupString(name)
			if !ok {
				return "", fmt.Errorf("variabel ${%s} tidak ditemukan", name)
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
			if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
				return false
			}
			continue
		}
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func modeString(m os.FileMode) string { return strconv.FormatUint(uint64(m.Perm()), 8) }
