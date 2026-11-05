package toolbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCatalogLoads parses and validates every embedded catalog manifest
// (REQ-TB-001, REQ-TB-009, REQ-TB-011).
func TestCatalogLoads(t *testing.T) {
	svc := testService(t)
	for _, id := range []string{"ffmpeg", "obscura", "pandoc", "yt-dlp"} {
		manifest, ok := svc.catalog.byID[id]
		if !ok {
			t.Fatalf("catalog is missing %q: %+v", id, svc.catalog.byID)
		}
		if issues := manifest.Validate(); len(issues) != 0 {
			t.Fatalf("%s issues: %+v", id, issues)
		}
	}
	if len(svc.catalog.manifests) != 4 {
		t.Fatalf("catalog has %d manifests want 4", len(svc.catalog.manifests))
	}

	obscura := svc.catalog.byID["obscura"]
	if obscura.Icon != "globe" || obscura.Category != "Browser" {
		t.Fatalf("obscura icon=%q category=%q", obscura.Icon, obscura.Category)
	}
	if obscura.MCP == nil || len(obscura.MCP.Args) != 4 {
		t.Fatalf("obscura mcp=%+v", obscura.MCP)
	}
	for _, md := range obscura.Install {
		if md.Extract != "*" {
			t.Fatalf("obscura install extract=%q want *", md.Extract)
		}
	}
	ytdlp := svc.catalog.byID["yt-dlp"]
	transcript, ok := findCommand(ytdlp, "transcript")
	if !ok {
		t.Fatalf("yt-dlp has no transcript command: %+v", ytdlp.Commands)
	}
	if transcript.OutputFrom != "files" || transcript.Transform != "subtitles" || transcript.Risk != "read" {
		t.Fatalf("transcript=%+v", transcript)
	}
	if !transcript.Input["url"].Required || transcript.Input["lang"].Default == nil {
		t.Fatalf("transcript input=%+v", transcript.Input)
	}
	pandoc := svc.catalog.byID["pandoc"]
	if got := pandoc.Install[0].Requires; len(got) != 1 || got[0] != "brew" {
		t.Fatalf("pandoc requires=%v", got)
	}
}

// TestCatalogMergeOverrideDelete covers the catalog merge rules of REQ-TB-001:
// a stored override wins, delete removes only the override, and a catalog-only
// entry cannot be deleted.
func TestCatalogMergeOverrideDelete(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)

	// A catalog-only entry is reported but never written to disk.
	pkg, err := svc.Get(ctx, "pandoc")
	if err != nil {
		t.Fatalf("get pandoc: %v", err)
	}
	if pkg.Source != SourceCatalog || pkg.Manifest.Name != "Pandoc" {
		t.Fatalf("pandoc=%+v", pkg)
	}
	if svc.Store().exists("pandoc") {
		t.Fatal("a catalog entry must not be on disk before it is edited")
	}

	// Adding a manifest with a catalog id is refused.
	if _, _, err := svc.Add(ctx, overrideManifest("pandoc", "Mine")); !errors.Is(err, ErrExists) {
		t.Fatalf("add catalog id err=%v want ErrExists", err)
	}

	// Updating a catalog entry writes a stored override.
	updated, issues, err := svc.Update(ctx, "pandoc", overrideManifest("pandoc", "My Pandoc"))
	if err != nil || len(issues) != 0 {
		t.Fatalf("update err=%v issues=%+v", err, issues)
	}
	if updated.Source != SourceCustom || updated.Manifest.Name != "My Pandoc" {
		t.Fatalf("updated=%+v", updated)
	}
	if !svc.Store().exists("pandoc") {
		t.Fatal("the override was not written to disk")
	}
	if got, _ := svc.Get(ctx, "pandoc"); got.Manifest.Name != "My Pandoc" || got.Source != SourceCustom {
		t.Fatalf("override not visible: %+v", got)
	}

	// Deleting removes the override and falls back to the catalog entry.
	if err := svc.Delete(ctx, "pandoc", false); err != nil {
		t.Fatalf("delete override: %v", err)
	}
	if svc.Store().exists("pandoc") {
		t.Fatal("the override directory is still present")
	}
	if got, _ := svc.Get(ctx, "pandoc"); got.Manifest.Name != "Pandoc" || got.Source != SourceCatalog {
		t.Fatalf("catalog entry not restored: %+v", got)
	}

	// A catalog-only entry cannot be removed.
	if err := svc.Delete(ctx, "pandoc", false); !errors.Is(err, ErrCatalog) {
		t.Fatalf("delete catalog-only err=%v want ErrCatalog", err)
	}
}

