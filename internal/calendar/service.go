package calendar

// This file holds the Service: every calendar operation the web, the agent
// tool and Telegram call (spec #35, contract API-CAL-001 … API-CAL-017), and
// the change broadcast of API-CAL-015. The scheduler loop is in scheduler.go.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	// Embed the IANA time zone database so settings can name any zone even
	// when the host has no zoneinfo files (API-CAL-009).
	_ "time/tzdata"
)

// Service tuning constants.
const (
	defaultTickInterval = 30 * time.Second
	subscriberBuffer    = 64
	// missedGrace is how late lana may first see a due action and still run
	// it (REQ-CAL-007, Q-001).
	missedGrace = 5 * time.Minute
	// agendaWindow is how long after agenda.time the daily agenda may still
	// be sent (AC-CAL-019).
	agendaWindow = 5 * time.Minute
	// maxScanBack caps how far back one due scan looks, so a daemon that was
	// off for a long time does not record years of missed firings.
	maxScanBack = 31 * 24 * time.Hour
	// firingLookback widens the firing read of Entries for occurrences that
	// start before the range and still overlap it.
	firingLookback = 32 * 24 * time.Hour
	detailFirings  = 20
	detailHistory  = 50
	maxMissedNames = 10
	icsBack        = 30 * 24 * time.Hour
	icsAhead       = 90 * 24 * time.Hour
	icsTokenBytes  = 32
	workflowActive = "active"
)

// KV keys of the calendar settings table.
const (
	kvSettings        = "settings"
	kvLastTick        = "last_tick"
	kvICSToken        = "ics.token"
	kvAgendaSentOn    = "agenda.sent_on"
	kvGoogleState     = "google.state"
	kvGoogleTarget    = "google.target"
	kvGoogleCalendars = "google.calendars"
)

// Google link states read from KV "google.state" (API-CAL-012).
const (
	GoogleOff         = "off"
	GoogleLinked      = "linked"
	GoogleNeedsReauth = "needs_reauth"
)

// Change kinds of the stream (API-CAL-015).
const (
	ChangeItem     = "item"
	ChangeFiring   = "firing"
	ChangeOverride = "override"
	ChangeRun      = "run"
	ChangeGoogle   = "google"
	ChangeSettings = "settings"
)

// Service errors with contract codes.
var (
	// ErrInvalidSettings rejects an unknown zone, a bad agenda time or a
	// week start other than 0 or 1 (API-CAL-009).
	ErrInvalidSettings error = &codedError{"invalid_settings", "invalid settings"}
	// ErrInvalidOverride rejects an override with an unknown source or op, a
	// malformed workflow ref, or a move without "to" (API-CAL-006).
	ErrInvalidOverride error = &codedError{"invalid_override", "invalid override"}
)

// Change is one event of the calendar stream (API-CAL-015).
type Change struct {
	What   string `json:"what"`
	ItemID string `json:"item_id,omitempty"`
}

// EntriesResult is the response of API-CAL-001.
type EntriesResult struct {
	Entries     []Entry
	Timezone    string
	WeekStart   int
	Now         int64
	GoogleState string
	Missed      int
}

// MarshalJSON writes the contract shape, with google as {"state": …}.
func (r EntriesResult) MarshalJSON() ([]byte, error) {
	type google struct {
		State string `json:"state"`
	}
	entries := r.Entries
	if entries == nil {
		entries = []Entry{}
	}
	return json.Marshal(struct {
		Entries   []Entry `json:"entries"`
		Timezone  string  `json:"timezone"`
		WeekStart int     `json:"week_start"`
		Now       int64   `json:"now"`
		Google    google  `json:"google"`
		Missed    int     `json:"missed"`
	}{entries, r.Timezone, r.WeekStart, r.Now, google{r.GoogleState}, r.Missed})
}

// ItemDetail is the response of API-CAL-003.
type ItemDetail struct {
	Item    Item          `json:"item"`
	Firings []Firing      `json:"firings"`
	History []HistoryLine `json:"history"`
}

// Option configures a Service.
type Option func(*Service)

// WithClock replaces the service clock; tests use it to control time.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithLogger sets the logger (default slog.Default).
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithTickInterval sets the scheduler period (default 30 s).
func WithTickInterval(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.tickInterval = d
		}
	}
}

// WithLocation sets the calendar time zone used until the owner saves one
// in the settings (default time.Local).
func WithLocation(loc *time.Location) Option {
	return func(s *Service) {
		if loc != nil {
			s.defaultLoc = loc
		}
	}
}

// Service is every calendar operation plus the scheduler. It is safe for
// concurrent use.
type Service struct {
	store        Store
	now          func() time.Time
	logger       *slog.Logger
	tickInterval time.Duration
	defaultLoc   *time.Location

	depMu     sync.RWMutex
	workflows WorkflowSource
	actions   Actions
	notifier  Notifier

	subMu  sync.Mutex
	subs   map[int]chan Change
	subSeq int

	// tickMu serializes scheduler passes; lastTick is guarded by it.
	tickMu   sync.Mutex
	lastTick time.Time

	ctxMu      sync.Mutex
	rootCtx    context.Context
	rootCancel context.CancelFunc
	started    bool
	closed     bool
	wg         sync.WaitGroup // scheduler loop
	runWG      sync.WaitGroup // running actions
}

