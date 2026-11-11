package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// MemStore is an in-memory Store used by tests and by the memstore runtime.
// It is safe for concurrent use and returns deep copies, so a caller can never
// mutate a stored value through a returned slice or map.
//
// REQ-WF-006: the per-workflow state map lives across runs.
// REQ-WF-007: runs, node runs and decisions survive for as long as the store does.
// REQ-WF-009: the approved lock is carried on the workflow record.
type MemStore struct {
	mu        sync.Mutex
	workflows map[string]Workflow
	versions  map[string][]Version
	runs      map[string]Run
	nodeRuns  map[string][]NodeRun
	decisions map[string]Decision
	state     map[string]map[string]StateEntry
	chat      map[string][]ChatMessage
	chatSeq   int64
	// candidates holds every candidate by id (spec 51).
	candidates   map[int64]Candidate
	candidateSeq int64
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		workflows:  map[string]Workflow{},
		versions:   map[string][]Version{},
		runs:       map[string]Run{},
		nodeRuns:   map[string][]NodeRun{},
		decisions:  map[string]Decision{},
		state:      map[string]map[string]StateEntry{},
		chat:       map[string][]ChatMessage{},
		candidates: map[int64]Candidate{},
	}
}

// MemStore implements Store.
var _ Store = (*MemStore)(nil)

// CreateWorkflow stores a new workflow and its first version (REQ-WF-001). It
// returns ErrExists when the id is already taken and always sets Version to 1.
func (s *MemStore) CreateWorkflow(_ context.Context, w Workflow, g Graph, author, summary string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workflows[w.ID]; ok {
		return Workflow{}, ErrExists
	}
	w.Version = 1
	s.workflows[w.ID] = cloneWorkflow(w)
	s.versions[w.ID] = []Version{{
		WorkflowID: w.ID,
		Version:    1,
		Graph:      cloneGraph(g),
		Author:     author,
		Summary:    summary,
		CreatedAt:  time.Now().UnixMilli(),
	}}
	return cloneWorkflow(w), nil
}

// GetWorkflow returns one workflow by id, or ErrNotFound.
func (s *MemStore) GetWorkflow(_ context.Context, id string) (Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workflows[id]
	if !ok {
		return Workflow{}, ErrNotFound
	}
	return cloneWorkflow(w), nil
}

// ListWorkflows returns every workflow ordered by name, then id.
func (s *MemStore) ListWorkflows(_ context.Context) ([]Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Workflow, 0, len(s.workflows))
	for _, w := range s.workflows {
		out = append(out, cloneWorkflow(w))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// UpdateWorkflow writes workflow metadata only; it keeps the stored Version
// and CreatedAt and returns ErrNotFound when the id is unknown.
func (s *MemStore) UpdateWorkflow(_ context.Context, w Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.workflows[w.ID]
	if !ok {
		return ErrNotFound
	}
	w.Version = current.Version
	w.CreatedAt = current.CreatedAt
	s.workflows[w.ID] = cloneWorkflow(w)
	return nil
}

// SaveVersion appends a new graph version when base is the current version
// (REQ-WF-001). It returns ErrVersionConflict otherwise, and updates the stored
// workflow's Version and UpdatedAt on success.
func (s *MemStore) SaveVersion(_ context.Context, id string, base int, g Graph, author, summary string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workflows[id]
	if !ok {
		return Version{}, ErrNotFound
	}
	if base != w.Version {
		return Version{}, ErrVersionConflict
	}
	now := time.Now().UnixMilli()
	v := Version{
		WorkflowID: id,
		Version:    w.Version + 1,
		Graph:      cloneGraph(g),
		Author:     author,
		Summary:    summary,
		CreatedAt:  now,
	}
	w.Version = v.Version
	w.UpdatedAt = now
	s.workflows[id] = cloneWorkflow(w)
	s.versions[id] = append(s.versions[id], v)
	return cloneVersion(v), nil
}

// GetVersion returns one stored version, or ErrNotFound.
func (s *MemStore) GetVersion(_ context.Context, id string, version int) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.versions[id] {
		if v.Version == version {
			return cloneVersion(v), nil
		}
	}
	return Version{}, ErrNotFound
}

