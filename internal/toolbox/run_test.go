package toolbox

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// runService stores a one-command package, writes its script and returns the
// service.
func runService(t *testing.T, script, command string, opts ...Option) *Service {
	t.Helper()
	svc := testService(t, opts...)
	addManifest(t, svc, `
id = "faketool"
name = "Fake"

[detect]
binary = "faketool"

[[install]]
command = "true"

`+command)
	writeScript(t, svc.Store(), "faketool", "faketool", script)
	return svc
}

// TestRunRendersArgs replaces every {{field}} of the argv template.
func TestRunRendersArgs(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho \"$@\"\n", `
[[commands]]
name = "run"
risk = "read"
args = ["fetch", "--format", "{{format}}", "{{url}}"]

[commands.input]
url = { type = "string", required = true }
format = { type = "string", required = true }
`)
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{
		"url": "http://example.com", "format": "text",
	}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(result.Output); got != "fetch --format text http://example.com" {
		t.Fatalf("output=%q", got)
	}
	if result.ExitCode != 0 || result.DurationMs < 0 {
		t.Fatalf("result=%+v", result)
	}
}

// TestRunDropsOptionalArg drops an arg that is only an absent optional field.
func TestRunDropsOptionalArg(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho \"$@\"\n", `
[[commands]]
name = "run"
risk = "read"
args = ["run", "{{debug}}"]

[commands.input]
debug = { type = "string" }
`)
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(result.Output); got != "run" {
		t.Fatalf("output=%q want run", got)
	}
	result, err = svc.Run(context.Background(), "faketool", "run", map[string]any{"debug": "yes"}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(result.Output); got != "run yes" {
		t.Fatalf("output=%q want 'run yes'", got)
	}
}

// TestRunRequiredInput errors on a missing required field.
func TestRunRequiredInput(t *testing.T) {
	svc := runService(t, "#!/bin/sh\ntrue\n", `
[[commands]]
name = "run"
risk = "read"
args = ["{{url}}"]

[commands.input]
url = { type = "string", required = true }
`)
	if _, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, ""); err == nil {
		t.Fatal("want a required-input error")
	}
}

// TestRunEnumInput errors on a value outside the enum.
func TestRunEnumInput(t *testing.T) {
	svc := runService(t, "#!/bin/sh\ntrue\n", `
[[commands]]
name = "run"
risk = "read"
args = ["{{format}}"]

[commands.input]
format = { type = "string", enum = ["a", "b"] }
`)
	if _, err := svc.Run(context.Background(), "faketool", "run", map[string]any{"format": "c"}, ""); err == nil {
		t.Fatal("want an enum error")
	}
	if _, err := svc.Run(context.Background(), "faketool", "run", map[string]any{"format": "a"}, ""); err != nil {
		t.Fatalf("valid enum value rejected: %v", err)
	}
}

