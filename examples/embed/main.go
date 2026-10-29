package main

import (
	"context"
	"encoding/json"
	"fmt"

	lana "github.com/ridhomujizat/lana-harness"
	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/providers/mock"
)

type echoTool struct{}

func (echoTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "echo", Description: "Returns the text.", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}
}
func (echoTool) Risk(json.RawMessage) core.Risk { return core.RiskRead }
func (echoTool) Call(ctx context.Context, tc core.ToolCtx, input json.RawMessage) (core.ToolOutput, error) {
	var value struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(input, &value); err != nil {
		return core.ToolOutput{Status: core.ToolStatusError, Output: err.Error()}, nil
	}
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: value.Text}, nil
}

func main() {
	text := "Hello from embed."
	stop := "end_turn"
	script := mock.Script{Responses: []mock.Response{{Events: []mock.Event{{Text: &text}, {Stop: &stop}}}}}
	layer := []byte("[providers.mock]\nkind = \"openai_compat\"\nbase_url = \"mock://local\"\n\n[agents.assistant]\nprovider = \"mock\"\nmodel = \"mock\"\ntools = [\"echo\"]\n\n[[routes]]\nchannel = \"library\"\nagent = \"assistant\"\n")
	rt, err := lana.New(context.Background(), lana.Options{
		Layers:    []config.Layer{{Name: "embed", TOML: layer}},
		Tools:     []core.Tool{echoTool{}},
		Providers: map[string]core.Provider{"mock": mock.New(script)},
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = rt.Close() }()
	result, err := rt.Run(context.Background(), lana.Request{Prompt: "hello"})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Text)
}
