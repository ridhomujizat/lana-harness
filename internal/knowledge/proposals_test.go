package knowledge

import (
	"database/sql"
	"errors"
	"testing"
)

// REQ-DKN-021, -060: proposals round-trip, filter by agent and status, order
// oldest first (pending across agents newest first), and report the absent
// case.
func TestProposalsCRUDAndOrdering(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	idx := newIndex(t, dir)
	idx.now = (&fakeClock{t: testNow}).now

	base := Proposal{Agent: "dana", FromAgent: "rio", Status: "pending", Op: "{}"}
	p1 := base
	p1.ID, p1.SessionID, p1.Title, p1.CreatedAt = "p_1", "s1", "Gaji", 100
	p2 := base
	p2.ID, p2.FromAgent, p2.Title, p2.OwnerTurn, p2.TurnID, p2.CreatedAt = "p_2", "dana", "Pajak", true, "t1", 200
	p3 := base
	p3.ID, p3.Title, p3.Status, p3.CreatedAt = "p_3", "Rencana", "approved", 300
	for _, p := range []Proposal{p1, p2, p3} {
		if err := idx.AddProposal(p); err != nil {
			t.Fatalf("AddProposal(%s): %v", p.ID, err)
		}
	}

	got, err := idx.GetProposal("p_2")
	if err != nil {
		t.Fatalf("GetProposal: %v", err)
	}
	if got.Title != "Pajak" || !got.OwnerTurn || got.FromAgent != "dana" || got.Agent != "dana" || got.TurnID != "t1" {
		t.Fatalf("proposal = %+v", got)
	}
	if _, err := idx.GetProposal("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetProposal(missing) err = %v, want sql.ErrNoRows", err)
	}

	// All of dana, oldest first.
	all, err := idx.Proposals("dana")
	if err != nil {
		t.Fatalf("Proposals: %v", err)
	}
	if len(all) != 3 || all[0].ID != "p_1" || all[2].ID != "p_3" {
		t.Fatalf("proposals = %+v", all)
	}

	pend, err := idx.Proposals("dana", "pending")
	if err != nil {
		t.Fatalf("Proposals(pending): %v", err)
	}
	if len(pend) != 2 || pend[0].ID != "p_1" || pend[1].ID != "p_2" {
		t.Fatalf("pending = %+v", pend)
	}

	// PendingProposals spans agents, newest first, pending only.
	pending, err := idx.PendingProposals()
	if err != nil {
		t.Fatalf("PendingProposals: %v", err)
	}
	if len(pending) != 2 || pending[0].ID != "p_2" || pending[1].ID != "p_1" {
		t.Fatalf("pending proposals = %+v", pending)
	}

	// SetProposal decides one and rejects an unknown id.
	if err := idx.SetProposal("p_1", "applied", "2 ops", 500); err != nil {
		t.Fatalf("SetProposal: %v", err)
	}
	got, _ = idx.GetProposal("p_1")
	if got.Status != "applied" || got.Result != "2 ops" || got.DecidedAt != 500 {
		t.Fatalf("decided proposal = %+v", got)
	}
	if err := idx.SetProposal("missing", "applied", "", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetProposal(missing) err = %v, want sql.ErrNoRows", err)
	}
	pend, _ = idx.Proposals("dana", "pending")
	if len(pend) != 1 || pend[0].ID != "p_2" {
		t.Fatalf("pending after decide = %+v", pend)
	}
}
