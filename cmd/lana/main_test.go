package main

import (
	"context"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if os.Getenv("LANA_MCP_TEST_SERVER") == "1" {
		runMCPTestServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMCPTestServer is the subprocess side of the real stdio test in mcp_test.go.
func runMCPTestServer() {
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool(server, &sdk.Tool{
		Name:        "echo",
		Description: "Echo the given text.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
			"required":   []string{"text"},
		},
	}, func(_ context.Context, _ *sdk.CallToolRequest, in echoInput) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: in.Text}}}, nil, nil
	})
	_ = server.Run(context.Background(), &sdk.StdioTransport{})
}

type echoInput struct {
	Text string `json:"text"`
}

func TestVersionExit(t *testing.T) {
	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if code := run([]string{"-V"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestHelpExit(t *testing.T) {
	if code := run([]string{"--help"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}

func TestUnknownCommand(t *testing.T) {
	if code := run([]string{"no-such-command"}); code != 2 {
		t.Fatalf("exit=%d", code)
	}
}
