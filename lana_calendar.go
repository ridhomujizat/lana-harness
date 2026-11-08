package lana

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/calendar"
	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
	"github.com/ridhomujizat/lana-harness/tools"
)

// The runtime side of the calendar (spec #35, design §Runtime glue,
// REQ-CAL-006, REQ-CAL-009): the calendar service over the session store, the
// workflow and action adapters, and the calendar agent tool.

// calendarTrigger is the workflow run trigger of calendar starts (API-CAL-018).
const calendarTrigger = "calendar"

// calendarRoomChannel and calendarRoomAddress name the room session of an
// agent: the owner's web session the room page shows (spec #32, API-ROOM-003).
const (
	calendarRoomChannel = "web"
	calendarRoomAddress = "owner"
)

// errNoWebChannel is returned for a "web" message when there is no web notice
// path and no Telegram owner chat to fall back to.
var errNoWebChannel = errors.New("no channel for web messages yet")

// calendarSender holds the owner message sender the daemon installs
// (SetCalendarTelegram). It is nil until then.
type calendarSender struct {
	mu   sync.RWMutex
	send func(ctx context.Context, text string) error
}

func (s *calendarSender) get() func(ctx context.Context, text string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.send
}

func (s *calendarSender) set(fn func(ctx context.Context, text string) error) {
	s.mu.Lock()
	s.send = fn
	s.mu.Unlock()
}

// newCalendar builds the calendar service over the session store when it
// implements calendar.Store, else an in-memory store, wires the workflow and
// action adapters and the workflow slot filter, and registers the calendar
// tool (design §Runtime glue).
func (rt *Runtime) newCalendar(wfStore workflow.Store) {
	var calStore calendar.Store = calendar.NewMemStore()
	if st, ok := rt.store.(calendar.Store); ok {
		calStore = st
	}
	rt.calendar = calendar.NewService(calStore,
		calendar.WithLocation(time.Local),
		calendar.WithLogger(slog.Default()))
	rt.calendarGoogle = calendar.NewGoogle(rt.calendar, calStore, newCalendarSecrets(rt.home),
		calendar.WithGoogleLogger(slog.Default()))
	rt.calendar.SetWorkflows(calendarWorkflows{svc: rt.workflows, store: wfStore})
	rt.calendar.SetActions(calendarActions{rt: rt})
	// The filter only acts while the workflow scheduler runs, which happens in
	// the daemon next to the calendar scheduler, so it is safe to install now.
	rt.workflows.SetSlotFilter(workflow.SlotFilter(rt.calendar.SlotFilter()))
	if rt.coreCtx != nil && rt.coreCtx.Tools != nil {
		if err := rt.coreCtx.Tools.Register(tools.NewCalendar(rt.calendar)); err != nil {
			slog.Warn("calendar: register tool", "error", err)
		}
	}
}

// Calendar returns the calendar service (design §Runtime glue). It is never
// nil after New.
func (rt *Runtime) Calendar() *calendar.Service { return rt.calendar }

// CalendarGoogle returns the Google Calendar link (REQ-CAL-012,
// API-CAL-012 … API-CAL-014). It is never nil after New.
func (rt *Runtime) CalendarGoogle() *calendar.Google { return rt.calendarGoogle }

// StartCalendar starts the calendar scheduler: missed firings, due actions,
// moved workflow slots and the daily agenda (REQ-CAL-006, REQ-CAL-007,
// REQ-CAL-011) and the Google sync loop (REQ-CAL-012). It is a no-op after
// the first call. Both stop with ctx or Close.
func (rt *Runtime) StartCalendar(ctx context.Context) {
	if rt.calendar == nil {
		return
	}
	if ctx == nil {
		ctx = rt.ctx
	}
	// The workflow source, actions and slot filter are installed by New.
	rt.calendarOnce.Do(func() {
		if err := rt.calendar.Start(ctx); err != nil {
			slog.Warn("calendar: start", "error", err)
		}
		// The Google sync loop is idle until an account is linked.
		if rt.calendarGoogle != nil {
			rt.calendarGoogle.Start(ctx)
		}
	})
}

// SetCalendarTelegram installs the Telegram owner sender: send_message actions
// with target "telegram" (and "web", for now) and the calendar's missed,
// failed and agenda notices go through it (REQ-CAL-006, REQ-CAL-007,
// REQ-CAL-011). nil removes it.
func (rt *Runtime) SetCalendarTelegram(send func(ctx context.Context, text string) error) {
	rt.calSender.set(send)
	if rt.calendar == nil {
		return
	}
	if send == nil {
		rt.calendar.SetNotifier(nil)
		return
	}
	rt.calendar.SetNotifier(calendarNotifier(send))
}

