package calendar

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// icsPointLength is the length given to entries without an end.
const icsPointLength = 30 * time.Minute

// icsLineOctets is the RFC 5545 line length limit, CRLF excluded.
const icsLineOctets = 75

// WriteICS writes entries as an RFC 5545 calendar (API-CAL-011): one VEVENT
// per item occurrence and per workflow slot; run and Google entries are left
// out. UID is the entry key + "@lana". Timed entries use UTC instants (an
// entry without an end lasts 30 minutes); dated entries use VALUE=DATE with
// an exclusive end day. Skipped entries are STATUS:CANCELLED. Lines end in
// CRLF and fold at 75 octets.
func WriteICS(w io.Writer, entries []Entry, loc *time.Location, now time.Time) error {
	if loc == nil {
		loc = time.Local
	}
	var b strings.Builder
	line := func(s string) { writeFolded(&b, s) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//lana//calendar//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Lana")
	stamp := now.UTC().Format("20060102T150405Z")
	for _, e := range entries {
		if e.Source != SourceItem && e.Source != SourceWorkflowSlot {
			continue
		}
		line("BEGIN:VEVENT")
		line("UID:" + icsEscape(e.Key+"@lana"))
		line("DTSTAMP:" + stamp)
		if entryTimed(e, loc) {
			start := time.UnixMilli(e.Start)
			end := start.Add(icsPointLength)
			if e.End > e.Start {
				end = time.UnixMilli(e.End)
			}
			line("DTSTART:" + start.UTC().Format("20060102T150405Z"))
			line("DTEND:" + end.UTC().Format("20060102T150405Z"))
		} else {
			first, ok := parseDay(e.Date)
			if !ok {
				first = dayOf(time.UnixMilli(e.Start), loc)
			}
			last, ok := parseDay(e.EndDate)
			if !ok || last.before(first) {
				last = first
			}
			line("DTSTART;VALUE=DATE:" + icsDate(first))
			line("DTEND;VALUE=DATE:" + icsDate(last.add(1)))
		}
		line("SUMMARY:" + icsEscape(icsSummary(e)))
		if d := icsDescription(e); d != "" {
			line("DESCRIPTION:" + icsEscape(d))
		}
		if e.Status == StatusSkipped {
			line("STATUS:CANCELLED")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	_, err := io.WriteString(w, b.String())
	return err
}

func icsDate(d civilDay) string { return fmt.Sprintf("%04d%02d%02d", d.y, int(d.m), d.d) }

func icsSummary(e Entry) string {
	if e.Source == SourceWorkflowSlot {
		if e.Workflow != nil && e.Workflow.EveryMinutes > 0 {
			return fmt.Sprintf("%s (every %d min)", e.Title, e.Workflow.EveryMinutes)
		}
		return e.Title + " (workflow)"
	}
	return e.Title
}

// icsDescription summarizes the parts of an entry a calendar app cannot show:
// kind, action, workflow, author and state.
func icsDescription(e Entry) string {
	var parts []string
	if e.Source == SourceItem && e.Kind != KindEvent {
		parts = append(parts, "Kind: "+string(e.Kind))
	}
	if e.Kind == KindTask && e.Status != "" {
		parts = append(parts, "State: "+e.Status)
	}
	if a := e.Action; a != nil {
		switch a.Type {
		case ActionAgentPrompt:
			parts = append(parts, "Action: "+a.Agent+" runs \""+a.Prompt+"\"")
		case ActionRunWorkflow:
			parts = append(parts, "Action: run workflow "+a.WorkflowID)
		case ActionSendMessage:
			parts = append(parts, "Action: send to "+a.Target+" \""+a.Text+"\"")
		}
	}
	if w := e.Workflow; w != nil && e.Source == SourceWorkflowSlot {
		s := "Workflow " + w.Name
		if w.Guardian != "" {
			s += ", guardian " + w.Guardian
		}
		parts = append(parts, s)
	}
	if e.Moved {
		parts = append(parts, "Moved from its usual time")
	}
	if name := agentName(e.CreatedBy); name != "" {
		parts = append(parts, "Added by "+name)
	}
	return strings.Join(parts, "\n")
}

// icsEscape escapes a TEXT value: backslash, semicolon, comma and newlines.
func icsEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", "")
	return r.Replace(s)
}

// writeFolded writes one content line, folded at 75 octets with CRLF and a
// space, never splitting a UTF-8 sequence.
func writeFolded(b *strings.Builder, s string) {
	limit := icsLineOctets
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		limit = icsLineOctets - 1
	}
	b.WriteString(s)
	b.WriteString("\r\n")
}
