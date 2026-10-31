package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

var skillLoadSpec = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`)
var skillWriteSpec = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"},"body":{"type":"string"}},"required":["name","description","body"],"additionalProperties":false}`)

type skillLoadTool struct{}
type skillWriteTool struct{}

// skillLoadGuidance is the head-prompt note shown when skill_load is offered
// (REQ-ECX-011). The skill list itself stays in the # Skills section.
const skillLoadGuidance = "Call skill_load with a skill's name before following that skill; the loaded text is the current procedure, and the list in the prompt is only an index."

// NewSkillLoad creates the skill_load tool.
func NewSkillLoad() core.Tool { return &skillLoadTool{} }

// NewSkillWrite creates the skill_write tool.
func NewSkillWrite() core.Tool { return &skillWriteTool{} }

func (t *skillLoadTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "skill_load", Description: "Load a skill by name: its instructions, its folder, and its files. Call this before following a skill listed in the prompt.", InputSchema: skillLoadSpec}
}
func (t *skillLoadTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }

func (t *skillLoadTool) PromptGuidance() string { return skillLoadGuidance }

func (t *skillWriteTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "skill_write", Description: "Create or update one of your own skills (SKILL.md) so you can reuse a procedure that worked. name: lowercase letters, digits, - and _. description: one line saying when to use it.", InputSchema: skillWriteSpec}
}
func (t *skillWriteTool) Risk(json.RawMessage) core.Risk { return core.RiskWrite }

// skillFiles lists the regular files in a skill folder except SKILL.md.
func skillFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (t *skillLoadTool) Call(_ context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Home == "" {
		return errorOutput("skills are not available")
	}
	visible, _ := agenthome.VisibleSkills(tc.Home, tc.AgentName, tc.Skills)
	names := make([]string, 0, len(visible))
	index := -1
	for i, skill := range visible {
		names = append(names, skill.Name)
		if skill.Name == in.Name {
			index = i
		}
	}
	if index < 0 {
		available := "none"
		if len(names) > 0 {
			available = strings.Join(names, ", ")
		}
		return errorOutput(fmt.Sprintf("unknown skill %q (available: %s)", in.Name, available))
	}
	skill := visible[index]
	dir, err := filepath.Abs(skill.Dir)
	if err != nil {
		return errorOutput(err.Error())
	}
	files, err := skillFiles(skill.Dir)
	if err != nil {
		return errorOutput(err.Error())
	}
	list := "none"
	if len(files) > 0 {
		list = strings.Join(files, ", ")
	}
	return successOutput(fmt.Sprintf("skill %s (%s)\nfolder: %s\nfiles: %s\n\n%s", skill.Name, skill.Scope, dir, list, skill.Body))
}

func (t *skillWriteTool) Call(_ context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	if tc.Home == "" || !agenthome.ValidName(tc.AgentName) {
		return errorOutput("skills are not available")
	}
	created, err := agenthome.WriteSkill(tc.Home, tc.AgentName, in.Name, in.Description, in.Body)
	if err != nil {
		return errorOutput("invalid input: " + err.Error())
	}
	if created {
		return successOutput("created skill " + in.Name)
	}
	return successOutput("updated skill " + in.Name)
}
