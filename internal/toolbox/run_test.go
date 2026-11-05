package toolbox

import (
	"context"
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
