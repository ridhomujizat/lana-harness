package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// setText replaces the scripted reply under the provider lock.
func (p *fakeProvider) setText(text string) {
	p.mu.Lock()
	p.text = text
	p.mu.Unlock()
}

// setErr replaces the scripted provider error under the lock.
func (p *fakeProvider) setErr(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

// newDomainPromoteEnv builds a vault env with one domain agent, dana, resolvable
// by the lookup so a domain promotion can run.
func newDomainPromoteEnv(t *testing.T, provider *fakeProvider) *vaultEnv {
	t.Helper()
	env := newVaultEnv(t, provider)
	cfg := core.KnowledgeConfig{Path: env.vaultDir}
	env.svc.cfg = cfg
	env.svc.Config = core.Config{
		Knowledge: cfg,
		Agents: map[string]core.AgentConfig{
			"dana": {Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}, Title: "Data analyst", Description: "Analyzes numbers"},
		},
	}
	env.svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		ac, ok := env.svc.Config.Agents[name]
		return ac, ok
	}
	return env
}

// writeHomeFile writes a file under $LANA_HOME (JOB.md, MEMORY.md, …).
func (e *vaultEnv) writeHomeFile(rel, content string) {
	e.t.Helper()
	abs := filepath.Join(filepath.Dir(e.vaultDir), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// readVault reads a vault-relative file.
func (e *vaultEnv) readVault(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.vaultDir, filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

// readHome reads a $LANA_HOME-relative file.
func (e *vaultEnv) readHome(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(e.vaultDir), filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

// proposalStatus reads the status of one stored proposal.
func (e *vaultEnv) proposalStatus(t *testing.T, id string) string {
	t.Helper()
	_, idx, err := e.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	p, err := idx.GetProposal(id)
	if err != nil {
		t.Fatal(err)
	}
	return p.Status
}

// promoteOffset reads the promote mark of one journal file.
func (e *vaultEnv) promoteOffset(t *testing.T, rel string) int64 {
	t.Helper()
	_, idx, err := e.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	off, err := idx.PromoteOffset(rel)
	if err != nil {
		t.Fatal(err)
	}
	return off
}

// AC-DKN-005 (REQ-DKN-031, -032): one request carries the JOB.md text and the
// domain prompt; a 0.7 domain op applies, a 0.5 op and a 0.7 proposal op are
// skipped, a 0.9 proposal op applies and its proposal becomes applied, a shared
// op lands in wiki/, a non-owner-turn pending proposal is not sent and stays
// pending, and the journal mark moves.
func TestPromoteDomainAppliesAndSkips(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	day := "2026-11-01"
	env.writeVault("agents/dana/journal/"+day+".md", "# "+day+"\n\n## 09:00 · dana\n- Gaji naik 10%\n")
	env.writeHomeFile("agents/dana/JOB.md", "You analyze payroll numbers for the team.")

	pSkip, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "append", Title: "Gaji", Text: "skip me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pApply, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "append", Title: "Gaji Naik", Text: "apply me"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pPending, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: false,
		Write: core.KnowledgeWrite{Action: "append", Title: "Rahasia", Text: "not sent"},
	})
	if err != nil {
		t.Fatal(err)
	}

	provider.setText(fmt.Sprintf(`{"ops":[
		{"op":"create","title":"Gaji","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.7},
		{"op":"create","title":"Tebakan","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.5},
		{"op":"create","title":"DariProposal","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.7,"proposal":"%s"},
		{"op":"create","title":"Gaji Naik","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.9,"proposal":"%s"},
		{"op":"create","title":"Kebijakan","type":"topic","description":"d","body":"isi","scope":"shared","confidence":0.7}
	],"applied_proposals":[],"prune":[]}`, pSkip.ID, pApply.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 3 || res.Skipped != 2 {
		t.Fatalf("applied=%d skipped=%d, want 3 and 2", res.Applied, res.Skipped)
	}
	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
	req := provider.recorded()[0]
	if req.System != domainSystem {
		t.Fatalf("system prompt is not domainSystem")
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != core.RoleUser {
		t.Fatalf("messages = %+v", req.Messages)
	}
	user := req.Messages[0].Content[0].Text
	if !strings.Contains(user, "You analyze payroll numbers for the team.") {
		t.Fatalf("prompt missing JOB.md text:\n%s", user)
	}
	if !strings.Contains(user, pSkip.ID) || !strings.Contains(user, pApply.ID) {
		t.Fatalf("prompt missing gathered proposals:\n%s", user)
	}
	if strings.Contains(user, pPending.ID) {
		t.Fatalf("prompt carries a non-owner-turn proposal:\n%s", user)
	}

	if !env.exists("agents/dana/wiki/Gaji.md") {
		t.Fatal("0.7 domain op was not applied")
	}
	if !env.exists("agents/dana/wiki/Gaji Naik.md") {
		t.Fatal("0.9 proposal op was not applied")
	}
	if !env.exists("wiki/Kebijakan.md") {
		t.Fatal("shared op was not applied to the shared wiki")
	}
	if env.exists("agents/dana/wiki/Tebakan.md") {
		t.Fatal("0.5 op was applied")
	}
	if env.exists("agents/dana/wiki/DariProposal.md") {
		t.Fatal("0.7 proposal op was applied")
	}

	if status := env.proposalStatus(t, pApply.ID); status != proposalStatusApplied {
		t.Fatalf("applied proposal status = %q, want applied", status)
	}
	if status := env.proposalStatus(t, pSkip.ID); status != proposalStatusPending {
		t.Fatalf("skipped proposal status = %q, want pending", status)
	}
	if status := env.proposalStatus(t, pPending.ID); status != proposalStatusPending {
		t.Fatalf("non-owner proposal status = %q, want pending", status)
	}

	size := int64(len(env.readVault("agents/dana/journal/" + day + ".md")))
	if off := env.promoteOffset(t, "agents/dana/journal/"+day+".md"); off != size {
		t.Fatalf("promote offset = %d, want %d", off, size)
	}
}

// AC-DKN-005 (REQ-DKN-033): nothing new from the journal and no promotable
// proposal makes no model call.
func TestPromoteDomainNothingNewNoCall(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[]}`}
	env := newDomainPromoteEnv(t, provider)

	res, err := env.svc.promoteDomain(context.Background(), "dana")
	if err != nil {
		t.Fatal(err)
	}
	if provider.count() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.count())
	}
	if res.Applied != 0 || res.Skipped != 0 {
		t.Fatalf("result = %+v", res)
	}
}

// AC-DKN-005 (REQ-DKN-033): a provider error leaves the marks and the proposals
// unchanged and returns the error.
func TestPromoteDomainProviderErrorKeepsState(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- note\n")
	p, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "append", Title: "Gaji", Text: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	provider.setErr(errors.New("boom"))
	if _, err := env.svc.promoteDomain(ctx, "dana"); err == nil {
		t.Fatal("promoteDomain succeeded, want the provider error")
	}
	if off := env.promoteOffset(t, "agents/dana/journal/2026-11-01.md"); off != 0 {
		t.Fatalf("promote offset = %d, want 0", off)
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", status)
	}
}

// AC-DKN-005 (REQ-DKN-034): above 6,000 characters the memory entries are sent
// and the prune ids whose facts a passing op wrote are removed; the log keeps
// their full text.
func TestPromoteDomainPrunesMemory(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Memo","type":"topic","body":"memo one memo two","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":["m1","m2"]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- note\n")
	pad := strings.Repeat("z", 2500)
	entries := []string{"memo one " + pad, "memo two " + pad, "memo three " + pad}
	env.writeHomeFile("agents/dana/MEMORY.md", agenthome.Join(entries))
	if size := agenthome.Size(entries); size <= domainMemoryTrigger {
		t.Fatalf("test memory is %d characters, want more than %d", size, domainMemoryTrigger)
	}

	if _, err := env.svc.promoteDomain(ctx, "dana"); err != nil {
		t.Fatal(err)
	}

	remaining, err := agenthome.ReadEntries(filepath.Join(filepath.Dir(env.vaultDir), "agents", "dana", "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || !strings.HasPrefix(remaining[0], "memo three") {
		t.Fatalf("remaining memory = %+v", remaining)
	}
	log := env.readLog()
	if !strings.Contains(log, "- pruned memory: memo one") || !strings.Contains(log, "- pruned memory: memo two") {
		t.Fatalf("log missing pruned entry text:\n%s", log)
	}
	prompt := provider.recorded()[0].Messages[0].Content[0].Text
	if !strings.Contains(prompt, "# Memory entries") || !strings.Contains(prompt, "m3:") {
		t.Fatalf("prompt missing memory entries:\n%s", prompt)
	}
}

// AC-DKN-005 (REQ-DKN-034, design §4.2 step 6): a prune id is honoured when the
// fact it names was written into a page by a passing op of the same run.
func TestPromoteDomainPruneKeepsOnlyWrittenFacts(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")
	pad := strings.Repeat("z", 6500)
	entry := "gaji naik sepuluh persen " + pad
	env.writeHomeFile("agents/dana/MEMORY.md", entry+"\n")
	if agenthome.Size([]string{entry}) <= domainMemoryTrigger {
		t.Fatalf("test memory is %d characters, want more than %d", agenthome.Size([]string{entry}), domainMemoryTrigger)
	}

	provider.setText(`{"ops":[
		{"op":"create","title":"Gaji","type":"topic","body":"gaji naik sepuluh persen","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":["m1"]}`)

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pruned) != 1 {
		t.Fatalf("pruned = %v, want one entry", res.Pruned)
	}
	if remaining := agenthome.Parse(env.readHome("agents/dana/MEMORY.md")); len(remaining) != 0 {
		t.Fatalf("remaining memory = %+v, want none", remaining)
	}
	log := env.readLog()
	if !strings.Contains(log, "- pruned memory: gaji naik sepuluh persen") {
		t.Fatalf("log missing pruned entry text:\n%s", log)
	}
	if strings.Contains(log, "- kept memory m1") {
		t.Fatalf("written entry was kept:\n%s", log)
	}
}

// AC-DKN-005 (REQ-DKN-034, design §4.2 step 6): a prune id whose fact the run
// did not write into any page is kept and logged.
func TestPromoteDomainKeepsMemoryNotWritten(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Gaji","type":"topic","body":"something unrelated","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":["m1"]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")
	pad := strings.Repeat("z", 6500)
	entry := "gaji naik sepuluh persen " + pad
	env.writeHomeFile("agents/dana/MEMORY.md", entry+"\n")

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pruned) != 0 {
		t.Fatalf("pruned = %v, want none", res.Pruned)
	}
	if remaining := agenthome.Parse(env.readHome("agents/dana/MEMORY.md")); len(remaining) != 1 || remaining[0] != entry {
		t.Fatalf("memory was changed: %+v", remaining)
	}
	log := env.readLog()
	if !strings.Contains(log, "- kept memory m1: not found in written pages") {
		t.Fatalf("log missing kept memory line:\n%s", log)
	}
	if strings.Contains(log, "- pruned memory:") {
		t.Fatalf("log holds a pruned line for a kept entry:\n%s", log)
	}
}

// AC-DKN-005 (REQ-DKN-034, design §4.2 step 6): with no passing op nothing is
// pruned, whatever the reply lists.
func TestPromoteDomainNoPassingOpKeepsMemory(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[],"applied_proposals":[],"prune":["m1"]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")
	pad := strings.Repeat("z", 6500)
	entry := "gaji naik sepuluh persen " + pad
	env.writeHomeFile("agents/dana/MEMORY.md", entry+"\n")

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 0 {
		t.Fatalf("applied=%d skipped=%d, want 0 and 0", res.Applied, res.Skipped)
	}
	if len(res.Pruned) != 0 {
		t.Fatalf("pruned = %v, want none", res.Pruned)
	}
	if remaining := agenthome.Parse(env.readHome("agents/dana/MEMORY.md")); len(remaining) != 1 {
		t.Fatalf("memory was changed: %+v", remaining)
	}
	if log := env.readLog(); strings.Contains(log, "- pruned memory:") {
		t.Fatalf("log holds a pruned line with no passing op:\n%s", log)
	}
}

// AC-DKN-005 (REQ-DKN-034, design §4.2 step 4): an "update" op name is an alias
// of "append" and is applied as one.
func TestPromoteDomainUpdateAliasAppends(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"update","title":"Gaji","text":"gaji naik dua belas persen","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	if _, err := env.svc.Write(ctx, core.KnowledgeWrite{Action: "create", Title: "Gaji", Type: "topic", Body: "gaji lama sepuluh persen\n", Scope: "dana"}); err != nil {
		t.Fatal(err)
	}
	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Skipped != 0 {
		t.Fatalf("applied=%d skipped=%d, want 1 and 0", res.Applied, res.Skipped)
	}
	page := env.readVault("agents/dana/wiki/Gaji.md")
	if !strings.Contains(page, "gaji naik dua belas persen") {
		t.Fatalf("update op was not applied as append:\n%s", page)
	}
}

// AC-DKN-005 (REQ-DKN-034): a memory of at most 6,000 characters is not sent
// and its entries are not pruned.
func TestPromoteDomainSkipsSmallMemory(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[],"applied_proposals":[],"prune":["m1"]}`}
	env := newDomainPromoteEnv(t, provider)

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- note\n")
	env.writeHomeFile("agents/dana/MEMORY.md", "small "+strings.Repeat("y", 5000)+"\n§\nsecond entry")

	if _, err := env.svc.promoteDomain(context.Background(), "dana"); err != nil {
		t.Fatal(err)
	}
	if provider.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.count())
	}
	prompt := provider.recorded()[0].Messages[0].Content[0].Text
	if strings.Contains(prompt, "# Memory entries") {
		t.Fatalf("memory below the trigger was sent:\n%s", prompt)
	}
	mem := env.readHome("agents/dana/MEMORY.md")
	if !strings.Contains(mem, "small") || !strings.Contains(mem, "second entry") {
		t.Fatalf("memory was changed: %q", mem)
	}
}

// AC-DKN-005 (REQ-DKN-025): a supersede op rewrites the one matching line into
// the struck and dated form.
func TestPromoteDomainSupersede(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[{"op":"supersede","title":"Gaji","old_text":"Gaji lama 10 juta","new_text":"Gaji baru 12 juta","source":"survei","scope":"domain","confidence":0.9}],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	if _, err := env.svc.Write(ctx, core.KnowledgeWrite{Action: "create", Title: "Gaji", Type: "topic", Body: "Gaji lama 10 juta\n", Scope: "dana"}); err != nil {
		t.Fatal(err)
	}
	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 {
		t.Fatalf("applied = %d, want 1", res.Applied)
	}
	body := env.readVault("agents/dana/wiki/Gaji.md")
	if !strings.Contains(body, "~~Gaji lama 10 juta~~") || !strings.Contains(body, "Gaji baru 12 juta") {
		t.Fatalf("superseded page = %q", body)
	}
}

// AC-DKN-005 (design §4.2 step 4): a create op whose type is missing or unknown
// is applied as topic.
func TestPromoteDomainCreateWithoutTypeUsesTopic(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Tanpa Tipe","description":"d","body":"isi","scope":"domain","confidence":0.7},
		{"op":"create","title":"Tipe Asing","type":"planet","description":"d","body":"isi","scope":"domain","confidence":0.7}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- catatan\n")
	res, err := env.svc.promoteDomain(context.Background(), "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 2 || res.Skipped != 0 {
		t.Fatalf("applied=%d skipped=%d, want 2 and 0", res.Applied, res.Skipped)
	}
	for _, rel := range []string{"agents/dana/wiki/Tanpa Tipe.md", "agents/dana/wiki/Tipe Asing.md"} {
		if page := env.readVault(rel); !strings.Contains(page, "type: topic") {
			t.Fatalf("%s frontmatter is not type topic:\n%s", rel, page)
		}
	}
}

// AC-DKN-005 (REQ-DKN-033): Consolidate promotes every domain agent in name
// order after the shared promotion and before lint, and the log names each.
func TestConsolidatePromotesDomainsAfterShared(t *testing.T) {
	provider := &fakeProvider{text: "[]"}
	env := newVaultEnv(t, provider)
	cfg := core.KnowledgeConfig{Path: env.vaultDir, Agent: "assistant"}
	env.svc.cfg = cfg
	env.svc.Config = core.Config{
		Knowledge: cfg,
		Agents: map[string]core.AgentConfig{
			"dana": {Provider: "mock", Model: "m", Tools: []string{"knowledge_write"}},
			"rio":  {Provider: "mock", Model: "m", Tools: []string{"knowledge_search"}},
		},
	}
	env.svc.LookupAgent = func(name string) (core.AgentConfig, bool) {
		if name == "assistant" {
			return knowledgeAgentConfig(), true
		}
		return core.AgentConfig{}, false
	}
	env.writeJournal("2026-11-01", "# 2026-11-01\n\n## 09:00 · lana\n- note\n")

	if err := env.svc.Consolidate(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := env.readLog()
	shared := strings.Index(log, "- promoted journal/2026-11-01.md")
	dana := strings.Index(log, "- promoted dana: 0 ops")
	rio := strings.Index(log, "- promoted rio: 0 ops")
	if shared < 0 || dana < 0 || rio < 0 {
		t.Fatalf("consolidation log missing a promotion line:\n%s", log)
	}
	if shared >= dana || dana >= rio {
		t.Fatalf("domain promotion is not after the shared promotion, in name order:\n%s", log)
	}
}

// ownerProposal proposes an owner-turn append on dana for the proposal-accounting
// tests.
func ownerProposal(t *testing.T, env *vaultEnv, title string) core.KnowledgeProposal {
	t.Helper()
	p, err := env.svc.Propose(context.Background(), core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "append", Title: title, Text: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// AC-DKN-005 (design §4.2 step 5): a proposal listed in applied_proposals whose
// only op was skipped for low confidence stays pending, and the log.md entry
// carries a skipped-op line with the reason.
func TestPromoteDomainKeepsProposalWhenOpSkipped(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	p := ownerProposal(t, env, "Gaji")
	provider.setText(fmt.Sprintf(`{"ops":[
		{"op":"create","title":"Gaji","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.7,"proposal":"%s"}
	],"applied_proposals":["%s"],"prune":[]}`, p.ID, p.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("applied=%d skipped=%d, want 0 and 1", res.Applied, res.Skipped)
	}
	if len(res.Proposals) != 0 {
		t.Fatalf("proposals applied = %v, want none", res.Proposals)
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", status)
	}
	want := "- skipped create [[Gaji]]: below confidence"
	if log := env.readLog(); !strings.Contains(log, want) {
		t.Fatalf("log missing skipped op line %q:\n%s", want, log)
	}
}

// AC-DKN-005 (design §4.2 step 5): a proposal listed in applied_proposals that
// no op references stays pending when any op in the reply was skipped.
func TestPromoteDomainKeepsListedProposalWhenOpSkipped(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	p := ownerProposal(t, env, "Gaji")
	provider.setText(fmt.Sprintf(`{"ops":[
		{"op":"create","title":"Tebakan","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.5}
	],"applied_proposals":["%s"],"prune":[]}`, p.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("applied=%d skipped=%d, want 0 and 1", res.Applied, res.Skipped)
	}
	if len(res.Proposals) != 0 {
		t.Fatalf("proposals applied = %v, want none", res.Proposals)
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", status)
	}
}

// AC-DKN-005 (design §4.2 step 5): a proposal listed in applied_proposals with
// no op referencing it at all becomes applied.
func TestPromoteDomainAppliesListedProposalWithoutOp(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	p := ownerProposal(t, env, "Gaji")
	provider.setText(fmt.Sprintf(`{"ops":[],"applied_proposals":["%s"],"prune":[]}`, p.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Proposals) != 1 || res.Proposals[0] != p.ID {
		t.Fatalf("proposals applied = %v, want [%s]", res.Proposals, p.ID)
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusApplied {
		t.Fatalf("proposal status = %q, want applied", status)
	}
}

// AC-DKN-005 (design §4.2 step 5): a proposal referenced by a passing op becomes
// applied even when it is not listed in applied_proposals.
func TestPromoteDomainAppliesProposalFromPassingOp(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	p := ownerProposal(t, env, "Gaji")
	provider.setText(fmt.Sprintf(`{"ops":[
		{"op":"create","title":"Gaji","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.9,"proposal":"%s"}
	],"applied_proposals":[],"prune":[]}`, p.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 {
		t.Fatalf("applied = %d, want 1", res.Applied)
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusApplied {
		t.Fatalf("proposal status = %q, want applied", status)
	}
}

// AC-DKN-005 (REQ-DKN-031, design §4.2 step 4): an op at 0.7 that carries a
// proposal the owner approved passes at the normal 0.6 bar and marks the
// proposal applied.
func TestPromoteDomainAppliesApprovedProposalOp(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	p := ownerProposal(t, env, "Harga")
	if _, err := env.svc.DecideProposal(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	provider.setText(fmt.Sprintf(`{"ops":[
		{"op":"create","title":"Harga","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.7,"proposal":"%s"}
	],"applied_proposals":[],"prune":[]}`, p.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Skipped != 0 {
		t.Fatalf("applied=%d skipped=%d, want 1 and 0", res.Applied, res.Skipped)
	}
	if !env.exists("agents/dana/wiki/Harga.md") {
		t.Fatal("0.7 op carrying an approved proposal was not applied")
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusApplied {
		t.Fatalf("proposal status = %q, want applied", status)
	}
}

// AC-DKN-005 (REQ-DKN-031, design §4.2 step 4): an op at 0.7 that carries a
// proposal the owner has not approved misses the 0.8 bar, so it is skipped and
// the proposal stays pending.
func TestPromoteDomainSkipsPendingProposalOp(t *testing.T) {
	provider := &fakeProvider{}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	p := ownerProposal(t, env, "Harga")
	provider.setText(fmt.Sprintf(`{"ops":[
		{"op":"create","title":"Harga","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.7,"proposal":"%s"}
	],"applied_proposals":[],"prune":[]}`, p.ID))

	res, err := env.svc.promoteDomain(ctx, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("applied=%d skipped=%d, want 0 and 1", res.Applied, res.Skipped)
	}
	if env.exists("agents/dana/wiki/Harga.md") {
		t.Fatal("0.7 op carrying a pending proposal was applied")
	}
	if status := env.proposalStatus(t, p.ID); status != proposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", status)
	}
}

// AC-DKN-005 (REQ-DKN-031, design §4.2 step 2): the prompt marks each gathered
// proposal as approved by the owner or not yet reviewed.
func TestPromoteDomainPromptMarksProposalState(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	approved := ownerProposal(t, env, "Harga")
	if _, err := env.svc.DecideProposal(ctx, approved.ID, true); err != nil {
		t.Fatal(err)
	}
	pending := ownerProposal(t, env, "Baru")

	if _, err := env.svc.promoteDomain(ctx, "dana"); err != nil {
		t.Fatal(err)
	}
	prompt := provider.recorded()[0].Messages[0].Content[0].Text
	for _, want := range []string{
		approved.ID + " from rio (approved by the owner): append Harga",
		pending.ID + " from rio (not yet reviewed): append Baru",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// AC-DKN-005 (design §4.2 step 4): a journal-derived op cites the journal day
// without the ".md" suffix.
func TestPromoteDomainJournalSourceLinkHasNoSuffix(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Gaji","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")
	if _, err := env.svc.promoteDomain(context.Background(), "dana"); err != nil {
		t.Fatal(err)
	}
	page := env.readVault("agents/dana/wiki/Gaji.md")
	if !strings.Contains(page, "[[agents/dana/journal/2026-11-01]]") {
		t.Fatalf("page missing journal source link:\n%s", page)
	}
	if strings.Contains(page, "[[agents/dana/journal/2026-11-01.md]]") {
		t.Fatalf("journal source link keeps the .md suffix:\n%s", page)
	}
}

// AC-DKN-005 (design §4.2): the domain prompt tells the model to take a fitting
// proposal and to name it on every op that carries it, and that a create needs a
// valid type.
func TestDomainSystemPromptCarriesProposalAndTypeRules(t *testing.T) {
	proposal := `Take a proposal when it fits your job and does not contradict what you know, and set "proposal" to its id on every op that carries it; leave out a proposal that is wrong or outside your job.`
	if !strings.Contains(domainSystem, proposal) {
		t.Fatalf("domainSystem missing the proposal sentence:\n%s", domainSystem)
	}
	if !strings.Contains(domainSystem, `a "create" op needs a "type"`) {
		t.Fatalf("domainSystem missing the create type rule:\n%s", domainSystem)
	}
	if !strings.Contains(domainSystem, "A proposal approved by the owner counts as stated by the owner.") {
		t.Fatalf("domainSystem missing the approved-proposal sentence:\n%s", domainSystem)
	}
}

// AC-DKN-005 (REQ-DKN-031, design §4.2 step 2): the prompt carries the whole
// content of each gathered proposal: every line of an append's text and the body
// of a create, not just a first line.
func TestPromoteDomainPromptCarriesProposalContent(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	appendText := "\n## THR\n- THR wajib dibayar paling lambat 7 hari\n- THR dihitung dari gaji pokok\n"
	if _, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "append", Title: "THR", Text: appendText},
	}); err != nil {
		t.Fatal(err)
	}
	createBody := "Upah dibayar tiap tanggal 25\nHitungan mingguan\n"
	if _, err := env.svc.Propose(ctx, core.KnowledgeProposal{
		Agent: "dana", FromAgent: "rio", OwnerTurn: true,
		Write: core.KnowledgeWrite{Action: "create", Title: "Upah", Type: "topic", Body: createBody},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := env.svc.promoteDomain(ctx, "dana"); err != nil {
		t.Fatal(err)
	}
	prompt := provider.recorded()[0].Messages[0].Content[0].Text
	for _, want := range []string{
		"## THR",
		"- THR wajib dibayar paling lambat 7 hari",
		"- THR dihitung dari gaji pokok",
		"Upah dibayar tiap tanggal 25",
		"Hitungan mingguan",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// AC-DKN-005 (REQ-DKN-031, design §4.2 step 4): a create op with no body and no
// text is skipped as an empty page and logs the reason.
func TestPromoteDomainSkipsEmptyCreate(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Kosong","type":"topic","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- catatan\n")
	res, err := env.svc.promoteDomain(context.Background(), "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("applied=%d skipped=%d, want 0 and 1", res.Applied, res.Skipped)
	}
	if env.exists("agents/dana/wiki/Kosong.md") {
		t.Fatal("empty create was applied")
	}
	want := "- skipped create [[Kosong]]: invalid: empty page"
	if log := env.readLog(); !strings.Contains(log, want) {
		t.Fatalf("log missing skipped op line %q:\n%s", want, log)
	}
}

// AC-DKN-005 (REQ-DKN-031, design §4.2 step 4): a create op with no body uses
// its text as the page body.
func TestPromoteDomainCreateUsesTextAsBody(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Upah","type":"topic","text":"Upah dibayar tiap tanggal 25","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- catatan\n")
	res, err := env.svc.promoteDomain(context.Background(), "dana")
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Skipped != 0 {
		t.Fatalf("applied=%d skipped=%d, want 1 and 0", res.Applied, res.Skipped)
	}
	page := env.readVault("agents/dana/wiki/Upah.md")
	if !strings.Contains(page, "Upah dibayar tiap tanggal 25") {
		t.Fatalf("create page missing its text body:\n%s", page)
	}
}

// REQ-DKN-031, design §4.2: a domain promotion re-indexes the pages it writes,
// so ScopeStats counts them without any other call.
func TestPromoteDomainScopeStatsFresh(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Gaji","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")
	if _, err := env.svc.promoteDomain(ctx, "dana"); err != nil {
		t.Fatal(err)
	}

	// The write is indexed at once, before any stats read runs its own sync.
	_, idx, err := env.svc.open()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := idx.Catalog(0, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || !strings.HasPrefix(catalog[0], "Gaji") {
		t.Fatalf("catalog = %v, want the promoted page indexed", catalog)
	}

	stats, err := env.svc.ScopeStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byScope := map[string]core.KnowledgeScopeStat{}
	for _, s := range stats {
		byScope[s.Scope] = s
	}
	if byScope["dana"].Pages != 1 {
		t.Fatalf("dana stat = %+v, want 1 page without any other call", byScope["dana"])
	}
}

// API-DKN-003, REQ-DKN-031: right after a promotion the Status scope count
// matches the domain folder on disk.
func TestStatusScopesAfterPromotion(t *testing.T) {
	provider := &fakeProvider{text: `{"ops":[
		{"op":"create","title":"Gaji","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.9},
		{"op":"create","title":"Upah","type":"topic","description":"d","body":"isi","scope":"domain","confidence":0.9}
	],"applied_proposals":[],"prune":[]}`}
	env := newDomainPromoteEnv(t, provider)
	ctx := context.Background()

	env.writeVault("agents/dana/journal/2026-11-01.md", "# 2026-11-01\n\n- gaji naik\n")
	if _, err := env.svc.promoteDomain(ctx, "dana"); err != nil {
		t.Fatal(err)
	}

	status, err := env.svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byScope := map[string]core.KnowledgeScopeStat{}
	for _, s := range status.Scopes {
		byScope[s.Scope] = s
	}
	folder, err := os.ReadDir(filepath.Join(env.vaultDir, "agents", "dana", "wiki"))
	if err != nil {
		t.Fatal(err)
	}
	if byScope["dana"].Pages != len(folder) || byScope["dana"].Pages != 2 {
		t.Fatalf("dana stat = %+v, domain folder holds %d pages", byScope["dana"], len(folder))
	}
}
