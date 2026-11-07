package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/sqlitex"
)

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, ok := ParseVersion(s)
	if !ok {
		t.Fatalf("ParseVersion(%q) failed", s)
	}
	return v
}

// TestParseVersion covers the version parser of design.md, Version compare.
func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0.1.0", "0.1.0", true},
		{"v0.1.0-beta.1", "0.1.0-beta.1", true},
		{"1.2.3+build.5", "1.2.3", true},
		{"1.2.3-rc.1+meta", "1.2.3-rc.1", true},
		{"dev", "", false},
		{"", "", false},
		{"1.2", "", false},
		{"01.2.3", "", false},
		{"1.2.3-01", "", false},
		{"1.2.3-", "", false},
		{"1.2.3-beta..1", "", false},
		{"1.2.3-be_ta", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseVersion(tc.in)
		if ok != tc.ok {
			t.Fatalf("ParseVersion(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
		if ok && got.String() != tc.want {
			t.Fatalf("ParseVersion(%q) = %q, want %q", tc.in, got.String(), tc.want)
		}
	}
}

// TestCompare covers SemVer 2.0 precedence with numeric prerelease parts.
func TestCompare(t *testing.T) {
	order := []string{"0.1.0-alpha", "0.1.0-alpha.1", "0.1.0-beta.1", "0.1.0-beta.2", "0.1.0-beta.10", "0.1.0", "0.1.1", "0.2.0-beta.1", "0.2.0", "1.0.0"}
	for i := range order {
		for j := range order {
			got := Compare(mustVersion(t, order[i]), mustVersion(t, order[j]))
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Fatalf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	if !SameVersion("v0.1.0", "0.1.0") || SameVersion("0.1.0", "0.1.0-beta.1") {
		t.Fatal("SameVersion")
	}
}

func release(tag string, prerelease bool, assets ...string) Release {
	r := Release{Version: mustVersionNoT(tag), Tag: tag, Prerelease: prerelease}
	for _, name := range assets {
		r.Assets = append(r.Assets, Asset{Name: name, URL: "https://example.invalid/" + tag + "/" + name})
	}
	return r
}

func mustVersionNoT(s string) Version {
	v, _ := ParseVersion(s)
	return v
}

// TestSelect covers AC-UPD-001, AC-UPD-002, and AC-UPD-003.
func TestSelect(t *testing.T) {
	asset := AssetName("darwin", "arm64")
	current := mustVersion(t, "0.1.0-beta.1")
	base := []Release{
		release("v0.1.0-beta.2", true, asset, SumsAsset),
		release("v0.1.0", false, asset, SumsAsset),
	}
	draft := release("v9.0.0", false, asset, SumsAsset)
	draft.Draft = true
	cases := []struct {
		name     string
		releases []Release
		channel  string
		want     string
	}{
		{"AC-UPD-001 stable", base, ChannelStable, "0.1.0"},
		{"AC-UPD-002 beta", append(append([]Release{}, base...), release("v0.2.0-beta.1", true, asset, SumsAsset)), ChannelBeta, "0.2.0-beta.1"},
		{"stable ignores newer beta", append(append([]Release{}, base...), release("v0.2.0-beta.1", true, asset, SumsAsset)), ChannelStable, "0.1.0"},
		{"AC-UPD-003 missing asset skipped", []Release{release("v0.1.0", false, asset, SumsAsset), release("v0.1.1", false, "lana-linux-amd64", SumsAsset)}, ChannelStable, "0.1.0"},
		{"missing sums skipped", []Release{release("v0.1.1", false, asset)}, ChannelStable, ""},
		{"AC-UPD-003 none", []Release{release("v0.1.1", false, "lana-linux-amd64", SumsAsset)}, ChannelStable, ""},
		{"draft ignored", []Release{draft}, ChannelBeta, ""},
		{"older ignored", []Release{release("v0.0.9", false, asset, SumsAsset), release("v0.1.0-beta.1", true, asset, SumsAsset)}, ChannelBeta, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Select(tc.releases, current, tc.channel, asset)
			if tc.want == "" {
				if ok {
					t.Fatalf("selected %s, want none", got.Version)
				}
				return
			}
			if !ok || got.Version.String() != tc.want {
				t.Fatalf("selected %v/%v, want %s", got.Version, ok, tc.want)
			}
		})
	}
}

func TestHasUpgradeNotes(t *testing.T) {
	if !HasUpgradeNotes("### Fixed\n- x\n\n### Upgrade notes\n- migrate") {
		t.Fatal("heading not found")
	}
	if HasUpgradeNotes("mentions upgrade notes inline") {
		t.Fatal("inline text is not a heading")
	}
}

// TestClientReleases covers the release client: the environment overrides,
// the token header, the per_page query, and the decoded fields (API-UPD-001).
func TestClientReleases(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = fmt.Fprint(w, `[
			{"tag_name":"v0.1.0","name":"lana 0.1.0","body":"notes","html_url":"https://x/v0.1.0","draft":false,"prerelease":false,
			 "published_at":"2026-11-08T10:00:00Z","assets":[{"name":"lana-linux-amd64","size":12,"browser_download_url":"https://x/a"}]},
			{"tag_name":"nightly","assets":[]}
		]`)
	}))
	defer server.Close()
	env := map[string]string{EnvAPI: server.URL + "/", EnvRepo: "owner/name", EnvToken: "tok"}
	client := NewClientFromEnv(server.Client(), func(k string) string { return env[k] }, "lana/test")
	releases, err := client.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || gotPath != "/repos/owner/name/releases" || gotQuery != "per_page=30" {
		t.Fatalf("request auth=%q path=%q query=%q", gotAuth, gotPath, gotQuery)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1 (the non-semver tag is dropped)", len(releases))
	}
	r := releases[0]
	if r.Version.String() != "0.1.0" || r.Name != "lana 0.1.0" || r.Notes != "notes" || r.URL != "https://x/v0.1.0" ||
		r.PublishedAt.IsZero() || len(r.Assets) != 1 || r.Assets[0].Size != 12 || r.Assets[0].URL != "https://x/a" {
		t.Fatalf("release = %+v", r)
	}

	// No token: no Authorization header; defaults apply.
	client = NewClientFromEnv(server.Client(), func(k string) string {
		if k == EnvAPI {
			return server.URL
		}
		return ""
	}, "")
	if client.Repo != DefaultRepo {
		t.Fatalf("repo = %q", client.Repo)
	}
	if _, err := client.Releases(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("auth = %q, want none", gotAuth)
	}
	if def := NewClientFromEnv(nil, func(string) string { return "" }, ""); def.API != DefaultAPI {
		t.Fatalf("api = %q", def.API)
	}
}

func TestClientReleasesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), API: server.URL, Repo: "o/n"}
	_, err := client.Releases(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v", err)
	}
}

func sumLine(data []byte, name string) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

// TestDownloadAndVerify covers downloading with progress, the size cap, and
// the checksum check (AC-UPD-007).
func TestDownloadAndVerify(t *testing.T) {
	payload := []byte(strings.Repeat("lana-binary-", 10000))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			_, _ = w.Write(payload)
		case "/sums":
			_, _ = fmt.Fprint(w, "# comment\n"+sumLine([]byte("other"), "lana-linux-amd64")+strings.Replace(sumLine(payload, "lana-darwin-arm64"), "  ", " *", 1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	dest := filepath.Join(dir, "lana-0.1.0.download")
	var last, total int64
	n, err := Download(context.Background(), server.Client(), server.URL+"/bin", dest, 0, func(done, t int64) { last, total = done, t })
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(payload)) || last != n || total != n {
		t.Fatalf("written=%d last=%d total=%d", n, last, total)
	}
	sums, err := FetchSums(context.Background(), server.Client(), server.URL+"/sums")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(dest, "lana-darwin-arm64", sums); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyFile(dest, "lana-linux-amd64", sums); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("mismatch err = %v", err)
	}
	if err := VerifyFile(dest, "lana-linux-arm64", sums); err == nil {
		t.Fatal("missing entry accepted")
	}

	// The cap stops the download and removes the file.
	_, err = Download(context.Background(), server.Client(), server.URL+"/bin", dest, 100, nil)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("cap err = %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("partial download kept: %v", statErr)
	}
	if _, err := Download(context.Background(), server.Client(), server.URL+"/missing", dest, 0, nil); err == nil {
		t.Fatal("404 accepted")
	}

	// A cancelled context stops the copy.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Download(ctx, server.Client(), server.URL+"/bin", dest, 0, nil); err == nil {
		t.Fatal("cancelled download succeeded")
	}
}

