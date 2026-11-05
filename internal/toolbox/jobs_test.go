package toolbox

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installManifest is a package whose install command copies a prepared binary
// from the data dir into bin/.
const installManifest = `
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"
version_args = ["--version"]
min_version = "0.2.0"

[[install]]
label = "copy"
command = 'cp "$DATA_DIR/fake" "$BIN_DIR/faketool" && chmod 0755 "$BIN_DIR/faketool"'
`

// prepareFake puts the fake binary content into the package data dir so the
// install command can copy it.
func prepareFake(t *testing.T, svc *Service) {
	t.Helper()
	dir := svc.Store().DataDir("faketool")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fake"), []byte(fakeBinary), 0o644); err != nil {
		t.Fatalf("write fake: %v", err)
	}
}

// TestInstallShellCommand installs through a shell command and detects the
// result (REQ-TB-003).
func TestInstallShellCommand(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, installManifest)
	prepareFake(t, svc)

	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	done := waitJob(t, svc, "faketool", job.ID)
	if done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	pkg, err := svc.Get(context.Background(), "faketool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pkg.Status.State != StateInstalled || pkg.Status.Version != "0.2.2" {
		t.Fatalf("pkg=%+v", pkg.Status)
	}
	if pkg.Installing {
		t.Fatal("Installing should be false after the job")
	}
	text, isDone, err := svc.Log("faketool", job.ID)
	if err != nil || !isDone {
		t.Fatalf("log done=%v err=%v", isDone, err)
	}
	if !strings.Contains(text, "[done]") {
		t.Fatalf("log=%q", text)
	}
}

// TestInstallFailure records a failed job with its log (REQ-TB-003).
func TestInstallFailure(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, `
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
label = "fail"
command = "echo oh-no >&2; exit 7"
`)
	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	done := waitJob(t, svc, "faketool", job.ID)
	if done.Status != JobFailed || done.Error == "" {
		t.Fatalf("job=%+v", done)
	}
	pkg, err := svc.Get(context.Background(), "faketool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pkg.Status.State != StateFailed {
		t.Fatalf("state=%q want failed", pkg.Status.State)
	}
	text, _, err := svc.Log("faketool", job.ID)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(text, "[error]") || !strings.Contains(text, "oh-no") {
		t.Fatalf("log=%q", text)
	}
}

// TestInstallBusy refuses a second job for the same package (REQ-TB-003).
func TestInstallBusy(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, `
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
label = "slow"
command = "sleep 1"
`)
	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := svc.Install(context.Background(), "faketool", 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("second install err=%v want ErrBusy", err)
	}
	waitJob(t, svc, "faketool", job.ID)
}

// TestInstallMethodMismatch rejects a method filtered to another OS.
func TestInstallMethodMismatch(t *testing.T) {
	otherOS := "linux"
	if runtime.GOOS == "linux" {
		otherOS = "darwin"
	}
	svc := testService(t)
	addManifest(t, svc, fmt.Sprintf(`
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
label = "other os"
url = "https://example.com/faketool.tar.gz"
os = %q
`, otherOS))
	if _, err := svc.Install(context.Background(), "faketool", 0); !errors.Is(err, ErrMethodMismatch) {
		t.Fatalf("err=%v want ErrMethodMismatch", err)
	}
}

// TestInstallDownloadRaw installs a raw binary from a URL after checking its
// sha256 (REQ-TB-003).
func TestInstallDownloadRaw(t *testing.T) {
	payload := []byte(fakeBinary)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	svc := testService(t)
	addManifest(t, svc, downloadManifest(srv.URL+"/faketool", sha256Hex(payload), ""))

	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	assertBinary(t, svc, payload)
}

// TestInstallDownloadTarGz installs one member of a tar.gz archive.
func TestInstallDownloadTarGz(t *testing.T) {
	payload := []byte(fakeBinary)
	archive := tarGz(t, "faketool", fakeBinary)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	svc := testService(t)
	addManifest(t, svc, downloadManifest(srv.URL+"/faketool.tar.gz", sha256Hex(archive), "faketool"))

	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	assertBinary(t, svc, payload)
}

// TestInstallDownloadZip installs one member of a zip archive.
func TestInstallDownloadZip(t *testing.T) {
	payload := []byte(fakeBinary)
	archive := zipFile(t, "faketool", fakeBinary)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	svc := testService(t)
	addManifest(t, svc, downloadManifest(srv.URL+"/faketool.zip", sha256Hex(archive), "faketool"))

	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	assertBinary(t, svc, payload)
}

// TestInstallDownloadBadSHA256 fails the job on a sha256 mismatch.
func TestInstallDownloadBadSHA256(t *testing.T) {
	payload := []byte(fakeBinary)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	svc := testService(t)
	addManifest(t, svc, downloadManifest(srv.URL+"/faketool", strings.Repeat("0", 64), ""))

	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobFailed {
		t.Fatalf("job=%+v want failed", done)
	}
	if _, err := os.Stat(filepath.Join(svc.Store().BinDir("faketool"), "faketool")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary should not exist: %v", err)
	}
}

// TestUninstallKeepsData removes bin/ but keeps data/ (REQ-TB-008).
func TestUninstallKeepsData(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, installManifest)
	prepareFake(t, svc)

	install, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitJob(t, svc, "faketool", install.ID)

	job, err := svc.Uninstall(context.Background(), "faketool")
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	entries, err := os.ReadDir(svc.Store().BinDir("faketool"))
	if err != nil {
		t.Fatalf("read bin: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("bin not empty: %+v", entries)
	}
	if _, err := os.Stat(svc.Store().DataDir("faketool")); err != nil {
		t.Fatalf("data dir removed: %v", err)
	}
	if _, err := svc.Uninstall(context.Background(), "faketool"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("second uninstall err=%v want ErrNotInstalled", err)
	}
}

