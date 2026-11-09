package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// rowScanner is the common Scan surface of *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

const workflowColumns = "id, name, description, guardian, status, version, lock_json, failure_json, webhook_token, consecutive_failures, created_at, updated_at"

const runColumns = "id, workflow_id, number, version, trigger, input_json, status, waiting_reason, wake_at, error, tokens, nodes_done, nodes_total, summary, overrides_json, started_at, ended_at"

const nodeRunColumns = "run_id, node_id, item_index, status, attempts_json, input_json, output_json, port, error, logs_json, tokens, session_id, started_at, ended_at, via"

const decisionColumns = "id, workflow_id, run_id, run_number, node_id, item_index, kind, action, reason, fix_json, persistent, confidence, tokens, status, choice, created_at, expires_at, resolved_at"

// rawText renders a raw JSON message as the text of a column, mapping an empty
// message to the JSON literal null.
func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

// parseRaw turns a column value back into a raw JSON message, mapping the empty
// string and the JSON literal null to nil.
func parseRaw(text string) json.RawMessage {
	if text == "" || text == "null" {
		return nil
	}
	return json.RawMessage(text)
}

// newRunID returns a run id of the form "r_" followed by 16 random hex chars.
func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "r_" + hex.EncodeToString(b[:]), nil
}

func scanWorkflow(row rowScanner) (workflow.Workflow, error) {
	var w workflow.Workflow
	var status, lockJSON, failureJSON string
	if err := row.Scan(&w.ID, &w.Name, &w.Description, &w.Guardian, &status, &w.Version, &lockJSON, &failureJSON, &w.WebhookToken, &w.ConsecutiveFailures, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return workflow.Workflow{}, err
	}
	w.Status = workflow.Status(status)
	if lockJSON != "" && lockJSON != "null" {
		var lock workflow.Lock
		if err := json.Unmarshal([]byte(lockJSON), &lock); err != nil {
			return workflow.Workflow{}, err
		}
		w.Lock = &lock
	}
	if failureJSON != "" {
		if err := json.Unmarshal([]byte(failureJSON), &w.FailureDefault); err != nil {
			return workflow.Workflow{}, err
		}
	}
	return w, nil
}

func scanRun(row rowScanner) (workflow.Run, error) {
	var r workflow.Run
	var input, overrides string
	if err := row.Scan(&r.ID, &r.WorkflowID, &r.Number, &r.Version, &r.Trigger, &input, &r.Status, &r.WaitingReason, &r.WakeAt, &r.Error, &r.Tokens, &r.NodesDone, &r.NodesTotal, &r.Summary, &overrides, &r.StartedAt, &r.EndedAt); err != nil {
		return workflow.Run{}, err
	}
	r.Input = parseRaw(input)
	if overrides != "" {
		if err := json.Unmarshal([]byte(overrides), &r.Overrides); err != nil {
			return workflow.Run{}, err
		}
	}
	return r, nil
}

func scanNodeRun(row rowScanner) (workflow.NodeRun, error) {
	var nr workflow.NodeRun
	var attempts, input, output, logs string
	if err := row.Scan(&nr.RunID, &nr.NodeID, &nr.ItemIndex, &nr.Status, &attempts, &input, &output, &nr.Port, &nr.Error, &logs, &nr.Tokens, &nr.SessionID, &nr.StartedAt, &nr.EndedAt, &nr.Via); err != nil {
		return workflow.NodeRun{}, err
	}
	if attempts != "" {
		if err := json.Unmarshal([]byte(attempts), &nr.Attempts); err != nil {
			return workflow.NodeRun{}, err
		}
	}
	nr.Input = parseRaw(input)
	nr.Output = parseRaw(output)
	if logs != "" {
		if err := json.Unmarshal([]byte(logs), &nr.Logs); err != nil {
			return workflow.NodeRun{}, err
		}
	}
	return nr, nil
}