func TestParseSums(t *testing.T) {
	hexA := strings.Repeat("a", 64)
	got := ParseSums([]byte(hexA + "  lana-darwin-arm64\n" + strings.ToUpper(hexA) + " *SHA\nbad line\n\n"))
	if got["lana-darwin-arm64"] != hexA || got["SHA"] != hexA || len(got) != 2 {
		t.Fatalf("sums = %v", got)
	}
}

// TestCheckEligibility covers REQ-UPD-005 and design.md, Eligibility.
func TestCheckEligibility(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "lana")
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "lana-link")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	fixed := func(path string) func() (string, error) { return func() (string, error) { return path, nil } }

	got := CheckEligibility("linux", fixed(link))
	resolvedExe, _ := filepath.EvalSymlinks(exe)
	if !got.Supported || !got.CanApply || got.ExePath != resolvedExe {
		t.Fatalf("writable = %+v, want can_apply with %s", got, resolvedExe)
	}
	if got := CheckEligibility("windows", fixed(exe)); got.Supported || got.CanApply {
		t.Fatalf("windows = %+v", got)
	}
	if got := CheckEligibility("linux", fixed("/nix/store/abc-lana/bin/lana")); got.CanApply || !strings.Contains(got.Reason, "/nix/store") {
		t.Fatalf("nix = %+v", got)
	}
	if got := CheckEligibility("darwin", fixed("/opt/homebrew/Cellar/lana/0.1.0/bin/lana")); got.CanApply || !strings.Contains(got.Reason, "Homebrew") {
		t.Fatalf("cellar = %+v", got)
	}
	if got := CheckEligibility("linux", func() (string, error) { return "", errors.New("boom") }); got.CanApply || got.Reason == "" {
		t.Fatalf("unresolved = %+v", got)
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		readOnly := t.TempDir()
		roExe := filepath.Join(readOnly, "lana")
		if err := os.WriteFile(roExe, []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(readOnly, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(readOnly, 0o755) })
		if got := CheckEligibility("linux", fixed(roExe)); got.CanApply || !strings.Contains(got.Reason, "cannot write") {
			t.Fatalf("read-only = %+v", got)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".lana-update-write-test") {
			t.Fatalf("write test file left behind: %s", entry.Name())
		}
	}
}

