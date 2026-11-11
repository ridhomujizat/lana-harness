// Package roommemory keeps the notes of a team room (spec 47 REQ-RDS-030,
// REQ-RDS-040): decisions, facts and open questions in
// <home>/rooms/<team>/memory.json. A replaced note stays, marked with
// valid_until and replaced_by.
package roommemory

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Note kinds.
const (
	KindDecision = "decision"
	KindFact     = "fact"
	KindOpen     = "open"
)

// MaxText is the longest note text, in characters.
const MaxText = 400

// Note is one room memory note (API-RDS-012).
type Note struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Agent      string `json:"agent"`
	Source     string `json:"source"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	ValidUntil int64  `json:"valid_until"`
	ReplacedBy string `json:"replaced_by"`
}

// Current reports whether the note has not been replaced.
func (n Note) Current() bool { return n.ReplacedBy == "" && n.ValidUntil == 0 }

// Errors.
var (
	ErrNotFound = errors.New("note not found")
	ErrInvalid  = errors.New("invalid note")
)

var teamRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Store reads and writes room memory under one home folder.
type Store struct {
	home string
	mu   sync.Mutex
	now  func() time.Time
}

// New returns the store of home. An empty home makes every call fail.
func New(home string) *Store { return &Store{home: home, now: time.Now} }

func (s *Store) path(team string) (string, error) {
	if s == nil || s.home == "" {
		return "", errors.New("room memory has no home folder")
	}
	if !teamRE.MatchString(team) {
		return "", fmt.Errorf("%w: team name", ErrInvalid)
	}
	return filepath.Join(s.home, "rooms", team, "memory.json"), nil
}

func (s *Store) load(team string) ([]Note, error) {
	p, err := s.path(team)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return []Note{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Notes []Note `json:"notes"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if file.Notes == nil {
		file.Notes = []Note{}
	}
	return file.Notes, nil
}

func (s *Store) save(team string, notes []Note) error {
	p, err := s.path(team)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(struct {
		Notes []Note `json:"notes"`
	}{notes}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// List returns every note of team, newest first.
func (s *Store) List(team string) ([]Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notes, err := s.load(team)
	if err != nil {
		return nil, err
	}
	sortNewest(notes)
	return notes, nil
}

func sortNewest(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].CreatedAt > notes[j].CreatedAt })
}

// Check validates kind and text.
func Check(kind, text string) error {
	switch kind {
	case KindDecision, KindFact, KindOpen:
	default:
		return fmt.Errorf("%w: kind must be decision, fact or open", ErrInvalid)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("%w: text is required", ErrInvalid)
	}
	if utf8.RuneCountInString(text) > MaxText {
		return fmt.Errorf("%w: text must be at most %d characters", ErrInvalid, MaxText)
	}
	return nil
}

func newID() string {
	var raw [10]byte
	_, _ = rand.Read(raw[:])
	return "n_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
}

// Add appends a note. replaces, when set, names an existing current note by
// id or by its text (case-insensitive); that note is marked replaced.
func (s *Store) Add(team, kind, text, agent, source, replaces string) (Note, error) {
	if err := Check(kind, text); err != nil {
		return Note{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	notes, err := s.load(team)
	if err != nil {
		return Note{}, err
	}
	now := s.now().UnixMilli()
	note := Note{ID: newID(), Kind: kind, Text: strings.TrimSpace(text), Agent: agent, Source: source, CreatedAt: now, UpdatedAt: now}
	if replaces = strings.TrimSpace(replaces); replaces != "" {
		for i := range notes {
			if !notes[i].Current() {
				continue
			}
			if notes[i].ID == replaces || strings.EqualFold(notes[i].Text, replaces) {
				notes[i].ValidUntil = now
				notes[i].ReplacedBy = note.ID
				notes[i].UpdatedAt = now
				break
			}
		}
	}
	notes = append(notes, note)
	if err := s.save(team, notes); err != nil {
		return Note{}, err
	}
	return note, nil
}

// Update changes a note's kind and/or text.
func (s *Store) Update(team, id string, kind, text *string) (Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notes, err := s.load(team)
	if err != nil {
		return Note{}, err
	}
	for i := range notes {
		if notes[i].ID != id {
			continue
		}
		k, t := notes[i].Kind, notes[i].Text
		if kind != nil {
			k = *kind
		}
		if text != nil {
			t = strings.TrimSpace(*text)
		}
		if err := Check(k, t); err != nil {
			return Note{}, err
		}
		notes[i].Kind, notes[i].Text, notes[i].UpdatedAt = k, t, s.now().UnixMilli()
		if err := s.save(team, notes); err != nil {
			return Note{}, err
		}
		return notes[i], nil
	}
	return Note{}, ErrNotFound
}

// Delete removes a note.
func (s *Store) Delete(team, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	notes, err := s.load(team)
	if err != nil {
		return err
	}
	for i := range notes {
		if notes[i].ID == id {
			notes = append(notes[:i], notes[i+1:]...)
			return s.save(team, notes)
		}
	}
	return ErrNotFound
}

// Prompt renders the current notes, newest first, as lines for a prompt, cut
// at limit characters (REQ-RDS-041). It returns "" when there are none.
func (s *Store) Prompt(team string, limit int) string {
	notes, err := s.List(team)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, n := range notes {
		if !n.Current() {
			continue
		}
		line := fmt.Sprintf("- [%s] %s (%s, %s)\n", n.Kind, n.Text, noteAuthor(n), time.UnixMilli(n.CreatedAt).Format("2 Jan"))
		if b.Len()+len(line) > limit {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func noteAuthor(n Note) string {
	if n.Agent == "" {
		return "owner"
	}
	return n.Agent
}
