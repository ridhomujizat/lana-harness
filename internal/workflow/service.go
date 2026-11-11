package workflow

// This file holds the Service: every operation the web and Telegram call
// (spec #33, design section "Service and scheduler"). It owns the Engine, the
// change broadcast of API-WF-019, trigger matching (REQ-WF-003), the permission
// lock rules (REQ-WF-009) and the guardian assistant (REQ-WF-010).

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrGuardianUnavailable marks an assistant turn the guardian could not answer
// (API-WF-017; the web maps it to 503 guardian_unavailable).
var ErrGuardianUnavailable = errors.New("workflow: guardian unavailable")

// GuardianBackgroundError is the error of a guardian structured call
// (AgentRequest.Background) whose agent runs only on Claude Code and has no
// background model (spec 39 revision 5, REQ-CCT-007). The web answers it with
// 503 guardian_needs_background_model and this message.
type GuardianBackgroundError struct {
	Agent string
}

func (e *GuardianBackgroundError) Error() string {
	return fmt.Sprintf("The guardian %s runs on Claude Code. Set a background model in Router (or add an API model to its combo) so it can plan workflow changes.", e.Agent)
}

// Service tuning constants.
const (
	defaultTickInterval = 30 * time.Second
	subscriberBuffer    = 64
	defaultRunLimit     = 50
	maxRunLimit         = 200
	assistTimeout       = 15 * time.Minute
	// assistEnvTimeout bounds starting MCP servers to list their tools.
	assistEnvTimeout = 20 * time.Second
)

// Option configures a Service.
type Option func(*Service)

// WithClock replaces the service clock; tests use it to control time.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithNotifier installs the owner notifier.
func WithNotifier(n Notifier) Option { return func(s *Service) { s.notifier = n } }

// WithSessionCheck installs the test IsWorkflowSession uses to recognize a
// session on channel "workflow".
func WithSessionCheck(fn func(sessionID string) bool) Option {
	return func(s *Service) { s.sessionCheck = fn }
}

// WithTickInterval sets the scheduler period (default 30 s).
func WithTickInterval(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.tickInterval = d
		}
	}
}

// WithSleep sets the retry backoff sleep passed to the engine.
func WithSleep(fn func(ctx context.Context, d time.Duration) error) Option {
	return func(s *Service) { s.sleep = fn }
}

// WithLogger sets the scheduler logger (default slog.Default).
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// Service is every workflow operation the web, Telegram and the scheduler use.
// It is safe for concurrent use.
type Service struct {
	store  Store
	deps   Deps
	engine *Engine

	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	sessionCheck func(string) bool
	tickInterval time.Duration
	logger       *slog.Logger

	notifierMu sync.RWMutex
	notifier   Notifier

	chanMu   sync.RWMutex
	channels map[string]messageChannel

	subMu  sync.Mutex
	subs   map[int]chan Change
	subSeq int

	runMu    sync.Mutex
	runLocks map[string]*sync.Mutex

	lastFiredMu sync.Mutex
	lastFired   map[string]int64
	// intervalConsumed holds each interval trigger's last suppressed slot
	// (spec #35, REQ-CAL-004), guarded by lastFiredMu.
	intervalConsumed map[string]time.Time

	slotMu     sync.RWMutex
	slotFilter SlotFilter

	// assistNoted is what the last guardian chat note told each session
	// (spec 51 REQ-WFC-002), guarded by assistMu.
	assistMu    sync.Mutex
	assistNoted map[string]assistNote

	ctxMu      sync.Mutex
	rootCtx    context.Context
	rootCancel context.CancelFunc
	started    bool
	wg         sync.WaitGroup
}

