package core

import "context"

// SentFile is one file the send_file tool hands to the FileSender port
// (REQ-AGF-003). Path is workspace-relative with forward slashes, so it is the
// path API-ATT-003 serves; AbsPath is where the file lives on disk.
type SentFile struct {
	Path      string
	AbsPath   string
	Name      string
	MediaType string
	Size      int64
	Caption   string
}

// FileSender appends the file/sent event of one sent file and delivers it to
// the session's channel (REQ-AGF-003, REQ-AGF-004). It is implemented by the
// file service; the turn fills ToolCtx.Files.
type FileSender interface {
	// SendFile records the event in the turn of tc and delivers the file to
	// the session's channel. The returned note is "" when the channel has
	// nothing to report, or a short message such as "Telegram delivery failed:
	// ..." when delivery failed. The event is written even then.
	SendFile(ctx context.Context, tc ToolCtx, f SentFile) (delivery string, err error)
}

// FileSendRequest is one file handed to the channel for delivery
// (REQ-AGF-004).
type FileSendRequest struct {
	SessionID string
	AgentName string
	File      SentFile
}

// FileSendOutcome is the channel's answer to a file delivery. Note is a short
// note for the tool output; it is empty when the channel delivered the file or
// has nothing to report.
type FileSendOutcome struct {
	Note string
}
