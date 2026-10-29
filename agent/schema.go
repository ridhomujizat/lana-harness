package agent

import (
	"encoding/json"
	"fmt"
)

func validateInput(schema json.RawMessage, input json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var spec struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
		Additional *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema, &spec); err != nil {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(input, &value); err != nil {
		return fmt.Errorf("input is not an object")
	}
	for _, key := range spec.Required {
		if _, ok := value[key]; !ok {
			return fmt.Errorf("field '%s' is required", key)
		}
	}
	if spec.Additional != nil && !*spec.Additional {
		for key := range value {
			if _, ok := spec.Properties[key]; !ok {
				return fmt.Errorf("field '%s' is unknown", key)
			}
		}
	}
	for key, raw := range spec.Properties {
		item, ok := value[key]
		if !ok {
			continue
		}
		var prop struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			continue
		}
		if prop.Type != "" && !jsonTypeMatches(prop.Type, item) {
			return fmt.Errorf("field '%s' must be %s", key, prop.Type)
		}
	}
	return nil
}

func jsonTypeMatches(want string, value any) bool {
	switch want {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == float64(int64(n))
	case "number":
		_, ok := value.(float64)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return true
	}
}
