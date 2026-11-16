//go:build !darwin && !linux

package workbench

import "github.com/ridhomujizat/lana-harness/internal/workbench/agentdetect"

// foreground is unavailable off macOS and Linux, so the started program's
// name is kept (REQ-WB-013).
func foreground(fd uintptr, shellPID int) (program, cwd string, ok bool) { return "", "", false }

// foregroundJob is unavailable off macOS and Linux (spec 55 A3).
func foregroundJob(fd uintptr) (agentdetect.Job, bool) { return agentdetect.Job{}, false }