// TestRunExitCode reports a nonzero exit as an error.
func TestRunExitCode(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho boom\nexit 3\n", `
[[commands]]
name = "run"
risk = "read"
`)
	_, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err == nil || !strings.Contains(err.Error(), "exit code 3") {
		t.Fatalf("err=%v want exit code 3", err)
	}
}

// TestRunJSONOutput requires a parseable output for output = "json".
func TestRunJSONOutput(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho '{\"ok\":true}'\n", `
[[commands]]
name = "run"
risk = "read"
output = "json"
`)
	if _, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, ""); err != nil {
		t.Fatalf("valid json rejected: %v", err)
	}

	bad := runService(t, "#!/bin/sh\necho not-json\n", `
[[commands]]
name = "run"
risk = "read"
output = "json"
`)
	if _, err := bad.Run(context.Background(), "faketool", "run", map[string]any{}, ""); err == nil {
		t.Fatal("want a JSON parse error")
	}
}

// TestRunJSONOutputTooLarge names the output cap when a JSON output was cut off.
func TestRunJSONOutputTooLarge(t *testing.T) {
	svc := runService(t, "#!/bin/sh\nprintf '{\"pad\":\"'\nhead -c 70000 /dev/zero | tr '\\0' a\nprintf '\"}'\n", `
[[commands]]
name = "run"
risk = "read"
output = "json"
`)
	_, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err == nil || !strings.Contains(err.Error(), "larger than 64 KiB") {
		t.Fatalf("err=%v want the 64 KiB cap named", err)
	}
}

// TestRunSecretEnv resolves a secret:<NAME> env value.
func TestRunSecretEnv(t *testing.T) {
	svc := runService(t, "#!/bin/sh\nprintf '%s' \"$MY_KEY\"\n", `
[[commands]]
name = "run"
risk = "read"

[env]
MY_KEY = "secret:TOKEN"
`, WithSecrets(func(name string) (string, bool) {
		if name == "TOKEN" {
			return "s3cr3t", true
		}
		return "", false
	}))
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Output != "s3cr3t" {
		t.Fatalf("output=%q want s3cr3t", result.Output)
	}
}

// TestRunMissingSecret errors when the resolver does not know the secret.
func TestRunMissingSecret(t *testing.T) {
	svc := runService(t, "#!/bin/sh\ntrue\n", `
[[commands]]
name = "run"
risk = "read"

[env]
MY_KEY = "secret:TOKEN"
`)
	if _, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, ""); err == nil {
		t.Fatal("want a missing-secret error")
	}
}

// TestRunTimeout stops a command that runs past its timeout.
func TestRunTimeout(t *testing.T) {
	svc := runService(t, "#!/bin/sh\nsleep 5\n", `
[[commands]]
name = "run"
risk = "read"
timeout_secs = 1
`)
	_, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err=%v want a timeout", err)
	}
}

// filesCommand is a files-mode command whose script gets the {{out}} directory
// as its first argument (REQ-TB-012).
const filesCommand = `
[[commands]]
name = "run"
risk = "read"
args = ["{{out}}", "{{name}}"]
output_from = "files"

[commands.input]
name = { type = "string", default = "a" }
`

// TestRunOutputFromFilesSingle returns the content of the one written file
// and removes the {{out}} directory after the run (REQ-TB-012).
func TestRunOutputFromFilesSingle(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho noise\nprintf '%s' \"$1\" > \"$1/$2.txt\"\n", filesCommand)
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	outDir := strings.TrimSpace(result.Output)
	if outDir == "" || strings.Contains(result.Output, "noise") || strings.Contains(result.Output, "==") {
		t.Fatalf("output=%q want only the file content", result.Output)
	}
	if _, err := os.Stat(outDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("out dir %s still exists after the run: %v", outDir, err)
	}
}

// TestRunOutputFromFilesMultiple joins the written files sorted by name, each
// under a "== <name>" line (REQ-TB-012).
func TestRunOutputFromFilesMultiple(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho two > \"$1/b.txt\"\nmkdir \"$1/sub\"\necho three > \"$1/sub/c.txt\"\necho one > \"$1/a.txt\"\n", filesCommand)
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "== a.txt\none\n\n== b.txt\ntwo\n\n== sub/c.txt\nthree\n"
	if result.Output != want {
		t.Fatalf("output=%q want %q", result.Output, want)
	}
}

// TestRunOutputFromFilesNone errors with the process output when no file was
// written (REQ-TB-012).
func TestRunOutputFromFilesNone(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho 'There are no subtitles'\n", filesCommand)
	_, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err == nil || !strings.Contains(err.Error(), "run wrote no output file") || !strings.Contains(err.Error(), "There are no subtitles") {
		t.Fatalf("err=%v want a no-output-file error with the process output", err)
	}
}

// TestRunOutputFromFilesExitCode reports a nonzero exit as an error even when
// a file was written (REQ-TB-012).
func TestRunOutputFromFilesExitCode(t *testing.T) {
	svc := runService(t, "#!/bin/sh\necho partial > \"$1/a.txt\"\necho 'HTTP Error 429'\nexit 1\n", filesCommand)
	_, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err == nil || !strings.Contains(err.Error(), "exit code 1") || !strings.Contains(err.Error(), "HTTP Error 429") {
		t.Fatalf("err=%v want exit code 1 with the process output", err)
	}
}

// TestRunOutputFromFilesTooLarge cuts the file output at 256 KiB with a note
// (REQ-TB-012).
func TestRunOutputFromFilesTooLarge(t *testing.T) {
	svc := runService(t, "#!/bin/sh\nhead -c 300000 /dev/zero | tr '\\0' a > \"$1/big.txt\"\n", filesCommand)
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasSuffix(result.Output, "\n…cut off at 256 KiB\n") {
		t.Fatalf("output tail=%q want the cut-off note", tail(result.Output, 40))
	}
	if got := strings.Count(strings.TrimSuffix(result.Output, "\n…cut off at 256 KiB\n"), "a"); got != maxFileOutput {
		t.Fatalf("kept %d bytes want %d", got, maxFileOutput)
	}
}

// TestRunOutputFromFilesSubtitles cleans the written files as a transcript
// (REQ-TB-012).
func TestRunOutputFromFilesSubtitles(t *testing.T) {
	svc := runService(t, "#!/bin/sh\nprintf 'WEBVTT\\n\\n00:00:01.000 --> 00:00:02.000\\nHello <c>there</c>\\n' > \"$1/v.en.vtt\"\n", `
[[commands]]
name = "run"
risk = "read"
args = ["{{out}}"]
output_from = "files"
transform = "subtitles"
`)
	result, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Output != "Hello there\n" {
		t.Fatalf("output=%q want the cleaned transcript", result.Output)
	}
}

// TestRunOutputFromFilesCleanupOnError removes the {{out}} directory when the
// command fails (REQ-TB-012).
func TestRunOutputFromFilesCleanupOnError(t *testing.T) {
	svc := runService(t, "#!/bin/sh\nprintf '%s' \"$1\"\nexit 2\n", filesCommand)
	_, err := svc.Run(context.Background(), "faketool", "run", map[string]any{}, "")
	if err == nil {
		t.Fatal("want an exit code error")
	}
	outDir := strings.TrimSpace(strings.TrimPrefix(err.Error(), "exit code 2: "))
	if outDir == "" {
		t.Fatalf("err=%v carries no out dir", err)
	}
	if _, statErr := os.Stat(outDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("out dir %s still exists after a failed run: %v", outDir, statErr)
	}
}
