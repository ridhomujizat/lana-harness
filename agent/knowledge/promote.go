package knowledge

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// promotion records one journal file that promotion compiled (design §7).
type promotion struct {
	Path string
	Ops  int
}

// promote compiles the journal text added since the last promotion into wiki
// pages (REQ-KNU-021). Only the new bytes are sent; the promote mark advances to
// the file size on success. It returns the per-file operation counts for the
// log entry.
func (s *Service) promote(ctx context.Context) ([]promotion, error) {
	agentName, _ := knowledgeAgent(s.Config)
	if agentName == "" {
		// REQ-KNU-030: without a knowledge agent promotion is skipped.
		return nil, nil
	}
	vault, idx, err := s.open()
	if err != nil {
		return nil, err
	}
	files, err := journalFiles(vault.Root())
	if err != nil {
		return nil, err
	}
	var (
		out  []promotion
		errs []error
	)
	for _, rel := range files {
		offset, err := idx.PromoteOffset(rel)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		abs := filepath.Join(vault.Root(), filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		size := info.Size()
		if size < offset {
			offset = 0
		}
		if size <= offset {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if offset > int64(len(data)) {
			offset = 0
		}
		text := string(data[offset:])
		if strings.TrimSpace(text) == "" {
			continue
		}
		day := strings.TrimSuffix(filepath.Base(rel), ".md")
		res, err := s.compile(ctx, compileInput{
			Text:        text,
			SourceLink:  "[[journal/" + day + "]]",
			SourceTitle: day,
			SourcePage:  false,
			Agent:       agentName,
			Actor:       "promotion",
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		if err := idx.SetPromoteOffset(rel, size); err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, promotion{Path: rel, Ops: len(res.Ops)})
	}
	return out, errors.Join(errs...)
}

// journalFiles lists the journal markdown files, sorted by path
// (design §7).
func journalFiles(root string) ([]string, error) {
	dir := filepath.Join(root, "journal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		out = append(out, "journal/"+entry.Name())
	}
	sort.Strings(out)
	return out, nil
}
