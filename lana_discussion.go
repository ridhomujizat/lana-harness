package lana

import "context"

// discussionShare is the runtime side of core.DiscussionShare (spec 47
// REQ-RDS-031, REQ-RDS-033).
type discussionShare struct {
	rt   *Runtime
	post DiscussionPostFunc
}

// DiscussionSource is the assistant/message source of a discussion summary
// posted into a member's room; SourceName is the team.
const DiscussionSource = "discussion"

func (d *discussionShare) PostMessage(ctx context.Context, sessionID, name, text string) {
	if d.post != nil {
		d.post(ctx, sessionID, name, text)
	}
}

func (d *discussionShare) ShareSummary(ctx context.Context, team, member, text string) error {
	_, err := d.rt.PostAgentMessage(ctx, member, text, DiscussionSource, team)
	return err
}