func scanDecision(row rowScanner) (workflow.Decision, error) {
	var d workflow.Decision
	var fix string
	var persistent int
	if err := row.Scan(&d.ID, &d.WorkflowID, &d.RunID, &d.RunNumber, &d.NodeID, &d.ItemIndex, &d.Kind, &d.Action, &d.Reason, &fix, &persistent, &d.Confidence, &d.Tokens, &d.Status, &d.Choice, &d.CreatedAt, &d.ExpiresAt, &d.ResolvedAt); err != nil {
		return workflow.Decision{}, err
	}
	d.Persistent = persistent != 0
	if fix != "" {
		if err := json.Unmarshal([]byte(fix), &d.Fix); err != nil {
			return workflow.Decision{}, err
		}
	}
	return d, nil
}

// CreateWorkflow stores a new workflow and its first version (REQ-WF-001). The
// id is checked inside the write transaction, so a duplicate returns ErrExists
// instead of a constraint error.
func (s *Store) CreateWorkflow(ctx context.Context, w workflow.Workflow, g workflow.Graph, author, summary string) (workflow.Workflow, error) {
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		var exists int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflows WHERE id = ?", w.ID).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return workflow.ErrExists
		}
		lockJSON := ""
		if w.Lock != nil {
			b, err := json.Marshal(w.Lock)
			if err != nil {
				return err
			}
			lockJSON = string(b)
		}
		failureJSON, err := json.Marshal(w.FailureDefault)
		if err != nil {
			return err
		}
		graphJSON, err := json.Marshal(g)
		if err != nil {
			return err
		}
		w.Version = 1
		if _, err := conn.ExecContext(ctx, "INSERT INTO workflows (id, name, description, guardian, status, version, lock_json, failure_json, webhook_token, consecutive_failures, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			w.ID, w.Name, w.Description, w.Guardian, string(w.Status), w.Version, lockJSON, string(failureJSON), w.WebhookToken, w.ConsecutiveFailures, w.CreatedAt, w.UpdatedAt); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, "INSERT INTO workflow_versions (workflow_id, version, graph_json, author, summary, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			w.ID, w.Version, string(graphJSON), author, summary, w.CreatedAt)
		return err
	})
	if err != nil {
		return workflow.Workflow{}, err
	}
	return w, nil
}

// GetWorkflow returns one workflow by id (REQ-WF-001).
func (s *Store) GetWorkflow(ctx context.Context, id string) (workflow.Workflow, error) {
	if err := s.assertOpen(); err != nil {
		return workflow.Workflow{}, err
	}
	return s.getWorkflow(ctx, id)
}

func (s *Store) getWorkflow(ctx context.Context, id string) (workflow.Workflow, error) {
	w, err := scanWorkflow(s.db.QueryRowContext(ctx, "SELECT "+workflowColumns+" FROM workflows WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.Workflow{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Workflow{}, err
	}
	return w, nil
}

// ListWorkflows returns every workflow ordered by name (REQ-WF-001).
func (s *Store) ListWorkflows(ctx context.Context) ([]workflow.Workflow, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+workflowColumns+" FROM workflows ORDER BY name, id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateWorkflow writes workflow metadata only; it never changes Version
// (REQ-WF-001).
func (s *Store) UpdateWorkflow(ctx context.Context, w workflow.Workflow) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		lockJSON := ""
		if w.Lock != nil {
			b, err := json.Marshal(w.Lock)
			if err != nil {
				return err
			}
			lockJSON = string(b)
		}
		failureJSON, err := json.Marshal(w.FailureDefault)
		if err != nil {
			return err
		}
		res, err := conn.ExecContext(ctx, "UPDATE workflows SET name = ?, description = ?, guardian = ?, status = ?, lock_json = ?, failure_json = ?, webhook_token = ?, consecutive_failures = ?, updated_at = ? WHERE id = ?",
			w.Name, w.Description, w.Guardian, string(w.Status), lockJSON, string(failureJSON), w.WebhookToken, w.ConsecutiveFailures, w.UpdatedAt, w.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return workflow.ErrNotFound
		}
		return nil
	})
}

