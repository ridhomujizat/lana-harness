package agenthome

import (
	"embed"
	"fmt"
	"slices"
	"sort"
	"strings"
)

//go:embed templates/*.md
var templateFS embed.FS

// Template is a built-in starting point for a new agent. Built-in templates
// share the full built-in tool set and differ only by persona text.
type Template struct {
	Name       string
	Tools      []string
	Permission string
}

// allTools is the full built-in tool set shared by every template.
var allTools = []string{
	"read_file", "write_file", "edit_file", "glob", "grep",
	"bash", "web_fetch", "memory_read", "memory_write", "skill_write", "ask_user",
}

// newTemplate returns the built-in template with the given name.
func newTemplate(name string) Template {
	return Template{Name: name, Permission: "ask", Tools: slices.Clone(allTools)}
}

var templates = map[string]Template{
	"assistant": newTemplate("assistant"),
	"worker":    newTemplate("worker"),
	"coder":     newTemplate("coder"),
}

// TemplateNames returns the template names in sorted order.
func TemplateNames() []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LookupTemplate returns the template with the given name.
func LookupTemplate(name string) (Template, bool) {
	t, ok := templates[name]
	return t, ok
}

// Persona returns the persona text of t for agent name.
func (t Template) Persona(name string) (string, error) {
	data, err := templateFS.ReadFile("templates/" + t.Name + ".md")
	if err != nil {
		return "", fmt.Errorf("template %s: %w", t.Name, err)
	}
	return strings.ReplaceAll(string(data), "{{name}}", name), nil
}