// NewService builds a Service over st.
func NewService(st Store, opts ...Option) *Service {
	s := &Service{
		store:        st,
		now:          time.Now,
		logger:       slog.Default(),
		tickInterval: defaultTickInterval,
		defaultLoc:   time.Local,
		subs:         map[int]chan Change{},
	}
	s.rootCtx, s.rootCancel = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SetWorkflows installs the workflow side; nil removes it.
func (s *Service) SetWorkflows(w WorkflowSource) {
	s.depMu.Lock()
	s.workflows = w
	s.depMu.Unlock()
}

// SetActions installs the agent and message runner; nil removes it.
func (s *Service) SetActions(a Actions) {
	s.depMu.Lock()
	s.actions = a
	s.depMu.Unlock()
}

// SetNotifier installs the owner notifier; nil removes it.
func (s *Service) SetNotifier(n Notifier) {
	s.depMu.Lock()
	s.notifier = n
	s.depMu.Unlock()
}

func (s *Service) workflowSource() WorkflowSource {
	s.depMu.RLock()
	defer s.depMu.RUnlock()
	return s.workflows
}

func (s *Service) actionRunner() Actions {
	s.depMu.RLock()
	defer s.depMu.RUnlock()
	return s.actions
}

func (s *Service) ownerNotifier() Notifier {
	s.depMu.RLock()
	defer s.depMu.RUnlock()
	return s.notifier
}

// notifyOwner sends one owner message when a notifier is set; errors are
// logged.
func (s *Service) notifyOwner(ctx context.Context, text string) {
	n := s.ownerNotifier()
	if n == nil {
		return
	}
	if err := n.NotifyOwner(ctx, text); err != nil {
		s.logger.Error("calendar: notify owner", "error", err)
	}
}

// rootContext returns the service context that bounds every action.
func (s *Service) rootContext() context.Context {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	return s.rootCtx
}

// ---- change broadcast (API-CAL-015) -----------------------------------------

// Subscribe returns a change stream and its unsubscribe function. Sends never
// block: a full subscriber drops the event.
func (s *Service) Subscribe() (<-chan Change, func()) {
	s.subMu.Lock()
	id := s.subSeq
	s.subSeq++
	ch := make(chan Change, subscriberBuffer)
	s.subs[id] = ch
	s.subMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.subMu.Lock()
			if c, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(c)
			}
			s.subMu.Unlock()
		})
	}
}

func (s *Service) broadcast(what, itemID string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- Change{What: what, ItemID: itemID}:
		default:
		}
	}
}

// ---- settings (API-CAL-009) ---------------------------------------------------

// Settings returns the saved settings, or the defaults.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st, _, err := s.loadSettings(ctx)
	return st, err
}

// loadSettings returns the settings and whether the owner saved any.
func (s *Service) loadSettings(ctx context.Context) (Settings, bool, error) {
	def := DefaultSettings(s.defaultLoc)
	raw, ok, err := s.store.GetKV(ctx, kvSettings)
	if err != nil {
		return def, false, err
	}
	if !ok || raw == "" {
		return def, false, nil
	}
	st := def
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		s.logger.Error("calendar: bad stored settings", "error", err)
		return def, false, nil
	}
	return st, true, nil
}

// location is the calendar time zone: the saved zone, else WithLocation's.
func (s *Service) location(ctx context.Context) *time.Location {
	_, loc := s.settingsLoc(ctx)
	return loc
}

// Location returns the calendar time zone: the saved zone, else the
// WithLocation zone. The agent tool reads local dates and times in it
// (API-CAL-016).
func (s *Service) Location(ctx context.Context) *time.Location { return s.location(ctx) }

func (s *Service) settingsLoc(ctx context.Context) (Settings, *time.Location) {
	st, saved, err := s.loadSettings(ctx)
	if err != nil {
		s.logger.Error("calendar: load settings", "error", err)
	}
	if !saved {
		return st, s.defaultLoc
	}
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil || st.Timezone == "" {
		return st, s.defaultLoc
	}
	return st, loc
}

// PutSettings validates and saves the settings (API-CAL-009). An empty
// agenda time means 07:00.
func (s *Service) PutSettings(ctx context.Context, st Settings) (Settings, error) {
	st.Timezone = strings.TrimSpace(st.Timezone)
	if st.Timezone == "" {
		return Settings{}, fmt.Errorf("%w: timezone is required", ErrInvalidSettings)
	}
	if _, err := time.LoadLocation(st.Timezone); err != nil {
		return Settings{}, fmt.Errorf("%w: unknown time zone %q", ErrInvalidSettings, st.Timezone)
	}
	if st.WeekStart != 0 && st.WeekStart != 1 {
		return Settings{}, fmt.Errorf("%w: week_start must be 0 or 1", ErrInvalidSettings)
	}
	if st.Agenda.Time == "" {
		st.Agenda.Time = "07:00"
	}
	if _, _, ok := parseClock(st.Agenda.Time); !ok {
		return Settings{}, fmt.Errorf("%w: agenda.time must be HH:MM", ErrInvalidSettings)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return Settings{}, err
	}
	if err := s.store.SetKV(ctx, kvSettings, string(raw)); err != nil {
		return Settings{}, err
	}
	s.broadcast(ChangeSettings, "")
	return st, nil
}