// NewService builds a Service over st and deps, applying opts, and creates the
// Engine with the service callbacks (design section "Service and scheduler").
func NewService(st Store, deps Deps, opts ...Option) *Service {
	s := &Service{
		store:        st,
		deps:         deps,
		now:          time.Now,
		tickInterval: defaultTickInterval,
		logger:       slog.Default(),
		subs:         map[int]chan Change{},
		runLocks:     map[string]*sync.Mutex{},
		lastFired:    map[string]int64{},
		channels:     map[string]messageChannel{},

		intervalConsumed: map[string]time.Time{},
	}
	s.rootCtx, s.rootCancel = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(s)
	}
	// Telegram is the built-in Message node channel, backed by the notifier
	// (REQ-WF-005); the runtime registers "web" on top.
	s.registerTelegramChannel()
	s.engine = NewEngine(st, deps, EngineOptions{
		Now:         s.now,
		Sleep:       s.sleep,
		Notify:      s.notify,
		SendMessage: s.dispatchMessage,
		Changed:     s.broadcast,
	})
	return s
}

// SetNotifier swaps the owner notifier; it may be called after Start.
func (s *Service) SetNotifier(n Notifier) {
	s.notifierMu.Lock()
	s.notifier = n
	s.notifierMu.Unlock()
}

// notifierRef returns the current notifier under the lock.
func (s *Service) notifierRef() Notifier {
	s.notifierMu.RLock()
	defer s.notifierMu.RUnlock()
	return s.notifier
}

// notify forwards one owner notice when a notifier is set.
func (s *Service) notify(ctx context.Context, n Notice) {
	if nf := s.notifierRef(); nf != nil {
		nf.Notify(ctx, n)
	}
}

// sendOwner sends a plain owner message; without a notifier Telegram is not
// connected (REQ-WF-005, message node).
func (s *Service) sendOwner(ctx context.Context, text string) error {
	nf := s.notifierRef()
	if nf == nil {
		return errors.New("telegram is not connected")
	}
	return nf.SendOwner(ctx, text)
}

// rootContext returns the service context that bounds every launched run.
func (s *Service) rootContext() context.Context {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	return s.rootCtx
}

// ---- change broadcast (API-WF-019) ----------------------------------------

// Subscribe returns a change stream and its unsubscribe function. Sends never
// block: a full subscriber drops the event (API-WF-019, REQ-WF-011).
func (s *Service) Subscribe() (<-chan Change, func()) {
	s.subMu.Lock()
	id := s.subSeq
	s.subSeq++
	ch := make(chan Change, subscriberBuffer)
	s.subs[id] = ch
	s.subMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.subMu.Lock()
			if c, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(c)
			}
			s.subMu.Unlock()
		})
	}
	return ch, unsubscribe
}

// broadcast publishes a change event; it is the engine Changed callback and is
// also called by every mutating service method.
func (s *Service) broadcast(workflowID, runID string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- Change{WorkflowID: workflowID, RunID: runID}:
		default:
		}
	}
}

// ---- read operations ------------------------------------------------------

// Get returns the workflow detail (API-WF-003, REQ-WF-001).
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	ver, err := s.store.GetVersion(ctx, id, wf.Version)
	if err != nil {
		return Detail{}, err
	}
	detail := detailOf(wf, ver)
	// The detail carries the Script Parse stats only when the graph has a
	// script_parse node (API-SP-009, REQ-SP-019).
	if graphHasScriptParse(ver.Graph) {
		detail.ScriptStats = s.ScriptStats(ctx, id, ver.Graph)
	}
	return detail, nil
}

// detailOf assembles the workflow (detail) object of API-WF-003.
func detailOf(wf Workflow, ver Version) Detail {
	return Detail{
		Workflow:       wf,
		VersionAuthor:  ver.Author,
		VersionSummary: ver.Summary,
		VersionAt:      ver.CreatedAt,
		Graph:          ver.Graph.Normalized(),
		LockCurrent:    LockCovers(wf.Lock, ver.Graph),
		Webhook:        Webhook{Path: "/hooks/" + wf.ID, Token: wf.WebhookToken},
	}
}

// Versions lists the saved versions of a workflow, newest first (API-WF-006).
func (s *Service) Versions(ctx context.Context, id string) ([]Version, error) {
	versions, err := s.store.ListVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range versions {
		versions[i].Graph = versions[i].Graph.Normalized()
	}
	return versions, nil
}

