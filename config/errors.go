package config

import (
	"fmt"
	"sort"
	"strings"
)

// Warning adalah peringatan yang tidak menggagalkan Load.
type Warning struct {
	File string
	Msg  string
}

// Error adalah satu kesalahan validasi.
type Error struct {
	File string
	Line int
	Col  int
	Key  string
	Msg  string
}

func (e Error) Error() string {
	switch {
	case e.File != "" && e.Line > 0 && e.Key != "":
		return fmt.Sprintf("%s:%d: %s: %s", e.File, e.Line, e.Key, e.Msg)
	case e.File != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	case e.File != "" && e.Key != "":
		return fmt.Sprintf("%s: %s: %s", e.File, e.Key, e.Msg)
	case e.Key != "":
		return fmt.Sprintf("%s: %s", e.Key, e.Msg)
	default:
		return e.Msg
	}
}

// Errors adalah kumpulan semua error dari semua lapisan.
type Errors []Error

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i := range e {
		parts[i] = e[i].Error()
	}
	return strings.Join(parts, "\n")
}

// Origins memetakan path key ke nama lapisan.
type Origins map[string]string

// Of mengembalikan nama lapisan untuk path key.
func (o Origins) Of(key string) (string, bool) {
	v, ok := o[key]
	return v, ok
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