// SaveVersion appends a new graph version when base is the current version,
// otherwise it returns ErrVersionConflict; it updates the workflow's Version
// and updated_at in the same transaction (REQ-WF-001).
func (s *Store) SaveVersion(ctx context.Context, id string, base int, g workflow.Graph, author, summary string) (workflow.Version, error) {
	var out workflow.Version
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		var current int
		err := conn.QueryRowContext(ctx, "SELECT version FROM workflows WHERE id = ?", id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return workflow.ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != base {
			return workflow.ErrVersionConflict
		}
		graphJSON, err := json.Marshal(g)
		if err != nil {
			return err
		}
		now := nowMS()
		version := current + 1
		if _, err := conn.ExecContext(ctx, "INSERT INTO workflow_versions (workflow_id, version, graph_json, author, summary, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			id, version, string(graphJSON), author, summary, now); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "UPDATE workflows SET version = ?, updated_at = ? WHERE id = ?", version, now, id); err != nil {
			return err
		}
		out = workflow.Version{WorkflowID: id, Version: version, Graph: g, Author: author, Summary: summary, CreatedAt: now}
		return nil
	})
	if err != nil {
		return workflow.Version{}, err
	}
	return out, nil
}

// GetVersion returns one stored version (REQ-WF-001).
func (s *Store) GetVersion(ctx context.Context, id string, version int) (workflow.Version, error) {
	if err := s.assertOpen(); err != nil {
		return workflow.Version{}, err
	}
	return s.getVersion(ctx, id, version)
}

func (s *Store) getVersion(ctx context.Context, id string, version int) (workflow.Version, error) {
	var v workflow.Version
	var graphJSON string
	err := s.db.QueryRowContext(ctx, "SELECT workflow_id, version, graph_json, author, summary, created_at FROM workflow_versions WHERE workflow_id = ? AND version = ?", id, version).
		Scan(&v.WorkflowID, &v.Version, &graphJSON, &v.Author, &v.Summary, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.Version{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Version{}, err
	}
	if err := json.Unmarshal([]byte(graphJSON), &v.Graph); err != nil {
		return workflow.Version{}, err
	}
	return v, nil
}

// ListVersions returns the versions of a workflow, newest first (REQ-WF-001).
func (s *Store) ListVersions(ctx context.Context, id string) ([]workflow.Version, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT workflow_id, version, graph_json, author, summary, created_at FROM workflow_versions WHERE workflow_id = ? ORDER BY version DESC", id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.Version
	for rows.Next() {
		var v workflow.Version
		var graphJSON string
		if err := rows.Scan(&v.WorkflowID, &v.Version, &graphJSON, &v.Author, &v.Summary, &v.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(graphJSON), &v.Graph); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteWorkflow removes a workflow and relies on the foreign key cascade to
// remove its versions, runs, node runs, decisions, state and chat rows
// (REQ-WF-001). It returns ErrNotFound when the workflow is missing.
func (s *Store) DeleteWorkflow(ctx context.Context, id string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, "DELETE FROM workflows WHERE id = ?", id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return workflow.ErrNotFound
		}
		return nil
	})
}

