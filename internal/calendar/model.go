// Package calendar implements the lana calendar (spec #35): items with
// repeats and actions, overrides, firings, habits, the entries builder that
// merges items with workflow slots, runs and Google events, and the text and
// ICS renderers. This file holds the shared model of the contract.
package calendar

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxRange is the longest range one entries read may cover (REQ-CAL-002).
const MaxRange = 92 * 24 * time.Hour

// Kind is the kind of a calendar item (REQ-CAL-001).
type Kind string

// Item kinds. KindWorkflow appears only on entries built from workflow slots
// and runs; it is never stored on an item.
const (
	KindEvent    Kind = "event"
	KindTask     Kind = "task"
	KindReminder Kind = "reminder"
	KindNote     Kind = "note"
	KindHabit    Kind = "habit"
	KindWorkflow Kind = "workflow"
)

// Valid reports whether k is one of the five item kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindEvent, KindTask, KindReminder, KindNote, KindHabit:
		return true
	}
	return false
}

// Repeat frequencies (REQ-CAL-003).
const (
	FreqNone     = "none"
	FreqDaily    = "daily"
	FreqWeekdays = "weekdays"
	FreqWeekly   = "weekly"
	FreqMonthly  = "monthly"
	FreqYearly   = "yearly"
)

// Task states (REQ-CAL-005).
const (
	StateOpen = "open"
	StateDone = "done"
)

// Action types (REQ-CAL-006).
const (
	ActionAgentPrompt = "agent_prompt"
	ActionRunWorkflow = "run_workflow"
	ActionSendMessage = "send_message"
)

// Firing statuses (REQ-CAL-006, REQ-CAL-007).
const (
	FiringScheduled = "scheduled"
	FiringRunning   = "running"
	FiringDone      = "done"
	FiringFailed    = "failed"
	FiringMissed    = "missed"
	FiringDismissed = "dismissed"
)

// Override sources and operations (REQ-CAL-004).
const (
	OverrideItem     = "item"
	OverrideWorkflow = "workflow"
	OpMove           = "move"
	OpSkip           = "skip"
)

// Entry sources (REQ-CAL-002).
const (
	SourceItem         = "item"
	SourceWorkflowSlot = "workflow_slot"
	SourceWorkflowRun  = "workflow_run"
	SourceGoogle       = "google"
)

// Google sync states of a linked item (REQ-CAL-012).
const (
	SyncSynced  = "synced"
	SyncPending = "pending"
	SyncError   = "error"
)

// Entry statuses that are not firing or run statuses.
const (
	StatusScheduled = "scheduled"
	StatusSkipped   = "skipped"
)

// Owner is the assignee and author name of the owner.
const Owner = "owner"

// Repeat is the repeat rule of an item (REQ-CAL-003). Days holds "mon".."sun"
// for a weekly repeat; Until is an inclusive "YYYY-MM-DD" date or "".
type Repeat struct {
	Freq  string   `json:"freq"`
	Days  []string `json:"days"`
	Until string   `json:"until"`
}

// Repeats reports whether the rule produces more than one occurrence.
func (r Repeat) Repeats() bool { return r.Freq != "" && r.Freq != FreqNone }

// MarshalJSON writes Days as [] when empty and an empty Freq as "none".
func (r Repeat) MarshalJSON() ([]byte, error) {
	type plain Repeat
	out := plain(r)
	if out.Days == nil {
		out.Days = []string{}
	}
	if out.Freq == "" {
		out.Freq = FreqNone
	}
	return json.Marshal(out)
}

// Action is what an item runs when an occurrence is due (REQ-CAL-006).
type Action struct {
	Type       string          `json:"type"`
	Agent      string          `json:"agent,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	WorkflowID string          `json:"workflow_id,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Target     string          `json:"target,omitempty"`
	Text       string          `json:"text,omitempty"`
}

// GoogleRef links an item to a Google Calendar event (REQ-CAL-012).
type GoogleRef struct {
	CalendarID string `json:"calendar_id"`
	EventID    string `json:"event_id"`
	ETag       string `json:"-"`
	Sync       string `json:"sync"`
}

// Item is one stored calendar record (REQ-CAL-001). Start and End are Unix
// milliseconds; Date, EndDate and Time are local to the calendar time zone.
type Item struct {
	ID        string     `json:"id"`
	Kind      Kind       `json:"kind"`
	Title     string     `json:"title"`
	Notes     string     `json:"notes"`
	Start     int64      `json:"start"`
	End       int64      `json:"end"`
	AllDay    bool       `json:"all_day"`
	Date      string     `json:"date"`
	EndDate   string     `json:"end_date"`
	Time      string     `json:"time"`
	Repeat    Repeat     `json:"repeat"`
	Assignee  string     `json:"assignee"`
	Color     string     `json:"color"`
	State     string     `json:"state"`
	Action    *Action    `json:"action"`
	Google    *GoogleRef `json:"google"`
	CreatedBy string     `json:"created_by"`
	UpdatedBy string     `json:"updated_by"`
	CreatedAt int64      `json:"created_at"`
	UpdatedAt int64      `json:"updated_at"`
	DeletedAt int64      `json:"-"`
}

