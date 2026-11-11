package knowledge

import (
	"database/sql"
	"errors"
	"fmt"
)

// proposalPending is the status of a proposal no one has decided yet
// (design §2).
const proposalPending = "pending"

// proposalColumns is the column list every proposal query shares.
const proposalColumns = `id, agent, from_agent, session_id, turn_id, owner_turn, op, title, status, result, created_at, decided_at`

// Proposal is one requested change to another agent's domain, stored in the
// index until that agent's promotion or the owner decides it (REQ-DKN-021).
type Proposal struct {
	ID        string
	Agent     string
	FromAgent string
	SessionID string
	TurnID    string
	OwnerTurn bool
	Op        string
	Title     string
	Status    string
	Result    string
	CreatedAt int64
	DecidedAt int64
}

// AddProposal stores one proposal.
func (i *Index) AddProposal(p Proposal) error {
	owner := 0
	if p.OwnerTurn {
		owner = 1
	}
	_, err := i.db.Exec(`
INSERT INTO proposals (id, agent, from_agent, session_id, turn_id, owner_turn, op, title, status, result, created_at, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Agent, p.FromAgent, p.SessionID, p.TurnID, owner, p.Op, p.Title, p.Status, p.Result, p.CreatedAt, p.DecidedAt)
	return err
}

// GetProposal returns one proposal by id. An unknown id wraps sql.ErrNoRows.
func (i *Index) GetProposal(id string) (Proposal, error) {
	p, err := scanProposal(i.db.QueryRow("SELECT "+proposalColumns+" FROM proposals WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Proposal{}, fmt.Errorf("knowledge proposal %s: %w", id, sql.ErrNoRows)
		}
		return Proposal{}, err
	}
	return p, nil
}

// Proposals returns the proposals of one agent, oldest first, optionally
// filtered by status; no statuses means every status (REQ-DKN-060).
func (i *Index) Proposals(agent string, statuses ...string) ([]Proposal, error) {
	query := "SELECT " + proposalColumns + " FROM proposals WHERE agent = ?"
	args := []any{agent}
	if len(statuses) > 0 {
		query += " AND status IN (" + placeholders(len(statuses)) + ")"
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	query += " ORDER BY created_at, id"
	return i.queryProposals(query, args...)
}

// PendingProposals returns every pending proposal of every agent, newest first
// (REQ-DKN-060).
func (i *Index) PendingProposals() ([]Proposal, error) {
	return i.queryProposals("SELECT "+proposalColumns+" FROM proposals WHERE status = ? ORDER BY created_at DESC, id DESC", proposalPending)
}

// DecidedProposals returns every proposal in one of statuses, newest decided
// first, at most limit (REQ-KGR-030). A limit of 0 returns every row.
func (i *Index) DecidedProposals(statuses []string, limit int) ([]Proposal, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query := "SELECT " + proposalColumns + " FROM proposals WHERE status IN (" + placeholders(len(statuses)) + ")"
	args := make([]any, 0, len(statuses)+1)
	for _, status := range statuses {
		args = append(args, status)
	}
	query += " ORDER BY decided_at DESC, id DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	return i.queryProposals(query, args...)
}

// SetProposal moves one proposal to status with a result and a decided time.
// An unknown id wraps sql.ErrNoRows.
func (i *Index) SetProposal(id, status, result string, at int64) error {
	res, err := i.db.Exec("UPDATE proposals SET status = ?, result = ?, decided_at = ? WHERE id = ?", status, result, at, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("knowledge proposal %s: %w", id, sql.ErrNoRows)
	}
	return nil
}

// queryProposals runs a proposal SELECT and scans every row.
func (i *Index) queryProposals(query string, args ...any) ([]Proposal, error) {
	rows, err := i.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// proposalScanner is the Scan surface shared by *sql.Row and *sql.Rows.
type proposalScanner interface {
	Scan(dest ...any) error
}

// scanProposal reads one proposal row in proposalColumns order.
func scanProposal(row proposalScanner) (Proposal, error) {
	var (
		p     Proposal
		owner int
	)
	if err := row.Scan(&p.ID, &p.Agent, &p.FromAgent, &p.SessionID, &p.TurnID, &owner, &p.Op, &p.Title, &p.Status, &p.Result, &p.CreatedAt, &p.DecidedAt); err != nil {
		return Proposal{}, err
	}
	p.OwnerTurn = owner != 0
	return p, nil
}