// CreateRun stores a run and sets its Number to max(number)+1 for the workflow
// in the same write transaction (REQ-WF-007). An empty ID becomes "r_" plus 16
// random hex characters.
func (s *Store) CreateRun(ctx context.Context, r workflow.Run) (workflow.Run, error) {
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		if r.ID == "" {
			id, err := newRunID()
			if err != nil {
				return err
			}
			r.ID = id
		}
		var maxNumber int
		if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(number), 0) FROM workflow_runs WHERE workflow_id = ?", r.WorkflowID).Scan(&maxNumber); err != nil {
			return err
		}
		r.Number = maxNumber + 1
		overrides := ""
		if r.Overrides != nil {
			b, err := json.Marshal(r.Overrides)
			if err != nil {
				return err
			}
			overrides = string(b)
		}
		_, err := conn.ExecContext(ctx, "INSERT INTO workflow_runs (id, workflow_id, number, version, trigger, input_json, status, waiting_reason, wake_at, error, tokens, nodes_done, nodes_total, summary, overrides_json, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			r.ID, r.WorkflowID, r.Number, r.Version, r.Trigger, rawText(r.Input), string(r.Status), r.WaitingReason, r.WakeAt, r.Error, r.Tokens, r.NodesDone, r.NodesTotal, r.Summary, overrides, r.StartedAt, r.EndedAt)
		return err
	})
	if err != nil {
		return workflow.Run{}, err
	}
	return r, nil
}

// UpdateRun writes an existing run (REQ-WF-007). It returns ErrNotFound when
// the run is missing.
func (s *Store) UpdateRun(ctx context.Context, r workflow.Run) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		overrides := ""
		if r.Overrides != nil {
			b, err := json.Marshal(r.Overrides)
			if err != nil {
				return err
			}
			overrides = string(b)
		}
		res, err := conn.ExecContext(ctx, "UPDATE workflow_runs SET version = ?, trigger = ?, input_json = ?, status = ?, waiting_reason = ?, wake_at = ?, error = ?, tokens = ?, nodes_done = ?, nodes_total = ?, summary = ?, overrides_json = ?, started_at = ?, ended_at = ? WHERE id = ?",
			r.Version, r.Trigger, rawText(r.Input), string(r.Status), r.WaitingReason, r.WakeAt, r.Error, r.Tokens, r.NodesDone, r.NodesTotal, r.Summary, overrides, r.StartedAt, r.EndedAt, r.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return workflow.ErrNotFound
		}
		return nil
	})
}

// GetRun returns one run by id (REQ-WF-007).
func (s *Store) GetRun(ctx context.Context, id string) (workflow.Run, error) {
	if err := s.assertOpen(); err != nil {
		return workflow.Run{}, err
	}
	return s.getRun(ctx, id)
}

func (s *Store) getRun(ctx context.Context, id string) (workflow.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM workflow_runs WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.Run{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Run{}, err
	}
	return r, nil
}

// GetRunByNumber returns one run by its workflow and run number (REQ-WF-007).
func (s *Store) GetRunByNumber(ctx context.Context, workflowID string, number int) (workflow.Run, error) {
	if err := s.assertOpen(); err != nil {
		return workflow.Run{}, err
	}
	r, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM workflow_runs WHERE workflow_id = ? AND number = ?", workflowID, number))
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.Run{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Run{}, err
	}
	return r, nil
}

// ListRuns returns the runs matching the filter, newest first (REQ-WF-007).
func (s *Store) ListRuns(ctx context.Context, f workflow.RunFilter) ([]workflow.Run, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	query := "SELECT " + runColumns + " FROM workflow_runs WHERE 1=1"
	args := []any{}
	if f.WorkflowID != "" {
		query += " AND workflow_id = ?"
		args = append(args, f.WorkflowID)
	}
	if len(f.Statuses) > 0 {
		placeholders := make([]string, len(f.Statuses))
		for i, status := range f.Statuses {
			placeholders[i] = "?"
			args = append(args, string(status))
		}
		query += " AND status IN (" + strings.Join(placeholders, ", ") + ")"
	}
	if f.Since > 0 {
		query += " AND started_at >= ?"
		args = append(args, f.Since)
	}
	if f.Before > 0 {
		query += " AND number < ?"
		args = append(args, f.Before)
	}
	query += " ORDER BY started_at DESC, number DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutNodeRun upserts a node run by (run, node, item) (REQ-WF-007).
func (s *Store) PutNodeRun(ctx context.Context, nr workflow.NodeRun) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		attempts, err := json.Marshal(nr.Attempts)
		if err != nil {
			return err
		}
		logs, err := json.Marshal(nr.Logs)
		if err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, "INSERT INTO workflow_node_runs (run_id, node_id, item_index, status, attempts_json, input_json, output_json, port, error, logs_json, tokens, session_id, started_at, ended_at, via) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(run_id, node_id, item_index) DO UPDATE SET status = excluded.status, attempts_json = excluded.attempts_json, input_json = excluded.input_json, output_json = excluded.output_json, port = excluded.port, error = excluded.error, logs_json = excluded.logs_json, tokens = excluded.tokens, session_id = excluded.session_id, started_at = excluded.started_at, ended_at = excluded.ended_at, via = excluded.via",
			nr.RunID, nr.NodeID, nr.ItemIndex, string(nr.Status), string(attempts), rawText(nr.Input), rawText(nr.Output), nr.Port, nr.Error, string(logs), nr.Tokens, nr.SessionID, nr.StartedAt, nr.EndedAt, nr.Via)
		return err
	})
}

