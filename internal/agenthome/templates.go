package agenthome

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed templates/*.md
var templateFS embed.FS

// Template is a built-in starting point for a new agent.
type Template struct {
	Name       string
	Tools      []string
	Permission string
}

var templates = map[string]Template{
	"assistant": {Name: "assistant", Permission: "ask", Tools: []string{"web_fetch", "read_file", "glob", "grep", "memory_read", "memory_write", "skill_write"}},
	"worker":    {Name: "worker", Permission: "ask", Tools: []string{"read_file", "write_file", "edit_file", "glob", "grep", "bash", "web_fetch", "memory_read", "memory_write", "skill_write"}},
	"coder":     {Name: "coder", Permission: "ask", Tools: []string{"read_file", "write_file", "edit_file", "glob", "grep", "bash", "memory_read", "memory_write", "skill_write"}},
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