// calendarNotifier adapts a send function to calendar.Notifier.
type calendarNotifier func(ctx context.Context, text string) error

// NotifyOwner sends one owner message.
func (n calendarNotifier) NotifyOwner(ctx context.Context, text string) error { return n(ctx, text) }

// calendarWorkflows adapts the workflow service to calendar.WorkflowSource.
type calendarWorkflows struct {
	svc   *workflow.Service
	store workflow.Store
}

// Slots returns the future workflow fire times in [from, to) (REQ-CAL-008).
func (w calendarWorkflows) Slots(ctx context.Context, from, to time.Time) ([]calendar.WorkflowSlot, error) {
	slots, err := w.svc.Slots(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]calendar.WorkflowSlot, 0, len(slots))
	for _, sl := range slots {
		out = append(out, calendar.WorkflowSlot{
			WorkflowID:   sl.WorkflowID,
			Name:         sl.Name,
			Guardian:     sl.Guardian,
			NodeID:       sl.NodeID,
			Trigger:      sl.Trigger,
			At:           sl.At,
			EveryMinutes: sl.EveryMinutes,
			Collapsed:    sl.Collapsed,
		})
	}
	return out, nil
}

// Runs returns the runs of every workflow started in [from, to), oldest
// first, with the workflow's name and guardian (REQ-CAL-008).
func (w calendarWorkflows) Runs(ctx context.Context, from, to time.Time) ([]calendar.WorkflowRun, error) {
	if w.store == nil {
		return nil, nil
	}
	runs, err := w.store.ListRuns(ctx, workflow.RunFilter{Since: from.UnixMilli()})
	if err != nil {
		return nil, err
	}
	wfs, err := w.store.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]workflow.Workflow, len(wfs))
	for _, wf := range wfs {
		byID[wf.ID] = wf
	}
	hi := to.UnixMilli()
	out := make([]calendar.WorkflowRun, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		if run.StartedAt >= hi {
			continue
		}
		wf := byID[run.WorkflowID]
		out = append(out, calendar.WorkflowRun{
			ID:         run.ID,
			WorkflowID: run.WorkflowID,
			Name:       wf.Name,
			Guardian:   wf.Guardian,
			Trigger:    run.Trigger,
			Status:     string(run.Status),
			Number:     run.Number,
			StartedAt:  run.StartedAt,
			EndedAt:    run.EndedAt,
		})
	}
	return out, nil
}

// StartRun starts one run with trigger "calendar" (API-CAL-018).
func (w calendarWorkflows) StartRun(ctx context.Context, workflowID string, input json.RawMessage) (string, int, error) {
	run, err := w.svc.StartRun(ctx, workflowID, calendarTrigger, input)
	if err != nil {
		return "", 0, err
	}
	return run.ID, run.Number, nil
}

// Workflow returns a workflow's name and status.
func (w calendarWorkflows) Workflow(ctx context.Context, id string) (string, string, bool) {
	detail, err := w.svc.Get(ctx, id)
	if err != nil {
		return "", "", false
	}
	return detail.Name, string(detail.Status), true
}

// calendarActions adapts the runtime to calendar.Actions (REQ-CAL-006).
type calendarActions struct{ rt *Runtime }

// AgentExists reports whether an agent (or team) is configured.
func (a calendarActions) AgentExists(name string) bool {
	_, ok := a.rt.agentConfig(name)
	return ok
}

// AgentPrompt runs one turn of agent in its room: the newest top-level owner
// web session of the agent, created when there is none (spec #32,
// API-ROOM-003). The prompt starts with "Calendar · <title>" so the room shows
// where it came from. When a turn already runs in the room the prompt is
// queued in its inbox. It blocks until the turn ends.
func (a calendarActions) AgentPrompt(ctx context.Context, agentName, prompt, title string) (string, error) {
	rt := a.rt
	// REQ-CCR-007, AC-CCR-008: a Claude Agent runs on a schedule only with
	// allow_scheduled = true.
	if err := rt.scheduledRunAllowed(agentName, ""); err != nil {
		return "", err
	}
	session, err := rt.roomSession(ctx, agentName)
	if err != nil {
		return "", err
	}
	// REQ-CCT-008: the item's turn skips Claude Code unless allow_scheduled.
	// A message queued behind a running turn is not marked.
	defer rt.registry.MarkScheduled(session.ID)()
	text := calendarPromptText(title, prompt)
	if rt.TurnActive(session.ID) {
		return session.ID, rt.Send(ctx, session.ID, text)
	}
	_, err = rt.Run(ctx, Request{Prompt: text, SessionID: session.ID})
	if errors.Is(err, ErrSessionBusy) {
		return session.ID, rt.Send(ctx, session.ID, text)
	}
	return session.ID, err
}

