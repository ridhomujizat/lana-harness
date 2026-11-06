package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func decodeInput(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return fmt.Errorf("input is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("input contains extra data")
	}
	return nil
}

func inputErrorOutput(err error) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusError, Output: "invalid input: " + err.Error()}, nil
}