// ListNodeRuns returns every node run of one run (REQ-WF-007).
func (s *Store) ListNodeRuns(ctx context.Context, runID string) ([]workflow.NodeRun, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+nodeRunColumns+" FROM workflow_node_runs WHERE run_id = ? ORDER BY started_at, node_id, item_index", runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.NodeRun
	for rows.Next() {
		nr, err := scanNodeRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, nr)
	}
	return out, rows.Err()
}

// PutDecision upserts a decision by id (REQ-WF-008).
func (s *Store) PutDecision(ctx context.Context, d workflow.Decision) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		fix := ""
		if d.Fix != nil {
			b, err := json.Marshal(d.Fix)
			if err != nil {
				return err
			}
			fix = string(b)
		}
		persistent := 0
		if d.Persistent {
			persistent = 1
		}
		_, err := conn.ExecContext(ctx, "INSERT INTO workflow_decisions (id, workflow_id, run_id, run_number, node_id, item_index, kind, action, reason, fix_json, persistent, confidence, tokens, status, choice, created_at, expires_at, resolved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET workflow_id = excluded.workflow_id, run_id = excluded.run_id, run_number = excluded.run_number, node_id = excluded.node_id, item_index = excluded.item_index, kind = excluded.kind, action = excluded.action, reason = excluded.reason, fix_json = excluded.fix_json, persistent = excluded.persistent, confidence = excluded.confidence, tokens = excluded.tokens, status = excluded.status, choice = excluded.choice, created_at = excluded.created_at, expires_at = excluded.expires_at, resolved_at = excluded.resolved_at",
			d.ID, d.WorkflowID, d.RunID, d.RunNumber, d.NodeID, d.ItemIndex, d.Kind, d.Action, d.Reason, fix, persistent, d.Confidence, d.Tokens, d.Status, d.Choice, d.CreatedAt, d.ExpiresAt, d.ResolvedAt)
		return err
	})
}