// ListVersions returns the versions of a workflow, newest first.
func (s *MemStore) ListVersions(_ context.Context, id string) ([]Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Version, 0, len(s.versions[id]))
	for _, v := range s.versions[id] {
		out = append(out, cloneVersion(v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

// DeleteWorkflow removes a workflow and cascades to its versions, runs, node
// runs, decisions, state and chat, or returns ErrNotFound.
func (s *MemStore) DeleteWorkflow(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workflows[id]; !ok {
		return ErrNotFound
	}
	delete(s.workflows, id)
	delete(s.versions, id)
	delete(s.state, id)
	delete(s.chat, id)
	for runID, r := range s.runs {
		if r.WorkflowID == id {
			delete(s.runs, runID)
			delete(s.nodeRuns, runID)
		}
	}
	for decisionID, d := range s.decisions {
		if d.WorkflowID == id {
			delete(s.decisions, decisionID)
		}
	}
	for candidateID, c := range s.candidates {
		if c.WorkflowID == id {
			delete(s.candidates, candidateID)
		}
	}
	return nil
}

// CreateRun stores a run and sets its Number to max(number)+1 for the workflow
// (REQ-WF-007). An empty id gets a random "r_" + 16 hex characters id.
func (s *MemStore) CreateRun(_ context.Context, r Run) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	max := 0
	for _, existing := range s.runs {
		if existing.WorkflowID == r.WorkflowID && existing.Number > max {
			max = existing.Number
		}
	}
	r.Number = max + 1
	if r.ID == "" {
		r.ID = randomHexID("r_", 16)
	}
	s.runs[r.ID] = cloneRun(r)
	return cloneRun(r), nil
}

// UpdateRun writes an existing run, or returns ErrNotFound.
func (s *MemStore) UpdateRun(_ context.Context, r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[r.ID]; !ok {
		return ErrNotFound
	}
	s.runs[r.ID] = cloneRun(r)
	return nil
}

// GetRun returns one run by id, or ErrNotFound.
func (s *MemStore) GetRun(_ context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return cloneRun(r), nil
}

// GetRunByNumber returns one run by its workflow and run number, or ErrNotFound.
func (s *MemStore) GetRunByNumber(_ context.Context, workflowID string, number int) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.WorkflowID == workflowID && r.Number == number {
			return cloneRun(r), nil
		}
	}
	return Run{}, ErrNotFound
}

// ListRuns returns the runs matching the filter, newest first. With one
// workflow the order is by run number descending; across workflows it is by
// start time descending.
func (s *MemStore) ListRuns(_ context.Context, f RunFilter) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(s.runs))
	for _, r := range s.runs {
		if f.WorkflowID != "" && r.WorkflowID != f.WorkflowID {
			continue
		}
		if len(f.Statuses) > 0 && !containsRunStatus(f.Statuses, r.Status) {
			continue
		}
		if f.Since != 0 && r.StartedAt < f.Since {
			continue
		}
		if f.Before != 0 && r.Number >= f.Before {
			continue
		}
		out = append(out, cloneRun(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if f.WorkflowID != "" {
			if out[i].Number != out[j].Number {
				return out[i].Number > out[j].Number
			}
		} else if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID < out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// PutNodeRun upserts a node run by (run, node, item).
func (s *MemStore) PutNodeRun(_ context.Context, nr NodeRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.nodeRuns[nr.RunID]
	for i := range list {
		if list[i].NodeID == nr.NodeID && list[i].ItemIndex == nr.ItemIndex {
			list[i] = cloneNodeRun(nr)
			s.nodeRuns[nr.RunID] = list
			return nil
		}
	}
	s.nodeRuns[nr.RunID] = append(list, cloneNodeRun(nr))
	return nil
}

// ListNodeRuns returns every node run of one run, ordered by start time, then
// node id, then item index.
func (s *MemStore) ListNodeRuns(_ context.Context, runID string) ([]NodeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.nodeRuns[runID]
	out := make([]NodeRun, 0, len(list))
	for _, nr := range list {
		out = append(out, cloneNodeRun(nr))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt < out[j].StartedAt
		}
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].ItemIndex < out[j].ItemIndex
	})
	return out, nil
}

// PutDecision upserts a decision by id.
func (s *MemStore) PutDecision(_ context.Context, d Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions[d.ID] = cloneDecision(d)
	return nil
}

// GetDecision returns one decision by id, or ErrNotFound.
func (s *MemStore) GetDecision(_ context.Context, id string) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.decisions[id]
	if !ok {
		return Decision{}, ErrNotFound
	}
	return cloneDecision(d), nil
}

