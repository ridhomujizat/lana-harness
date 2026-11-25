package agent

import (
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
)

// The bounds of the job notice text: the command is cut to its first 200 runes
// and the tail to its last 2000 runes (REQ-JBN-002).
const (
	jobNoticeCommandMax = 200
	jobNoticeTailMax    = 2000
)

// JobNoticeText builds the <task-notification> text the model sees when a
// background job ends (REQ-JBN-002). runtime is the job's age label, computed
// by the caller from its start and end times. The command is cut to its first
// 200 runes and the tail to its last 2000 runes, rune-safe.
func JobNoticeText(n core.JobNotice, runtime string) string {
	status := "killed"
	if n.Status != "killed" {
		status = fmt.Sprintf("exited %d", n.ExitCode)
	} else if n.StoppedBy == "owner" {
		status = "killed (stopped by the owner from the web)"
	}
	var b strings.Builder
	b.WriteString("<task-notification>\n")
	b.WriteString("job: " + n.ID + "\n")
	b.WriteString("command: " + firstRunes(n.Command, jobNoticeCommandMax) + "\n")
	b.WriteString("status: " + status + "\n")
	b.WriteString("runtime: " + runtime + "\n")
	b.WriteString("output tail:\n")
	b.WriteString(lastRunes(n.Tail, jobNoticeTailMax) + "\n")
	b.WriteString("</task-notification>\n")
	b.WriteString("Background job " + n.ID + " ended. Read more with job_output if needed, then tell the owner what happened and whether anything must be restarted.")
	return b.String()
}

// firstRunes returns the first n runes of s.
func firstRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// lastRunes returns the last n runes of s.
func lastRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