// LockPreview returns what activation would lock (API-WF-008, REQ-WF-009).
func (s *Service) LockPreview(ctx context.Context, id string) ([]LockTool, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return nil, err
	}
	ver, err := s.store.GetVersion(ctx, id, wf.Version)
	if err != nil {
		return nil, err
	}
	return ComputeLock(ver.Graph, s.deps.ToolRisk), nil
}

// Runs lists a workflow's runs, newest first (API-WF-012, REQ-WF-007).
func (s *Service) Runs(ctx context.Context, id string, limit, before int) ([]Run, error) {
	if limit <= 0 {
		limit = defaultRunLimit
	}
	if limit > maxRunLimit {
		limit = maxRunLimit
	}
	return s.store.ListRuns(ctx, RunFilter{WorkflowID: id, Limit: limit, Before: before})
}

// RunDetail returns one run with the graph it ran, node runs and decisions
// (API-WF-013, REQ-WF-004): the version graph, or the candidate graph of a
// candidate test run (spec 51). ref is a run id or a run number.
func (s *Service) RunDetail(ctx context.Context, id, ref string) (RunDetail, error) {
	run, err := s.resolveRun(ctx, id, ref)
	if err != nil {
		return RunDetail{}, err
	}
	var graph Graph
	if g, err := runGraph(ctx, s.store, run); err == nil {
		graph = g.Normalized()
	}
	nodes, _ := s.store.ListNodeRuns(ctx, run.ID)
	decisions, _ := s.store.ListDecisions(ctx, DecisionFilter{RunID: run.ID})
	return RunDetail{Run: run, Graph: graph, Nodes: nodes, Decisions: decisions}, nil
}

// resolveRun finds a run by id or number and checks it belongs to the workflow.
func (s *Service) resolveRun(ctx context.Context, id, ref string) (Run, error) {
	var (
		run Run
		err error
	)
	if isDigits(ref) {
		number, cerr := strconv.Atoi(ref)
		if cerr != nil {
			return Run{}, ErrNotFound
		}
		run, err = s.store.GetRunByNumber(ctx, id, number)
	} else {
		run, err = s.store.GetRun(ctx, ref)
	}
	if err != nil {
		return Run{}, err
	}
	if run.WorkflowID != id {
		return Run{}, ErrNotFound
	}
	return run, nil
}

// State lists the workflow state entries (API-WF-016, REQ-WF-006).
func (s *Service) State(ctx context.Context, id string) ([]StateEntry, error) {
	return s.store.ListState(ctx, id)
}

// Chat lists the last messages of the workflow assistant chat (API-WF-017,
// REQ-WF-010).
func (s *Service) Chat(ctx context.Context, id string) ([]ChatMessage, error) {
	return s.store.ListChat(ctx, id, 100)
}

// IsWorkflowSession reports whether a session runs on channel "workflow"
// (REQ-WF-003). It is false without a configured check.
func (s *Service) IsWorkflowSession(sessionID string) bool {
	if s.sessionCheck == nil {
		return false
	}
	return s.sessionCheck(sessionID)
}

// ---- write operations -----------------------------------------------------

// Create saves a new workflow and its first version (API-WF-002, REQ-WF-001,
// REQ-WF-002).
func (s *Service) Create(ctx context.Context, in CreateInput) (Detail, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Detail{}, &InvalidGraphError{Issues: []Issue{{Code: "invalid_name", Message: "a workflow needs a name"}}}
	}
	if !s.deps.AgentExists(in.Guardian) {
		return Detail{}, &InvalidGraphError{Issues: []Issue{{
			Code:    "unknown_agent",
			Message: fmt.Sprintf("guardian %q is not a known agent", in.Guardian),
		}}}
	}
	graph := defaultGraph()
	if in.Graph != nil {
		graph = *in.Graph
	}
	graph = graph.Normalized()
	if issues := Validate(graph, s.deps.AgentExists); len(issues) > 0 {
		return Detail{}, &InvalidGraphError{Issues: issues}
	}

	now := s.now().UnixMilli()
	created, err := s.store.CreateWorkflow(ctx, Workflow{
		ID:             Slug(name),
		Name:           name,
		Description:    in.Description,
		Guardian:       in.Guardian,
		Status:         StatusDraft,
		FailureDefault: FailurePolicy{Mode: "guardian", Retries: intPtr(2), BackoffSecs: []int{10, 30}},
		WebhookToken:   newWebhookToken(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}, graph, "owner", "created")
	if err != nil {
		return Detail{}, err
	}
	s.broadcast(created.ID, "")
	return s.Get(ctx, created.ID)
}

