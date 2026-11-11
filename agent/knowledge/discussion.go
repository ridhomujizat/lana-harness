package knowledge

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"
)

// NoteDiscussion appends a room discussion block to agent's domain journal and
// requests a domain promotion (spec 47 REQ-RDS-032). The promotion pass gates
// what becomes a page, as for any journal note.
func (s *Service) NoteDiscussion(ctx context.Context, agent string, at time.Time, block string) error {
	if !s.HasDomain(agent) {
		return errors.New("agent has no knowledge domain")
	}
	vault, idx, err := s.open()
	if err != nil {
		return err
	}
	rel, err := vault.AppendJournalIn(agent, at.Format("2006-01-02"), block)
	if err != nil {
		return err
	}
	if err := idx.SyncFile(filepath.Join(vault.Root(), filepath.FromSlash(rel))); err != nil {
		slog.Warn("knowledge: re-index after discussion failed", "path", rel, "error", err)
	}
	s.PromoteSoon(agent)
	return nil
}
