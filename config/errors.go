package config

import (
	"fmt"
	"sort"
	"strings"
)

// Warning is a notice that does not make Load fail.
type Warning struct {
	File string
	Msg  string
}

// Error is a single validation error.
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

// Errors is the collection of all errors from all layers.
type Errors []Error

func (e Errors) Error() string {
	parts := make([]string, len(e))
	for i := range e {
		parts[i] = e[i].Error()
	}
	return strings.Join(parts, "\n")
}

// Origins maps key paths to layer names.
type Origins map[string]string

// Of returns the layer name for a key path.
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
