package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorsCollectAllLayers(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\npermision = \"ask\"\nmax_steps = -1\n")
	patch := filepath.Join(t.TempDir(), "p.toml")
	writeFile(t, patch, "[[modules]]\nid = \"x\"\nmodule = \"missing_mod\"\n")
	_, err := Load(context.Background(), Options{Home: home, Patches: []string{patch}, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if len(errs) < 3 {
		t.Fatalf("only %d errors: %v", len(errs), errs)
	}
	wantFiles := map[string]bool{filepath.Join(home, "config.toml"): false, patch: false}
	for _, e := range errs {
		if _, ok := wantFiles[e.File]; ok {
			wantFiles[e.File] = true
		}
	}
	for file, seen := range wantFiles {
		if !seen {
			t.Fatalf("no error from %s: %v", file, errs)
		}
	}
}

func TestRefErrorLocation(t *testing.T) {
	home := t.TempDir()
	lines := []string{
		"# 1",
		"# 2",
		"# 3",
		"# 4",
		"# 5",
		"# 6",
		"# 7",
		"# 8",
		"# 9",
		"# 10",
		"[agents.coder]",
		"provider = \"x\"",
	}
	writeFile(t, filepath.Join(home, "config.toml"), strings.Join(lines, "\n")+"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.coder.provider" {
			found = true
			if e.Line != 12 {
				t.Fatalf("line=%d want 12 (%v)", e.Line, e)
			}
			if !strings.Contains(e.Error(), filepath.Join(home, "config.toml")+":12") {
				t.Fatalf("incomplete location: %s", e.Error())
			}
		}
	}
	if !found {
		t.Fatalf("reference error missing: %v", errs)
	}
}

func TestUnknownKeySuggestion(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.coder]\npermision = \"ask\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.coder.permision" {
			found = true
			if !strings.Contains(e.Msg, "permission") {
				t.Fatalf("no suggestion: %s", e.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("key error missing: %v", errs)
	}
}

func TestVersionTooHigh(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "version = 2\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "config version 2 is higher than the supported version (1)") {
		t.Fatalf("wrong version message: %v", err)
	}
}

func TestMemoryLayerRefError(t *testing.T) {
	_, err := Load(context.Background(), Options{
		Layers: []Layer{{Name: "mem", TOML: []byte("[agents.x]\nprovider = \"nope\"\n")}},
		Env:    envMap(nil),
	})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "agents.x.provider" {
			if e.File != "mem" || e.Line != 2 {
				t.Fatalf("wrong location: %+v", e)
			}
			return
		}
	}
	t.Fatalf("memory layer reference error missing: %v", errs)
}

// REQ-LTC-040: max_steps accepts 0 (no limit) and any positive value with no
// upper bound, and rejects a negative value.
func TestMaxStepsRange(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmax_steps = 300\n")
	if _, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)}); err != nil {
		t.Fatalf("max_steps = 300 rejected: %v", err)
	}

	home = t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmax_steps = -1\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "agents.x.max_steps" {
			found = true
		}
	}
	if !found {
		t.Fatalf("negative max_steps not rejected: %v", errs)
	}
}

// REQ-LTC-030: a provider timeout that parses to <= 0 is rejected.
func TestProviderTimeoutsRejectNonPositive(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"),
		"[providers.p]\nkind = \"openai_compat\"\nstream_idle_timeout = \"0s\"\ntimeout = \"-1s\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	keys := map[string]bool{}
	for _, e := range errs {
		keys[e.Key] = true
	}
	if !keys["providers.p.stream_idle_timeout"] || !keys["providers.p.timeout"] {
		t.Fatalf("non-positive timeouts not rejected: %v", errs)
	}
}

func TestAgentMCPNotRegistered(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nmcp = [\"nope\"]\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error does not mention nope: %v", err)
	}
}

// API-KNW-001: [knowledge] accepts its keys and types, including a valid
// openai_compat embedding provider.
func TestKnowledgeSectionValid(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), strings.Join([]string{
		"[providers.openai]",
		"kind = \"openai_compat\"",
		"base_url = \"https://api.openai.com/v1\"",
		"",
		"[knowledge]",
		"path = \"~/Knowledge\"",
		"auto_recall = false",
		"embedding_provider = \"openai\"",
		"embedding_model = \"text-embedding-3-small\"",
	}, "\n")+"\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatalf("valid knowledge section rejected: %v", err)
	}
	k := res.Config.Knowledge
	if k.Path != "~/Knowledge" || k.AutoRecall == nil || *k.AutoRecall || k.EmbeddingProvider != "openai" || k.EmbeddingModel != "text-embedding-3-small" {
		t.Fatalf("knowledge config = %+v", k)
	}
}

// API-KNW-001: an unknown [knowledge] key is rejected with a suggestion.
func TestKnowledgeUnknownKey(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[knowledge]\nembedding_modle = \"x\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Key == "knowledge.embedding_modle" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown knowledge key not rejected: %v", errs)
	}
}

// API-KNW-001: embedding_provider must name a registered provider.
func TestKnowledgeProviderUnknown(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[knowledge]\nembedding_provider = \"foo\"\nembedding_model = \"m\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "knowledge.embedding_provider" {
			if !strings.Contains(e.Msg, `unknown provider "foo"`) {
				t.Fatalf("message = %q", e.Msg)
			}
			return
		}
	}
	t.Fatalf("unknown embedding provider not rejected: %v", errs)
}