// TestStateFiles covers state.json and pending.json (API-UPD-011).
func TestStateFiles(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	state, err := ReadState(p)
	if err != nil || state.Skipped != "" || state.LastResult != nil {
		t.Fatalf("empty state = %+v, %v", state, err)
	}
	if _, err := UpdateState(p, func(s *State) {
		s.Skipped = "0.1.0"
		s.LastResult = &Result{Outcome: OutcomeUpdated, From: "a", To: "b", At: 5}
	}); err != nil {
		t.Fatal(err)
	}
	state, err = ReadState(p)
	if err != nil || state.Skipped != "0.1.0" || state.LastResult == nil || state.LastResult.To != "b" {
		t.Fatalf("state = %+v, %v", state, err)
	}
	data, _ := os.ReadFile(p.State())
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil || raw["skipped"] != "0.1.0" {
		t.Fatalf("state.json = %s", data)
	}

	pending, err := ReadPending(p)
	if err != nil || pending != nil {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := WritePending(p, Pending{From: "a", To: "b", ExePath: "/x", BackupDir: "/y", Boots: 1, CreatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	pending, err = ReadPending(p)
	if err != nil || pending == nil || pending.Boots != 1 || pending.BackupDir != "/y" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := RemovePending(p); err != nil {
		t.Fatal(err)
	}
	if err := RemovePending(p); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if got := p.Download("v0.1.0-beta.2"); filepath.Base(got) != "lana-0.1.0-beta.2.download" {
		t.Fatalf("download path = %s", got)
	}
}

func makeDB(t *testing.T, path, value string) {
	t.Helper()
	db, err := sqlitex.Open("file:" + path + "?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS kv (v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM kv"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO kv (v) VALUES (?)", value); err != nil {
		t.Fatal(err)
	}
}

func readDB(t *testing.T, path string) string {
	t.Helper()
	db, err := sqlitex.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow("SELECT v FROM kv").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestBackupRestorePrune covers REQ-UPD-003 step 5, design.md Rollback, and
// REQ-UPD-006.
func TestBackupRestorePrune(t *testing.T) {
	home := t.TempDir()
	p := Paths{Home: home}
	makeDB(t, filepath.Join(home, "lana.db"), "old-sessions")
	makeDB(t, filepath.Join(home, "knowledge.db"), "old-index")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "nested.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	exe := filepath.Join(binDir, "lana")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 8, 10, 15, 0, 0, time.UTC)
	dir, manifest, err := Backup(context.Background(), BackupOptions{Home: home, ExePath: exe, From: "0.1.0-beta.1", To: "0.1.0", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "0.1.0-beta.1-to-0.1.0-20261108T101500Z" {
		t.Fatalf("backup dir = %s", dir)
	}
	wantFiles := []string{"knowledge.db", "lana.db", "config.toml", "lana"}
	if strings.Join(manifest.Files, ",") != strings.Join(wantFiles, ",") || manifest.ExePath != exe || manifest.CreatedAt != now.UnixMilli() {
		t.Fatalf("manifest = %+v", manifest)
	}

	// The new version changes everything.
	makeDB(t, filepath.Join(home, "lana.db"), "new-sessions")
	makeDB(t, filepath.Join(home, "knowledge.db"), "new-index")
	_ = os.WriteFile(filepath.Join(home, "config.toml"), []byte("# new\n"), 0o644)
	_ = os.WriteFile(filepath.Join(home, "lana.db-wal"), []byte("stale wal"), 0o600)
	newBin := filepath.Join(home, "updates", "lana-0.1.0.download")
	_ = os.WriteFile(newBin, []byte("new-binary"), 0o600)
	if err := ReplaceExecutable(newBin, exe); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "new-binary" {
		t.Fatalf("exe = %q", data)
	}
	if info, _ := os.Stat(exe); info.Mode().Perm() != 0o755 {
		t.Fatalf("exe mode = %v", info.Mode())
	}

	if err := Restore(dir, home, ""); err != nil {
		t.Fatal(err)
	}
	if got := readDB(t, filepath.Join(home, "lana.db")); got != "old-sessions" {
		t.Fatalf("lana.db = %q", got)
	}
	if got := readDB(t, filepath.Join(home, "knowledge.db")); got != "old-index" {
		t.Fatalf("knowledge.db = %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(data) != "# old\n" {
		t.Fatalf("config = %q", data)
	}
	if data, _ := os.ReadFile(exe); string(data) != "old-binary" {
		t.Fatalf("exe = %q", data)
	}

	// Prune keeps the newest 2.
	for i := 1; i <= 3; i++ {
		if _, _, err := Backup(context.Background(), BackupOptions{Home: home, ExePath: exe, From: "a", To: fmt.Sprint(i), Now: now.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(p, KeepBackups); err != nil {
		t.Fatal(err)
	}
	backups, err := ListBackups(p)
	if err != nil || len(backups) != 2 || backups[0].Manifest.To != "3" || backups[1].Manifest.To != "2" {
		t.Fatalf("backups = %+v, %v", backups, err)
	}
	newest, ok, err := NewestBackup(p)
	if err != nil || !ok || newest.Manifest.To != "3" {
		t.Fatalf("newest = %+v %v %v", newest, ok, err)
	}
}

// TestBackupFailureCleansUp: a *.db that is not SQLite fails the backup and
// leaves no partial folder.
func TestBackupFailureCleansUp(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "broken.db"), []byte("not sqlite at all, definitely not"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Backup(context.Background(), BackupOptions{Home: home, From: "a", To: "b"}); err == nil {
		t.Fatal("backup of a broken db succeeded")
	}
	backups, _ := ListBackups(Paths{Home: home})
	if len(backups) != 0 {
		t.Fatalf("partial backups left: %+v", backups)
	}
}

// TestClaimPending: one claimer wins the rename of pending.json; a claim left
// by a dead process can be taken over; Release removes the claim.
func TestClaimPending(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	if _, ok, err := ClaimPending(p, 1); ok || err != nil {
		t.Fatalf("claim without pending = %v, %v", ok, err)
	}
	if pids, err := ClaimPIDs(p); len(pids) != 0 || err != nil {
		t.Fatalf("pids = %v, %v", pids, err)
	}
	for round := 0; round < 50; round++ {
		if err := WritePending(p, Pending{From: "0.1.0", To: "0.2.0", Boots: 1}); err != nil {
			t.Fatal(err)
		}
		results := make(chan Claim, 8)
		done := make(chan struct{})
		for pid := 10; pid < 18; pid++ {
			go func(pid int) {
				defer func() { done <- struct{}{} }()
				if claim, ok, err := ClaimPending(p, pid); ok && err == nil {
					results <- claim
				}
			}(pid)
		}
		for i := 0; i < 8; i++ {
			<-done
		}
		close(results)
		if len(results) != 1 {
			t.Fatalf("round %d: %d winners", round, len(results))
		}
		claim := <-results
		if claim.Pending.To != "0.2.0" || claim.Pending.Boots != 1 || claim.Path != p.Claim(claim.PID) {
			t.Fatalf("claim = %+v", claim)
		}
		if pending, _ := ReadPending(p); pending != nil {
			t.Fatal("pending.json still there")
		}
		if pids, _ := ClaimPIDs(p); len(pids) != 1 || pids[0] != claim.PID {
			t.Fatalf("pids = %v", pids)
		}
		// The watchdog takes over the claim of a dead claimer.
		taken, ok, err := TakeOverClaim(p, claim.PID, 99)
		if !ok || err != nil || taken.Pending.To != "0.2.0" {
			t.Fatalf("take over = %+v, %v, %v", taken, ok, err)
		}
		if _, ok, _ := TakeOverClaim(p, claim.PID, 98); ok {
			t.Fatal("second take over won")
		}
		if err := taken.Release(); err != nil {
			t.Fatal(err)
		}
		if err := taken.Release(); err != nil {
			t.Fatalf("second release: %v", err)
		}
		if pids, _ := ClaimPIDs(p); len(pids) != 0 {
			t.Fatalf("pids after release = %v", pids)
		}
	}
	// An unreadable pending.json is dropped by the claim.
	if err := os.WriteFile(p.Pending(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ClaimPending(p, 5); ok || err == nil {
		t.Fatalf("corrupt claim = %v, %v", ok, err)
	}
	if pids, _ := ClaimPIDs(p); len(pids) != 0 {
		t.Fatalf("pids = %v", pids)
	}
}
