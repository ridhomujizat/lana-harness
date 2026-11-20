//go:build !darwin

package app

import "os"

// platformChip is empty where the app does not read the processor; the
// host chip shows the hostname alone there (spec 58, window design).
func platformChip() string { return "" }

// platformMemoryGB is zero where the app does not read the memory.
func platformMemoryGB() int { return 0 }

// platformHostName is the hostname where the app does not read the name
// the system keeps (spec 58, window design).
func platformHostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return cleanHostName(name)
}