// GetDecision returns one decision by id (REQ-WF-008).
func (s *Store) GetDecision(ctx context.Context, id string) (workflow.Decision, error) {
	if err := s.assertOpen(); err != nil {
		return workflow.Decision{}, err
	}
	d, err := scanDecision(s.db.QueryRowContext(ctx, "SELECT "+decisionColumns+" FROM workflow_decisions WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.Decision{}, workflow.ErrNotFound
	}
	if err != nil {
		return workflow.Decision{}, err
	}
	return d, nil
}

// ListDecisions returns the decisions matching the filter, newest first
// (REQ-WF-008).
func (s *Store) ListDecisions(ctx context.Context, f workflow.DecisionFilter) ([]workflow.Decision, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	query := "SELECT " + decisionColumns + " FROM workflow_decisions WHERE 1=1"
	args := []any{}
	if f.WorkflowID != "" {
		query += " AND workflow_id = ?"
		args = append(args, f.WorkflowID)
	}
	if f.RunID != "" {
		query += " AND run_id = ?"
		args = append(args, f.RunID)
	}
	if f.Status != "" {
		query += " AND status = ?"
		args = append(args, f.Status)
	}
	query += " ORDER BY created_at DESC, rowid DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.Decision
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListState returns the workflow state entries ordered by key (REQ-WF-006).
func (s *Store) ListState(ctx context.Context, workflowID string) ([]workflow.StateEntry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT key, value_json, updated_at FROM workflow_state WHERE workflow_id = ? ORDER BY key", workflowID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.StateEntry
	for rows.Next() {
		var key, value string
		var entry workflow.StateEntry
		if err := rows.Scan(&key, &value, &entry.UpdatedAt); err != nil {
			return nil, err
		}
		entry.Key = key
		entry.Value = parseRaw(value)
		out = append(out, entry)
	}
	return out, rows.Err()
}

// SetState writes one state entry with the current time (REQ-WF-006).
func (s *Store) SetState(ctx context.Context, workflowID, key string, value json.RawMessage) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, "INSERT INTO workflow_state (workflow_id, key, value_json, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(workflow_id, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at",
			workflowID, key, rawText(value), nowMS())
		return err
	})
}

// DeleteState removes one state entry; an empty key clears all of them
// (REQ-WF-006).
func (s *Store) DeleteState(ctx context.Context, workflowID, key string) error {
	return s.withWrite(ctx, func(conn *sql.Conn) error {
		if key == "" {
			_, err := conn.ExecContext(ctx, "DELETE FROM workflow_state WHERE workflow_id = ?", workflowID)
			return err
		}
		_, err := conn.ExecContext(ctx, "DELETE FROM workflow_state WHERE workflow_id = ? AND key = ?", workflowID, key)
		return err
	})
}

// AppendChat stores one chat message and sets its ID (REQ-WF-001).
func (s *Store) AppendChat(ctx context.Context, m workflow.ChatMessage) (workflow.ChatMessage, error) {
	err := s.withWrite(ctx, func(conn *sql.Conn) error {
		change := ""
		if m.Change != nil {
			b, err := json.Marshal(m.Change)
			if err != nil {
				return err
			}
			change = string(b)
		}
		issues := ""
		if m.Issues != nil {
			b, err := json.Marshal(m.Issues)
			if err != nil {
				return err
			}
			issues = string(b)
		}
		res, err := conn.ExecContext(ctx, "INSERT INTO workflow_chat (workflow_id, role, text, change_json, issues_json, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			m.WorkflowID, m.Role, m.Text, change, issues, m.CreatedAt)
		if err != nil {
			return err
		}
		m.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return workflow.ChatMessage{}, err
	}
	return m, nil
}

// ListChat returns the last limit messages of a workflow, oldest first; a limit
// of 0 returns all of them (REQ-WF-001).
func (s *Store) ListChat(ctx context.Context, workflowID string, limit int) ([]workflow.ChatMessage, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	query := "SELECT id, workflow_id, role, text, change_json, issues_json, created_at FROM workflow_chat WHERE workflow_id = ? ORDER BY id"
	args := []any{workflowID}
	if limit > 0 {
		query = "SELECT id, workflow_id, role, text, change_json, issues_json, created_at FROM workflow_chat WHERE workflow_id = ? ORDER BY id DESC LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []workflow.ChatMessage
	for rows.Next() {
		var m workflow.ChatMessage
		var change, issues string
		if err := rows.Scan(&m.ID, &m.WorkflowID, &m.Role, &m.Text, &change, &issues, &m.CreatedAt); err != nil {
			return nil, err
		}
		if change != "" {
			if err := json.Unmarshal([]byte(change), &m.Change); err != nil {
				return nil, err
			}
		}
		if issues != "" {
			if err := json.Unmarshal([]byte(issues), &m.Issues); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if limit > 0 {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
}

var _ workflow.Store = (*Store)(nil)
