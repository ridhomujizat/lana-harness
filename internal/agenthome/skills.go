package agenthome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	skillDescriptionLimit = 300
	skillBodyLimit        = 20000
)

var skillNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Skill is one skill folder with parsed frontmatter and body.
type Skill struct {
	Name        string
	Description string
	Body        string
	Dir         string
	Scope       string
}

// InvalidSkill is a skill folder that could not be parsed.
type InvalidSkill struct {
	Path string
	Err  error
}

// ValidSkillName reports whether name is a valid skill name.
func ValidSkillName(name string) bool { return skillNameRE.MatchString(name) }

// ParseSkill reads and parses the SKILL.md file in dir.
func ParseSkill(dir string) (Skill, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return Skill{}, errors.New("missing frontmatter")
	}
	close := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			close = i
			break
		}
	}
	if close < 0 {
		return Skill{}, errors.New("missing frontmatter")
	}

	fields := make(map[string]string)
	for _, line := range lines[1:close] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		fields[key] = value
	}

	name := fields["name"]
	if name == "" {
		return Skill{}, errors.New("missing name")
	}
	description := fields["description"]
	if description == "" {
		return Skill{}, errors.New("missing description")
	}
	if !ValidSkillName(name) {
		return Skill{}, fmt.Errorf("invalid name %q", name)
	}
	if folder := filepath.Base(dir); name != folder {
		return Skill{}, fmt.Errorf("name %q does not match folder %q", name, folder)
	}
	if utf8.RuneCountInString(description) > skillDescriptionLimit {
		return Skill{}, fmt.Errorf("description is longer than %d characters", skillDescriptionLimit)
	}

	body := lines[close+1:]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return Skill{
		Name:        name,
		Description: description,
		Body:        strings.Join(body, "\n"),
		Dir:         dir,
	}, nil
}

// GlobalSkillsDir returns the global skills folder under home.
func GlobalSkillsDir(home string) string { return filepath.Join(home, "skills") }

// AgentSkillsDir returns the skills folder of agent under home.
func AgentSkillsDir(home, agent string) string {
	return filepath.Join(home, "agents", agent, "skills")
}

// ReadSkills reads every skill folder in dir and tags the valid ones with scope.
func ReadSkills(dir, scope string) ([]Skill, []InvalidSkill) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []InvalidSkill{{Path: dir, Err: err}}
	}
	var skills []Skill
	var invalid []InvalidSkill
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Stat(filepath.Join(path, "SKILL.md"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			invalid = append(invalid, InvalidSkill{Path: path, Err: err})
			continue
		}
		if info.IsDir() {
			continue
		}
		skill, err := ParseSkill(path)
		if err != nil {
			invalid = append(invalid, InvalidSkill{Path: path, Err: err})
			continue
		}
		skill.Scope = scope
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	sort.Slice(invalid, func(i, j int) bool { return invalid[i].Path < invalid[j].Path })
	return skills, invalid
}

// VisibleSkills returns the global skills in listed plus every agent skill.
func VisibleSkills(home, agent string, listed []string) ([]Skill, []InvalidSkill) {
	global, globalInvalid := ReadSkills(GlobalSkillsDir(home), "global")
	agentSkills, agentInvalid := ReadSkills(AgentSkillsDir(home, agent), "agent")

	allowed := make(map[string]bool, len(listed))
	for _, name := range listed {
		allowed[name] = true
	}
	shadowed := make(map[string]bool, len(agentSkills))
	for _, skill := range agentSkills {
		shadowed[skill.Name] = true
	}

	visible := make([]Skill, 0, len(global)+len(agentSkills))
	for _, skill := range global {
		if allowed[skill.Name] && !shadowed[skill.Name] {
			visible = append(visible, skill)
		}
	}
	visible = append(visible, agentSkills...)
	sort.Slice(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })

	invalid := append(globalInvalid, agentInvalid...)
	return visible, invalid
}

// WriteSkill creates or replaces the SKILL.md of an agent skill.
func WriteSkill(home, agent, name, description, body string) (created bool, err error) {
	if !ValidSkillName(name) {
		return false, fmt.Errorf("invalid name %q", name)
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return false, errors.New("description is required")
	}
	if strings.ContainsAny(description, "\n\r") {
		return false, errors.New("description must be one line")
	}
	if utf8.RuneCountInString(description) > skillDescriptionLimit {
		return false, fmt.Errorf("description is longer than %d characters", skillDescriptionLimit)
	}
	if strings.TrimSpace(body) == "" {
		return false, errors.New("body is required")
	}
	if utf8.RuneCountInString(body) > skillBodyLimit {
		return false, fmt.Errorf("body is longer than %d characters", skillBodyLimit)
	}

	path := filepath.Join(AgentSkillsDir(home, agent), name, "SKILL.md")
	created = true
	if _, err := os.Stat(path); err == nil {
		created = false
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	text := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + strings.TrimSpace(body) + "\n"
	if err := WriteFile(path, text); err != nil {
		return false, err
	}
	return created, nil
}