// Firing is the run record of one item occurrence (REQ-CAL-006). It is keyed
// by (ItemID, Occurrence), the original computed instant in Unix ms.
type Firing struct {
	ItemID      string
	Occurrence  int64
	Status      string
	StartedAt   int64
	EndedAt     int64
	SessionID   string
	RunWorkflow string
	RunNumber   int
	Error       string
}

type firingRun struct {
	WorkflowID string `json:"workflow_id"`
	Number     int    `json:"number"`
}

type firingJSON struct {
	Status    string     `json:"status"`
	At        int64      `json:"at"`
	StartedAt int64      `json:"started_at"`
	EndedAt   int64      `json:"ended_at"`
	SessionID string     `json:"session_id"`
	Run       *firingRun `json:"run"`
	Error     string     `json:"error"`
}

// MarshalJSON writes the contract's firing object; run is null when the
// firing started no workflow run.
func (f Firing) MarshalJSON() ([]byte, error) {
	out := firingJSON{
		Status:    f.Status,
		At:        f.Occurrence,
		StartedAt: f.StartedAt,
		EndedAt:   f.EndedAt,
		SessionID: f.SessionID,
		Error:     f.Error,
	}
	if f.RunWorkflow != "" {
		out.Run = &firingRun{WorkflowID: f.RunWorkflow, Number: f.RunNumber}
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the contract's firing object. ItemID is not part of it.
func (f *Firing) UnmarshalJSON(data []byte) error {
	var in firingJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*f = Firing{
		ItemID:     f.ItemID,
		Occurrence: in.At,
		Status:     in.Status,
		StartedAt:  in.StartedAt,
		EndedAt:    in.EndedAt,
		SessionID:  in.SessionID,
		Error:      in.Error,
	}
	if in.Run != nil {
		f.RunWorkflow = in.Run.WorkflowID
		f.RunNumber = in.Run.Number
	}
	return nil
}

// Override moves or skips one occurrence of a repeating item or one workflow
// slot (REQ-CAL-004). It is keyed by (Source, Ref, Occurrence). Ref is the
// item id or "<workflow id>/<trigger node id>". FiredAt is set once the
// scheduler started the moved workflow slot.
type Override struct {
	Source     string `json:"source"`
	Ref        string `json:"ref"`
	Occurrence int64  `json:"occurrence"`
	Op         string `json:"op"`
	To         int64  `json:"to"`
	By         string `json:"by"`
	At         int64  `json:"at"`
	FiredAt    int64  `json:"-"`
}

// HabitCheck marks one habit occurrence, keyed by (ItemID, Date), done
// (REQ-CAL-005). The same record marks one occurrence of a repeating task.
type HabitCheck struct {
	ItemID string `json:"item_id"`
	Date   string `json:"date"`
	By     string `json:"by"`
	At     int64  `json:"at"`
}

// HistoryLine is one change note of an item. Snapshot holds a JSON copy of
// a losing Google conflict side (REQ-CAL-012); it never reaches the API.
type HistoryLine struct {
	ItemID   string `json:"-"`
	At       int64  `json:"at"`
	By       string `json:"by"`
	Summary  string `json:"summary"`
	Snapshot string `json:"-"`
}

// AgendaSettings controls the daily Telegram agenda (REQ-CAL-011).
type AgendaSettings struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"`
}

// Settings are the calendar settings of API-CAL-009 without the ICS URL.
type Settings struct {
	Timezone  string         `json:"timezone"`
	WeekStart int            `json:"week_start"`
	Agenda    AgendaSettings `json:"agenda"`
}

// DefaultSettings returns the settings used before the owner saves any:
// loc's zone name, weeks starting on Monday, the agenda off at 07:00.
func DefaultSettings(loc *time.Location) Settings {
	return Settings{
		Timezone:  ZoneName(loc),
		WeekStart: 1,
		Agenda:    AgendaSettings{Enabled: false, Time: "07:00"},
	}
}

// Location loads the settings' time zone, falling back to time.Local.
func (s Settings) Location() *time.Location {
	if s.Timezone == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.Local
	}
	return loc
}

// ZoneName returns the IANA name of loc. For time.Local it tries $TZ and the
// /etc/localtime link before falling back to "Local".
func ZoneName(loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	name := loc.String()
	if name != "Local" {
		return name
	}
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		target = filepath.ToSlash(target)
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			tz := target[i+len("zoneinfo/"):]
			if _, err := time.LoadLocation(tz); err == nil {
				return tz
			}
		}
	}
	return name
}

// GoogleEvent is one cached Google Calendar event (REQ-CAL-012). JSON is the
// Google API event resource; ItemID is set when the event is linked to an
// item. Events are expected as single instances (singleEvents=true).
type GoogleEvent struct {
	CalendarID string
	EventID    string
	ETag       string
	ItemID     string
	JSON       string
	UpdatedMs  int64
}

