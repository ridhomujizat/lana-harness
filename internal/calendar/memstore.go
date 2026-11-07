package calendar

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// MemStore is an in-memory Store used by tests and by the memstore runtime.
// It is safe for concurrent use and returns copies, so a caller can never
// mutate a stored value through a returned slice or pointer.
//
// REQ-CAL-004: overrides are unique by (source, ref, occurrence).
// REQ-CAL-006: firings are unique by (item_id, occurrence) and change state
// only through the compare-and-set UpdateFiring.
type MemStore struct {
	mu        sync.Mutex
	items     map[string]Item
	overrides map[overrideKey]Override
	firings   map[firingKey]Firing
	checks    map[string]map[string]HabitCheck
	history   []HistoryLine
	kv        map[string]string
	google    map[[2]string]GoogleEvent
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		items:     map[string]Item{},
		overrides: map[overrideKey]Override{},
		firings:   map[firingKey]Firing{},
		checks:    map[string]map[string]HabitCheck{},
		kv:        map[string]string{},
		google:    map[[2]string]GoogleEvent{},
	}
}

// MemStore implements Store.
var _ Store = (*MemStore)(nil)

// CreateItem stores a new item; it fails when the id is taken.
func (s *MemStore) CreateItem(_ context.Context, it Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[it.ID]; ok {
		return fmt.Errorf("calendar: item %q already exists", it.ID)
	}
	s.items[it.ID] = cloneItem(it)
	return nil
}

// GetItem returns one item, or ErrNotFound when it is missing or deleted.
func (s *MemStore) GetItem(_ context.Context, id string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok || it.DeletedAt != 0 {
		return Item{}, ErrNotFound
	}
	return cloneItem(it), nil
}

// UpdateItem writes an existing item, or returns ErrNotFound.
func (s *MemStore) UpdateItem(_ context.Context, it Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.items[it.ID]
	if !ok || cur.DeletedAt != 0 {
		return ErrNotFound
	}
	s.items[it.ID] = cloneItem(it)
	return nil
}

// DeleteItem soft-deletes an item at the given Unix ms, or returns ErrNotFound.
func (s *MemStore) DeleteItem(_ context.Context, id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok || it.DeletedAt != 0 {
		return ErrNotFound
	}
	if at == 0 {
		at = 1
	}
	it.DeletedAt = at
	s.items[id] = it
	return nil
}

// ListItems returns the items that are not deleted, by created_at then id.
func (s *MemStore) ListItems(_ context.Context) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		if it.DeletedAt == 0 {
			out = append(out, cloneItem(it))
		}
	}
	sortItems(out)
	return out, nil
}

// ListDeletedItems returns the soft-deleted items with a Google link, by
// created_at then id.
func (s *MemStore) ListDeletedItems(_ context.Context) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Item{}
	for _, it := range s.items {
		if it.DeletedAt != 0 && it.Google != nil {
			out = append(out, cloneItem(it))
		}
	}
	sortItems(out)
	return out, nil
}

// PurgeItem removes a soft-deleted item, or returns ErrNotFound when it is
// missing or not deleted.
func (s *MemStore) PurgeItem(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok || it.DeletedAt == 0 {
		return ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func sortItems(out []Item) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
}

// PutOverride upserts an override by (source, ref, occurrence).
func (s *MemStore) PutOverride(_ context.Context, o Override) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[overrideKey{o.Source, o.Ref, o.Occurrence}] = o
	return nil
}

// DeleteOverride removes one override; a missing one is not an error.
func (s *MemStore) DeleteOverride(_ context.Context, source, ref string, occurrence int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.overrides, overrideKey{source, ref, occurrence})
	return nil
}

// ListOverrides returns every override by source, ref, occurrence.
func (s *MemStore) ListOverrides(_ context.Context) ([]Override, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Override, 0, len(s.overrides))
	for _, o := range s.overrides {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Occurrence < b.Occurrence
	})
	return out, nil
}

// InsertFiring stores a new firing; it returns false when (item_id,
// occurrence) exists, so a firing starts at most once (REQ-CAL-006).
func (s *MemStore) InsertFiring(_ context.Context, f Firing) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := firingKey{f.ItemID, f.Occurrence}
	if _, ok := s.firings[k]; ok {
		return false, nil
	}
	s.firings[k] = f
	return true, nil
}

