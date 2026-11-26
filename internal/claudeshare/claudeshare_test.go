package claudeshare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Unix(1_700_000_000, 0)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// linkTarget returns the target of the symlink at path, failing when path is
// not a symlink.
func linkTarget(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode %v)", path, info.Mode())
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func notExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists, want none (err %v)", path, err)
	}
}

func actions(r Report) map[string]Action {
	out := map[string]Action{}
	for _, item := range r.Items {
		out[item.Name] = item.Action
	}
	return out
}

// TestLinkNewAccount is AC-WRA-001: a new account folder gets links for
// projects and history.jsonl (created in the default folder when missing) and
// for settings.json, CLAUDE.md and skills only because the default has them.
func TestLinkNewAccount(t *testing.T) {
	def := t.TempDir()
	acct := t.TempDir()
	write(t, filepath.Join(def, "settings.json"), `{}`)
	write(t, filepath.Join(def, "CLAUDE.md"), "# me")
	write(t, filepath.Join(def, "skills", "a", "SKILL.md"), "x")
	write(t, filepath.Join(acct, ".claude.json"), `{"secret":1}`)

	report, err := Link(acct, def, testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"projects", "history.jsonl", "settings.json", "CLAUDE.md", "skills"} {
		if got := linkTarget(t, filepath.Join(acct, name)); got != filepath.Join(def, name) {
			t.Errorf("%s -> %s, want %s", name, got, filepath.Join(def, name))
		}
		if !filepath.IsAbs(linkTarget(t, filepath.Join(acct, name))) {
			t.Errorf("%s target is not absolute", name)
		}
	}
	for _, name := range []string{"agents", "commands"} {
		notExists(t, filepath.Join(acct, name))
	}
	if info, err := os.Stat(filepath.Join(def, "projects")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("default projects: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(def, "history.jsonl")); err != nil || info.Size() != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("default history.jsonl: %v %v", info, err)
	}
	if got := read(t, filepath.Join(acct, ".claude.json")); got != `{"secret":1}` {
		t.Fatalf(".claude.json changed: %q", got)
	}
	want := map[string]Action{
		"projects": Linked, "history.jsonl": Linked, "settings.json": Linked, "CLAUDE.md": Linked,
		"skills": Linked, "agents": Skipped, "commands": Skipped,
	}
	got := actions(report)
	for name, action := range want {
		if got[name] != action {
			t.Errorf("%s action = %q, want %q", name, got[name], action)
		}
	}
	if !report.Changed() {
		t.Fatal("Changed = false")
	}
}

// TestLinkAdoptsProjects is AC-WRA-002: the account's real projects/a/1.jsonl
// joins the default's projects/a/2.jsonl and the account's projects becomes a
// link. A name the shared folder already has stays behind in the renamed
// leftover.
func TestLinkAdoptsProjects(t *testing.T) {
	def := t.TempDir()
	acct := t.TempDir()
	write(t, filepath.Join(acct, "projects", "a", "1.jsonl"), "one\n")
	write(t, filepath.Join(acct, "projects", "a", "memory", "MEMORY.md"), "m")
	write(t, filepath.Join(acct, "projects", "b", "3.jsonl"), "three\n")
	write(t, filepath.Join(def, "projects", "a", "2.jsonl"), "two\n")

	report, err := Link(acct, def, testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a/1.jsonl", "a/2.jsonl", "a/memory/MEMORY.md", "b/3.jsonl"} {
		if _, err := os.Stat(filepath.Join(def, "projects", rel)); err != nil {
			t.Errorf("shared projects/%s: %v", rel, err)
		}
	}
	linkTarget(t, filepath.Join(acct, "projects"))
	notExists(t, filepath.Join(acct, "projects.before-share-1700000000"))
	if actions(report)["projects"] != Adopted {
		t.Fatalf("projects action = %q", actions(report)["projects"])
	}

	// A clash: the same file name in both stays behind, renamed aside.
	acct2 := t.TempDir()
	write(t, filepath.Join(acct2, "projects", "a", "2.jsonl"), "other two\n")
	write(t, filepath.Join(acct2, "projects", "a", "4.jsonl"), "four\n")
	report, err = Link(acct2, def, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(def, "projects", "a", "2.jsonl")); got != "two\n" {
		t.Fatalf("shared 2.jsonl overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(def, "projects", "a", "4.jsonl")); err != nil {
		t.Fatalf("4.jsonl not moved: %v", err)
	}
	backup := filepath.Join(acct2, "projects.before-share-1700000000")
	if got := read(t, filepath.Join(backup, "a", "2.jsonl")); got != "other two\n" {
		t.Fatalf("leftover 2.jsonl = %q", got)
	}
	linkTarget(t, filepath.Join(acct2, "projects"))
	if report.Items[0].Backup != backup {
		t.Fatalf("backup = %q, want %q", report.Items[0].Backup, backup)
	}
}

// TestLinkAdoptsHistory appends the account's history lines to the shared file,
// with a newline between them, and renames the account's file aside.
func TestLinkAdoptsHistory(t *testing.T) {
	def := t.TempDir()
	acct := t.TempDir()
	write(t, filepath.Join(def, "history.jsonl"), `{"d":1}`)
	write(t, filepath.Join(acct, "history.jsonl"), `{"a":1}`+"\n"+`{"a":2}`)

	if _, err := Link(acct, def, testNow); err != nil {
		t.Fatal(err)
	}
	want := `{"d":1}` + "\n" + `{"a":1}` + "\n" + `{"a":2}` + "\n"
	if got := read(t, filepath.Join(def, "history.jsonl")); got != want {
		t.Fatalf("shared history = %q, want %q", got, want)
	}
	linkTarget(t, filepath.Join(acct, "history.jsonl"))
	if got := read(t, filepath.Join(acct, "history.jsonl.before-share-1700000000")); got != `{"a":1}`+"\n"+`{"a":2}` {
		t.Fatalf("history backup = %q", got)
	}
}

// TestLinkKeepsExistingLinkAndRealSettings keeps a link that points elsewhere
// and the account's own settings.json.
func TestLinkKeepsExistingLinkAndRealSettings(t *testing.T) {
	def := t.TempDir()
	acct := t.TempDir()
	elsewhere := t.TempDir()
	write(t, filepath.Join(def, "settings.json"), `{"shared":true}`)
	write(t, filepath.Join(acct, "settings.json"), `{"own":true}`)
	if err := os.Symlink(elsewhere, filepath.Join(acct, "projects")); err != nil {
		t.Fatal(err)
	}

	report, err := Link(acct, def, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := linkTarget(t, filepath.Join(acct, "projects")); got != elsewhere {
		t.Fatalf("projects -> %s, want kept %s", got, elsewhere)
	}
	if info, err := os.Lstat(filepath.Join(acct, "settings.json")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("own settings.json replaced: %v %v", info, err)
	}
	if got := read(t, filepath.Join(acct, "settings.json")); got != `{"own":true}` {
		t.Fatalf("own settings.json = %q", got)
	}
	got := actions(report)
	if got["projects"] != Kept || got["settings.json"] != Kept {
		t.Fatalf("actions = %v", got)
	}
}

// TestLinkIdempotent: a second run changes nothing.
func TestLinkIdempotent(t *testing.T) {
	def := t.TempDir()
	acct := t.TempDir()
	write(t, filepath.Join(def, "CLAUDE.md"), "# me")
	write(t, filepath.Join(acct, "history.jsonl"), "x\n")
	if _, err := Link(acct, def, testNow); err != nil {
		t.Fatal(err)
	}
	before := read(t, filepath.Join(def, "history.jsonl"))
	report, err := Link(acct, def, testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed() {
		t.Fatalf("second run changed: %+v", report)
	}
	if got := read(t, filepath.Join(def, "history.jsonl")); got != before {
		t.Fatalf("history changed on second run: %q", got)
	}
	entries, _ := os.ReadDir(acct)
	for _, e := range entries {
		if strings.Contains(e.Name(), "before-share") && e.Name() != "history.jsonl.before-share-1700000000" {
			t.Fatalf("unexpected leftover %s", e.Name())
		}
	}
}

// TestLinkNoOp: the default folder itself, an empty path, and a missing
// account folder do nothing.
func TestLinkNoOp(t *testing.T) {
	def := t.TempDir()
	for _, c := range []struct{ name, acct, def string }{
		{"same folder", def, def},
		{"same folder, unclean", def + "/./", def},
		{"empty account", "", def},
		{"empty default", def, ""},
		{"missing account", filepath.Join(def, "nope"), t.TempDir()},
	} {
		report, err := Link(c.acct, c.def, testNow)
		if err != nil || len(report.Items) != 0 {
			t.Errorf("%s: Link = (%+v, %v), want no-op", c.name, report, err)
		}
	}
	entries, _ := os.ReadDir(def)
	if len(entries) != 0 {
		t.Fatalf("default folder touched: %v", entries)
	}

	// A symlink to the default folder is the default folder.
	parent := t.TempDir()
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(def, alias); err != nil {
		t.Fatal(err)
	}
	if report, err := Link(alias, def, testNow); err != nil || len(report.Items) != 0 {
		t.Fatalf("alias: Link = (%+v, %v), want no-op", report, err)
	}
}