// ---- entries (API-CAL-001) --------------------------------------------------------

// sourceSet is the parsed sources filter of API-CAL-001.
type sourceSet struct{ items, slots, runs, google bool }

func parseSources(sources []string) sourceSet {
	var set sourceSet
	given := false
	for _, raw := range sources {
		for _, src := range strings.Split(raw, ",") {
			switch strings.TrimSpace(src) {
			case "":
				continue
			case SourceItem:
				set.items = true
			case "workflow":
				set.slots, set.runs = true, true
			case SourceWorkflowSlot:
				set.slots = true
			case SourceWorkflowRun:
				set.runs = true
			case SourceGoogle:
				set.google = true
			}
			given = true
		}
	}
	if !given {
		return sourceSet{true, true, true, true}
	}
	return set
}

// Entries returns every entry overlapping [from, to) (API-CAL-001,
// REQ-CAL-002). sources filters by "item", "workflow" (both workflow
// sources), "workflow_slot", "workflow_run" and "google"; empty means all.
// A failing workflow side is logged and its entries are left out.
func (s *Service) Entries(ctx context.Context, from, to time.Time, sources []string) (EntriesResult, error) {
	if !from.Before(to) {
		return EntriesResult{}, ErrInvalidRange
	}
	if to.Sub(from) > MaxRange {
		return EntriesResult{}, ErrRangeTooLarge
	}
	st, loc := s.settingsLoc(ctx)
	now := s.now()
	in, err := s.buildInput(ctx, from, to, now, loc, parseSources(sources))
	if err != nil {
		return EntriesResult{}, err
	}
	missed, err := s.store.ListFirings(ctx, FiringFilter{Statuses: []string{FiringMissed}})
	if err != nil {
		return EntriesResult{}, err
	}
	return EntriesResult{
		Entries:     Build(in),
		Timezone:    st.Timezone,
		WeekStart:   st.WeekStart,
		Now:         now.UnixMilli(),
		GoogleState: s.googleState(ctx),
		Missed:      len(missed),
	}, nil
}

// googleState is KV "google.state", or "off".
func (s *Service) googleState(ctx context.Context) string {
	v, ok, err := s.store.GetKV(ctx, kvGoogleState)
	if err != nil || !ok || v == "" {
		return GoogleOff
	}
	return v
}

func (s *Service) buildInput(ctx context.Context, from, to, now time.Time, loc *time.Location, src sourceSet) (BuildInput, error) {
	in := BuildInput{From: from, To: to, Now: now, Loc: loc}
	overrides, err := s.store.ListOverrides(ctx)
	if err != nil {
		return in, err
	}
	in.Overrides = overrides
	if src.items {
		if in.Items, err = s.store.ListItems(ctx); err != nil {
			return in, err
		}
		if in.Firings, err = s.rangeFirings(ctx, overrides, from, to); err != nil {
			return in, err
		}
		if in.Checks, err = s.store.ListHabitChecks(ctx, ""); err != nil {
			return in, err
		}
	}
	if src.google {
		if in.Google, err = s.store.ListGoogleEvents(ctx); err != nil {
			return in, err
		}
		in.GoogleCalendars = s.googleCalendars(ctx)
	}
	if ws := s.workflowSource(); ws != nil && (src.slots || src.runs) {
		if src.slots {
			slots, err := ws.Slots(ctx, from, to)
			if err != nil {
				s.logger.Error("calendar: workflow slots", "error", err)
			}
			in.Slots = append(slots, s.overriddenSlots(ctx, ws, slots, overrides, from, to, now)...)
		}
		if src.runs {
			runs, err := ws.Runs(ctx, from, to)
			if err != nil {
				s.logger.Error("calendar: workflow runs", "error", err)
			}
			in.Runs = runs
		}
	}
	return in, nil
}

// rangeFirings loads the firings whose occurrence may show in [from, to):
// the range widened back by firingLookback, plus the firings of item
// occurrences moved into the range from outside it.
func (s *Service) rangeFirings(ctx context.Context, overrides []Override, from, to time.Time) ([]Firing, error) {
	lo := from.Add(-firingLookback).UnixMilli()
	hi := to.UnixMilli()
	firings, err := s.store.ListFirings(ctx, FiringFilter{From: lo, To: hi})
	if err != nil {
		return nil, err
	}
	for _, o := range overrides {
		if o.Source != OverrideItem || o.Op != OpMove || (o.Occurrence >= lo && o.Occurrence < hi) {
			continue
		}
		if o.To < lo || o.To >= hi {
			continue
		}
		f, err := s.store.GetFiring(ctx, o.Ref, o.Occurrence)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		firings = append(firings, f)
	}
	return firings, nil
}

// googleCalendar is one entry of KV "google.calendars", written by the sync.
type googleCalendar struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// googleCalendars reads the display data of the linked calendars from KV
// "google.calendars" (a JSON array of {"id","name","color",…}); none when
// absent or unreadable.
func (s *Service) googleCalendars(ctx context.Context) map[string]GoogleCalendarInfo {
	raw, ok, err := s.store.GetKV(ctx, kvGoogleCalendars)
	if err != nil || !ok || raw == "" {
		return nil
	}
	var cals []googleCalendar
	if err := json.Unmarshal([]byte(raw), &cals); err != nil {
		return nil
	}
	out := make(map[string]GoogleCalendarInfo, len(cals))
	for _, c := range cals {
		out[c.ID] = GoogleCalendarInfo{Name: c.Name, Color: c.Color}
	}
	return out
}