// UpdateFiring writes f when the stored status is one of fromStatuses (any
// when empty) and reports whether it wrote; ErrNotFound when missing.
func (s *MemStore) UpdateFiring(_ context.Context, f Firing, fromStatuses ...string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := firingKey{f.ItemID, f.Occurrence}
	cur, ok := s.firings[k]
	if !ok {
		return false, ErrNotFound
	}
	if len(fromStatuses) > 0 && !slices.Contains(fromStatuses, cur.Status) {
		return false, nil
	}
	s.firings[k] = f
	return true, nil
}

// GetFiring returns one firing, or ErrNotFound.
func (s *MemStore) GetFiring(_ context.Context, itemID string, occurrence int64) (Firing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.firings[firingKey{itemID, occurrence}]
	if !ok {
		return Firing{}, ErrNotFound
	}
	return f, nil
}

// ListFirings returns the firings matching the filter, newest occurrence
// first, then by item id.
func (s *MemStore) ListFirings(_ context.Context, filter FiringFilter) ([]Firing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Firing{}
	for _, f := range s.firings {
		if filter.ItemID != "" && f.ItemID != filter.ItemID {
			continue
		}
		if len(filter.Statuses) > 0 && !slices.Contains(filter.Statuses, f.Status) {
			continue
		}
		if filter.From != 0 && f.Occurrence < filter.From {
			continue
		}
		if filter.To != 0 && f.Occurrence >= filter.To {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurrence != out[j].Occurrence {
			return out[i].Occurrence > out[j].Occurrence
		}
		return out[i].ItemID < out[j].ItemID
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// SetHabitCheck upserts a habit check by (item_id, date).
func (s *MemStore) SetHabitCheck(_ context.Context, c HabitCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checks[c.ItemID] == nil {
		s.checks[c.ItemID] = map[string]HabitCheck{}
	}
	s.checks[c.ItemID][c.Date] = c
	return nil
}

// DeleteHabitCheck removes one habit check; a missing one is not an error.
func (s *MemStore) DeleteHabitCheck(_ context.Context, itemID, date string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.checks[itemID], date)
	return nil
}

// ListHabitChecks returns the checks of one item ("" = all) by item, date.
func (s *MemStore) ListHabitChecks(_ context.Context, itemID string) ([]HabitCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []HabitCheck{}
	for id, byDate := range s.checks {
		if itemID != "" && id != itemID {
			continue
		}
		for _, c := range byDate {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ItemID != out[j].ItemID {
			return out[i].ItemID < out[j].ItemID
		}
		return out[i].Date < out[j].Date
	})
	return out, nil
}

// AppendHistory stores one history line.
func (s *MemStore) AppendHistory(_ context.Context, h HistoryLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, h)
	return nil
}

// ListHistory returns the last limit lines of one item ("" = all; limit 0 =
// all), newest first.
func (s *MemStore) ListHistory(_ context.Context, itemID string, limit int) ([]HistoryLine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []HistoryLine{}
	for i := len(s.history) - 1; i >= 0; i-- {
		h := s.history[i]
		if itemID != "" && h.ItemID != itemID {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetKV returns one setting value and whether it exists.
func (s *MemStore) GetKV(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.kv[key]
	return v, ok, nil
}

// SetKV writes one setting value.
func (s *MemStore) SetKV(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[key] = value
	return nil
}

// PutGoogleEvent upserts a cached Google event by (calendar_id, event_id).
func (s *MemStore) PutGoogleEvent(_ context.Context, e GoogleEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.google[[2]string{e.CalendarID, e.EventID}] = e
	return nil
}

// DeleteGoogleEvent removes one cached event; a missing one is not an error.
func (s *MemStore) DeleteGoogleEvent(_ context.Context, calendarID, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.google, [2]string{calendarID, eventID})
	return nil
}

// ListGoogleEvents returns every cached event by calendar_id, event_id.
func (s *MemStore) ListGoogleEvents(_ context.Context) ([]GoogleEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GoogleEvent, 0, len(s.google))
	for _, e := range s.google {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CalendarID != out[j].CalendarID {
			return out[i].CalendarID < out[j].CalendarID
		}
		return out[i].EventID < out[j].EventID
	})
	return out, nil
}

// cloneItem deep-copies the slice and pointer fields of an item.
func cloneItem(it Item) Item {
	if it.Repeat.Days != nil {
		it.Repeat.Days = append([]string{}, it.Repeat.Days...)
	}
	it.Action = cloneAction(it.Action)
	if it.Google != nil {
		g := *it.Google
		it.Google = &g
	}
	return it
}
