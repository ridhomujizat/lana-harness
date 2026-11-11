package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

type frameJSON struct {
	Kind   string          `json:"kind"`
	TurnID string          `json:"turn_id,omitempty"`
	Text   string          `json:"text,omitempty"`
	CallID string          `json:"call_id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Status string          `json:"status,omitempty"`
	Output string          `json:"output,omitempty"`
	// ChildSessionID and Agent tag a frame of a descendant session
	// (API-TEAM-011). Root frames leave them empty.
	ChildSessionID string `json:"child_session_id,omitempty"`
	Agent          string `json:"agent,omitempty"`
}

// childEventJSON is a stored event of a descendant session. It has the root
// event keys plus the child tags and is written without an SSE id line
// (API-TEAM-011).
type childEventJSON struct {
	eventJSON
	ChildSessionID string `json:"child_session_id"`
	Agent          string `json:"agent"`
}

// childSessionLister is an optional Backend extension that lists the direct
// children of a session (API-TEAM-011). A backend without it keeps the
// root-only stream behavior.
type childSessionLister interface {
	ChildSessions(ctx context.Context, parentID string) ([]core.SessionInfo, error)
}

type turnJSON struct {
	TurnID     string `json:"turn_id"`
	StopReason string `json:"stop_reason"`
}

type statusJSON struct {
	Holder      string `json:"holder"`
	PID         *int   `json:"pid"`
	TurnRunning bool   `json:"turn_running"`
}

// registerStreamRoutes registers API-WEB-008.
func (s *Server) registerStreamRoutes() {
	s.Handle("GET /v1/sessions/{id}/stream", http.HandlerFunc(s.handleSessionStream))
}

func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request) {
	backend := s.backendOrError(w)
	if backend == nil {
		return
	}
	fromSeq, err := parseStreamFromSeq(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	session, err := backend.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeBackendError(w, "stream session", err)
		return
	}
	id := session.ID
	holder, err := backend.Holder(r.Context(), id)
	if err != nil {
		s.writeBackendError(w, "stream status", err)
		return
	}
	running := backend.TurnActive(id)
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("web: stream: response writer cannot flush")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe before the first status and the replay, so a turn that starts
	// once the client sees the stream is open is not missed. Frames are
	// best-effort; the log poll below is the source of truth.
	sub, err := backend.Subscribe(r.Context(), id)
	if err != nil {
		s.logger.Warn("web: stream subscribe", "session", id, "error", err)
	}
	if sub != nil {
		defer sub.Close()
	}
	lastStatus := statusFromHolder(holder, running)
	if err := writeSSE(w, flusher, "status", "", lastStatus); err != nil {
		return
	}
	// REQ-TTL-011: the poll below sends a "title" event when the title, its
	// source, or the pending flag changes, so an open page shows the
	// automatic title when it arrives.
	titlePort := s.titleBackend()
	lastTitle := titleJSON{Title: session.Title, TitleSource: session.TitleSource, TitlePending: s.titlePending(id)}

	// Subscribe to approvals before replaying the open ones so a request that
	// opens during the handshake is not missed. The resolver lets a child
	// approval reach the root's stream (API-TEAM-011).
	approvalReplay, approvalEvents, unsubscribeApprovals := s.approvals.subscribe(id, func(childID string) string {
		return sessionRoot(r.Context(), backend, childID)
	})
	defer unsubscribeApprovals()
	for _, event := range approvalReplay {
		if err := writeSSE(w, flusher, "approval", "", event); err != nil {
			return
		}
	}

	// Questions subscribe the same way: before the replay, so a request that
	// opens during the handshake is not missed, and a child question reaches
	// the root's stream (API-ASK-004, API-TEAM-011).
	questionReplay, questionEvents, unsubscribeQuestions := s.questions.subscribe(id, func(childID string) string {
		return sessionRoot(r.Context(), backend, childID)
	})
	defer unsubscribeQuestions()
	for _, event := range questionReplay {
		if err := writeSSE(w, flusher, "question", "", event); err != nil {
			return
		}
	}

	var frames chan core.LiveFrame
	if sub != nil {
		frames = make(chan core.LiveFrame)
		go pumpFrames(r.Context(), sub, frames)
	}

	lastSeq := fromSeq
	page, err := backend.Events(r.Context(), id, lastSeq)
	if err != nil {
		s.logger.Warn("web: stream replay", "session", id, "error", err)
		return
	}
	if err := writeEventLines(w, flusher, page.Events, &lastSeq); err != nil {
		return
	}
	if page.LastSeq > lastSeq {
		lastSeq = page.LastSeq
	}

	// Children always replay from seq 1 on connect; the root's Last-Event-ID
	// and from_seq apply to the root's seq only (API-TEAM-011).
	childLastSeq := map[string]int64{}
	childAgents := map[string]string{}
	if err := s.streamChildren(w, flusher, r.Context(), backend, id, childLastSeq, childAgents); err != nil {
		return
	}

	heartbeat := time.NewTicker(s.heartbeatInterval())
	defer heartbeat.Stop()
	poll := time.NewTicker(s.streamPollInterval())
	defer poll.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stopping():
			_ = writeSSE(w, flusher, "shutdown", "", struct{}{})
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			if err := writeSSE(w, flusher, "frame", "", frameJSONFromLive(frame, id, childAgents)); err != nil {
				return
			}
		case event := <-approvalEvents:
			if err := writeSSE(w, flusher, "approval", "", event); err != nil {
				return
			}
		case event := <-questionEvents:
			if err := writeSSE(w, flusher, "question", "", event); err != nil {
				return
			}
		case <-poll.C:
			page, err := backend.Events(r.Context(), id, lastSeq)
			if err != nil {
				s.logger.Warn("web: stream poll", "session", id, "error", err)
				continue
			}
			if err := writeEventLines(w, flusher, page.Events, &lastSeq); err != nil {
				return
			}
			if err := s.streamChildren(w, flusher, r.Context(), backend, id, childLastSeq, childAgents); err != nil {
				return
			}
			holder, err := backend.Holder(r.Context(), id)
			if err != nil {
				s.logger.Warn("web: stream poll holder", "session", id, "error", err)
				continue
			}
			if titlePort != nil {
				if title, source, err := titlePort.SessionTitle(r.Context(), id); err == nil {
					next := titleJSON{Title: title, TitleSource: source, TitlePending: titlePort.TitlePending(id)}
					if next != lastTitle {
						if err := writeSSE(w, flusher, "title", "", next); err != nil {
							return
						}
						lastTitle = next
					}
				}
			}
			running = backend.TurnActive(id)
			status := statusFromHolder(holder, running)
			if !sameStatus(status, lastStatus) {
				if err := writeSSE(w, flusher, "status", "", status); err != nil {
					return
				}
				lastStatus = status
			}
		}
	}
}

// pumpFrames forwards live frames from a subscription. Lagged notifications
// are skipped because the log is the source of truth.
func pumpFrames(ctx context.Context, sub *core.Subscription[core.LiveFrame], out chan<- core.LiveFrame) {
	defer close(out)
	for {
		received, err := sub.Recv(ctx)
		if err != nil {
			return
		}
		if received.Lagged > 0 {
			continue
		}
		select {
		case out <- received.Value:
		case <-ctx.Done():
			return
		}
	}
}

// streamChildren reads the descendants of the root newer than their last seq
// and writes them as tagged event items. A child log that cannot be read is
// skipped with a warn line so the root stream keeps going (API-TEAM-011).
func (s *Server) streamChildren(w http.ResponseWriter, flusher http.Flusher, ctx context.Context, backend Backend, rootID string, lastSeq map[string]int64, agents map[string]string) error {
	for _, child := range s.descendantSessions(ctx, backend, rootID) {
		if _, ok := lastSeq[child.ID]; !ok {
			lastSeq[child.ID] = 0
		}
		agents[child.ID] = child.Agent
		page, err := backend.Events(ctx, child.ID, lastSeq[child.ID])
		if err != nil {
			s.logger.Warn("web: stream child events", "session", rootID, "child", child.ID, "error", err)
			continue
		}
		seq := lastSeq[child.ID]
		if err := writeChildEventLines(w, flusher, child, page.Events, &seq); err != nil {
			return err
		}
		if page.LastSeq > seq {
			seq = page.LastSeq
		}
		lastSeq[child.ID] = seq
	}
	return nil
}

// descendantSessions returns the depth 1 and depth 2 children of the root. A
// listing failure is logged and skipped.
func (s *Server) descendantSessions(ctx context.Context, backend Backend, rootID string) []core.SessionInfo {
	lister, ok := backend.(childSessionLister)
	if !ok {
		return nil
	}
	children, err := lister.ChildSessions(ctx, rootID)
	if err != nil {
		s.logger.Warn("web: stream children", "session", rootID, "error", err)
		return nil
	}
	seen := map[string]bool{rootID: true}
	out := make([]core.SessionInfo, 0, len(children))
	for _, child := range children {
		if seen[child.ID] {
			continue
		}
		seen[child.ID] = true
		out = append(out, child)
		grandchildren, err := lister.ChildSessions(ctx, child.ID)
		if err != nil {
			s.logger.Warn("web: stream children", "session", rootID, "child", child.ID, "error", err)
			continue
		}
		for _, grandchild := range grandchildren {
			if seen[grandchild.ID] {
				continue
			}
			seen[grandchild.ID] = true
			out = append(out, grandchild)
		}
	}
	return out
}

// writeChildEventLines emits a child's stored events as tagged event items
// without an SSE id and never as a turn line, so a child turn/end cannot change
// the root's running state (API-TEAM-011, AC-TEAM-034).
func writeChildEventLines(w http.ResponseWriter, flusher http.Flusher, child core.SessionInfo, events []core.SessionEvent, lastSeq *int64) error {
	for _, event := range events {
		if event.Seq <= *lastSeq {
			continue
		}
		payload := childEventJSON{
			eventJSON: eventJSON{
				Seq:       event.Seq,
				Kind:      event.Kind,
				TurnID:    event.TurnID,
				CreatedAt: event.CreatedAt,
				Payload:   publicEventPayload(event.Kind, event.Payload),
			},
			ChildSessionID: child.ID,
			Agent:          child.Agent,
		}
		if err := writeSSE(w, flusher, "event", "", payload); err != nil {
			return err
		}
		*lastSeq = event.Seq
	}
	return nil
}

// writeEventLines emits one SSE event per stored event newer than lastSeq and
// updates lastSeq. A turn/end event is followed by a turn line.
func writeEventLines(w http.ResponseWriter, flusher http.Flusher, events []core.SessionEvent, lastSeq *int64) error {
	for _, event := range events {
		if event.Seq <= *lastSeq {
			continue
		}
		payload := eventJSON{
			Seq:       event.Seq,
			Kind:      event.Kind,
			TurnID:    event.TurnID,
			CreatedAt: event.CreatedAt,
			Payload:   publicEventPayload(event.Kind, event.Payload),
		}
		if err := writeSSE(w, flusher, "event", strconv.FormatInt(event.Seq, 10), payload); err != nil {
			return err
		}
		*lastSeq = event.Seq
		if event.Kind != core.KindTurnEnd {
			continue
		}
		var end core.TurnEnd
		_ = json.Unmarshal(event.Payload, &end)
		turnID := end.TurnID
		if turnID == "" {
			turnID = event.TurnID
		}
		if err := writeSSE(w, flusher, "turn", "", turnJSON{TurnID: turnID, StopReason: end.Reason}); err != nil {
			return err
		}
	}
	return nil
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, name, id string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if name != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", name); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func frameJSONFromLive(frame core.LiveFrame, rootID string, childAgents map[string]string) frameJSON {
	status := ToolStatusWord(frame.Status)
	if frame.Kind == "tool_call" && status == "" {
		status = "running"
	}
	out := frameJSON{
		Kind:   frame.Kind,
		TurnID: frame.TurnID,
		Text:   frame.Text,
		CallID: frame.CallID,
		Name:   frame.Name,
		Input:  frame.Input,
		Status: status,
		Output: frame.Output,
	}
	// An empty SessionID is a legacy frame of the root and keeps the root shape.
	if frame.SessionID != "" && frame.SessionID != rootID {
		out.ChildSessionID = frame.SessionID
		out.Agent = frame.Agent
		if out.Agent == "" {
			out.Agent = childAgents[frame.SessionID]
		}
	}
	return out
}

func statusFromHolder(holder Holder, running bool) statusJSON {
	status := statusJSON{Holder: holder.State, TurnRunning: running}
	if holder.State == HolderOther {
		pid := holder.PID
		status.PID = &pid
	}
	return status
}

func sameStatus(a, b statusJSON) bool {
	if a.Holder != b.Holder || a.TurnRunning != b.TurnRunning {
		return false
	}
	if a.PID == nil || b.PID == nil {
		return a.PID == nil && b.PID == nil
	}
	return *a.PID == *b.PID
}

// parseStreamFromSeq applies Last-Event-ID precedence over from_seq.
func parseStreamFromSeq(r *http.Request) (int64, error) {
	if header := r.Header.Get("Last-Event-ID"); header != "" {
		seq, err := strconv.ParseInt(header, 10, 64)
		if err != nil || seq < 0 {
			return 0, errors.New("Last-Event-ID must be a non-negative integer")
		}
		return seq, nil
	}
	return parseFromSeq(r)
}