// overriddenSlots returns the workflow slots Build needs but the workflow
// side does not list, because it lists future slots only: the original slot
// of a move whose new time is in the range and still ahead (when the original
// is past or outside the range), and the original slot of a skip in the range
// whose time passed (shown "skipped"). Slots of a workflow that is missing or
// not active are left out.
func (s *Service) overriddenSlots(ctx context.Context, ws WorkflowSource, slots []WorkflowSlot, overrides []Override, from, to, now time.Time) []WorkflowSlot {
	have := map[overrideKey]bool{}
	templates := map[string]WorkflowSlot{}
	for _, sl := range slots {
		if sl.Collapsed {
			continue
		}
		have[overrideKey{OverrideWorkflow, sl.Ref(), sl.At.UnixMilli()}] = true
		if _, ok := templates[sl.Ref()]; !ok {
			templates[sl.Ref()] = sl
		}
	}
	type wfInfo struct {
		name   string
		active bool
	}
	infos := map[string]wfInfo{}
	var out []WorkflowSlot
	for _, o := range overrides {
		if o.Source != OverrideWorkflow {
			continue
		}
		occ := time.UnixMilli(o.Occurrence)
		switch o.Op {
		case OpMove:
			moved := time.UnixMilli(o.To)
			if !moved.After(now) || moved.Before(from) || !moved.Before(to) {
				continue
			}
		case OpSkip:
			if occ.Before(from) || !occ.Before(to) || occ.After(now) {
				continue
			}
		default:
			continue
		}
		key := overrideKey{OverrideWorkflow, o.Ref, o.Occurrence}
		if have[key] {
			continue
		}
		wfID, nodeID, ok := splitRef(o.Ref)
		if !ok {
			continue
		}
		info, seen := infos[wfID]
		if !seen {
			name, status, exists := ws.Workflow(ctx, wfID)
			info = wfInfo{name: name, active: exists && status == workflowActive}
			infos[wfID] = info
		}
		if !info.active {
			continue
		}
		slot, ok := templates[o.Ref]
		if !ok {
			slot = WorkflowSlot{WorkflowID: wfID, NodeID: nodeID, Trigger: "schedule"}
		}
		if slot.Name == "" {
			slot.Name = info.name
		}
		slot.At = occ
		slot.Collapsed = false
		out = append(out, slot)
		have[key] = true
	}
	return out
}

// splitRef splits "<workflow id>/<node id>".
func splitRef(ref string) (wfID, nodeID string, ok bool) {
	wfID, nodeID, ok = strings.Cut(ref, "/")
	if !ok || wfID == "" || nodeID == "" {
		return "", "", false
	}
	return wfID, nodeID, true
}

// ---- items (API-CAL-002 … 005) ------------------------------------------------------

// actor returns by, or "owner" when it is empty.
func actorOf(by string) string {
	if strings.TrimSpace(by) == "" {
		return Owner
	}
	return by
}

// validate runs Validate with the agent and workflow checks of the current
// dependencies. Names old already uses stay accepted, so an item whose agent
// was removed can still be edited.
func (s *Service) validate(ctx context.Context, it Item, old *Item) error {
	var agentExists, workflowExists func(string) bool
	if acts := s.actionRunner(); acts != nil {
		agentExists = func(name string) bool {
			if old != nil && (name == old.Assignee || (old.Action != nil && name == old.Action.Agent)) {
				return true
			}
			return acts.AgentExists(name)
		}
	}
	if ws := s.workflowSource(); ws != nil {
		workflowExists = func(id string) bool {
			if old != nil && old.Action != nil && id == old.Action.WorkflowID {
				return true
			}
			_, _, ok := ws.Workflow(ctx, id)
			return ok
		}
	}
	if errs := Validate(it, agentExists, workflowExists); len(errs) > 0 {
		return &InvalidItemError{Fields: errs}
	}
	return nil
}

// markGooglePending flags an event or reminder for the next Google push: a
// linked item becomes pending; an unlinked one gets a pending link to the
// target calendar when an account is linked (REQ-CAL-012).
func (s *Service) markGooglePending(ctx context.Context, it *Item) {
	if it.Kind != KindEvent && it.Kind != KindReminder {
		return
	}
	if it.Google != nil {
		it.Google.Sync = SyncPending
		return
	}
	// A link waiting for a new consent still collects changes, so they are
	// pushed once the owner connects again.
	if st := s.googleState(ctx); st != GoogleLinked && st != GoogleNeedsReauth {
		return
	}
	target, _, _ := s.store.GetKV(ctx, kvGoogleTarget)
	it.Google = &GoogleRef{CalendarID: target, Sync: SyncPending}
}

func (s *Service) history(ctx context.Context, itemID, by, summary string) {
	err := s.store.AppendHistory(ctx, HistoryLine{ItemID: itemID, At: s.now().UnixMilli(), By: by, Summary: summary})
	if err != nil {
		s.logger.Error("calendar: append history", "item", itemID, "error", err)
	}
}

