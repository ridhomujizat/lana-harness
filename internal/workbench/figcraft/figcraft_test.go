package figcraft

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake CLI is tested without a real figcraft-cli: a shell script re-execs
// this test binary into TestFakeCLIMain with the same arguments, so the fake
// can parse the run scripts in Go. It records its arguments and reads canned
// outputs from the fixture folder named by FIGCRAFT_FAKE_DIR (REQ-FV-100).
const (
	fakeEnvDir     = "FIGCRAFT_FAKE_DIR"
	fakeEnvTestbin = "FIGCRAFT_FAKE_TESTBIN"
)

// TestFakeCLIMain is the fake figcraft-cli, reached only through the helper
// script. It exits before the testing framework prints anything.
func TestFakeCLIMain(t *testing.T) {
	dir := os.Getenv(fakeEnvDir)
	if dir == "" {
		return
	}
	var rest []string
	for i, a := range os.Args {
		if a == "--" {
			rest = os.Args[i+1:]
			break
		}
	}
	os.Exit(fakeMain(dir, rest))
}

// fakeMain dispatches one fake CLI invocation.
func fakeMain(dir string, args []string) int {
	record(dir, args)
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "--version":
		_, _ = fmt.Fprintln(os.Stdout, strings.TrimRight(readOr(dir, "version.txt", "figcraft-cli 0.18.1"), "\n"))
		return 0
	case "run":
		return fakeRun(dir, args)
	case "render", "export":
		return fakeRender(dir, args)
	}
	return 2
}

