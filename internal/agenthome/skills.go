package agenthome

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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
	bodyText := strings.Join(body, "\n")
	if utf8.RuneCountInString(bodyText) > skillBodyLimit {
		return Skill{}, fmt.Errorf("body is longer than %d characters", skillBodyLimit)
	}
	return Skill{
		Name:        name,
		Description: description,
		Body:        bodyText,
		Dir:         dir,
	}, nil
}

// GlobalSkillsDir returns the global skills folder under home.
func GlobalSkillsDir(home string) string { return filepath.Join(home, "skills") }

// AgentSkillsDir returns the skills folder of agent under home.
func AgentSkillsDir(home, agent string) string {
	return filepath.Join(home, "agents", agent, "skills")
}

// AgentProposedSkillsDir returns the folder of pending skill proposals of agent
// under home (REQ-DKN-050). It is separate from AgentSkillsDir, so skill_load
// and the prompt never see a proposal.
func AgentProposedSkillsDir(home, agent string) string {
	return filepath.Join(home, "agents", agent, "proposed-skills")
}

// proposeDir returns the proposal folder of one skill name.
func proposeDir(home, agent, name string) string {
	return filepath.Join(AgentProposedSkillsDir(home, agent), name)
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
	if err := WriteFile(path, skillText(name, description, body)); err != nil {
		return false, err
	}
	return created, nil
}

// skillText renders a SKILL.md with the shared frontmatter and body format.
func skillText(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n" + strings.TrimSpace(body) + "\n"
}

// SkillProposalMeta is the origin of a skill proposal (REQ-DKN-050).
type SkillProposalMeta struct {
	SessionID string
	OwnerTurn bool
	// CreatedAt is a Unix time in milliseconds; 0 means now.
	CreatedAt int64
}

// SkillProposal is one pending skill proposal.
type SkillProposal struct {
	Agent       string
	Name        string
	Description string
	Body        string
	SessionID   string
	// Exists reports whether the agent already has a skill with this name.
	Exists    bool
	OwnerTurn bool
	CreatedAt int64
}

// proposalFile is the proposal.json file of a skill proposal.
type proposalFile struct {
	CreatedAt int64  `json:"created_at"`
	SessionID string `json:"session_id"`
	OwnerTurn bool   `json:"owner_turn"`
}

// ProposeSkill stages a skill as a proposal instead of writing it to the
// agent's skills (REQ-DKN-050, REQ-DKN-051). It validates the same way WriteSkill
// does, writes the proposal atomically, and replaces a pending proposal with the
// same name.
func ProposeSkill(home, agent, name, description, body string, meta SkillProposalMeta) error {
	if !ValidSkillName(name) {
		return fmt.Errorf("invalid name %q", name)
	}
	description = strings.TrimSpace(description)
	if description == "" {
		return errors.New("description is required")
	}
	if strings.ContainsAny(description, "\n\r") {
		return errors.New("description must be one line")
	}
	if utf8.RuneCountInString(description) > skillDescriptionLimit {
		return fmt.Errorf("description is longer than %d characters", skillDescriptionLimit)
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("body is required")
	}
	if utf8.RuneCountInString(body) > skillBodyLimit {
		return fmt.Errorf("body is longer than %d characters", skillBodyLimit)
	}
	if !ValidName(agent) {
		return fmt.Errorf("invalid agent name %q", agent)
	}
	createdAt := meta.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().UnixMilli()
	}
	dir := proposeDir(home, agent, name)
	if err := WriteFile(filepath.Join(dir, "SKILL.md"), skillText(name, description, body)); err != nil {
		return err
	}
	data, err := json.Marshal(proposalFile{CreatedAt: createdAt, SessionID: meta.SessionID, OwnerTurn: meta.OwnerTurn})
	if err != nil {
		return err
	}
	return WriteFile(filepath.Join(dir, "proposal.json"), string(data))
}

// SkillProposals reads every pending skill proposal under home, across all
// agents, newest first. A folder that does not parse is skipped (REQ-DKN-060).
func SkillProposals(home string) ([]SkillProposal, error) {
	agents, err := os.ReadDir(filepath.Join(home, "agents"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var proposals []SkillProposal
	for _, agentEntry := range agents {
		if !agentEntry.IsDir() || strings.HasPrefix(agentEntry.Name(), ".") {
			continue
		}
		agent := agentEntry.Name()
		entries, err := os.ReadDir(AgentProposedSkillsDir(home, agent))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			dir := filepath.Join(AgentProposedSkillsDir(home, agent), entry.Name())
			skill, err := ParseSkill(dir)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, "proposal.json"))
			if err != nil {
				continue
			}
			var meta proposalFile
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}
			exists := false
			if info, err := os.Stat(filepath.Join(AgentSkillsDir(home, agent), skill.Name, "SKILL.md")); err == nil && !info.IsDir() {
				exists = true
			}
			proposals = append(proposals, SkillProposal{
				Agent: agent, Name: skill.Name, Description: skill.Description, Body: skill.Body,
				SessionID: meta.SessionID, OwnerTurn: meta.OwnerTurn, CreatedAt: meta.CreatedAt, Exists: exists,
			})
		}
	}
	sort.Slice(proposals, func(i, j int) bool {
		if proposals[i].CreatedAt != proposals[j].CreatedAt {
			return proposals[i].CreatedAt > proposals[j].CreatedAt
		}
		if proposals[i].Agent != proposals[j].Agent {
			return proposals[i].Agent < proposals[j].Agent
		}
		return proposals[i].Name < proposals[j].Name
	})
	return proposals, nil
}

// ApproveSkill writes a pending proposal as a real agent skill and removes the
// proposal folder. A missing proposal is an os.ErrNotExist-wrapping error
// (REQ-DKN-051).
func ApproveSkill(home, agent, name string) error {
	if !ValidName(agent) || !ValidSkillName(name) {
		return fmt.Errorf("invalid proposal %q", name)
	}
	dir := proposeDir(home, agent, name)
	skill, err := ParseSkill(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("skill proposal %s/%s: %w", agent, name, os.ErrNotExist)
		}
		return err
	}
	if _, err := WriteSkill(home, agent, skill.Name, skill.Description, skill.Body); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// RejectSkill drops a pending skill proposal. A missing proposal is an
// os.ErrNotExist-wrapping error (REQ-DKN-051).
func RejectSkill(home, agent, name string) error {
	if !ValidName(agent) || !ValidSkillName(name) {
		return fmt.Errorf("invalid proposal %q", name)
	}
	dir := proposeDir(home, agent, name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("skill proposal %s/%s: %w", agent, name, os.ErrNotExist)
		}
		return err
	}
	return os.RemoveAll(dir)
}
