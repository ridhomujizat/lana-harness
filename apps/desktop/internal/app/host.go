package app

import (
	"strings"
	"sync"
)

// HostInfo is what the host chip shows: the name of the machine the app
// runs on and, where the system tells it, the processor and the memory
// (spec 58, window design).
type HostInfo interface {
	// Name is the hostname, without the ".local" suffix.
	Name() string
	// Chip is the processor, shortened ("M4 Pro"); "" when unknown.
	Chip() string
	// MemoryGB is the memory in whole gigabytes, 0 when unknown.
	MemoryGB() int
}

// systemHost reads the machine from the system, once: the chip and the
// memory are system calls, and the chip is drawn in every frame.
type systemHost struct {
	once   sync.Once
	name   string
	chip   string
	memory int
}

func (h *systemHost) load() {
	h.name = platformHostName()
	h.chip = platformChip()
	h.memory = platformMemoryGB()
}

// Name is the hostname, without the ".local" suffix.
func (h *systemHost) Name() string {
	h.once.Do(h.load)
	return h.name
}

// Chip is the processor, where the system tells it.
func (h *systemHost) Chip() string {
	h.once.Do(h.load)
	return h.chip
}

// MemoryGB is the memory, where the system tells it.
func (h *systemHost) MemoryGB() int {
	h.once.Do(h.load)
	return h.memory
}

// hostNameFrom is the machine's name for the host chip, chosen from the
// candidates the platform offers in order: the first that answers with a
// non-empty name wins, and a platform that answers with nothing falls
// through to the next (spec 58, window design).
func hostNameFrom(candidates ...func() (string, error)) string {
	for _, candidate := range candidates {
		name, err := candidate()
		if err != nil {
			continue
		}
		if name = cleanHostName(name); name != "" {
			return name
		}
	}
	return ""
}

// cleanHostName is a system name as the chip shows it: without the
// whitespace and the ".local" suffix a system name may carry.
func cleanHostName(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ".local")
}