// EntryHabit is the habit state of an entry (REQ-CAL-005).
type EntryHabit struct {
	Done   bool `json:"done"`
	Streak int  `json:"streak"`
}

// EntryWorkflow describes the workflow behind a slot or run entry.
type EntryWorkflow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Guardian     string `json:"guardian"`
	NodeID       string `json:"node_id"`
	Trigger      string `json:"trigger"`
	EveryMinutes int    `json:"every_minutes"`
	RunNumber    int    `json:"run_number"`
	RunStatus    string `json:"run_status"`
}

// EntryGoogle is the Google link of an entry.
type EntryGoogle struct {
	CalendarID   string `json:"calendar_id"`
	EventID      string `json:"event_id"`
	Sync         string `json:"sync"`
	Color        string `json:"color,omitempty"`
	CalendarName string `json:"calendar_name,omitempty"`
}

// Entry is one thing the calendar shows (REQ-CAL-002): an item occurrence, a
// workflow slot, a workflow run or a Google event.
type Entry struct {
	Key        string         `json:"key"`
	Source     string         `json:"source"`
	Kind       Kind           `json:"kind"`
	Title      string         `json:"title"`
	Start      int64          `json:"start"`
	End        int64          `json:"end"`
	AllDay     bool           `json:"all_day"`
	Date       string         `json:"date"`
	EndDate    string         `json:"end_date"`
	Color      string         `json:"color"`
	Status     string         `json:"status"`
	Moved      bool           `json:"moved"`
	Occurrence int64          `json:"occurrence"`
	ItemID     string         `json:"item_id"`
	Repeating  bool           `json:"repeating"`
	Assignee   string         `json:"assignee"`
	CreatedBy  string         `json:"created_by"`
	Action     *Action        `json:"action"`
	Firing     *Firing        `json:"firing"`
	Habit      *EntryHabit    `json:"habit"`
	Workflow   *EntryWorkflow `json:"workflow"`
	Google     *EntryGoogle   `json:"google"`
	Readonly   bool           `json:"readonly"`
}

// DefaultColor is the color an item of kind k shows when it has none. The
// values match the web UI.
func DefaultColor(k Kind) string {
	switch k {
	case KindEvent:
		return "blue"
	case KindTask:
		return "violet"
	case KindReminder:
		return "amber"
	case KindNote:
		return "gray"
	case KindHabit:
		return "green"
	case KindWorkflow:
		return "indigo"
	}
	return "gray"
}

// codedError is a sentinel error with a contract result code.
type codedError struct{ code, msg string }

func (e *codedError) Error() string { return "calendar: " + e.msg }

// Code returns the contract result code.
func (e *codedError) Code() string { return e.code }

// Sentinel errors. Each has a Code() matching the contract's result codes.
var (
	ErrNotFound         error = &codedError{"not_found", "not found"}
	ErrRangeTooLarge    error = &codedError{"range_too_large", "range is longer than 92 days"}
	ErrInvalidRange     error = &codedError{"invalid_range", "from must be before to"}
	ErrNotRepeating     error = &codedError{"not_repeating", "item does not repeat"}
	ErrNoSuchOccurrence error = &codedError{"no_such_occurrence", "no such occurrence"}
	ErrNotCompletable   error = &codedError{"not_completable", "only tasks and habits can be completed"}
	ErrAlreadyRunning   error = &codedError{"already_running", "firing is already running"}
	ErrAlreadyDone      error = &codedError{"already_done", "firing is already done"}
	// Google link errors (API-CAL-012 … API-CAL-014).
	ErrInvalidTarget error = &codedError{"invalid_target", "target must be a selected, writable Google calendar"}
	ErrClientMissing error = &codedError{"client_missing", "the Google OAuth client is not set"}
	ErrInvalidClient error = &codedError{"invalid_client", "client_id and client_secret are required"}
	ErrNotLinked     error = &codedError{"not_linked", "no Google account is linked"}
	ErrGoogleReauth  error = &codedError{"google_reauth", "Google access was revoked or expired; connect again"}
)

// FieldError is one validation problem of an item.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// InvalidItemError carries every validation problem of an item (AC-CAL-002).
type InvalidItemError struct{ Fields []FieldError }

// Error renders the problems as "<field>: <message>" joined by "; ".
func (e *InvalidItemError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Message)
	}
	return "calendar: invalid item: " + strings.Join(parts, "; ")
}

// Code returns "invalid_item".
func (e *InvalidItemError) Code() string { return "invalid_item" }

// ErrorCode returns the contract code of err, or "" when it has none.
func ErrorCode(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return coded.Code()
	}
	return ""
}

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// NewItemID returns "ci_" followed by 10 random lowercase base36 characters.
func NewItemID() string {
	out := make([]byte, 0, 13)
	out = append(out, "ci_"...)
	buf := make([]byte, 16)
	for len(out) < 13 {
		_, _ = rand.Read(buf)
		for _, b := range buf {
			// 252 is the largest multiple of 36 below 256: no modulo bias.
			if b < 252 && len(out) < 13 {
				out = append(out, idAlphabet[b%36])
			}
		}
	}
	return string(out)
}
