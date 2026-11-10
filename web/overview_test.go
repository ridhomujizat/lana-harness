package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/id"
)

func overviewForTest(t *testing.T, s *Server, cookie *http.Cookie, name string) overviewResponse {
	t.Helper()
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/agents/"+name+"/overview", "", cookie, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/agents/%s/overview status = %d, body = %q", name, rr.Code, rr.Body.String())
	}
	var body overviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	return body
}

// AC-ROOM-017: Right now shows the finished steps and the current step of the
// running turn.
func TestOverviewNowSteps(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", Status: "running", CreatedAt: 1000, UpdatedAt: 9000})
	backend.setTurnActive(info.ID, true)
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindTurnStart, TurnID: "t1", CreatedAt: 8000, Payload: json.RawMessage(`{"v":1,"turn_id":"t1"}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindToolCall, TurnID: "t1", CreatedAt: 8100, Payload: json.RawMessage(`{"v":1,"call_id":"c1","name":"gmail.search","input":{}}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindToolResult, TurnID: "t1", CreatedAt: 8200, Payload: json.RawMessage(`{"v":1,"call_id":"c1","status":"success","output":"ok"}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindToolCall, TurnID: "t1", CreatedAt: 8300, Payload: json.RawMessage(`{"v":1,"call_id":"c2","name":"read_file","input":{}}`)})

	s, cookie, _ := newLoggedInSessionServer(t, backend)
	body := overviewForTest(t, s, cookie, "dana")
	if body.Now == nil {
		t.Fatalf("now is null: %+v", body)
	}
	if body.Now.SessionID != info.ID || body.Now.TurnID != "t1" {
		t.Fatalf("now = %+v, want session %s turn t1", body.Now, info.ID)
	}
	if len(body.Now.Steps) != 2 {
		t.Fatalf("steps = %+v, want 2", body.Now.Steps)
	}
	if body.Now.Steps[0].Name != "gmail.search" || body.Now.Steps[0].Status != "ok" {
		t.Fatalf("step 0 = %+v, want gmail.search ok", body.Now.Steps[0])
	}
	if body.Now.Steps[1].Name != "read_file" || body.Now.Steps[1].Status != "running" {
		t.Fatalf("step 1 = %+v, want read_file running", body.Now.Steps[1])
	}
}

// AC-ROOM-018: This week counts today's finished turns, questions, and notes,
// and Activity lists the items newest first.
func TestOverviewWeekAndActivity(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	info := backend.addSession(core.SessionInfo{ID: id.New(), Agent: "dana", Channel: "web", Address: "owner", CreatedAt: 1000, UpdatedAt: 5000})
	now := time.Now()
	for i := 0; i < 3; i++ {
		at := now.Add(-time.Duration(i+1) * time.Second).UnixMilli()
		backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindTurnEnd, TurnID: fmt.Sprintf("t%d", i), CreatedAt: at, Payload: json.RawMessage(`{"v":1,"reason":"end_turn"}`)})
	}
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindQuestionAsked, CreatedAt: now.Add(-20 * time.Second).UnixMilli(), Payload: json.RawMessage(`{"v":1,"request_id":"q1"}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindMemoryCaptured, CreatedAt: now.Add(-10 * time.Second).UnixMilli(), Payload: json.RawMessage(`{"v":1,"ops":[]}`)})
	backend.appendEvent(info.ID, core.SessionEvent{Kind: core.KindMemoryCaptured, CreatedAt: now.Add(-9 * time.Second).UnixMilli(), Payload: json.RawMessage(`{"v":1,"ops":[]}`)})

	s, cookie, _ := newLoggedInSessionServer(t, backend)
	body := overviewForTest(t, s, cookie, "dana")

	if len(body.Week.Days) != 7 {
		t.Fatalf("week.days = %d, want 7", len(body.Week.Days))
	}
	today := now.Format("2006-01-02")
	if body.Week.Days[6].Day != today || body.Week.Days[6].Turns != 3 {
		t.Fatalf("today bucket = %+v, want %s with 3 turns", body.Week.Days[6], today)
	}
	if body.Week.Turns != 3 || body.Week.Questions != 1 || body.Week.Notes != 2 {
		t.Fatalf("week = %+v, want 3 turns, 1 question, 2 notes", body.Week)
	}

	if len(body.Activity) != 6 {
		t.Fatalf("activity = %d items, want 6: %+v", len(body.Activity), body.Activity)
	}
	if body.Activity[0].Kind != "answered" || body.Activity[0].Title != "Answered in Web" {
		t.Fatalf("newest activity = %+v, want answered in Web", body.Activity[0])
	}
	if body.Activity[len(body.Activity)-1].Kind != "asked" {
		t.Fatalf("oldest activity = %+v, want asked", body.Activity[len(body.Activity)-1])
	}
	for i := 1; i < len(body.Activity); i++ {
		if body.Activity[i-1].At < body.Activity[i].At {
			t.Fatalf("activity is not newest first: %+v", body.Activity)
		}
	}
}