// TestJobLogUnknown errors for an unknown job.
func TestJobLogUnknown(t *testing.T) {
	svc := testService(t)
	addManifest(t, svc, installManifest)
	if _, _, err := svc.Log("faketool", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	if _, _, err := svc.Log("ghost", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

// downloadManifest builds a one-method download manifest.
func downloadManifest(url, sha, extract string) string {
	extractLine := ""
	if extract != "" {
		extractLine = fmt.Sprintf("extract = %q\n", extract)
	}
	return fmt.Sprintf(`
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"
version_args = ["--version"]

[[install]]
label = "download"
url = %q
sha256 = %q
%s`, url, sha, extractLine)
}

// assertBinary checks that the installed binary matches the payload and is
// executable.
func assertBinary(t *testing.T, svc *Service, payload []byte) {
	t.Helper()
	path := filepath.Join(svc.Store().BinDir("faketool"), "faketool")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("binary=%q want %q", got, payload)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary is not executable: %v", info.Mode())
	}
}

// sha256Hex returns the lowercase hex sha256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// tarGz builds a one-member gzipped tar archive.
func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// zipFile builds a one-member zip archive.
func zipFile(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestExtractWholeArchive unpacks every member of a tar.gz into binDir,
// stripping the shared leading folder and setting the modes (REQ-TB-010).
func TestExtractWholeArchive(t *testing.T) {
	archive := tarGzEntries(t, []tarEntry{
		{"obscura-0.2.6/obscura", "#!/bin/sh\necho obscura\n", 0o755},
		{"obscura-0.2.6/obscura-worker", "#!/bin/sh\necho worker\n", 0o644},
		{"obscura-0.2.6/lib/helper", "helper", 0o755},
		{"obscura-0.2.6/README.md", "readme", 0o644},
		{"obscura-0.2.6/notes.md", "notes", 0o755},
	})
	binDir := t.TempDir()
	md := InstallMethod{URL: "https://example.com/obscura.tar.gz", Extract: "*"}
	if err := extract(writeTemp(t, archive), md, binDir, "obscura"); err != nil {
		t.Fatalf("extract: %v", err)
	}

	assertMode(t, filepath.Join(binDir, "obscura"), 0o755)
	assertMode(t, filepath.Join(binDir, "obscura-worker"), 0o755)
	assertMode(t, filepath.Join(binDir, "lib", "helper"), 0o755)
	assertMode(t, filepath.Join(binDir, "README.md"), 0o644)
	assertMode(t, filepath.Join(binDir, "notes.md"), 0o755)
}

// TestExtractWholeArchiveZip unpacks a zip the same way (REQ-TB-010).
func TestExtractWholeArchiveZip(t *testing.T) {
	archive := zipEntries(t, [][2]string{
		{"obscura/obscura", "binary"},
		{"obscura/README.md", "readme"},
	})
	binDir := t.TempDir()
	md := InstallMethod{URL: "https://example.com/obscura.zip", Extract: "*"}
	if err := extract(writeTemp(t, archive), md, binDir, "obscura"); err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertMode(t, filepath.Join(binDir, "obscura"), 0o755)
	assertMode(t, filepath.Join(binDir, "README.md"), 0o644)
}

// TestExtractWholeArchiveRejectsEscape refuses an archive member that would
// write outside binDir (REQ-TB-010).
func TestExtractWholeArchiveRejectsEscape(t *testing.T) {
	archive := tarGzEntries(t, []tarEntry{{"../evil", "boom", 0o755}})
	md := InstallMethod{URL: "https://example.com/x.tar.gz", Extract: "*"}
	if err := extract(writeTemp(t, archive), md, t.TempDir(), "x"); err == nil {
		t.Fatal("want an unsafe-path error")
	}
}

// TestInstallWholeArchive downloads a tar.gz and installs every member
// (REQ-TB-003, REQ-TB-010).
func TestInstallWholeArchive(t *testing.T) {
	archive := tarGzEntries(t, []tarEntry{
		{"obscura-0.2.6/obscura", fakeBinary, 0o755},
		{"obscura-0.2.6/obscura-worker", "worker", 0o755},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	svc := testService(t)
	addManifest(t, svc, downloadManifest(srv.URL+"/obscura.tar.gz", sha256Hex(archive), "*"))

	job, err := svc.Install(context.Background(), "faketool", 0)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if done := waitJob(t, svc, "faketool", job.ID); done.Status != JobSucceeded {
		t.Fatalf("job=%+v", done)
	}
	assertMode(t, filepath.Join(svc.Store().BinDir("faketool"), "obscura"), 0o755)
	assertMode(t, filepath.Join(svc.Store().BinDir("faketool"), "obscura-worker"), 0o755)
}

// tarEntry is one member of a test tar archive.
type tarEntry struct {
	name    string
	content string
	mode    int64
}

// tarGzEntries builds a gzipped tar archive from entries, in name order.
func tarGzEntries(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(entry.content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// zipEntries builds a zip archive from name/content pairs.
func zipEntries(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.Create(entry[0])
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write([]byte(entry[1])); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// writeTemp writes data to a temp file and returns its path.
func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

// assertMode checks that path exists with the wanted permission bits.
func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode of %s = %v want %v", path, got, want)
	}
}
