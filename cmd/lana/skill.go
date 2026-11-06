package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

func cmdSkill(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: use lana skill list|new <name>|show <name> [--agent <name>]")
		return 2
	}
	switch args[0] {
	case "list":
		return skillList()
	case "new":
		return skillNew(args[1:])
	case "show":
		return skillShow(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown skill command %q (use list, new, show)\n", args[0])
	return 2
}

// skillList prints the global skills and the skills of every configured agent.
func skillList() int {
	home, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	lines := 0
	global, globalInvalid := agenthome.ReadSkills(agenthome.GlobalSkillsDir(home), "global")
	lines += printSkills(global)
	lines += printInvalidSkills(globalInvalid, "global")
	for _, agent := range agentNames(cfg) {
		scope := "agent:" + agent
		skills, invalid := agenthome.ReadSkills(agenthome.AgentSkillsDir(home, agent), scope)
		lines += printSkills(skills)
		lines += printInvalidSkills(invalid, scope)
	}
	if lines == 0 {
		fmt.Println("no skills")
	}
	return 0
}

// printSkills prints the valid skills and returns the number of lines.
func printSkills(skills []agenthome.Skill) int {
	for _, skill := range skills {
		fmt.Printf("%s  %s  %s\n", skill.Name, skill.Scope, skill.Description)
	}
	return len(skills)
}

// printInvalidSkills prints the invalid skills and returns the number of lines.
func printInvalidSkills(invalid []agenthome.InvalidSkill, scope string) int {
	for _, item := range invalid {
		fmt.Printf("%s  %s  invalid: %v\n", filepath.Base(item.Path), scope, item.Err)
	}
	return len(invalid)
}

// skillNew creates a global skill with placeholder frontmatter and body.
func skillNew(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "error: use lana skill new <name>")
		return 2
	}
	name := args[0]
	if !agenthome.ValidSkillName(name) {
		fmt.Fprintf(os.Stderr, "error: invalid skill name %q: use a-z, 0-9, - and _, start with a letter, max 64 characters\n", name)
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	path := filepath.Join(agenthome.GlobalSkillsDir(home), name, "SKILL.md")
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "error: skill %q already exists\n", name)
		return 2
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err, 1)
	}
	text := fmt.Sprintf("---\nname: %s\ndescription: TODO: describe when to use this skill\n---\n# %s\n\nSteps:\n1. \n", name, name)
	if err := agenthome.WriteFile(path, text); err != nil {
		return fail(err, 1)
	}
	fmt.Printf("created %s\n", path)
	fmt.Printf("add %q to skills = [...] of an agent to use it\n", name)
	return 0
}

// skillShow prints the raw SKILL.md of a global or agent-visible skill.
func skillShow(args []string) int {
	fs := flag.NewFlagSet("skill show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agentFlag := fs.String("agent", "", "agent name")
	flags, positional := splitArgs(args, map[string]bool{"agent": true})
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "error: use lana skill show <name> [--agent <name>]")
		return 2
	}
	name := positional[0]

	if *agentFlag == "" {
		home, err := homeDir()
		if err != nil {
			return fail(err, 1)
		}
		if !agenthome.ValidSkillName(name) {
			fmt.Fprintf(os.Stderr, "error: unknown skill %q\n", name)
			return 2
		}
		path := filepath.Join(agenthome.GlobalSkillsDir(home), name, "SKILL.md")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "error: unknown skill %q\n", name)
			return 2
		}
		if err != nil {
			return fail(err, 1)
		}
		fmt.Print(string(data))
		return 0
	}

	home, cfg, code := loadConfigForAgent()
	if code != 0 {
		return code
	}
	agent, ok := cfg.Agents[*agentFlag]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unknown agent %q\n", *agentFlag)
		return 2
	}
	skills, _ := agenthome.VisibleSkills(home, *agentFlag, agent.Skills)
	for _, skill := range skills {
		if skill.Name != name {
			continue
		}
		data, err := os.ReadFile(filepath.Join(skill.Dir, "SKILL.md"))
		if err != nil {
			return fail(err, 1)
		}
		fmt.Print(string(data))
		return 0
	}
	fmt.Fprintf(os.Stderr, "error: unknown skill %q\n", name)
	return 2
}

// skillWarnings returns config check warnings for invalid global and agent skills.
func skillWarnings(home string, cfg core.Config) []string {
	var out []string
	_, invalid := agenthome.ReadSkills(agenthome.GlobalSkillsDir(home), "global")
	out = appendInvalidWarnings(out, invalid)
	for _, agent := range agentNames(cfg) {
		_, invalid := agenthome.ReadSkills(agenthome.AgentSkillsDir(home, agent), "agent")
		out = appendInvalidWarnings(out, invalid)
	}
	return out
}

func appendInvalidWarnings(out []string, invalid []agenthome.InvalidSkill) []string {
	for _, item := range invalid {
		out = append(out, fmt.Sprintf("· warning: skill %s: %v", item.Path, item.Err))
	}
	return out
}