// CreateItem validates and stores a new item (API-CAL-002). by is "owner"
// or "agent:<name>". Validation problems return *InvalidItemError.
func (s *Service) CreateItem(ctx context.Context, it Item, by string) (Item, error) {
	by = actorOf(by)
	if !it.Kind.Valid() {
		return Item{}, &InvalidItemError{Fields: []FieldError{{Field: "kind", Message: "must be one of event, task, reminder, note, habit"}}}
	}
	it = Normalize(it)
	now := s.now().UnixMilli()
	it.ID = NewItemID()
	it.CreatedBy, it.UpdatedBy = by, by
	it.CreatedAt, it.UpdatedAt = now, now
	it.DeletedAt = 0
	it.Google = nil
	if err := s.validate(ctx, it, nil); err != nil {
		return Item{}, err
	}
	s.markGooglePending(ctx, &it)
	if err := s.store.CreateItem(ctx, it); err != nil {
		return Item{}, err
	}
	s.history(ctx, it.ID, by, "Created")
	s.broadcast(ChangeItem, it.ID)
	return it, nil
}

// GetItem returns an item with its latest 20 firings and 50 history lines
// (API-CAL-003).
func (s *Service) GetItem(ctx context.Context, id string) (ItemDetail, error) {
	it, err := s.store.GetItem(ctx, id)
	if err != nil {
		return ItemDetail{}, err
	}
	firings, err := s.store.ListFirings(ctx, FiringFilter{ItemID: id, Limit: detailFirings})
	if err != nil {
		return ItemDetail{}, err
	}
	hist, err := s.store.ListHistory(ctx, id, detailHistory)
	if err != nil {
		return ItemDetail{}, err
	}
	if firings == nil {
		firings = []Firing{}
	}
	if hist == nil {
		hist = []HistoryLine{}
	}
	return ItemDetail{Item: it, Firings: firings, History: hist}, nil
}

// itemFields are the client-writable item fields, in history order.
var itemFields = []struct {
	name string
	get  func(Item) any
}{
	{"title", func(i Item) any { return i.Title }},
	{"notes", func(i Item) any { return i.Notes }},
	{"start", func(i Item) any { return i.Start }},
	{"end", func(i Item) any { return i.End }},
	{"all_day", func(i Item) any { return i.AllDay }},
	{"date", func(i Item) any { return i.Date }},
	{"end_date", func(i Item) any { return i.EndDate }},
	{"time", func(i Item) any { return i.Time }},
	{"repeat", func(i Item) any { return i.Repeat }},
	{"assignee", func(i Item) any { return i.Assignee }},
	{"color", func(i Item) any { return i.Color }},
	{"state", func(i Item) any { return i.State }},
	{"action", func(i Item) any { return i.Action }},
}

// changedFields lists the client fields that differ between a and b.
func changedFields(a, b Item) []string {
	var out []string
	for _, f := range itemFields {
		x, errX := json.Marshal(f.get(a))
		y, errY := json.Marshal(f.get(b))
		if errX != nil || errY != nil || !bytes.Equal(x, y) {
			out = append(out, f.name)
		}
	}
	return out
}

// scheduleFields change where occurrences fall.
var scheduleFields = map[string]bool{"start": true, "all_day": true, "date": true, "time": true, "repeat": true}

// UpdateItem applies a PATCH body to an item (API-CAL-004). Unknown fields,
// kind and server fields are ignored. When a schedule field changes, the
// item's overrides whose original instant is no longer an occurrence are
// dropped.
func (s *Service) UpdateItem(ctx context.Context, id string, patch json.RawMessage, by string) (Item, error) {
	by = actorOf(by)
	cur, err := s.store.GetItem(ctx, id)
	if err != nil {
		return Item{}, err
	}
	next := cloneItem(cur)
	if len(bytes.TrimSpace(patch)) > 0 {
		if err := json.Unmarshal(patch, &next); err != nil {
			return Item{}, &InvalidItemError{Fields: []FieldError{{Field: "body", Message: "must be a JSON item object"}}}
		}
	}
	next.ID, next.Kind = cur.ID, cur.Kind
	next.CreatedBy, next.CreatedAt = cur.CreatedBy, cur.CreatedAt
	next.UpdatedBy, next.UpdatedAt = cur.UpdatedBy, cur.UpdatedAt
	next.DeletedAt = cur.DeletedAt
	next.Google = cloneItem(cur).Google
	next = Normalize(next)
	if err := s.validate(ctx, next, &cur); err != nil {
		return Item{}, err
	}
	changed := changedFields(Normalize(cur), next)
	if len(changed) == 0 {
		return cur, nil
	}
	next.UpdatedBy = by
	next.UpdatedAt = s.now().UnixMilli()
	s.markGooglePending(ctx, &next)
	if err := s.store.UpdateItem(ctx, next); err != nil {
		return Item{}, err
	}
	for _, f := range changed {
		if scheduleFields[f] {
			s.dropStaleOverrides(ctx, next)
			break
		}
	}
	s.history(ctx, id, by, "Updated "+strings.Join(changed, ", "))
	s.broadcast(ChangeItem, id)
	return next, nil
}