// API-KNW-001: embedding_provider must be kind openai_compat.
func TestKnowledgeProviderWrongKind(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"),
		"[providers.anth]\nkind = \"anthropic\"\nbase_url = \"https://example.com\"\n\n[knowledge]\nembedding_provider = \"anth\"\nembedding_model = \"m\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "knowledge.embedding_provider" {
			if !strings.Contains(e.Msg, "openai_compat") {
				t.Fatalf("message = %q", e.Msg)
			}
			return
		}
	}
	t.Fatalf("non-openai_compat embedding provider not rejected: %v", errs)
}

// API-KNW-001: embedding_model is required when embedding_provider is set.
func TestKnowledgeModelRequired(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"),
		"[providers.openai]\nkind = \"openai_compat\"\nbase_url = \"https://example.com/v1\"\n\n[knowledge]\nembedding_provider = \"openai\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "knowledge.embedding_model" {
			if e.Msg != "required when embedding_provider is set" {
				t.Fatalf("message = %q", e.Msg)
			}
			return
		}
	}
	t.Fatalf("missing embedding_model not rejected: %v", errs)
}

// API-KNU-001: [knowledge] accepts agent, journal, and consolidate_at.
func TestKnowledgeUpkeepKeysValid(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), strings.Join([]string{
		"[agents.lana]",
		"",
		"[knowledge]",
		"agent = \"lana\"",
		"journal = false",
		"consolidate_at = \"03:00\"",
	}, "\n")+"\n")
	res, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	if err != nil {
		t.Fatalf("valid knowledge upkeep keys rejected: %v", err)
	}
	k := res.Config.Knowledge
	if k.Agent != "lana" {
		t.Fatalf("agent = %q, want lana", k.Agent)
	}
	if k.Journal == nil || *k.Journal {
		t.Fatalf("journal = %v, want false", k.Journal)
	}
	if k.ConsolidateAt == nil || *k.ConsolidateAt != "03:00" {
		t.Fatalf("consolidate_at = %v, want 03:00", k.ConsolidateAt)
	}
}

// API-KNU-001: knowledge.agent must name a configured agent.
func TestKnowledgeAgentUnknown(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[agents.lana]\n\n[knowledge]\nagent = \"ghost\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "knowledge.agent" {
			if e.Msg != `unknown agent "ghost"` {
				t.Fatalf("message = %q", e.Msg)
			}
			return
		}
	}
	t.Fatalf("unknown knowledge agent not rejected: %v", errs)
}

// API-KNU-001: consolidate_at accepts "" or a 24-hour HH:MM time.
func TestKnowledgeConsolidateAt(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{`""`, true},
		{`"03:00"`, true},
		{`"3pm"`, false},
		{`"25:00"`, false},
	} {
		home := t.TempDir()
		writeFile(t, filepath.Join(home, "config.toml"), "[knowledge]\nconsolidate_at = "+tc.value+"\n")
		_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
		if tc.ok {
			if err != nil {
				t.Fatalf("consolidate_at = %s rejected: %v", tc.value, err)
			}
			continue
		}
		var errs Errors
		if !errors.As(err, &errs) {
			t.Fatalf("consolidate_at = %s not rejected: %v", tc.value, err)
		}
		found := false
		for _, e := range errs {
			if e.Key == "knowledge.consolidate_at" {
				found = true
				if e.Msg != `want HH:MM or ""` {
					t.Fatalf("message = %q", e.Msg)
				}
			}
		}
		if !found {
			t.Fatalf("consolidate_at = %s not rejected: %v", tc.value, errs)
		}
	}
}

// API-DKN-002: agents.<name>.skill_write accepts "" (the default), "approve", or
// "direct", and rejects anything else with the exact message.
func TestSkillWriteValue(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{`""`, true},
		{`"approve"`, true},
		{`"direct"`, true},
		{`"always"`, false},
	} {
		home := t.TempDir()
		writeFile(t, filepath.Join(home, "config.toml"), "[agents.x]\nskill_write = "+tc.value+"\n")
		_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
		if tc.ok {
			if err != nil {
				t.Fatalf("skill_write = %s rejected: %v", tc.value, err)
			}
			continue
		}
		var errs Errors
		if !errors.As(err, &errs) {
			t.Fatalf("skill_write = %s not rejected: %v", tc.value, err)
		}
		found := false
		for _, e := range errs {
			if e.Key == "agents.x.skill_write" {
				found = true
				if e.Msg != `want "approve" or "direct"` {
					t.Fatalf("message = %q", e.Msg)
				}
			}
		}
		if !found {
			t.Fatalf("skill_write = %s not rejected: %v", tc.value, errs)
		}
	}
}

// API-KNU-001: journal must be a boolean.
func TestKnowledgeJournalType(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "config.toml"), "[knowledge]\njournal = \"yes\"\n")
	_, err := Load(context.Background(), Options{Home: home, Env: envMap(nil)})
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error is not Errors: %v", err)
	}
	for _, e := range errs {
		if e.Key == "knowledge.journal" {
			if !strings.Contains(e.Msg, "boolean") {
				t.Fatalf("message = %q", e.Msg)
			}
			return
		}
	}
	t.Fatalf("non-bool journal not rejected: %v", errs)
}
