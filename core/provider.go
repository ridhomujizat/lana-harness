package core

import "fmt"

// ProviderError is a provider error.
type ProviderError struct {
	Class   string
	Status  int
	Message string
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Status != 0 {
		return fmt.Sprintf("provider error (%s, HTTP %d): %s", e.Class, e.Status, e.Message)
	}
	return fmt.Sprintf("provider error (%s): %s", e.Class, e.Message)
}

// Is makes errors.Is(err, ErrContextTooLong) return true when Class = context_length.
func (e *ProviderError) Is(target error) bool {
	return e != nil && target == ErrContextTooLong && e.Class == "context_length"
}