// dropStaleOverrides removes the item's overrides whose original instant is
// no longer an occurrence of it (all of them when it stopped repeating).
func (s *Service) dropStaleOverrides(ctx context.Context, it Item) {
	overrides, err := s.store.ListOverrides(ctx)
	if err != nil {
		s.logger.Error("calendar: list overrides", "error", err)
		return
	}
	loc := s.location(ctx)
	for _, o := range overrides {
		if o.Source != OverrideItem || o.Ref != it.ID {
			continue
		}
		if it.Repeat.Repeats() && IsOccurrence(it, loc, time.UnixMilli(o.Occurrence)) {
			continue
		}
		if err := s.store.DeleteOverride(ctx, o.Source, o.Ref, o.Occurrence); err != nil {
			s.logger.Error("calendar: drop override", "item", it.ID, "error", err)
		}
	}
}

// DeleteItem soft-deletes an item (API-CAL-005). A Google-linked item is
// marked pending first, so the sync can delete the Google event.
func (s *Service) DeleteItem(ctx context.Context, id, by string) error {
	by = actorOf(by)
	it, err := s.store.GetItem(ctx, id)
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	if it.Google != nil {
		it.Google.Sync = SyncPending
		it.UpdatedBy, it.UpdatedAt = by, now
		if err := s.store.UpdateItem(ctx, it); err != nil {
			return err
		}
	}
	if err := s.store.DeleteItem(ctx, id, now); err != nil {
		return err
	}
	s.history(ctx, id, by, "Deleted")
	s.broadcast(ChangeItem, id)
	return nil
}

// ---- overrides (API-CAL-006) ------------------------------------------------------------

// SaveOverride validates and upserts an override (API-CAL-006). An item
// override needs a repeating item and one of its computed occurrences. A
// workflow override needs ref "<workflow id>/<node id>" and a minute-aligned
// occurrence that is a listed slot of that node, already overridden, or
// past. Saving again replaces the override and clears its fired mark.
func (s *Service) SaveOverride(ctx context.Context, o Override, by string) (Override, error) {
	by = actorOf(by)
	switch o.Op {
	case OpMove:
		if o.To <= 0 {
			return Override{}, fmt.Errorf("%w: move needs \"to\"", ErrInvalidOverride)
		}
	case OpSkip:
		o.To = 0
	default:
		return Override{}, fmt.Errorf("%w: op must be move or skip", ErrInvalidOverride)
	}
	if o.Occurrence <= 0 {
		return Override{}, ErrNoSuchOccurrence
	}
	loc := s.location(ctx)
	switch o.Source {
	case OverrideItem:
		it, err := s.store.GetItem(ctx, o.Ref)
		if err != nil {
			return Override{}, err
		}
		if !it.Repeat.Repeats() {
			return Override{}, ErrNotRepeating
		}
		if !IsOccurrence(it, loc, time.UnixMilli(o.Occurrence)) {
			return Override{}, ErrNoSuchOccurrence
		}
	case OverrideWorkflow:
		if err := s.checkWorkflowSlot(ctx, o); err != nil {
			return Override{}, err
		}
	default:
		return Override{}, fmt.Errorf("%w: source must be item or workflow", ErrInvalidOverride)
	}
	o.By = by
	o.At = s.now().UnixMilli()
	o.FiredAt = 0
	if err := s.store.PutOverride(ctx, o); err != nil {
		return Override{}, err
	}
	itemID := ""
	if o.Source == OverrideItem {
		itemID = o.Ref
		when := fmtWhen(time.UnixMilli(o.Occurrence), loc)
		if o.Op == OpMove {
			s.history(ctx, o.Ref, by, "Moved the "+when+" occurrence to "+fmtWhen(time.UnixMilli(o.To), loc))
		} else {
			s.history(ctx, o.Ref, by, "Skipped the "+when+" occurrence")
		}
	}
	s.broadcast(ChangeOverride, itemID)
	return o, nil
}

// checkWorkflowSlot validates the ref and occurrence of a workflow override.
func (s *Service) checkWorkflowSlot(ctx context.Context, o Override) error {
	wfID, nodeID, ok := splitRef(o.Ref)
	if !ok {
		return fmt.Errorf("%w: ref must be <workflow id>/<node id>", ErrInvalidOverride)
	}
	if o.Occurrence%int64(time.Minute/time.Millisecond) != 0 {
		return ErrNoSuchOccurrence
	}
	ws := s.workflowSource()
	if ws == nil {
		return nil
	}
	if _, _, ok := ws.Workflow(ctx, wfID); !ok {
		return ErrNotFound
	}
	occ := time.UnixMilli(o.Occurrence)
	if !occ.After(s.now()) {
		return nil
	}
	overrides, err := s.store.ListOverrides(ctx)
	if err != nil {
		return err
	}
	for _, x := range overrides {
		if x.Source == OverrideWorkflow && x.Ref == o.Ref && x.Occurrence == o.Occurrence {
			return nil
		}
	}
	slots, err := ws.Slots(ctx, occ, occ.Add(time.Minute))
	if err != nil {
		return err
	}
	for _, sl := range slots {
		if !sl.Collapsed && sl.WorkflowID == wfID && sl.NodeID == nodeID && sl.At.UnixMilli() == o.Occurrence {
			return nil
		}
	}
	return ErrNoSuchOccurrence
}

// ClearOverride removes one override, restoring the computed time
// (API-CAL-006 DELETE). A missing override is not an error.
func (s *Service) ClearOverride(ctx context.Context, source, ref string, occurrence int64) error {
	if err := s.store.DeleteOverride(ctx, source, ref, occurrence); err != nil {
		return err
	}
	itemID := ""
	if source == OverrideItem {
		itemID = ref
	}
	s.broadcast(ChangeOverride, itemID)
	return nil
}

