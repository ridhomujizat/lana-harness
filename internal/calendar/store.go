package calendar

import "context"

// FiringFilter selects firings in Store.ListFirings.
type FiringFilter struct {
	ItemID   string   // "" = every item
	Statuses []string // empty = every status
	From     int64    // occurrence >= From (0 = open)
	To       int64    // occurrence < To (0 = open)
	Limit    int      // 0 = no limit
}

// Store persists calendar items, overrides, firings, habit checks, item
// history, key/value settings and cached Google events. It is implemented by
// MemStore and by the SQLite store. All methods are safe for concurrent use.
type Store interface {
	// CreateItem stores a new item. It fails when the id is taken.
	CreateItem(ctx context.Context, it Item) error
	// GetItem returns one item, or ErrNotFound when it is missing or deleted.
	GetItem(ctx context.Context, id string) (Item, error)
	// UpdateItem writes an existing item, or returns ErrNotFound.
	UpdateItem(ctx context.Context, it Item) error
	// DeleteItem soft-deletes an item at the given Unix ms, or returns ErrNotFound.
	DeleteItem(ctx context.Context, id string, at int64) error
	// ListItems returns the items that are not deleted, by created_at then id.
	ListItems(ctx context.Context) ([]Item, error)
	// ListDeletedItems returns the soft-deleted items that still have a
	// Google link, by created_at then id. The Google push deletes their
	// events (REQ-CAL-012).
	ListDeletedItems(ctx context.Context) ([]Item, error)
	// PurgeItem removes a soft-deleted item for good, once its Google event
	// is gone. Its history lines stay. It returns ErrNotFound when the item
	// is missing or not deleted.
	PurgeItem(ctx context.Context, id string) error

	// PutOverride upserts an override by (source, ref, occurrence) (REQ-CAL-004).
	PutOverride(ctx context.Context, o Override) error
	// DeleteOverride removes one override; a missing one is not an error.
	DeleteOverride(ctx context.Context, source, ref string, occurrence int64) error
	// ListOverrides returns every override by source, ref, occurrence.
	ListOverrides(ctx context.Context) ([]Override, error)

	// InsertFiring stores a new firing. It returns false, and changes nothing,
	// when a firing with the same (item_id, occurrence) exists (REQ-CAL-006).
	InsertFiring(ctx context.Context, f Firing) (bool, error)
	// UpdateFiring writes f only when the stored status is one of
	// fromStatuses (any status when empty) and reports whether it wrote. It
	// returns ErrNotFound when the firing does not exist.
	UpdateFiring(ctx context.Context, f Firing, fromStatuses ...string) (bool, error)
	// GetFiring returns one firing, or ErrNotFound.
	GetFiring(ctx context.Context, itemID string, occurrence int64) (Firing, error)
	// ListFirings returns the firings matching the filter, newest occurrence first.
	ListFirings(ctx context.Context, filter FiringFilter) ([]Firing, error)

	// SetHabitCheck upserts a habit check by (item_id, date).
	SetHabitCheck(ctx context.Context, c HabitCheck) error
	// DeleteHabitCheck removes one habit check; a missing one is not an error.
	DeleteHabitCheck(ctx context.Context, itemID, date string) error
	// ListHabitChecks returns the checks of one item ("" = all) by item, date.
	ListHabitChecks(ctx context.Context, itemID string) ([]HabitCheck, error)

	// AppendHistory stores one history line.
	AppendHistory(ctx context.Context, h HistoryLine) error
	// ListHistory returns the last limit lines of one item ("" = all; limit 0 =
	// all), newest first.
	ListHistory(ctx context.Context, itemID string, limit int) ([]HistoryLine, error)

	// GetKV returns one setting value and whether it exists.
	GetKV(ctx context.Context, key string) (string, bool, error)
	// SetKV writes one setting value.
	SetKV(ctx context.Context, key, value string) error

	// PutGoogleEvent upserts a cached Google event by (calendar_id, event_id).
	PutGoogleEvent(ctx context.Context, e GoogleEvent) error
	// DeleteGoogleEvent removes one cached event; a missing one is not an error.
	DeleteGoogleEvent(ctx context.Context, calendarID, eventID string) error
	// ListGoogleEvents returns every cached event by calendar_id, event_id.
	ListGoogleEvents(ctx context.Context) ([]GoogleEvent, error)
}
