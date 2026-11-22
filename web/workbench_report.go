package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
)

// The daemon-side worktree reports of spec 69 (REQ-WA-030..034): the worktree
// an agent reported for a Workbench session, the session events that carry it,
// and the small bus a report change publishes on.
const (
	// workbenchEventSessionUpdated is the event a report change publishes and
	// the type of the core session events the daemon decorates (REQ-WA-033).
	workbenchEventSessionUpdated = "session.updated"
	// workbenchEventSessionRemoved is the core event that ends a report at
	// once (REQ-WA-032).
	workbenchEventSessionRemoved = "session.removed"
	// workbenchBusBuffer is one bus subscriber's queue; a full queue drops the
	// event for that subscriber, so a slow reader never holds up a publisher.
	workbenchBusBuffer = 32
	// workbenchLocalWorktreePath is the daemon-only mirror of the report route
	// (spec 69, API-WLG-008): the CLI reports and clears a session's worktree.
	workbenchLocalWorktreePath = "/v1/local/workbench/sessions/{id}/worktree"
	// workbenchLocalWorktreesPath is the daemon-only mirror of the spec 66
	// create route (spec 69): the CLI creates a worktree.
	workbenchLocalWorktreesPath = "/v1/local/workbench/git/worktrees"
)

// workbenchReport is one session's reported worktree (REQ-WA-031): the session
// id, the worktree root an agent reported, the time it reported, and the agent
// that was in the foreground then ("" when none).
type workbenchReport struct {
	Session    string    `json:"session"`
	WorkDir    string    `json:"work_dir"`
	ReportedAt time.Time `json:"reported_at"`
	Agent      string    `json:"agent"`
}

// workbenchReportStore keeps the worktree reports keyed by session id
// (REQ-WA-031). It persists them to <home>/workbench/worktree-reports.json and
// keeps them in memory only when the daemon has no home. The file is read
// lazily on first use and rewritten atomically on every change. It is safe for
// concurrent use.
type workbenchReportStore struct {
	path    string
	once    sync.Once
	mu      sync.Mutex
	reports map[string]workbenchReport
}

// newWorkbenchReportStore returns a store under the given Lana home; an empty
// home keeps the reports in memory only.
func newWorkbenchReportStore(home string) *workbenchReportStore {
	st := &workbenchReportStore{}
	if home != "" {
		st.path = filepath.Join(home, "workbench", "worktree-reports.json")
	}
	return st
}

// load reads the file once; a missing or invalid file starts empty.
func (st *workbenchReportStore) load() {
	if st == nil {
		return
	}
	st.once.Do(func() {
		st.reports = map[string]workbenchReport{}
		if st.path == "" {
			return
		}
		data, err := os.ReadFile(st.path)
		if err != nil {
			return
		}
		var reports map[string]workbenchReport
		if err := json.Unmarshal(data, &reports); err != nil {
			return
		}
		for id, rep := range reports {
			st.reports[id] = rep
		}
	})
}

// get returns the report of a session.
func (st *workbenchReportStore) get(session string) (workbenchReport, bool) {
	if st == nil {
		return workbenchReport{}, false
	}
	st.load()
	st.mu.Lock()
	defer st.mu.Unlock()
	rep, ok := st.reports[session]
	return rep, ok
}

// put stores a report and persists the store.
func (st *workbenchReportStore) put(rep workbenchReport) error {
	if st == nil {
		return nil
	}
	st.load()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.reports[rep.Session] = rep
	return st.persistLocked()
}

// delete removes a session's report and persists the store. Removing a report
// that is not there changes nothing.
func (st *workbenchReportStore) delete(session string) error {
	if st == nil {
		return nil
	}
	st.load()
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.reports[session]; !ok {
		return nil
	}
	delete(st.reports, session)
	return st.persistLocked()
}

// ids returns the session ids the store holds a report for.
func (st *workbenchReportStore) ids() []string {
	if st == nil {
		return nil
	}
	st.load()
	st.mu.Lock()
	defer st.mu.Unlock()
	ids := make([]string, 0, len(st.reports))
	for id := range st.reports {
		ids = append(ids, id)
	}
	return ids
}