// fakeRun answers a "run <script>" by echoing one JSON line per step.
func fakeRun(dir string, args []string) int {
	if fails(dir) {
		fmt.Fprintln(os.Stderr, "figcraft: boom")
		return 1
	}
	sleep(dir)
	if len(args) < 2 {
		return 2
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var steps []struct {
		Command string          `json:"command"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &steps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()
	for _, st := range steps {
		switch st.Command {
		case "page.list":
			_, _ = fmt.Fprintf(out, "{\"command\":\"page.list\",\"ok\":true,\"result\":%s}\n", strings.TrimSpace(readOr(dir, "pages.json", "[]")))
		case "node.get":
			var p struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(st.Params, &p)
			if b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("node-%d.json", p.ID))); err == nil {
				_, _ = fmt.Fprintf(out, "{\"command\":\"node.get\",\"ok\":true,\"result\":%s}\n", strings.TrimSpace(string(b)))
			} else {
				_, _ = fmt.Fprintf(out, "{\"command\":\"node.get\",\"ok\":false,\"error\":\"no layer %d\"}\n", p.ID)
			}
		}
	}
	return 0
}

// fakeRender writes a small valid PNG to the -o path.
func fakeRender(dir string, args []string) int {
	if fails(dir) {
		fmt.Fprintln(os.Stderr, "figcraft: boom")
		return 1
	}
	sleep(dir)
	out := ""
	for i, a := range args {
		if a == "-o" && i+1 < len(args) {
			out = args[i+1]
		}
	}
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing -o")
		return 2
	}
	if err := writePNG(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// record appends one invocation's arguments as a JSON line to argv.log.
func record(dir string, args []string) {
	line, err := json.Marshal(args)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "argv.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// fails reports whether the fake should fail (the fail marker exists).
func fails(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "fail.txt"))
	return err == nil
}

// sleep pauses the fake for the milliseconds in sleep.txt, when present, so a
// test can overlap two runs.
func sleep(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, "sleep.txt"))
	if err != nil {
		return
	}
	if ms, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// readOr reads a fixture file, or returns fallback when it is missing.
func readOr(dir, name, fallback string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fallback
	}
	return string(b)
}

// writePNG writes a 2x2 PNG, the render the fake produces.
func writePNG(path string) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// fakeCLI is the test harness around the fake CLI.
type fakeCLI struct {
	t        *testing.T
	dir      string // fixtures and argv.log
	tempRoot string // the Service's run temp root
	work     string // design files
	cli      string // the helper script
}

// newFakeCLI builds the fixture folders and the helper script.
func newFakeCLI(t *testing.T) *fakeCLI {
	t.Helper()
	testbin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCLI{t: t, dir: t.TempDir(), tempRoot: t.TempDir(), work: t.TempDir()}
	t.Setenv(fakeEnvDir, f.dir)
	t.Setenv(fakeEnvTestbin, testbin)

	f.cli = filepath.Join(t.TempDir(), "figcraft-cli")
	script := fmt.Sprintf("#!/bin/sh\nexec \"$%s\" -test.run=^TestFakeCLIMain$ -- \"$@\"\n", fakeEnvTestbin)
	if err := os.WriteFile(f.cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// service builds a Service that uses the fake CLI.
func (f *fakeCLI) service(mutate func(*Options)) *Service {
	f.t.Helper()
	opts := Options{
		HomeDir:    f.t.TempDir(),
		TempDir:    f.tempRoot,
		Settings:   func() Settings { return Settings{CLIPath: f.cli, Live: true, LivePorts: []int{}} },
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		AppVersion: func() string { return "" },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Never scan the machine's real FigCraft apps from a test.
		DiscoverPorts: func(context.Context) ([]int, error) { return nil, nil },
	}
	if mutate != nil {
		mutate(&opts)
	}
	return New(opts)
}

// write puts a fixture file in the fake's folder.
func (f *fakeCLI) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// pages sets the page.list result.
func (f *fakeCLI) pages(content string) { f.write("pages.json", content) }

// node sets one node.get result.
func (f *fakeCLI) node(id int64, content string) {
	f.write(fmt.Sprintf("node-%d.json", id), content)
}

// version sets the --version output.
func (f *fakeCLI) version(v string) { f.write("version.txt", v) }

// overhead makes each run pause, to overlap two concurrent calls.
func (f *fakeCLI) overhead(ms int) { f.write("sleep.txt", strconv.Itoa(ms)) }

// failOn makes the fake exit non-zero on run, render and export.
func (f *fakeCLI) failOn(on bool) {
	if on {
		f.write("fail.txt", "1")
		return
	}
	_ = os.Remove(filepath.Join(f.dir, "fail.txt"))
}

// design writes a design file and ages its mtime, so the fingerprint is stable.
func (f *fakeCLI) design(rel, content string) string {
	f.t.Helper()
	path := filepath.Join(f.work, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.rewrite(path, content)
	return path
}

// rewrite replaces a design file and ages its mtime.
func (f *fakeCLI) rewrite(path, content string) {
	f.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		f.t.Fatal(err)
	}
}

// symlinkCLI links another CLI path to the helper script.
func (f *fakeCLI) symlinkCLI(path string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(f.cli, path); err != nil {
		f.t.Fatal(err)
	}
}

// argv returns every recorded invocation.
func (f *fakeCLI) argv() [][]string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "argv.log"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		f.t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			f.t.Fatalf("argv line %q: %v", line, err)
		}
		out = append(out, args)
	}
	return out
}

// count is how many invocations ran the given subcommand.
func (f *fakeCLI) count(sub string) int {
	n := 0
	for _, args := range f.argv() {
		if len(args) > 0 && args[0] == sub {
			n++
		}
	}
	return n
}

// flagValues returns the values of a repeated flag in one invocation.
func flagValues(args []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}

// Fixtures used across the tests.
const (
	pagesHome = `[{"id":10,"name":"Home","divider":false,"children":3},{"id":11,"name":"Divider","divider":true}]`
	nodeHome  = `{"id":10,"name":"Home","type":"PAGE","background":"#E4E7EC","children":[` +
		`{"id":101,"name":"A","type":"FRAME","absoluteBounds":{"x":100,"y":50,"width":200,"height":100},"childCount":0,"visible":true},` +
		`{"id":102,"name":"B","type":"FRAME","absoluteBounds":{"x":0,"y":0,"width":1000,"height":1000},"childCount":3,"visible":false},` +
		`{"id":103,"name":"C","type":"RECTANGLE","absoluteBounds":{"x":300,"y":300,"width":100,"height":100}}]}`
)

// homeFixtures writes the Home page and its node.
func (f *fakeCLI) homeFixtures() {
	f.pages(pagesHome)
	f.node(10, nodeHome)
}

func TestInfoPagesOriginAndLayers(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	info, err := svc.Info(context.Background(), abs)
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "file" || info.LivePort != 0 || info.Unsaved {
		t.Errorf("source/port/unsaved = %q/%d/%v, want file/0/false", info.Source, info.LivePort, info.Unsaved)
	}
	if info.Path != abs {
		t.Errorf("path = %q, want %q", info.Path, abs)
	}
	if !strings.HasPrefix(info.Fingerprint, "f-") {
		t.Errorf("fingerprint = %q, want an f- prefix", info.Fingerprint)
	}
	if info.CLI == nil || info.CLI.Path != f.cli {
		t.Errorf("cli = %+v, want path %q", info.CLI, f.cli)
	}
	if len(info.Pages) != 2 {
		t.Fatalf("pages = %d, want 2", len(info.Pages))
	}

	home := info.Pages[0]
	if home.Name != "Home" || home.Divider {
		t.Errorf("page 0 = %+v, want Home, not a divider", home)
	}
	if home.Background != "#E4E7EC" {
		t.Errorf("background = %q, want #E4E7EC", home.Background)
	}
	if home.Origin != (Point{X: 100, Y: 50}) {
		t.Errorf("origin = %+v, want {100 50}", home.Origin)
	}
	if home.Size != (Size{Width: 300, Height: 350}) {
		t.Errorf("size = %+v, want {300 350}", home.Size)
	}
	if len(home.Layers) != 3 {
		t.Fatalf("layers = %d, want 3", len(home.Layers))
	}
	if home.Layers[1].Visible {
		t.Error("layer B is visible, want hidden")
	}
	if home.Layers[2].Visible == false {
		t.Error("layer C with no visible key should default to visible")
	}
	if home.Layers[2].ChildCount != 0 {
		t.Errorf("layer C child_count = %d, want 0", home.Layers[2].ChildCount)
	}
	if home.Layers[1].ChildCount != 3 {
		t.Errorf("layer B child_count = %d, want 3", home.Layers[1].ChildCount)
	}

	if !info.Pages[1].Divider || info.Pages[1].Layers == nil {
		t.Errorf("page 1 = %+v, want a divider with empty layers", info.Pages[1])
	}
}

func TestInfoNoVisibleLayers(t *testing.T) {
	f := newFakeCLI(t)
	f.pages(`[{"id":20,"name":"Empty","divider":false,"children":1}]`)
	f.node(20, `{"id":20,"name":"Empty","type":"PAGE","children":[{"id":201,"name":"H","type":"FRAME","absoluteBounds":{"x":5,"y":5,"width":10,"height":10},"visible":false}]}`)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	info, err := svc.Info(context.Background(), abs)
	if err != nil {
		t.Fatal(err)
	}
	page := info.Pages[0]
	if page.Origin != (Point{}) || page.Size != (Size{}) {
		t.Errorf("origin/size = %+v/%+v, want zero", page.Origin, page.Size)
	}
}

func TestInfoChildCountFromInlinedChildren(t *testing.T) {
	f := newFakeCLI(t)
	f.pages(`[{"id":30,"name":"P","divider":false,"children":1}]`)
	f.node(30, `{"id":30,"name":"P","type":"PAGE","children":[{"id":301,"name":"G","type":"GROUP","absoluteBounds":{"x":0,"y":0,"width":1,"height":1},"children":[{},{}]}]}`)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	info, err := svc.Info(context.Background(), abs)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Pages[0].Layers[0].ChildCount; got != 2 {
		t.Errorf("child_count = %d, want 2 from the inlined children", got)
	}
}

func TestInfoMissingFile(t *testing.T) {
	f := newFakeCLI(t)
	f.pages("[]")
	svc := f.service(nil)
	_, err := svc.Info(context.Background(), filepath.Join(f.work, "missing.figcraft"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Info(missing) = %v, want ErrNotFound", err)
	}
}

func TestCLILocationOrder(t *testing.T) {
	f := newFakeCLI(t)
	f.pages("[]")
	abs := f.design("a.figcraft", "zip")
	ctx := context.Background()

	t.Run("setting", func(t *testing.T) {
		svc := f.service(nil)
		info, err := svc.Info(ctx, abs)
		if err != nil {
			t.Fatal(err)
		}
		if info.CLI == nil || info.CLI.Path != f.cli {
			t.Fatalf("cli path = %+v, want the setting %q", info.CLI, f.cli)
		}
	})

	t.Run("home", func(t *testing.T) {
		home := t.TempDir()
		homeCLI := filepath.Join(home, ".local", "share", "figcraft-cli", "figcraft-cli")
		f.symlinkCLI(homeCLI)
		svc := f.service(func(o *Options) {
			o.HomeDir = home
			o.Settings = func() Settings { return Settings{CLIPath: filepath.Join(home, "nope")} }
		})
		info, err := svc.Info(ctx, abs)
		if err != nil {
			t.Fatal(err)
		}
		if info.CLI == nil || info.CLI.Path != homeCLI {
			t.Fatalf("cli path = %+v, want the home install %q", info.CLI, homeCLI)
		}
	})

	t.Run("path", func(t *testing.T) {
		pathCLI := filepath.Join(t.TempDir(), "figcraft-cli")
		f.symlinkCLI(pathCLI)
		svc := f.service(func(o *Options) {
			o.HomeDir = t.TempDir()
			o.Settings = func() Settings { return Settings{} }
			o.LookPath = func(string) (string, error) { return pathCLI, nil }
		})
		info, err := svc.Info(ctx, abs)
		if err != nil {
			t.Fatal(err)
		}
		if info.CLI == nil || info.CLI.Path != pathCLI {
			t.Fatalf("cli path = %+v, want the PATH result %q", info.CLI, pathCLI)
		}
	})
}

func TestCLIMissing(t *testing.T) {
	f := newFakeCLI(t)
	abs := f.design("a.figcraft", "zip")
	home := t.TempDir()
	svc := f.service(func(o *Options) {
		o.HomeDir = home
		o.Settings = func() Settings { return Settings{CLIPath: filepath.Join(home, "nope")} }
	})

	_, err := svc.Info(context.Background(), abs)
	var missing *CLIMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("Info = %v, want *CLIMissingError", err)
	}
	homeCLI := filepath.Join(home, ".local", "share", "figcraft-cli", "figcraft-cli")
	if len(missing.Tried) != 3 {
		t.Errorf("tried = %v, want 3 paths", missing.Tried)
	}
	if !strings.Contains(err.Error(), homeCLI) {
		t.Errorf("error %q does not mention the home path", err)
	}
}

func TestVersionParseAndWarning(t *testing.T) {
	cases := []struct {
		name     string
		cli      string
		app      string
		wantVer  string
		wantWarn bool
		wantApp  bool
	}{
		{"older cli", "figcraft-cli 0.18.1", "0.20.0", "0.18.1", true, true},
		{"same", "figcraft-cli 0.18.1", "0.18.1", "0.18.1", false, true},
		{"newer cli", "figcraft-cli 0.20.0", "0.18.1", "0.20.0", false, true},
		{"no app", "figcraft-cli 0.18.1", "", "0.18.1", false, false},
		{"cli unparseable", "figcraft-cli 0.18", "0.20.0", "0.18", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCLI(t)
			f.pages("[]")
			f.version(tc.cli)
			app := tc.app
			svc := f.service(func(o *Options) { o.AppVersion = func() string { return app } })
			abs := f.design("a.figcraft", "zip")

			info, err := svc.Info(context.Background(), abs)
			if err != nil {
				t.Fatal(err)
			}
			if info.CLI == nil || info.CLI.Version != tc.wantVer {
				t.Fatalf("cli = %+v, want version %q", info.CLI, tc.wantVer)
			}
			if tc.wantApp && (info.App == nil || info.App.Version != tc.app) {
				t.Errorf("app = %+v, want %q", info.App, tc.app)
			}
			if !tc.wantApp && info.App != nil {
				t.Errorf("app = %+v, want nil", info.App)
			}
			warned := false
			for _, w := range info.Warnings {
				if w == "cli_older_than_app" {
					warned = true
				}
			}
			if warned != tc.wantWarn {
				t.Errorf("warnings = %v, want cli_older_than_app=%v", info.Warnings, tc.wantWarn)
			}
		})
	}
}

func TestAutomationRootsAndRunDirCleanup(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	if _, err := svc.Info(context.Background(), abs); err != nil {
		t.Fatal(err)
	}

	runs := 0
	for _, args := range f.argv() {
		if len(args) == 0 || args[0] != "run" {
			continue
		}
		runs++
		read := flagValues(args, "--automation-read-root")
		write := flagValues(args, "--automation-write-root")
		if len(read) != 2 || len(write) != 1 {
			t.Fatalf("roots = read %v, write %v, want 2 and 1", read, write)
		}
		if read[0] != filepath.Dir(abs) {
			t.Errorf("read root 0 = %q, want the file folder %q", read[0], filepath.Dir(abs))
		}
		if !strings.HasPrefix(filepath.Base(read[1]), "lana-figcraft-") || write[0] != read[1] {
			t.Errorf("run temp roots = read %q, write %q, want the same lana-figcraft- folder", read[1], write[0])
		}
		if _, err := os.Stat(read[1]); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("run temp folder %q still exists", read[1])
		}
	}
	if runs != 2 {
		t.Errorf("runs = %d, want 2", runs)
	}

	entries, err := os.ReadDir(f.tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp root still holds %v", entries)
	}
}

func TestClampScale(t *testing.T) {
	cases := []struct {
		in, longest, want float64
	}{
		{0, 200, 1},
		{-3, 200, 1},
		{0.05, 200, 0.1},
		{5, 200, 4},
		{2, 1000, 2},
		{4, 10000, 8192.0 / 10000.0},
		{1, 0, 1},
	}
	for _, tc := range cases {
		if got := clampScale(tc.in, tc.longest); got != tc.want {
			t.Errorf("clampScale(%v, %v) = %v, want %v", tc.in, tc.longest, got, tc.want)
		}
	}
}

func TestStableFileWait(t *testing.T) {
	f := newFakeCLI(t)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")
	ctx := context.Background()

	info, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("f-%d-%d", info.ModTime().UnixNano(), info.Size())
	start := time.Now()
	fp, err := svc.fileFingerprint(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if fp != want {
		t.Errorf("fingerprint = %q, want %q", fp, want)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("old file waited %v, want the fast path", elapsed)
	}

	// A fresh file makes the Service poll until it settles.
	now := time.Now()
	if err := os.Chtimes(abs, now, now); err != nil {
		t.Fatal(err)
	}

	oldWindow, oldPoll, oldMax := stableWindow, stablePoll, stableMax
	stableWindow, stablePoll, stableMax = 100*time.Millisecond, 20*time.Millisecond, time.Second
	t.Cleanup(func() { stableWindow, stablePoll, stableMax = oldWindow, oldPoll, oldMax })

	start = time.Now()
	fp, err = svc.fileFingerprint(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("fresh file waited %v, want at least the stable window", elapsed)
	}
	info, err = os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("f-%d-%d", info.ModTime().UnixNano(), info.Size()); fp != want {
		t.Errorf("fingerprint = %q, want the settled %q", fp, want)
	}
}

func TestStableFileNowHook(t *testing.T) {
	f := newFakeCLI(t)
	abs := f.design("a.figcraft", "zip")
	now := time.Now()
	if err := os.Chtimes(abs, now, now); err != nil {
		t.Fatal(err)
	}
	svc := f.service(func(o *Options) { o.Now = func() time.Time { return time.Now().Add(time.Hour) } })

	start := time.Now()
	if _, err := svc.fileFingerprint(context.Background(), abs); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("waited %v, want the Now hook to skip the wait", elapsed)
	}
}

func TestStaleFallback(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")
	ctx := context.Background()

	first, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stale {
		t.Fatal("first Info is stale, want fresh")
	}

	f.failOn(true)
	f.rewrite(abs, "zip-with-a-different-size")

	second, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatalf("Info after a failing run = %v, want the last good result", err)
	}
	if !second.Stale {
		t.Error("stale = false, want true")
	}
	if len(second.Pages) != len(first.Pages) {
		t.Errorf("pages = %d, want the last good %d", len(second.Pages), len(first.Pages))
	}
}

func TestCacheHitAndMiss(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")
	ctx := context.Background()

	if _, err := svc.Info(ctx, abs); err != nil {
		t.Fatal(err)
	}
	if got := f.count("run"); got != 2 {
		t.Fatalf("runs after the first Info = %d, want 2", got)
	}
	if _, err := svc.Info(ctx, abs); err != nil {
		t.Fatal(err)
	}
	if got := f.count("run"); got != 2 {
		t.Errorf("runs after a second Info = %d, want 2 (a cache hit)", got)
	}

	f.rewrite(abs, "zip-with-a-different-size")
	if _, err := svc.Info(ctx, abs); err != nil {
		t.Fatal(err)
	}
	if got := f.count("run"); got != 4 {
		t.Errorf("runs after the file changed = %d, want 4 (a miss)", got)
	}
}

func TestSingleflightSharesRun(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	f.overhead(300)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]Info, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.Info(ctx, abs)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Info %d = %v", i, err)
		}
	}
	if results[0].Fingerprint != results[1].Fingerprint {
		t.Errorf("fingerprints differ: %q vs %q", results[0].Fingerprint, results[1].Fingerprint)
	}
	if got := f.count("run"); got != 2 {
		t.Errorf("runs = %d, want 2 (one shared pair)", got)
	}
}

func TestLayers(t *testing.T) {
	f := newFakeCLI(t)
	f.pages("[]")
	f.node(101, `{"id":101,"name":"A","type":"FRAME","absoluteBounds":{"x":0,"y":0,"width":10,"height":10},"childCount":1,"children":[{"id":111,"name":"Child","type":"TEXT","absoluteBounds":{"x":1,"y":1,"width":2,"height":2},"visible":true}]}`)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	ch, err := svc.Layers(context.Background(), abs, 101)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Node != 101 || !strings.HasPrefix(ch.Fingerprint, "f-") {
		t.Errorf("node/fingerprint = %d/%q", ch.Node, ch.Fingerprint)
	}
	if len(ch.Children) != 1 || ch.Children[0].ID != 111 || ch.Children[0].Type != "TEXT" {
		t.Fatalf("children = %+v, want one TEXT node 111", ch.Children)
	}
}

func TestLayersNotFound(t *testing.T) {
	f := newFakeCLI(t)
	f.pages("[]")
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	if _, err := svc.Layers(context.Background(), abs, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Layers(999) = %v, want ErrNotFound", err)
	}
}

func TestRenderPage(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")
	ctx := context.Background()

	render, err := svc.RenderPage(ctx, abs, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(render.PNG)); err != nil {
		t.Fatalf("render is not a valid PNG: %v", err)
	}
	if render.Scale != 4 {
		t.Errorf("scale = %v, want the clamp to 4", render.Scale)
	}
	if render.OriginX != 100 || render.OriginY != 50 {
		t.Errorf("origin = %v/%v, want 100/50", render.OriginX, render.OriginY)
	}
	if !strings.HasPrefix(render.Fingerprint, "f-") {
		t.Errorf("fingerprint = %q, want an f- prefix", render.Fingerprint)
	}

	var renderArgs []string
	for _, args := range f.argv() {
		if len(args) > 0 && args[0] == "render" {
			renderArgs = args
		}
	}
	if got := flagValues(renderArgs, "--page"); len(got) != 1 || got[0] != "Home" {
		t.Errorf("--page = %v, want [Home]", got)
	}
	if got := flagValues(renderArgs, "--scale"); len(got) != 1 || got[0] != "4" {
		t.Errorf("--scale = %v, want [4]", got)
	}
}

func TestRenderPageNotFound(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	if _, err := svc.RenderPage(context.Background(), abs, 999, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RenderPage(999) = %v, want ErrNotFound", err)
	}
}

func TestRenderNode(t *testing.T) {
	f := newFakeCLI(t)
	f.pages("[]")
	f.node(101, `{"id":101,"name":"A","type":"FRAME","absoluteBounds":{"x":100,"y":50,"width":200,"height":100},"childCount":0,"visible":true}`)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	render, err := svc.RenderNode(context.Background(), abs, 101, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(render.PNG)); err != nil {
		t.Fatalf("render is not a valid PNG: %v", err)
	}
	if render.Scale != 2 || render.OriginX != 100 || render.OriginY != 50 {
		t.Errorf("scale/origin = %v/%v/%v, want 2/100/50", render.Scale, render.OriginX, render.OriginY)
	}

	var exportArgs []string
	for _, args := range f.argv() {
		if len(args) > 0 && args[0] == "export" {
			exportArgs = args
		}
	}
	if got := flagValues(exportArgs, "--node"); len(got) != 1 || got[0] != "101" {
		t.Errorf("--node = %v, want [101]", got)
	}
	if got := flagValues(exportArgs, "--scale"); len(got) != 1 || got[0] != "2" {
		t.Errorf("--scale = %v, want [2]", got)
	}
}

func TestRunTimeout(t *testing.T) {
	old := cliTimeout
	cliTimeout = 100 * time.Millisecond
	t.Cleanup(func() { cliTimeout = old })

	f := newFakeCLI(t)
	f.homeFixtures()
	f.overhead(500)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	_, err := svc.Info(context.Background(), abs)
	var failed *FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Info = %v, want *FailedError", err)
	}
	if failed.Code != "figcraft_timeout" {
		t.Errorf("code = %q, want figcraft_timeout", failed.Code)
	}
}

func TestRunFailureMessage(t *testing.T) {
	f := newFakeCLI(t)
	f.pages("[]")
	f.failOn(true)
	svc := f.service(nil)
	abs := f.design("a.figcraft", "zip")

	_, err := svc.Info(context.Background(), abs)
	var failed *FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Info = %v, want *FailedError", err)
	}
	if failed.Code != "figcraft_failed" || failed.Message != "figcraft: boom" {
		t.Errorf("failure = %+v, want figcraft_failed/figcraft: boom", failed)
	}
}

func TestDefaultSettings(t *testing.T) {
	got := DefaultSettings()
	if !got.Live {
		t.Error("Live = false, want true")
	}
	if got.CLIPath != "" {
		t.Errorf("CLIPath = %q, want empty", got.CLIPath)
	}
	if got.LivePorts == nil || len(got.LivePorts) != 0 {
		t.Errorf("LivePorts = %v, want an empty non-nil slice", got.LivePorts)
	}
}
