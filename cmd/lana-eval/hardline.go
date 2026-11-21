package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/sqlitex"
	"github.com/ridhomujizat/lana-harness/tools/permission"
)

// hardlineTools are the tools the hardline floor covers (spec 63, Scope). The
// replay reads only tool/call events of these names.
var hardlineTools = map[string]bool{
	"bash":       true,
	"write_file": true,
	"edit_file":  true,
	"read_file":  true,
}

// hardlineReplay replays the hardline rules over the recorded tool calls of a
// lana database and prints every call that would have matched, so false
// positives are fixed before they block real work (spec 63, "Rollout"). It is
// read-only: the database is opened with mode=ro, so it is safe to run while
// the daemon holds the database in WAL mode.
func hardlineReplay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hardline-replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaultHome, _ := os.UserHomeDir()
	dbPath := fs.String("db", filepath.Join(defaultHome, ".lana", "lana.db"), "path to the lana SQLite database")
	home := fs.String("home", defaultHome, "owner home directory used for path resolution")
	since := fs.String("since", "", "only replay calls at or after this time (RFC3339 or YYYY-MM-DD)")
	limitArg := fs.Int("limit-arg", 160, "maximum number of runes of the printed argument")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: lana-eval hardline-replay [flags]")
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "Replays the hardline rules over the recorded tool calls of a lana database.")
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	dbFile := expandReplayHome(strings.TrimSpace(*dbPath), defaultHome)
	if _, err := os.Stat(dbFile); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: cannot open %s: %v\n", dbFile, err)
		return 1
	}
	sinceMS, err := parseReplaySince(*since)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: invalid -since: %v\n", err)
		return 1
	}

	ctx := context.Background()
	db, err := sqlitex.Open("file:" + dbFile + "?mode=ro")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: open %s: %v\n", dbFile, err)
		return 1
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: open %s: %v\n", dbFile, err)
		return 1
	}

	query := "SELECT s.agent, s.workspace, e.payload, e.created_at" +
		" FROM session_events e JOIN sessions s ON s.id = e.session_id" +
		" WHERE e.kind = ?"
	queryArgs := []any{core.KindToolCall}
	if sinceMS > 0 {
		query += " AND e.created_at >= ?"
		queryArgs = append(queryArgs, sinceMS)
	}
	query += " ORDER BY e.created_at, e.session_id, e.seq"
	rows, err := db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: read events: %v\n", err)
		return 1
	}

	// lanaHome is the home the session workspaces are created under
	// (createChannelSession). The database lives in it, so its directory is the
	// fallback for a session with no stored workspace.
	lanaHome := filepath.Dir(dbFile)
	type match struct {
		when  int64
		agent string
		rule  string
		tier  string
		tool  string
		arg   string
	}
	var matches []match
	scanned := 0
	for rows.Next() {
		var agent, workspace, payload string
		var createdAt int64
		if err := rows.Scan(&agent, &workspace, &payload, &createdAt); err != nil {
			_ = rows.Close()
			_, _ = fmt.Fprintf(stderr, "error: read events: %v\n", err)
			return 1
		}
		var call core.ToolCallPayload
		if err := json.Unmarshal([]byte(payload), &call); err != nil {
			continue
		}
		if !hardlineTools[call.Name] {
			continue
		}
		scanned++
		ws := replayWorkspace(workspace, agent, lanaHome)
		m := permission.MatchHardline(call.Name, call.Input, ws, *home)
		if m == nil {
			continue
		}
		matches = append(matches, match{
			when:  createdAt,
			agent: agent,
			rule:  m.Rule,
			tier:  m.Tier,
			tool:  call.Name,
			arg:   shortenArg(hardlineArg(call.Name, call.Input), *limitArg),
		})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_, _ = fmt.Fprintf(stderr, "error: read events: %v\n", err)
		return 1
	}
	_ = rows.Close()

	// The query returns events in created_at order, so matches are printed in
	// the order the calls happened.
	for _, m := range matches {
		_, _ = fmt.Fprintf(stdout, "%s  %s  %s  %s  %s  %s\n",
			time.UnixMilli(m.when).UTC().Format(time.RFC3339), m.agent, m.rule, m.tier, m.tool, m.arg)
	}
	counts := map[string]int{}
	for _, m := range matches {
		counts[m.rule]++
	}
	rules := make([]string, 0, len(counts))
	for rule := range counts {
		rules = append(rules, rule)
	}
	sort.Strings(rules)
	if len(matches) > 0 {
		_, _ = fmt.Fprintln(stdout)
	}
	for _, rule := range rules {
		_, _ = fmt.Fprintf(stdout, "%s  %d\n", rule, counts[rule])
	}
	_, _ = fmt.Fprintf(stdout, "%d calls scanned\n", scanned)
	return 0
}

// parseReplaySince turns a -since value into a Unix millisecond bound, or 0
// when it is empty. It accepts RFC3339 and a bare YYYY-MM-DD date (UTC).
func parseReplaySince(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("want RFC3339 or YYYY-MM-DD, got %q", s)
}

// replayWorkspace is the working directory a recorded call is replayed
// against: the session's workspace, else the agent's workspace under the lana
// home, else the current directory.
func replayWorkspace(workspace, agent, lanaHome string) string {
	if strings.TrimSpace(workspace) != "" {
		return workspace
	}
	if agent != "" {
		return filepath.Join(lanaHome, "workspaces", agent)
	}
	return "."
}

// hardlineArg reads the argument the matcher keys on: the command for bash, the
// path for the file tools.
func hardlineArg(tool string, input json.RawMessage) string {
	var value map[string]any
	if err := json.Unmarshal(input, &value); err != nil {
		return ""
	}
	if tool == "bash" {
		command, _ := value["command"].(string)
		return command
	}
	path, _ := value["path"].(string)
	return path
}

// shortenArg caps an argument at limit runes, adding an ellipsis when it is
// longer. A non-positive limit leaves the argument unchanged.
func shortenArg(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// expandReplayHome expands a leading ~ in a flag path.
func expandReplayHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}