// persistLocked writes the reports to the file atomically, so a reader never
// sees a partial file. A store without a path keeps them in memory only.
func (st *workbenchReportStore) persistLocked() error {
	if st.path == "" {
		return nil
	}
	data, err := json.Marshal(st.reports)
	if err != nil {
		return err
	}
	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "worktree-reports-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, st.path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// workbenchBusEvent is one event of the daemon's workbench bus: the SSE event
// name and the data to write under it.
type workbenchBusEvent struct {
	Type string
	Data any
}

// workbenchEventBus is a small in-process publish/subscribe bus for the daemon
// workbench events (spec 69). The /v1/workbench/events streams subscribe to it;
// a full subscriber queue drops the event, so a slow reader never holds up a
// publisher.
type workbenchEventBus struct {
	mu   sync.Mutex
	subs map[chan workbenchBusEvent]struct{}
}

// newWorkbenchEventBus returns an empty bus.
func newWorkbenchEventBus() *workbenchEventBus {
	return &workbenchEventBus{subs: map[chan workbenchBusEvent]struct{}{}}
}

// subscribe registers a subscriber and returns its channel and a cancel func.
func (b *workbenchEventBus) subscribe() (<-chan workbenchBusEvent, func()) {
	ch := make(chan workbenchBusEvent, workbenchBusBuffer)
	if b == nil {
		return ch, func() {}
	}
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// publish sends ev to every subscriber, dropping it for a full queue.
func (b *workbenchEventBus) publish(ev workbenchBusEvent) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// PublishWorkbenchEvent sends one event to every open /v1/workbench/events
// stream (spec 69). It is exported so later features reuse the bus.
func (s *Server) PublishWorkbenchEvent(typ string, data any) {
	s.workbenchBus.publish(workbenchBusEvent{Type: typ, Data: data})
}

// decorateWorkbenchSession sets WorkDir and WorkDirAt from the report store and
// applies the end rules of REQ-WA-032: a report that no longer matches the
// session is dropped and the session is returned without the fields.
func (s *Server) decorateWorkbenchSession(sess workbench.Session) workbench.Session {
	rep, ok := s.workbenchReports.get(sess.ID)
	if !ok {
		return sess
	}
	if reportEnded(sess, rep) {
		s.dropWorkbenchReport(sess.ID)
		return sess
	}
	sess.WorkDir = rep.WorkDir
	at := rep.ReportedAt
	sess.WorkDirAt = &at
	return sess
}

// reportEnded reports whether a stored report ends for the session it was made
// for (REQ-WA-032): the session exited, another agent than the reported one
// runs, or the foreground agent left and the session is not a restore that
// resumes the reported agent.
func reportEnded(sess workbench.Session, rep workbenchReport) bool {
	if sess.State == workbenchExitedState {
		return true
	}
	if sess.Agent != "" {
		return rep.Agent != "" && sess.Agent != rep.Agent
	}
	// No foreground agent: only a restored session that resumes the reported
	// agent keeps the report.
	if sess.Restored == nil {
		return true
	}
	return sess.ResumedAgent != rep.Agent
}

// dropWorkbenchReport removes a report the end rules ended.
func (s *Server) dropWorkbenchReport(id string) {
	if err := s.workbenchReports.delete(id); err != nil {
		s.logger.Warn("web: workbench report drop", "error", err)
	}
}

// decorateWorkbenchEvent decorates the session of a core event with the report
// store (REQ-WA-033). A session.removed event ends the session's report at
// once.
func (s *Server) decorateWorkbenchEvent(ev workbench.Event) workbench.Event {
	if ev.Session == nil {
		return ev
	}
	if ev.Type == workbenchEventSessionRemoved {
		s.dropWorkbenchReport(ev.Session.ID)
		return ev
	}
	decorated := s.decorateWorkbenchSession(*ev.Session)
	ev.Session = &decorated
	return ev
}

// dropOrphanWorkbenchReports removes the reports of sessions no longer in a
// session list (REQ-WA-032).
func (s *Server) dropOrphanWorkbenchReports(sessions []workbench.Session) {
	live := make(map[string]struct{}, len(sessions))
	for _, sess := range sessions {
		live[sess.ID] = struct{}{}
	}
	for _, id := range s.workbenchReports.ids() {
		if _, ok := live[id]; !ok {
			s.dropWorkbenchReport(id)
		}
	}
}

// workbenchReportBody is the PUT /v1/workbench/sessions/{id}/worktree body
// (spec 69 api-contract).
type workbenchReportBody struct {
	Path string `json:"path"`
}

// workbenchReportAnswer is the answer of the report route (spec 69
// api-contract): the stored report and the resolved worktree, the shape of one
// /v1/workbench/git/resolve entry (REQ-WA-030).
type workbenchReportAnswer struct {
	Session    string       `json:"session"`
	WorkDir    string       `json:"work_dir"`
	ReportedAt time.Time    `json:"reported_at"`
	Agent      string       `json:"agent"`
	Worktree   gitinfo.Info `json:"worktree"`
}

// handleWorkbenchSetWorktree serves PUT
// /v1/workbench/sessions/{id}/worktree (REQ-WA-030).
func (s *Server) handleWorkbenchSetWorktree(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	var body workbenchReportBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	s.setWorkbenchReport(w, r, backend, r.PathValue("id"), body.Path)
}

// handleWorkbenchClearWorktree serves DELETE
// /v1/workbench/sessions/{id}/worktree (REQ-WA-030).
func (s *Server) handleWorkbenchClearWorktree(w http.ResponseWriter, r *http.Request) {
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	s.clearWorkbenchReport(w, r, backend, r.PathValue("id"))
}

// setWorkbenchReport resolves path to its worktree, stores the session's report
// and answers it (REQ-WA-030); the browser route and its local mirror share it.
// An unknown session is 404 not_found, a path the policy refuses is 403
// forbidden_path and one outside any repository is 404 not_a_repo.
func (s *Server) setWorkbenchReport(w http.ResponseWriter, r *http.Request, backend WorkbenchBackend, id, path string) {
	sess, err := backend.Get(r.Context(), id)
	if err != nil {
		s.writeWorkbenchError(w, "workbench report worktree", err)
		return
	}
	runner, _ := s.workbenchGit()
	info, err := runner.Resolve(r.Context(), path)
	if err != nil {
		s.writeWorkbenchGitError(w, err)
		return
	}
	rep := workbenchReport{
		Session:    id,
		WorkDir:    info.Worktree,
		ReportedAt: s.nowTime(),
		Agent:      sess.Agent,
	}
	if err := s.workbenchReports.put(rep); err != nil {
		s.logger.Error("web: workbench report", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.publishWorkbenchReportEvent(r.Context(), backend, id)
	writeJSON(w, http.StatusOK, workbenchReportAnswer{
		Session:    rep.Session,
		WorkDir:    rep.WorkDir,
		ReportedAt: rep.ReportedAt,
		Agent:      rep.Agent,
		Worktree:   info,
	})
}

// clearWorkbenchReport removes a session's report and answers 204, also when
// there was none (REQ-WA-030).
func (s *Server) clearWorkbenchReport(w http.ResponseWriter, r *http.Request, backend WorkbenchBackend, id string) {
	if err := s.workbenchReports.delete(id); err != nil {
		s.logger.Error("web: workbench report", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.publishWorkbenchReportEvent(r.Context(), backend, id)
	w.WriteHeader(http.StatusNoContent)
}

// publishWorkbenchReportEvent publishes session.updated with the decorated
// session after a report change (REQ-WA-033), so open pages update at once.
func (s *Server) publishWorkbenchReportEvent(ctx context.Context, backend WorkbenchBackend, id string) {
	sess, err := backend.Get(ctx, id)
	if err != nil {
		return
	}
	decorated := s.decorateWorkbenchSession(sess)
	s.PublishWorkbenchEvent(workbenchEventSessionUpdated, workbench.Event{
		Type:    workbenchEventSessionUpdated,
		Session: &decorated,
	})
}

// handleLocalWorkbenchWorktree answers the local report route (spec 69, API-WLG-008):
// PUT sets a session's worktree, DELETE removes it. It authenticates like the
// other local control routes and never goes through the browser session.
func (s *Server) handleLocalWorkbenchWorktree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodPut+", "+http.MethodDelete)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only PUT and DELETE are allowed")
		return
	}
	if !s.localControlAllowed(w, r) {
		return
	}
	backend := s.workbenchOrError(w)
	if backend == nil {
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodDelete {
		s.clearWorkbenchReport(w, r, backend, id)
		return
	}
	var body workbenchReportBody
	if !decodeJSONObject(w, r, workbenchBodyLimit, &body) {
		return
	}
	s.setWorkbenchReport(w, r, backend, id, body.Path)
}

// handleLocalWorkbenchWorktreeCreate answers the local create route (spec 69):
// the spec 66 create (REQ-WG-031) for the CLI.
func (s *Server) handleLocalWorkbenchWorktreeCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if !s.localControlAllowed(w, r) {
		return
	}
	if s.workbenchOrError(w) == nil {
		return
	}
	s.createWorkbenchWorktree(w, r)
}