// ListDecisions returns the decisions matching the filter, newest first.
func (s *MemStore) ListDecisions(_ context.Context, f DecisionFilter) ([]Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Decision, 0, len(s.decisions))
	for _, d := range s.decisions {
		if f.WorkflowID != "" && d.WorkflowID != f.WorkflowID {
			continue
		}
		if f.RunID != "" && d.RunID != f.RunID {
			continue
		}
		if f.Status != "" && d.Status != f.Status {
			continue
		}
		out = append(out, cloneDecision(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ListState returns the workflow state entries ordered by key (REQ-WF-006).
func (s *MemStore) ListState(_ context.Context, workflowID string) ([]StateEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.state[workflowID]
	out := make([]StateEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, cloneState(e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SetState writes one workflow state entry with the current time (REQ-WF-006).
func (s *MemStore) SetState(_ context.Context, workflowID, key string, value json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.state[workflowID]
	if entries == nil {
		entries = map[string]StateEntry{}
		s.state[workflowID] = entries
	}
	entries[key] = StateEntry{Key: key, Value: cloneRaw(value), UpdatedAt: time.Now().UnixMilli()}
	return nil
}

// DeleteState removes one state entry; an empty key clears all of them
// (REQ-WF-006).
func (s *MemStore) DeleteState(_ context.Context, workflowID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		delete(s.state, workflowID)
		return nil
	}
	delete(s.state[workflowID], key)
	return nil
}

// AppendChat stores one chat message and assigns it the next id.
func (s *MemStore) AppendChat(_ context.Context, m ChatMessage) (ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatSeq++
	m.ID = s.chatSeq
	s.chat[m.WorkflowID] = append(s.chat[m.WorkflowID], cloneChat(m))
	return cloneChat(m), nil
}

// ListChat returns the last limit messages of a workflow, oldest first. A limit
// of 0 returns all of them.
func (s *MemStore) ListChat(_ context.Context, workflowID string, limit int) ([]ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.chat[workflowID]
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, cloneChat(m))
	}
	return out, nil
}

// CreateCandidate marks any open candidate of the workflow superseded and
// stores c as the new open candidate (spec 51 REQ-WFC-003). It sets ID, Status
// "open", CreatedAt and UpdatedAt, and returns ErrNotFound for an unknown
// workflow.
func (s *MemStore) CreateCandidate(_ context.Context, c Candidate) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workflows[c.WorkflowID]; !ok {
		return Candidate{}, ErrNotFound
	}
	now := time.Now().UnixMilli()
	for id, existing := range s.candidates {
		if existing.WorkflowID == c.WorkflowID && existing.Status == CandidateOpen {
			existing.Status = CandidateSuperseded
			existing.UpdatedAt = now
			s.candidates[id] = existing
		}
	}
	s.candidateSeq++
	c.ID = s.candidateSeq
	c.Status = CandidateOpen
	c.CreatedAt = now
	c.UpdatedAt = now
	s.candidates[c.ID] = cloneCandidate(c)
	return cloneCandidate(c), nil
}

// GetCandidate returns one candidate by id, or ErrNotFound.
func (s *MemStore) GetCandidate(_ context.Context, id int64) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.candidates[id]
	if !ok {
		return Candidate{}, ErrNotFound
	}
	return cloneCandidate(c), nil
}

// OpenCandidate returns the workflow's open candidate, or ErrNotFound. When
// several are open (never after CreateCandidate) the newest wins.
func (s *MemStore) OpenCandidate(_ context.Context, workflowID string) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best Candidate
	for _, c := range s.candidates {
		if c.WorkflowID == workflowID && c.Status == CandidateOpen && c.ID > best.ID {
			best = c
		}
	}
	if best.ID == 0 {
		return Candidate{}, ErrNotFound
	}
	return cloneCandidate(best), nil
}

// UpdateCandidate writes graph, summary, status, published_version and
// updated_at of an existing candidate, or returns ErrNotFound. A zero
// UpdatedAt becomes the current time.
func (s *MemStore) UpdateCandidate(_ context.Context, c Candidate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.candidates[c.ID]
	if !ok {
		return ErrNotFound
	}
	current.Graph = cloneGraph(c.Graph)
	current.Summary = c.Summary
	current.Status = c.Status
	current.PublishedVersion = c.PublishedVersion
	current.UpdatedAt = c.UpdatedAt
	if current.UpdatedAt == 0 {
		current.UpdatedAt = time.Now().UnixMilli()
	}
	s.candidates[c.ID] = current
	return nil
}

// cloneCandidate deep copies a candidate and its graph.
func cloneCandidate(c Candidate) Candidate {
	out := c
	out.Graph = cloneGraph(c.Graph)
	return out
}

// containsRunStatus reports whether statuses holds status.
func containsRunStatus(statuses []RunStatus, status RunStatus) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}
	return false
}

// randomHexID returns prefix followed by chars random hex characters. It falls
// back to the clock if the system random source fails.
func randomHexID(prefix string, chars int) string {
	buf := make([]byte, chars/2)
	if _, err := rand.Read(buf); err != nil {
		now := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(now >> (uint(i) * 8))
		}
	}
	return prefix + hex.EncodeToString(buf)
}