// calendarPromptText is the room message of an agent_prompt action.
func calendarPromptText(title, prompt string) string {
	header := "Calendar · " + strings.TrimSpace(title)
	if strings.TrimSpace(prompt) == "" {
		return header
	}
	return header + "\n\n" + prompt
}

// SendMessage sends text to the owner (REQ-CAL-006). "telegram" uses the
// Telegram owner chat. "web" has no notice path of its own yet, so it falls
// back to Telegram and fails when Telegram is not connected.
func (a calendarActions) SendMessage(ctx context.Context, target, text string) error {
	send := a.rt.calSender.get()
	switch target {
	case "telegram":
		if send == nil {
			return errors.New("telegram is not connected")
		}
		return send(ctx, text)
	case "web":
		if send == nil {
			return errNoWebChannel
		}
		return send(ctx, text)
	default:
		return fmt.Errorf("unknown message target %q", target)
	}
}

// roomSession returns the room session of an agent: its newest top-level web
// session of the owner, or a new one (spec #32, the web rooms' current
// session).
func (rt *Runtime) roomSession(ctx context.Context, agentName string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	unlock := rt.lockChannelSession(calendarRoomChannel, calendarRoomAddress, agentName)
	defer unlock()
	list, err := rt.store.List(ctx, core.SessionFilter{
		Agent:        agentName,
		Channel:      calendarRoomChannel,
		Address:      calendarRoomAddress,
		TopLevelOnly: true,
		Limit:        1,
	})
	if err != nil {
		return core.SessionInfo{}, err
	}
	if len(list) > 0 {
		return list[0], nil
	}
	return rt.CreateChannelSession(ctx, calendarRoomChannel, calendarRoomAddress, agentName)
}

// calendarSecrets adapts the runtime's secret store, <home>/secrets.env, to
// calendar.Secrets for the Google link (design "Data"). secrets.env names are
// env names, so "calendar.google.client_id" is stored as
// CALENDAR_GOOGLE_CLIENT_ID. Reads go to the file each time, so values the
// link writes are seen without a restart; a set environment variable wins,
// as in the config loader. Without a home the values live in memory.
type calendarSecrets struct {
	home string
	mu   sync.Mutex
	mem  map[string]string
}

func newCalendarSecrets(home string) *calendarSecrets {
	return &calendarSecrets{home: home, mem: map[string]string{}}
}

// calendarSecretEnvName maps "calendar.google.client_id" to
// "CALENDAR_GOOGLE_CLIENT_ID".
func calendarSecretEnvName(name string) string {
	return strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(name))
}

// Get returns one secret and whether it is set.
func (s *calendarSecrets) Get(_ context.Context, name string) (string, bool, error) {
	env := calendarSecretEnvName(name)
	if v, ok := os.LookupEnv(env); ok && v != "" {
		return v, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.home == "" {
		v, ok := s.mem[env]
		return v, ok && v != "", nil
	}
	values, err := webfiles.ReadSecrets(filepath.Join(s.home, "secrets.env"))
	if err != nil {
		return "", false, err
	}
	v, ok := values[env]
	return v, ok && v != "", nil
}

// Set writes one secret and its update time.
func (s *calendarSecrets) Set(_ context.Context, name, value string) error {
	env := calendarSecretEnvName(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.home == "" {
		s.mem[env] = value
		return nil
	}
	if err := webfiles.SetSecret(filepath.Join(s.home, "secrets.env"), env, value); err != nil {
		return err
	}
	if err := webfiles.SetSecretMeta(filepath.Join(s.home, webfiles.SecretsMetaFileName), env, time.Now().UnixMilli()); err != nil {
		slog.Warn("calendar: write secrets meta", "error", err)
	}
	return nil
}

// Delete removes one secret; a missing one is not an error.
func (s *calendarSecrets) Delete(_ context.Context, name string) error {
	env := calendarSecretEnvName(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.home == "" {
		delete(s.mem, env)
		return nil
	}
	err := webfiles.RemoveSecret(filepath.Join(s.home, "secrets.env"), env)
	if err != nil && !errors.Is(err, webfiles.ErrSecretNotFound) {
		return err
	}
	if err := webfiles.RemoveSecretMeta(filepath.Join(s.home, webfiles.SecretsMetaFileName), env); err != nil {
		slog.Warn("calendar: remove secrets meta", "error", err)
	}
	return nil
}