// Update patches a workflow; a graph change saves a new owner version
// (API-WF-004, REQ-WF-001, REQ-WF-009).
func (s *Service) Update(ctx context.Context, id string, in UpdateInput) (Detail, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}

	graphChanged := false
	var newGraph Graph
	if in.Graph != nil {
		if in.BaseVersion != wf.Version {
			return Detail{}, ErrVersionConflict
		}
		graph := in.Graph.Normalized()
		if issues := Validate(graph, s.deps.AgentExists); len(issues) > 0 {
			return Detail{}, &InvalidGraphError{Issues: issues}
		}
		summary := in.Summary
		if summary == "" {
			summary = "edited"
		}
		if _, err := s.store.SaveVersion(ctx, id, in.BaseVersion, graph, "owner", summary); err != nil {
			return Detail{}, err
		}
		wf, err = s.store.GetWorkflow(ctx, id)
		if err != nil {
			return Detail{}, err
		}
		newGraph = graph
		graphChanged = true
	}

	if in.Name != nil {
		wf.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		wf.Description = *in.Description
	}
	if in.FailureDefault != nil {
		wf.FailureDefault = *in.FailureDefault
	}
	if in.Guardian != nil {
		if !s.deps.AgentExists(*in.Guardian) {
			return Detail{}, &InvalidGraphError{Issues: []Issue{{
				Code:    "unknown_agent",
				Message: fmt.Sprintf("guardian %q is not a known agent", *in.Guardian),
			}}}
		}
		if *in.Guardian != wf.Guardian {
			wf.Guardian = *in.Guardian
			// A new guardian must accept the lock again (REQ-WF-010).
			switch wf.Status {
			case StatusActive, StatusPaused, StatusError:
				wf.Status = StatusDraft
			}
		}
	}

	if graphChanged && wf.Status == StatusActive && !LockCovers(wf.Lock, newGraph) {
		wf.Status = StatusDraft
	}

	wf.UpdatedAt = s.now().UnixMilli()
	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return Detail{}, err
	}
	s.broadcast(id, "")
	return s.Get(ctx, id)
}

// Delete cancels the live runs and removes a workflow (API-WF-005,
// REQ-WF-001).
func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := s.store.GetWorkflow(ctx, id); err != nil {
		return err
	}
	live, err := s.store.ListRuns(ctx, RunFilter{WorkflowID: id, Statuses: []RunStatus{RunRunning, RunWaiting}})
	if err == nil {
		for _, r := range live {
			_, _ = s.engine.Cancel(ctx, r.ID)
		}
	}
	if err := s.store.DeleteWorkflow(ctx, id); err != nil {
		return err
	}
	s.broadcast(id, "")
	return nil
}

// Restore saves a stored version as a new owner version (API-WF-007,
// REQ-WF-001, REQ-WF-010).
func (s *Service) Restore(ctx context.Context, id string, version int) (Detail, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	ver, err := s.store.GetVersion(ctx, id, version)
	if err != nil {
		return Detail{}, err
	}
	graph := ver.Graph.Normalized()
	if _, err := s.store.SaveVersion(ctx, id, wf.Version, graph, "owner", fmt.Sprintf("restore v%d", version)); err != nil {
		return Detail{}, err
	}
	current, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if current.Status == StatusActive && !LockCovers(current.Lock, graph) {
		current.Status = StatusDraft
		current.UpdatedAt = s.now().UnixMilli()
		if err := s.store.UpdateWorkflow(ctx, current); err != nil {
			return Detail{}, err
		}
	}
	s.broadcast(id, "")
	return s.Get(ctx, id)
}