// cloneWorkflow deep copies a workflow, including its lock.
func cloneWorkflow(w Workflow) Workflow {
	out := w
	if w.Lock != nil {
		out.Lock = cloneLock(w.Lock)
	}
	out.FailureDefault = *cloneFailure(&w.FailureDefault)
	return out
}

// cloneLock deep copies a lock and its tool rows.
func cloneLock(l *Lock) *Lock {
	out := *l
	if l.Tools != nil {
		out.Tools = make([]LockTool, len(l.Tools))
		for i, t := range l.Tools {
			out.Tools[i] = t
			if t.UsedBy != nil {
				out.Tools[i].UsedBy = append([]string(nil), t.UsedBy...)
			}
		}
	}
	return &out
}

// cloneVersion deep copies a version and its graph.
func cloneVersion(v Version) Version {
	out := v
	out.Graph = cloneGraph(v.Graph)
	return out
}

// cloneGraph deep copies a graph.
func cloneGraph(g Graph) Graph {
	out := g
	if g.Nodes != nil {
		out.Nodes = make([]Node, len(g.Nodes))
		for i, n := range g.Nodes {
			out.Nodes[i] = cloneNode(n)
		}
	}
	if g.Edges != nil {
		out.Edges = append([]Edge(nil), g.Edges...)
	}
	return out
}

// cloneNode deep copies a node, its params and its failure policy.
func cloneNode(n Node) Node {
	out := n
	if n.Params != nil {
		out.Params = make(map[string]any, len(n.Params))
		for k, v := range n.Params {
			out.Params[k] = cloneAny(v)
		}
	}
	if n.OnFailure != nil {
		out.OnFailure = cloneFailure(n.OnFailure)
	}
	return out
}

// cloneFailure deep copies a failure policy.
func cloneFailure(f *FailurePolicy) *FailurePolicy {
	out := *f
	if f.Retries != nil {
		retries := *f.Retries
		out.Retries = &retries
	}
	if f.BackoffSecs != nil {
		out.BackoffSecs = append([]int(nil), f.BackoffSecs...)
	}
	out.Fallback = cloneAny(f.Fallback)
	return &out
}

// cloneRun deep copies a run, its input and its per-node overrides.
func cloneRun(r Run) Run {
	out := r
	out.Input = cloneRaw(r.Input)
	if r.Overrides != nil {
		out.Overrides = make(map[string]map[string]any, len(r.Overrides))
		for node, params := range r.Overrides {
			cp := make(map[string]any, len(params))
			for k, v := range params {
				cp[k] = cloneAny(v)
			}
			out.Overrides[node] = cp
		}
	}
	return out
}

// cloneNodeRun deep copies a node run, its attempts, io and logs.
func cloneNodeRun(nr NodeRun) NodeRun {
	out := nr
	if nr.Attempts != nil {
		out.Attempts = append([]Attempt(nil), nr.Attempts...)
	}
	out.Input = cloneRaw(nr.Input)
	out.Output = cloneRaw(nr.Output)
	if nr.Logs != nil {
		out.Logs = append([]string(nil), nr.Logs...)
	}
	return out
}

// cloneDecision deep copies a decision and its fix map.
func cloneDecision(d Decision) Decision {
	out := d
	if d.Fix != nil {
		out.Fix = make(map[string]any, len(d.Fix))
		for k, v := range d.Fix {
			out.Fix[k] = cloneAny(v)
		}
	}
	return out
}

// cloneState deep copies a state entry and its value.
func cloneState(e StateEntry) StateEntry {
	out := e
	out.Value = cloneRaw(e.Value)
	return out
}

// cloneChat deep copies a chat message, its change card and its issues.
func cloneChat(m ChatMessage) ChatMessage {
	out := m
	if m.Change != nil {
		change := *m.Change
		change.Added = append([]string(nil), m.Change.Added...)
		change.Removed = append([]string(nil), m.Change.Removed...)
		change.Changed = append([]string(nil), m.Change.Changed...)
		out.Change = &change
	}
	if m.Issues != nil {
		out.Issues = append([]Issue(nil), m.Issues...)
	}
	return out
}

// cloneAny deep copies a decoded JSON value (maps and slices only).
func cloneAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = cloneAny(item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = cloneAny(item)
		}
		return out
	default:
		return v
	}
}

// cloneRaw copies raw JSON bytes, keeping nil as nil.
func cloneRaw(r json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	out := make(json.RawMessage, len(r))
	copy(out, r)
	return out
}
