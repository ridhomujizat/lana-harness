package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn and returns what it wrote to stderr.
func captureStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	code := fn()
	_ = w.Close()
	os.Stderr = old
	data, _ := io.ReadAll(r)
	return string(data), code
}

// appendConfig appends TOML to the home config and fails the test if it is invalid.
func appendConfig(t *testing.T, home, text string) {
	t.Helper()
	path := filepath.Join(home, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimRight(string(data), "\n") + "\n\n" + text
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPList(t *testing.T) {
	home := initHome(t)
	appendConfig(t, home, "[mcp.local]\ncommand = \"srv\"\nargs = [\"--x\", \"y\"]\n\n[mcp.remote]\nurl = \"https://example.com/mcp\"\n\n[agents.dev]\nprovider = \"deepseek\"\nmodel = \"deepseek-chat\"\nmcp = [\"local\"]\n")

	out, code := captureStdout(t, func() int { return cmdMCP([]string{"list"}) })
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	want := "local  stdio  srv --x y  agents=dev\nremote  http  https://example.com/mcp  agents=-\n"
	if out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
}

func TestMCPListNoServers(t *testing.T) {
	initHome(t)
	out, code := captureStdout(t, func() int { return cmdMCP([]string{"list"}) })
	if code != 0 || out != "no MCP servers configured\n" {
		t.Fatalf("exit = %d, stdout = %q", code, out)
	}
}

func TestMCPToolsStdio(t *testing.T) {
	home := initHome(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	appendConfig(t, home, fmt.Sprintf("[mcp.test]\ncommand = %q\nargs = [\"-test.run=^$\"]\nenv = { LANA_MCP_TEST_SERVER = \"1\" }\n", exe))

	out, code := captureStdout(t, func() int { return cmdMCP([]string{"tools", "test"}) })
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, out)
	}
	found := false
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, "mcp__test__echo  read  ") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stdout = %q", out)
	}
}

func TestMCPToolsUnavailable(t *testing.T) {
	home := initHome(t)
	appendConfig(t, home, "[mcp.bad]\ncommand = \"definitely-not-a-command-xyz\"\n")

	stderr, code := captureStderr(t, func() int { return cmdMCP([]string{"tools", "bad"}) })
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "error: mcp bad: command not found") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMCPToolsUnknownServer(t *testing.T) {
	initHome(t)
	stderr, code := captureStderr(t, func() int { return cmdMCP([]string{"tools", "ghost"}) })
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stderr != "error: unknown MCP server \"ghost\"\n" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestMCPUsage(t *testing.T) {
	initHome(t)
	stderr, code := captureStderr(t, func() int { return cmdMCP([]string{"tools"}) })
	if code != 2 || !strings.Contains(stderr, "use lana mcp tools <server>") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}
