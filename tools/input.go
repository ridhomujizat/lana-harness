package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ridhomujizat/lana-harness/core"
)

func decodeInput(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return fmt.Errorf("input kosong")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("input berisi data tambahan")
	}
	return nil
}

func inputErrorOutput(err error) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusError, Output: "input tidak valid: " + err.Error()}, nil
}