// fmtWhen renders an instant for history lines and owner notices.
func fmtWhen(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("Mon 2 Jan 2006 15:04")
}

// ---- complete (API-CAL-007) ---------------------------------------------------------------

// Complete marks a task done or open, or one habit occurrence done or not
// (API-CAL-007). A single task sets its state and ignores occurrence; a
// repeating task and a habit record a check on the local date of
// occurrence, which must be a computed occurrence. The habit state with its
// streak is returned for habits.
func (s *Service) Complete(ctx context.Context, id string, occurrence int64, done bool, by string) (Item, *EntryHabit, error) {
	by = actorOf(by)
	it, err := s.store.GetItem(ctx, id)
	if err != nil {
		return Item{}, nil, err
	}
	if it.Kind != KindTask && it.Kind != KindHabit {
		return Item{}, nil, ErrNotCompletable
	}
	loc := s.location(ctx)
	now := s.now()
	if it.Kind == KindTask && !it.Repeat.Repeats() {
		state := StateOpen
		if done {
			state = StateDone
		}
		if it.State != state {
			it.State = state
			it.UpdatedBy, it.UpdatedAt = by, now.UnixMilli()
			if err := s.store.UpdateItem(ctx, it); err != nil {
				return Item{}, nil, err
			}
			s.history(ctx, id, by, "Marked "+state)
			s.broadcast(ChangeItem, id)
		}
		return it, nil, nil
	}
	occ := time.UnixMilli(occurrence)
	if occurrence <= 0 || !IsOccurrence(it, loc, occ) {
		return Item{}, nil, ErrNoSuchOccurrence
	}
	date := dayOf(occ, loc).String()
	if done {
		err = s.store.SetHabitCheck(ctx, HabitCheck{ItemID: id, Date: date, By: by, At: now.UnixMilli()})
	} else {
		err = s.store.DeleteHabitCheck(ctx, id, date)
	}
	if err != nil {
		return Item{}, nil, err
	}
	word := "not done"
	if done {
		word = "done"
	}
	s.history(ctx, id, by, "Marked "+date+" "+word)
	s.broadcast(ChangeItem, id)
	if it.Kind != KindHabit {
		return it, nil, nil
	}
	habit, err := s.habitState(ctx, it, occ, now, loc)
	if err != nil {
		return Item{}, nil, err
	}
	return it, habit, nil
}

// habitState builds the entry of one habit occurrence and returns its habit
// state. A move of that occurrence is ignored, so the entry stays at its
// original instant; moves never change the streak.
func (s *Service) habitState(ctx context.Context, it Item, occ, now time.Time, loc *time.Location) (*EntryHabit, error) {
	all, err := s.store.ListOverrides(ctx)
	if err != nil {
		return nil, err
	}
	var overrides []Override
	for _, o := range all {
		if o.Source == OverrideItem && o.Ref == it.ID && o.Occurrence == occ.UnixMilli() && o.Op == OpMove {
			continue
		}
		overrides = append(overrides, o)
	}
	checks, err := s.store.ListHabitChecks(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	entries := Build(BuildInput{
		Items:     []Item{it},
		Overrides: overrides,
		Checks:    checks,
		From:      occ,
		To:        occ.Add(time.Millisecond),
		Now:       now,
		Loc:       loc,
	})
	for _, e := range entries {
		if e.Occurrence == occ.UnixMilli() && e.Habit != nil {
			h := *e.Habit
			return &h, nil
		}
	}
	return &EntryHabit{}, nil
}

// ---- firings (API-CAL-008) -------------------------------------------------------------------

// RunFiring runs a missed, failed or dismissed firing again by hand
// (API-CAL-008, AC-CAL-014). The firing becomes running and the action
// starts in the background; a running firing returns ErrAlreadyRunning and
// a done one ErrAlreadyDone.
func (s *Service) RunFiring(ctx context.Context, itemID string, occurrence int64, by string) (Firing, error) {
	by = actorOf(by)
	f, err := s.store.GetFiring(ctx, itemID, occurrence)
	if err != nil {
		return Firing{}, err
	}
	if err := firingBusy(f.Status); err != nil {
		return Firing{}, err
	}
	it, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		return Firing{}, err
	}
	next := Firing{
		ItemID:     itemID,
		Occurrence: occurrence,
		Status:     FiringRunning,
		StartedAt:  s.now().UnixMilli(),
	}
	ok, err := s.store.UpdateFiring(ctx, next, FiringMissed, FiringFailed, FiringDismissed)
	if err != nil {
		return Firing{}, err
	}
	if !ok {
		cur, err := s.store.GetFiring(ctx, itemID, occurrence)
		if err != nil {
			return Firing{}, err
		}
		if err := firingBusy(cur.Status); err != nil {
			return Firing{}, err
		}
		return Firing{}, ErrAlreadyRunning
	}
	s.history(ctx, itemID, by, "Ran the "+fmtWhen(time.UnixMilli(occurrence), s.location(ctx))+" action by hand")
	s.broadcast(ChangeFiring, itemID)
	s.launch(it, next)
	return next, nil
}

