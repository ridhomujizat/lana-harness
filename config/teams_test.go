package config

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// teamTestAgents is the agent roster shared by most team tests. The names are
// sorted in the "(available: ...)" messages.
var teamTestAgents = []string{"lead", "researcher", "writer"}

// teamsConfig builds a config.toml with the given agent tables followed by
// body.
func teamsConfig(agents []string, body string) string {
	var b strings.Builder
	for _, name := range agents {
		fmt.Fprintf(&b, "[agents.%s]\n", name)
	}
	b.WriteString(body)
	return b.String()
}

// checkTeams runs config.Check with text as the home config.toml layer. On a
// validation failure it returns the collected Errors.
func checkTeams(t *testing.T, text string) (Result, Errors) {
	t.Helper()
	res, err := Check(context.Background(), t.TempDir(), []byte(text))
	if err == nil {
		return res, nil
	}
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	return Result{}, errs
}

// TestTeamsValid covers AC-TEAM-001: a valid team loads into the effective
// config with its lead, members, and description.
func TestTeamsValid(t *testing.T) {
	text := teamsConfig(teamTestAgents, `
[teams.research-squad]
lead = "lead"
members = ["researcher", "writer"]
description = "Research a topic and write a short digest"
`)
	res, errs := checkTeams(t, text)
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	team, ok := res.Config.Teams["research-squad"]
	if !ok {
		t.Fatalf("team research-squad not loaded: %+v", res.Config.Teams)
	}
	if team.Lead != "lead" {
		t.Fatalf("lead = %q, want %q", team.Lead, "lead")
	}
	if got, want := team.Members, []string{"researcher", "writer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	if team.Description != "Research a topic and write a short digest" {
		t.Fatalf("description = %q", team.Description)
	}
}

// TestTeamsBoundaries accepts the largest allowed members list and description.
func TestTeamsBoundaries(t *testing.T) {
	members := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}
	text := teamsConfig(append([]string{"lead"}, members...), fmt.Sprintf(`[teams.research-squad]
lead = "lead"
members = [%s]
description = %q
`, quoteList(members), strings.Repeat("é", 120)))
	if _, errs := checkTeams(t, text); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

