package toolbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// smallManifest is a minimal valid manifest for the service tests.
func smallManifest(id string) string {
	return `
id = "` + id + `"
name = "` + id + `"

[detect]
binary = "faketool"

[[install]]
command = "true"
`
}

// TestNewDetectsStoredPackages runs a detection pass in New (REQ-TB-002).
func TestNewDetectsStoredPackages(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	manifest := storeManifest(t, "faketool")
	if err := store.Save(manifest); err != nil {
		t.Fatalf("save: %v", err)
	}
	writeScript(t, store, "faketool", "faketool", fakeBinary)

	svc := New(root, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(svc.Close)

	pkg, err := svc.Get(context.Background(), "faketool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pkg.Status.State != StateInstalled {
		t.Fatalf("status=%+v want installed", pkg.Status)
	}
}

// TestServiceAddListGet stores, lists and reads a package.
func TestServiceAddListGet(t *testing.T) {
	svc := testService(t)
	added := addManifest(t, svc, smallManifest("faketool"))
	if added.Manifest.ID != "faketool" || added.Status.State != StateMissing || added.Source != SourceCustom {
		t.Fatalf("added=%+v", added)
	}
	// List merges the stored package with the built-in catalog (REQ-TB-001).
	var found bool
	for _, pkg := range svc.List(context.Background()) {
		if pkg.Manifest.ID == "faketool" {
			found = true
		}
	}
	if !found {
		t.Fatal("faketool is not in the list")
	}
	if _, err := svc.Get(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get ghost err=%v want ErrNotFound", err)
	}
}

// TestServiceAddDuplicate returns ErrExists on a taken id.
func TestServiceAddDuplicate(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, smallManifest("faketool"))
	if _, _, err := svc.Add(context.Background(), smallManifest("faketool")); !errors.Is(err, ErrExists) {
		t.Fatalf("err=%v want ErrExists", err)
	}
}

// TestServiceAddInvalid returns validation issues.
func TestServiceAddInvalid(t *testing.T) {
	svc := testService(t)
	_, issues, err := svc.Add(context.Background(), "id = \"\"\n")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("want validation issues")
	}
}

// TestServiceUpdate replaces a manifest and refuses an id change.
func TestServiceUpdate(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, smallManifest("faketool"))

	updated, issues, err := svc.Update(context.Background(), "faketool", `
id = "faketool"
name = "Renamed"

[detect]
binary = "faketool"

[[install]]
command = "true"
`)
	if err != nil || len(issues) != 0 {
		t.Fatalf("update err=%v issues=%+v", err, issues)
	}
	if updated.Manifest.Name != "Renamed" {
		t.Fatalf("name=%q", updated.Manifest.Name)
	}

	_, issues, err = svc.Update(context.Background(), "faketool", smallManifest("other"))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !hasField(issues, "id") {
		t.Fatalf("issues=%+v want an id issue", issues)
	}

	if _, _, err := svc.Update(context.Background(), "ghost", smallManifest("ghost")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

// TestServiceDeleteRefusesInstalled refuses to delete an installed package
// without the uninstall flag (REQ-TB-008).
func TestServiceDeleteRefusesInstalled(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, installManifest)
	prepareFake(t, svc)
	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitJob(t, svc, "faketool", job.ID)

	if err := svc.Delete(context.Background(), "faketool", false); !errors.Is(err, ErrInstalled) {
		t.Fatalf("err=%v want ErrInstalled", err)
	}
	if err := svc.Delete(context.Background(), "faketool", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), "faketool"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err=%v want ErrNotFound", err)
	}
}

// TestServiceCheck recomputes the status on demand.
func TestServiceCheck(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, storeManifestString())
	writeScript(t, svc.Store(), "faketool", "faketool", fakeBinary)
	pkg, err := svc.Check(context.Background(), "faketool")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if pkg.Status.State != StateInstalled {
		t.Fatalf("status=%+v", pkg.Status)
	}
	if _, err := svc.Check(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

// TestServiceListBrokenEntry reports a broken manifest as a problem.
func TestServiceListBrokenEntry(t *testing.T) {
	svc := testService(t)
	if err := svc.Store().Save(storeManifest(t, "good")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := writeBrokenManifest(svc.Store(), "broken"); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	list := svc.List(context.Background())
	var broken Package
	for _, pkg := range list {
		if pkg.Manifest.ID == "broken" {
			broken = pkg
		}
	}
	if broken.Problem == "" || broken.Status.State != StateFailed {
		t.Fatalf("broken=%+v", broken)
	}
}

// TestServiceInstalledAndFindBinary finds an installed package's binary.
func TestServiceInstalledAndFindBinary(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, installManifest)
	prepareFake(t, svc)
	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitJob(t, svc, "faketool", job.ID)

	// Installed merges the catalog too, so only faketool is asserted here.
	var found bool
	for _, pkg := range svc.Installed() {
		if pkg.Manifest.ID == "faketool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("faketool is not installed: %+v", svc.Installed())
	}
	path, ok := svc.FindBinary("faketool")
	if !ok || path != svc.Store().BinDir("faketool")+"/faketool" {
		t.Fatalf("find=%q ok=%v", path, ok)
	}
}

// TestServiceSubscribe receives the changed package id.
func TestServiceSubscribe(t *testing.T) {
	svc := testService(t)
	ch, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	addManifest(t, svc, smallManifest("faketool"))
	select {
	case got := <-ch:
		if got != "faketool" {
			t.Fatalf("changed=%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no change received")
	}
}

// TestPackageView builds the web view with tool names and sorted input fields.
func TestPackageView(t *testing.T) {
	manifest, issues := ParseManifest(goodManifest)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	pkg := Package{Manifest: manifest, Status: Status{State: StateInstalled, Version: "0.2.2"}, UpdatedAt: 42}
	view := pkg.View()
	if view.ID != "obscura" || view.Status != StateInstalled || view.UpdatedAt != 42 {
		t.Fatalf("view=%+v", view)
	}
	if len(view.Commands) != 1 || view.Commands[0].Tool != "obscura_fetch" {
		t.Fatalf("commands=%+v", view.Commands)
	}
	input := view.Commands[0].Input
	if len(input) != 2 || input[0].Name != "format" || input[1].Name != "url" {
		t.Fatalf("input=%+v", input)
	}
	if len(view.Install) != 2 || view.Install[0].Kind != "command" || view.Install[1].Kind != "download" {
		t.Fatalf("install=%+v", view.Install)
	}
}

// storeManifestString is a minimal manifest for Check tests.
func storeManifestString() string {
	return `
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
command = "true"
`
}

// writeBrokenManifest writes an unparsable tool.toml into a package directory.
func writeBrokenManifest(store *Store, id string) error {
	if err := os.MkdirAll(store.Dir(id), 0o700); err != nil {
		return err
	}
	return os.WriteFile(store.ManifestPath(id), []byte("id = "), 0o600)
}