// firingBusy maps a status that blocks Run now to its error.
func firingBusy(status string) error {
	switch status {
	case FiringRunning, FiringScheduled:
		return ErrAlreadyRunning
	case FiringDone:
		return ErrAlreadyDone
	}
	return nil
}

// DismissFiring dismisses a missed or failed firing (API-CAL-008). A
// dismissed firing is returned unchanged.
func (s *Service) DismissFiring(ctx context.Context, itemID string, occurrence int64, by string) (Firing, error) {
	by = actorOf(by)
	f, err := s.store.GetFiring(ctx, itemID, occurrence)
	if err != nil {
		return Firing{}, err
	}
	if f.Status == FiringDismissed {
		return f, nil
	}
	if err := firingBusy(f.Status); err != nil {
		return Firing{}, err
	}
	next := f
	next.Status = FiringDismissed
	ok, err := s.store.UpdateFiring(ctx, next, FiringMissed, FiringFailed)
	if err != nil {
		return Firing{}, err
	}
	if !ok {
		cur, err := s.store.GetFiring(ctx, itemID, occurrence)
		if err != nil {
			return Firing{}, err
		}
		if err := firingBusy(cur.Status); err != nil {
			return Firing{}, err
		}
		return cur, nil
	}
	s.history(ctx, itemID, by, "Dismissed the "+fmtWhen(time.UnixMilli(occurrence), s.location(ctx))+" action")
	s.broadcast(ChangeFiring, itemID)
	return next, nil
}

// ---- ICS (API-CAL-010, API-CAL-011) -------------------------------------------------------------

// ICSToken returns the feed token, creating one on first use.
func (s *Service) ICSToken(ctx context.Context) (string, error) {
	tok, ok, err := s.store.GetKV(ctx, kvICSToken)
	if err != nil {
		return "", err
	}
	if ok && tok != "" {
		return tok, nil
	}
	return s.RotateICS(ctx)
}

// RotateICS replaces the feed token; the old one stops working at once.
func (s *Service) RotateICS(ctx context.Context) (string, error) {
	buf := make([]byte, icsTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(buf)
	if err := s.store.SetKV(ctx, kvICSToken, tok); err != nil {
		return "", err
	}
	s.broadcast(ChangeSettings, "")
	return tok, nil
}

// CheckICSToken reports, in constant time, whether token is the feed token.
func (s *Service) CheckICSToken(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	want, ok, err := s.store.GetKV(ctx, kvICSToken)
	if err != nil || !ok || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

// WriteFeed writes the ICS feed: items and workflow slots from 30 days back
// to 90 days ahead (API-CAL-011). The span is read in two parts because one
// read covers at most MaxRange.
func (s *Service) WriteFeed(ctx context.Context, w io.Writer) error {
	now := s.now()
	from, to := now.Add(-icsBack), now.Add(icsAhead)
	var all []Entry
	seen := map[string]bool{}
	for start := from; start.Before(to); {
		end := start.Add(MaxRange)
		if end.After(to) {
			end = to
		}
		res, err := s.Entries(ctx, start, end, []string{SourceItem, SourceWorkflowSlot})
		if err != nil {
			return err
		}
		for _, e := range res.Entries {
			if !seen[e.Key] {
				seen[e.Key] = true
				all = append(all, e)
			}
		}
		start = end
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Start != all[j].Start {
			return all[i].Start < all[j].Start
		}
		return all[i].Key < all[j].Key
	})
	return WriteICS(w, all, s.location(ctx), now)
}

// ---- agenda (API-CAL-017) --------------------------------------------------------------------------

// Agenda renders the agenda of day's local date, with yesterday's missed and
// failed firings (API-CAL-017).
func (s *Service) Agenda(ctx context.Context, day time.Time) (string, error) {
	loc := s.location(ctx)
	d := dayOf(day, loc)
	from, to := d.midnight(loc), d.add(1).midnight(loc)
	today, err := s.Entries(ctx, from, to, nil)
	if err != nil {
		return "", err
	}
	yesterday, err := s.Entries(ctx, d.add(-1).midnight(loc), from, []string{SourceItem})
	if err != nil {
		return "", err
	}
	var problems []Entry
	for _, e := range yesterday.Entries {
		if e.Firing != nil && (e.Firing.Status == FiringMissed || e.Firing.Status == FiringFailed) {
			problems = append(problems, e)
		}
	}
	return AgendaText(day, today.Entries, problems, loc), nil
}

// ---- workflow hook ---------------------------------------------------------------------------------

// SlotFilter returns the workflow scheduler's slot filter (design section
// "Workflow hook"): false when a workflow override (move or skip) exists for
// that slot, so the workflow does not fire at its original time
// (AC-CAL-006, AC-CAL-007). A store error allows the fire.
func (s *Service) SlotFilter() func(ctx context.Context, workflowID, nodeID string, at time.Time) bool {
	return func(ctx context.Context, workflowID, nodeID string, at time.Time) bool {
		overrides, err := s.store.ListOverrides(ctx)
		if err != nil {
			s.logger.Error("calendar: slot filter", "error", err)
			return true
		}
		ref := workflowID + "/" + nodeID
		ms := at.UnixMilli()
		for _, o := range overrides {
			if o.Source == OverrideWorkflow && o.Ref == ref && o.Occurrence == ms {
				return false
			}
		}
		return true
	}
}