// TestTeamsValidation covers AC-TEAM-003: one case per row of the API-TEAM-001
// validation table.
func TestTeamsValidation(t *testing.T) {
	nineMembers := []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9"}
	tests := []struct {
		name string
		toml string
		key  string
		msg  string
	}{
		{
			name: "teams not a table",
			toml: "teams = \"x\"\n",
			key:  "teams",
			msg:  "must be table, got string",
		},
		{
			name: "name does not match the rule",
			toml: teamsConfig(teamTestAgents, `[teams."Bad-Name"]
lead = "lead"
members = ["researcher"]
`),
			key: "teams.Bad-Name",
			msg: "name must match [a-z][a-z0-9_-]{0,31}",
		},
		{
			name: "name clash with an agent",
			toml: teamsConfig([]string{"lead", "researcher", "writer", "coder"}, `[teams.coder]
lead = "lead"
members = ["researcher"]
`),
			key: "teams.coder",
			msg: "name is already used by an agent",
		},
		{
			name: "team value not a table",
			toml: teamsConfig(teamTestAgents, `[teams]
coder = "x"
`),
			key: "teams.coder",
			msg: "must be table, got string",
		},
		{
			name: "unknown key with suggestion",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["researcher"]
member = ["writer"]
`),
			key: "teams.research-squad.member",
			msg: "unknown key (did you mean: members?)",
		},
		{
			name: "lead missing",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
members = ["researcher"]
`),
			key: "teams.research-squad.lead",
			msg: "is required",
		},
		{
			name: "lead not a string",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = 1
members = ["researcher"]
`),
			key: "teams.research-squad.lead",
			msg: "must be string, got integer",
		},
		{
			name: "lead names no agent",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "ghost"
members = ["researcher"]
`),
			key: "teams.research-squad.lead",
			msg: "agent 'ghost' is not registered (available: lead, researcher, writer)",
		},
		{
			name: "lead names a team",
			toml: teamsConfig(teamTestAgents, `[teams.other-team]
lead = "lead"
members = ["researcher"]

[teams.research-squad]
lead = "other-team"
members = ["writer"]
`),
			key: "teams.research-squad.lead",
			msg: "is a team; a team lead must be an agent",
		},
		{
			name: "members missing",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
`),
			key: "teams.research-squad.members",
			msg: "is required",
		},
		{
			name: "members not an array of strings",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = "researcher"
`),
			key: "teams.research-squad.members",
			msg: "must be array string, got string",
		},
		{
			name: "members empty",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = []
`),
			key: "teams.research-squad.members",
			msg: "must list at least one agent",
		},
		{
			name: "members more than eight",
			toml: teamsConfig(append([]string{"lead"}, nineMembers...), fmt.Sprintf(`[teams.research-squad]
lead = "lead"
members = [%s]
`, quoteList(nineMembers))),
			key: "teams.research-squad.members",
			msg: "must list at most 8 agents",
		},
		{
			name: "member names no agent",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["ghost"]
`),
			key: "teams.research-squad.members.0",
			msg: "agent 'ghost' is not registered (available: lead, researcher, writer)",
		},
		{
			name: "member names a team",
			toml: teamsConfig(teamTestAgents, `[teams.other-team]
lead = "lead"
members = ["researcher"]

[teams.research-squad]
lead = "lead"
members = ["other-team"]
`),
			key: "teams.research-squad.members.0",
			msg: "is a team; teams cannot contain teams",
		},
		{
			name: "member listed twice",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["researcher", "researcher", "writer"]
`),
			key: "teams.research-squad.members.1",
			msg: "agent 'researcher' is listed twice",
		},
		{
			name: "member equals lead",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "researcher"
members = ["researcher", "writer"]
`),
			key: "teams.research-squad.members.0",
			msg: "is the lead; the lead is not listed in members",
		},
		{
			name: "description not a string",
			toml: teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["researcher"]
description = 1
`),
			key: "teams.research-squad.description",
			msg: "must be string, got integer",
		},
		{
			name: "description over 120 characters",
			toml: teamsConfig(teamTestAgents, fmt.Sprintf(`[teams.research-squad]
lead = "lead"
members = ["researcher"]
description = %q
`, strings.Repeat("a", 121))),
			key: "teams.research-squad.description",
			msg: "must be at most 120 characters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, errs := checkTeams(t, tt.toml)
			if errs == nil {
				t.Fatalf("no error; config = %+v", res.Config.Teams)
			}
			found := findError(errs, tt.key)
			if found == nil {
				t.Fatalf("no error for %s: %v", tt.key, errs)
			}
			if found.Msg != tt.msg {
				t.Fatalf("msg = %q, want %q", found.Msg, tt.msg)
			}
		})
	}
}

// TestTeamsSubagentNamesTeam covers AC-TEAM-004.
func TestTeamsSubagentNamesTeam(t *testing.T) {
	text := `
[agents.lead]
[agents.researcher]
[agents.a]
subagents = ["research-squad"]

[teams.research-squad]
lead = "lead"
members = ["researcher"]
`
	_, errs := checkTeams(t, text)
	if errs == nil {
		t.Fatal("no error")
	}
	found := findError(errs, "agents.a.subagents.0")
	if found == nil {
		t.Fatalf("no error for agents.a.subagents.0: %v", errs)
	}
	if want := "is a team; subagents must be agents"; found.Msg != want {
		t.Fatalf("msg = %q, want %q", found.Msg, want)
	}
}

// TestTeamsRouteNamesTeam covers the accepted half of AC-TEAM-005.
func TestTeamsRouteNamesTeam(t *testing.T) {
	text := teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["researcher"]

[[routes]]
channel = "telegram"
agent = "research-squad"
`)
	if _, errs := checkTeams(t, text); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

// TestTeamsRouteUnknownListsTeams covers the rejected half of AC-TEAM-005: the
// message lists the available agents and teams.
func TestTeamsRouteUnknownListsTeams(t *testing.T) {
	text := teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["researcher"]

[[routes]]
channel = "telegram"
agent = "ghost"
`)
	_, errs := checkTeams(t, text)
	if errs == nil {
		t.Fatal("no error")
	}
	found := findError(errs, "routes.0.agent")
	if found == nil {
		t.Fatalf("no error for routes.0.agent: %v", errs)
	}
	want := "agent 'ghost' is not registered (available: lead, research-squad, researcher, writer)"
	if found.Msg != want {
		t.Fatalf("msg = %q, want %q", found.Msg, want)
	}
}

// TestTeamsMergeAcrossLayers confirms that teams merge across config layers
// like agents: the later layer replaces the team's values.
func TestTeamsMergeAcrossLayers(t *testing.T) {
	base := teamsConfig(teamTestAgents, `[teams.research-squad]
lead = "lead"
members = ["researcher"]
description = "base"
`)
	override := `[teams.research-squad]
lead = "lead"
members = ["writer", "researcher"]
description = "override"
`
	res, err := Load(context.Background(), Options{
		Layers: []Layer{
			{Name: "base", TOML: []byte(base)},
			{Name: "override", TOML: []byte(override)},
		},
		Env: envMap(nil),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	team, ok := res.Config.Teams["research-squad"]
	if !ok {
		t.Fatalf("team not loaded: %+v", res.Config.Teams)
	}
	if team.Description != "override" {
		t.Fatalf("description = %q, want override", team.Description)
	}
	if want := []string{"writer", "researcher"}; !reflect.DeepEqual(team.Members, want) {
		t.Fatalf("members = %v, want %v", team.Members, want)
	}
}

// quoteList renders names as a TOML array of basic strings.
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return strings.Join(quoted, ", ")
}