// Activate locks the tools of a version and makes it active (API-WF-009,
// REQ-WF-009).
func (s *Service) Activate(ctx context.Context, id string, version int) (Detail, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if version != wf.Version {
		return Detail{}, ErrVersionConflict
	}
	ver, err := s.store.GetVersion(ctx, id, version)
	if err != nil {
		return Detail{}, err
	}
	if issues := ValidateForActivation(ver.Graph, s.deps.AgentExists); len(issues) > 0 {
		return Detail{}, &InvalidGraphError{Issues: issues}
	}
	now := s.now().UnixMilli()
	wf.Lock = &Lock{Version: version, LockedAt: now, Tools: ComputeLock(ver.Graph, s.deps.ToolRisk)}
	wf.Status = StatusActive
	wf.ConsecutiveFailures = 0
	wf.UpdatedAt = now
	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return Detail{}, err
	}
	s.broadcast(id, "")
	return s.Get(ctx, id)
}

// Pause stops a workflow's triggers from firing (API-WF-010, REQ-WF-003).
func (s *Service) Pause(ctx context.Context, id string) (Detail, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	wf.Status = StatusPaused
	wf.UpdatedAt = s.now().UnixMilli()
	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return Detail{}, err
	}
	s.broadcast(id, "")
	return s.Get(ctx, id)
}

// Resume reactivates a paused workflow; it needs a current lock (API-WF-010,
// REQ-WF-009).
func (s *Service) Resume(ctx context.Context, id string) (Detail, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	ver, err := s.store.GetVersion(ctx, id, wf.Version)
	if err != nil {
		return Detail{}, err
	}
	if !LockCovers(wf.Lock, ver.Graph) {
		return Detail{}, ErrLockRequired
	}
	wf.Status = StatusActive
	wf.ConsecutiveFailures = 0
	wf.UpdatedAt = s.now().UnixMilli()
	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return Detail{}, err
	}
	s.broadcast(id, "")
	return s.Get(ctx, id)
}

// StartRun creates and launches a run (API-WF-011, REQ-WF-003). It refuses to
// start while a run of the workflow is already running.
func (s *Service) StartRun(ctx context.Context, id, trigger string, input json.RawMessage) (Run, error) {
	return s.startRun(ctx, id, trigger, input, nil)
}

// startRun creates and launches a run of the current version, or of candidate
// when it is not nil (spec 51 REQ-WFC-005): the run then carries the
// candidate id and its base version. It refuses with ErrRunActive while a run
// of the workflow is running.
func (s *Service) startRun(ctx context.Context, id, trigger string, input json.RawMessage, candidate *Candidate) (Run, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Run{}, err
	}

	// A per-workflow mutex keeps two concurrent starts from both passing the
	// running check.
	lock := s.runLock(id)
	lock.Lock()
	defer lock.Unlock()

	active, err := s.store.ListRuns(ctx, RunFilter{WorkflowID: id, Statuses: []RunStatus{RunRunning}})
	if err != nil {
		return Run{}, err
	}
	if len(active) > 0 {
		return Run{}, ErrRunActive
	}

	version := wf.Version
	var graph Graph
	var candidateID int64
	if candidate != nil {
		version = candidate.BaseVersion
		graph = candidate.Graph
		candidateID = candidate.ID
	} else {
		ver, err := s.store.GetVersion(ctx, id, wf.Version)
		if err != nil {
			return Run{}, err
		}
		graph = ver.Graph
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	run, err := s.store.CreateRun(ctx, Run{
		WorkflowID:  id,
		Version:     version,
		CandidateID: candidateID,
		Trigger:     trigger,
		Input:       input,
		Status:      RunQueued,
		StartedAt:   s.now().UnixMilli(),
		NodesTotal:  len(graph.Nodes),
	})
	if err != nil {
		return Run{}, err
	}
	s.engine.Launch(s.rootContext(), run.ID)
	s.broadcast(id, run.ID)
	return run, nil
}