// TestCatalogGetViews reads a catalog-only entry through the operations the web
// and runtime call (REQ-TB-001, REQ-TB-009, REQ-TB-011).
func TestCatalogGetViews(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)

	pkg, err := svc.Get(ctx, "yt-dlp")
	if err != nil {
		t.Fatalf("get yt-dlp: %v", err)
	}
	view := pkg.View()
	if view.Source != SourceCatalog || view.Category != "Media" {
		t.Fatalf("view=%+v", view)
	}
	// auto_method mirrors AutoMethod, and is -1 only when nothing fits
	// (REQ-TB-009).
	if want, ok := pkg.Manifest.AutoMethod(); ok {
		if view.AutoMethod != want {
			t.Fatalf("auto_method=%d want %d", view.AutoMethod, want)
		}
	} else if view.AutoMethod != -1 {
		t.Fatalf("auto_method=%d want -1", view.AutoMethod)
	}
	if _, err := svc.Check(ctx, "yt-dlp"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, _, err := svc.Log("yt-dlp", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("log err=%v want ErrNotFound", err)
	}
}

// TestMCPServers renders the args and env of an installed package's MCP spec
// (REQ-TB-011).
func TestMCPServers(t *testing.T) {
	svc := testService(t, WithSecrets(func(name string) (string, bool) {
		if name == "TOK" {
			return "s3cr3t", true
		}
		return "", false
	}))
	addManifest(t, svc, `
id = "fakey"
name = "Fakey"

[detect]
binary = "fakey"

[[install]]
command = "true"

[mcp]
args = ["--stealth", "--storage-dir", "{{data}}/profile", "--bin", "{{bin}}", "mcp"]

[mcp.env]
TOKEN = "secret:TOK"
`)
	writeScript(t, svc.Store(), "fakey", "fakey", "#!/bin/sh\necho hi\n")
	if _, err := svc.Check(context.Background(), "fakey"); err != nil {
		t.Fatalf("check: %v", err)
	}

	var found bool
	for _, server := range svc.MCPServers() {
		if server.Package != "fakey" {
			continue
		}
		found = true
		wantArgs := []string{
			"--stealth",
			"--storage-dir",
			filepath.Join(svc.Store().DataDir("fakey"), "profile"),
			"--bin",
			svc.Store().BinDir("fakey"),
			"mcp",
		}
		if len(server.Args) != len(wantArgs) {
			t.Fatalf("args=%v want %v", server.Args, wantArgs)
		}
		for i := range wantArgs {
			if server.Args[i] != wantArgs[i] {
				t.Fatalf("args[%d]=%q want %q", i, server.Args[i], wantArgs[i])
			}
		}
		if server.Command != filepath.Join(svc.Store().BinDir("fakey"), "fakey") {
			t.Fatalf("command=%q", server.Command)
		}
		if server.Env["TOKEN"] != "s3cr3t" {
			t.Fatalf("env=%v", server.Env)
		}
		if _, err := os.Stat(filepath.Join(svc.Store().DataDir("fakey"), "profile")); err != nil {
			t.Fatalf("profile dir: %v", err)
		}
	}
	if !found {
		t.Fatalf("fakey MCP server not returned: %+v", svc.MCPServers())
	}
}

// TestMCPServersSkipsMissingSecret omits a server whose secret env cannot be
// resolved (REQ-TB-007, REQ-TB-011).
func TestMCPServersSkipsMissingSecret(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, `
id = "fakey"
name = "Fakey"

[detect]
binary = "fakey"

[[install]]
command = "true"

[mcp]
args = ["mcp"]

[mcp.env]
TOKEN = "secret:TOK"
`)
	writeScript(t, svc.Store(), "fakey", "fakey", "#!/bin/sh\necho hi\n")
	if _, err := svc.Check(context.Background(), "fakey"); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, server := range svc.MCPServers() {
		if server.Package == "fakey" {
			t.Fatal("a server with an unresolvable secret must be skipped")
		}
	}
}

// overrideManifest builds a minimal valid manifest for a catalog override.
func overrideManifest(id, name string) string {
	return `
id = "` + id + `"
name = "` + name + `"

[detect]
binary = "faketool"

[[install]]
command = "true"
`
}