// TestOverviewMemoryJobPersona covers the profile files: the persona first
// line, the memory entries and total, and the job text.
func TestOverviewMemoryJobPersona(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	home := t.TempDir()
	paths := agenthome.Paths(home, "dana")
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Agent, []byte("# Dana\n\nSaya mencatat setiap rupiah\nbaris kedua\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []string{
		"Gaji masuk tiap tanggal 25",
		"Pajak jatuh tempo April",
		"Tabungan darurat 6 bulan",
		"Langganan streaming tiap bulan",
		"Budget makan 2 juta",
		"Kartu kredit batas 50 juta",
		"Investasi reksadana rutin",
		"Asuransi kesehatan keluarga",
	}
	if err := os.WriteFile(paths.Memory, []byte(agenthome.Join(entries)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Job, []byte("• Catat transaksi"), 0o600); err != nil {
		t.Fatal(err)
	}

	token := authTokenFor(t, home)
	s, err := New(Config{Addr: authTestAddr, Enabled: true, Home: home})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.SetBackend(backend)
	s.SetAgentFileStore(&testSettingsStore{home: home, agents: []string{"dana"}})
	cookie, _ := loginAndSession(t, s, token)

	body := overviewForTest(t, s, cookie, "dana")
	if body.PersonaLine != "Saya mencatat setiap rupiah" {
		t.Fatalf("persona_line = %q", body.PersonaLine)
	}
	if body.Memory.Total != len(entries) {
		t.Fatalf("memory.total = %d, want %d", body.Memory.Total, len(entries))
	}
	if len(body.Memory.Entries) != overviewMemoryLimit {
		t.Fatalf("memory.entries = %d, want %d", len(body.Memory.Entries), overviewMemoryLimit)
	}
	if body.Memory.Entries[0] != entries[0] {
		t.Fatalf("memory.entries[0] = %q, want %q", body.Memory.Entries[0], entries[0])
	}
	if body.Job != "• Catat transaksi" {
		t.Fatalf("job = %q", body.Job)
	}
}

// TestOverviewNotFound covers the 404 of API-ROOM-008.
func TestOverviewNotFound(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/agents/missing/overview", "", cookie, ""))
	assertAuthError(t, rr, http.StatusNotFound, "not_found")
}

// TestOverviewKnowledgeBlock covers the API-DKN-008 "What it knows" block.
func TestOverviewKnowledgeBlock(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	backend.knowledgeAvail = true
	backend.knowledgeStats = core.KnowledgeDomainStats{
		Pages: 12, Changed7d: 3, Weeks: [8]int{0, 0, 1, 2, 0, 4, 2, 3},
		Recent: []core.KnowledgePageInfo{{Title: "Gaji", Path: "agents/dana/wiki/Gaji.md", Updated: "2026-11-10"}},
	}
	s, cookie, _ := newLoggedInSessionServer(t, backend)
	body := overviewForTest(t, s, cookie, "dana")

	if !body.Knowledge.Available || body.Knowledge.Pages != 12 || body.Knowledge.Changed7d != 3 {
		t.Fatalf("knowledge = %+v", body.Knowledge)
	}
	if body.Knowledge.Weeks != [8]int{0, 0, 1, 2, 0, 4, 2, 3} {
		t.Fatalf("weeks = %v", body.Knowledge.Weeks)
	}
	if len(body.Knowledge.Recent) != 1 || body.Knowledge.Recent[0].Title != "Gaji" ||
		body.Knowledge.Recent[0].Path != "agents/dana/wiki/Gaji.md" || body.Knowledge.Recent[0].Updated != "2026-11-10" {
		t.Fatalf("recent = %+v", body.Knowledge.Recent)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/agents/dana/overview", "", cookie, ""))
	if !strings.Contains(rr.Body.String(), `"knowledge":{"available":true`) {
		t.Fatalf("body has no knowledge block: %s", rr.Body.String())
	}
}

// TestOverviewKnowledgeUnavailable covers API-DKN-008: an agent without a
// domain, or a runtime without a knowledge service, reports available:false
// with zero fields and a never-null recent list.
func TestOverviewKnowledgeUnavailable(t *testing.T) {
	backend := newFakeKnowledgeBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := overviewForTest(t, s, cookie, "dana")
	if body.Knowledge.Available || body.Knowledge.Pages != 0 || body.Knowledge.Changed7d != 0 {
		t.Fatalf("knowledge = %+v", body.Knowledge)
	}
	if body.Knowledge.Recent == nil || len(body.Knowledge.Recent) != 0 {
		t.Fatalf("recent = %+v, want empty non-null", body.Knowledge.Recent)
	}
	rr := serveAuth(s, sessionTestRequest(http.MethodGet, "/v1/agents/dana/overview", "", cookie, ""))
	if !strings.Contains(rr.Body.String(), `"knowledge":{"available":false`) {
		t.Fatalf("body has no unavailable knowledge block: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"recent":[]`) {
		t.Fatalf("body has no empty recent: %s", rr.Body.String())
	}
}

// TestOverviewKnowledgeWithoutPort covers a Backend that does not implement the
// optional KnowledgeDomainBackend port.
func TestOverviewKnowledgeWithoutPort(t *testing.T) {
	backend := newFakeWebBackend()
	backend.agents = []Agent{{Name: "dana", Kind: "agent"}}
	s, cookie, _ := newLoggedInSessionServer(t, backend)

	body := overviewForTest(t, s, cookie, "dana")
	if body.Knowledge.Available || body.Knowledge.Recent == nil {
		t.Fatalf("knowledge = %+v", body.Knowledge)
	}
}