// runLock returns the per-workflow start mutex.
func (s *Service) runLock(id string) *sync.Mutex {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	mu, ok := s.runLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		s.runLocks[id] = mu
	}
	return mu
}

// Cancel stops a run (API-WF-014, REQ-WF-007). ref is a run id or number.
func (s *Service) Cancel(ctx context.Context, id, ref string) (Run, error) {
	run, err := s.resolveRun(ctx, id, ref)
	if err != nil {
		return Run{}, err
	}
	return s.engine.Cancel(ctx, run.ID)
}

// Answer resolves a pending decision (API-WF-015, REQ-WF-005, REQ-WF-008).
func (s *Service) Answer(ctx context.Context, id, decisionID, choice string) (Run, error) {
	d, err := s.store.GetDecision(ctx, decisionID)
	if err != nil {
		return Run{}, err
	}
	if d.WorkflowID != id {
		return Run{}, ErrNotFound
	}
	return s.engine.Answer(ctx, decisionID, choice)
}

// SetState writes one state entry (API-WF-016, REQ-WF-006).
func (s *Service) SetState(ctx context.Context, id, key string, value json.RawMessage) (StateEntry, error) {
	if !json.Valid(value) {
		return StateEntry{}, errors.New("invalid JSON value")
	}
	if err := s.store.SetState(ctx, id, key, value); err != nil {
		return StateEntry{}, err
	}
	s.broadcast(id, "")
	return StateEntry{Key: key, Value: value, UpdatedAt: s.now().UnixMilli()}, nil
}

// DeleteState removes one state entry, or all when key is empty (API-WF-016,
// REQ-WF-006).
func (s *Service) DeleteState(ctx context.Context, id, key string) error {
	if err := s.store.DeleteState(ctx, id, key); err != nil {
		return err
	}
	s.broadcast(id, "")
	return nil
}

// RotateWebhook replaces a workflow's webhook secret (API-WF-018,
// REQ-WF-003).
func (s *Service) RotateWebhook(ctx context.Context, id string) (string, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return "", err
	}
	token := newWebhookToken()
	wf.WebhookToken = token
	wf.UpdatedAt = s.now().UnixMilli()
	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return "", err
	}
	s.broadcast(id, "")
	return token, nil
}

// ---- triggers -------------------------------------------------------------

// Webhook authenticates and handles an inbound hook (API-WF-020, REQ-WF-003).
func (s *Service) Webhook(ctx context.Context, id, token string, body []byte) (Run, error) {
	wf, err := s.store.GetWorkflow(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if token == "" || wf.WebhookToken == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(wf.WebhookToken)) != 1 {
		return Run{}, ErrBadToken
	}
	if wf.Status != StatusActive {
		return Run{}, ErrNotFound
	}
	ver, err := s.store.GetVersion(ctx, id, wf.Version)
	if err != nil {
		return Run{}, err
	}
	if !hasEnabledTriggerOfType(ver.Graph, "trigger.webhook") {
		return Run{}, ErrNotFound
	}

	input := json.RawMessage(body)
	if !json.Valid(body) {
		input, _ = json.Marshal(map[string]string{"text": string(body)})
	}
	return s.StartRun(ctx, id, "webhook", input)
}

// Command matches a chat command and starts a run (API-WF-021, REQ-WF-003).
func (s *Service) Command(ctx context.Context, channel, text string) (CommandResult, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return CommandResult{}, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return CommandResult{}, false
	}
	word := fields[0][1:]
	if i := strings.IndexByte(word, '@'); i >= 0 {
		word = word[:i]
	}
	command := strings.ToLower(strings.TrimSpace(word))
	if command == "" {
		return CommandResult{}, false
	}
	rest := strings.TrimSpace(trimmed[len(fields[0]):])

	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		return CommandResult{}, false
	}
	for _, wf := range wfs {
		if wf.Status != StatusActive {
			continue
		}
		ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
		if err != nil {
			continue
		}
		if !chatCommandMatches(ver.Graph, command) {
			continue
		}
		input, _ := json.Marshal(map[string]string{"text": rest, "channel": channel})
		run, err := s.StartRun(ctx, wf.ID, "chat", input)
		switch {
		case err == nil:
			return CommandResult{
				WorkflowID: wf.ID, RunID: run.ID, Number: run.Number,
				Reply: fmt.Sprintf("Run #%d of %s started", run.Number, wf.Name),
			}, true
		case errors.Is(err, ErrRunActive):
			return CommandResult{WorkflowID: wf.ID, Reply: wf.Name + " is already running"}, true
		default:
			return CommandResult{WorkflowID: wf.ID, Reply: fmt.Sprintf("Could not start %s: %v", wf.Name, err)}, true
		}
	}
	return CommandResult{}, false
}

// HandleEvent starts runs for the active event triggers matching kind
// (REQ-WF-003). Events from workflow sessions never fire.
func (s *Service) HandleEvent(ctx context.Context, kind, sessionID, agent string, data json.RawMessage) {
	if s.IsWorkflowSession(sessionID) {
		return
	}
	wfs, err := s.store.ListWorkflows(ctx)
	if err != nil {
		return
	}
	for _, wf := range wfs {
		if wf.Status != StatusActive {
			continue
		}
		ver, err := s.store.GetVersion(ctx, wf.ID, wf.Version)
		if err != nil {
			continue
		}
		if !eventTriggerMatches(ver.Graph, kind, agent) {
			continue
		}
		input, _ := json.Marshal(map[string]any{
			"kind": kind, "session_id": sessionID, "agent": agent, "data": json.RawMessage(data),
		})
		if _, err := s.StartRun(ctx, wf.ID, "event", input); err != nil && !errors.Is(err, ErrRunActive) {
			s.logger.Error("workflow: event run", "workflow", wf.ID, "error", err)
		}
	}
}

// ---- helpers --------------------------------------------------------------

// defaultGraph is the one-node manual graph Create uses without a body graph
// (API-WF-002).
func defaultGraph() Graph {
	return Graph{Nodes: []Node{{
		ID: "run-button", Type: "trigger.manual", Name: "Run button",
		Position: Position{X: 240, Y: 0}, Params: map[string]any{"enabled": true},
	}}}
}

// hasEnabledTriggerOfType reports whether the graph has an enabled trigger of a
// type.
func hasEnabledTriggerOfType(g Graph, nodeType string) bool {
	for _, n := range g.Nodes {
		if n.Type == nodeType && triggerEnabled(n) {
			return true
		}
	}
	return false
}

// chatCommandMatches reports whether the graph has an enabled trigger.chat node
// whose command matches (leading "/" and case ignored).
func chatCommandMatches(g Graph, command string) bool {
	for _, n := range g.Nodes {
		if n.Type != "trigger.chat" || !triggerEnabled(n) {
			continue
		}
		c, _ := n.Params["command"].(string)
		c = strings.ToLower(strings.TrimSpace(c))
		c = strings.TrimPrefix(c, "/")
		if c == command {
			return true
		}
	}
	return false
}

// eventTriggerMatches reports whether the graph has an enabled trigger.event
// node for the kind, with no agent filter or the same agent.
func eventTriggerMatches(g Graph, kind, agent string) bool {
	for _, n := range g.Nodes {
		if n.Type != "trigger.event" || !triggerEnabled(n) {
			continue
		}
		k, _ := n.Params["kind"].(string)
		if k != kind {
			continue
		}
		filter, _ := n.Params["agent"].(string)
		if filter == "" || filter == agent {
			return true
		}
	}
	return false
}

// paramEnabled reports whether a trigger node's enabled param is not false.
func paramEnabled(params map[string]any) bool {
	if b, ok := params["enabled"].(bool); ok {
		return b
	}
	return true
}

// newWebhookToken returns 32 random bytes as hex (API-WF-002, REQ-WF-003).
func newWebhookToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return randomHexID("", 64)
	}
	return hex.EncodeToString(buf)
}

// intPtr returns a pointer to n.
func intPtr(n int) *int { return &n }

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
