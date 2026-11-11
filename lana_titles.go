package lana

import (
	"context"
	"errors"

	"github.com/ridhomujizat/lana-harness/agent/titles"
	"github.com/ridhomujizat/lana-harness/core"
)

// Session title errors (spec 40).
var (
	// ErrTitleBusy is returned by RegenerateSessionTitle while a title pass
	// runs for the session (REQ-TTL-010).
	ErrTitleBusy = titles.ErrBusy
	// ErrNothingToTitle is returned by RegenerateSessionTitle when the
	// session has no owner message yet.
	ErrNothingToTitle = titles.ErrNothingToTitle
	// ErrInvalidTitle is returned by SetSessionTitle for a title longer than
	// 80 characters (REQ-TTL-008).
	ErrInvalidTitle = errors.New("title must be at most 80 characters")
	// ErrTitlesUnsupported is returned when the session store cannot store
	// titles.
	ErrTitlesUnsupported = titles.ErrNoTitleStore
)

// Fixed title prefixes of sessions lana starts on its own (REQ-TTL-007).
const (
	workflowTitlePrefix = "Workflow"
	calendarTitlePrefix = "Calendar"
	// workflowChatTitlePrefix titles a workflow's guardian chat session
	// (spec 51): "Workflow chat · <workflow name>".
	workflowChatTitlePrefix = "Workflow chat"
)

// startTitles installs the session title service on the agent registry
// (spec 40). Passes derive from the runtime context, so Close stops them.
func (rt *Runtime) startTitles() {
	rt.titles = titles.New(rt.ctx, rt.store, rt.coreCtx.LLM)
	rt.titles.LookupAgent = rt.agentConfig
	rt.registry.SetSessionTitler(rt.titles)
}

// SetSessionTitle renames a session (REQ-TTL-008, REQ-TTL-009). Whitespace
// runs become one space. A non-empty title is stored as the owner's and is
// never replaced by the title pass; an empty title clears the owner title and
// queues a fresh automatic one.
func (rt *Runtime) SetSessionTitle(ctx context.Context, id, title string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	normalized, ok := titles.OwnerTitle(title)
	if !ok {
		return core.SessionInfo{}, ErrInvalidTitle
	}
	info, err := rt.SessionInfo(ctx, id)
	if err != nil {
		return core.SessionInfo{}, err
	}
	store, ok := rt.store.(core.SessionTitleStore)
	if !ok {
		return core.SessionInfo{}, ErrTitlesUnsupported
	}
	source := core.TitleSourceOwner
	if normalized == "" {
		source = ""
	}
	if _, err := store.SetSessionTitle(ctx, info.ID, normalized, source, false); err != nil {
		return core.SessionInfo{}, err
	}
	if normalized == "" && rt.titles != nil {
		rt.titles.TurnEnded(core.TitleRequest{SessionID: info.ID, AgentName: info.Agent})
	}
	return rt.store.Info(ctx, info.ID)
}

// RegenerateSessionTitle names the session again now, replacing any title,
// the owner's included, since the owner asked for it (REQ-TTL-010). It
// returns ErrTitleBusy while another pass runs for the session.
func (rt *Runtime) RegenerateSessionTitle(ctx context.Context, id string) (core.SessionInfo, error) {
	if err := rt.closedError(); err != nil {
		return core.SessionInfo{}, err
	}
	info, err := rt.SessionInfo(ctx, id)
	if err != nil {
		return core.SessionInfo{}, err
	}
	if rt.titles == nil {
		return core.SessionInfo{}, ErrTitlesUnsupported
	}
	if _, err := rt.titles.Regenerate(info.ID, info.Agent); err != nil {
		return core.SessionInfo{}, err
	}
	return rt.store.Info(ctx, info.ID)
}

// TitlePending reports whether a title pass runs for the session in this
// process (REQ-TTL-012).
func (rt *Runtime) TitlePending(id string) bool {
	return rt.titles != nil && rt.titles.Pending(id)
}

// SessionTitle reads the title and title source of a session by its full id.
func (rt *Runtime) SessionTitle(ctx context.Context, id string) (title, source string, err error) {
	info, err := rt.store.Info(ctx, id)
	if err != nil {
		return "", "", err
	}
	return info.Title, info.TitleSource, nil
}

// workflowSessionTitle is the fixed title of a workflow run session:
// "Workflow · <workflow name>" (REQ-TTL-007).
func (rt *Runtime) workflowSessionTitle(ctx context.Context, workflowID string) string {
	name := workflowID
	if rt.workflows != nil {
		if detail, err := rt.workflows.Get(ctx, workflowID); err == nil && detail.Name != "" {
			name = detail.Name
		}
	}
	return titles.Fixed(workflowTitlePrefix, name)
}

// calendarSessionTitle is the fixed title of a session a calendar item
// starts: "Calendar · <item title>" (REQ-TTL-007).
func calendarSessionTitle(itemTitle string) string {
	return titles.Fixed(calendarTitlePrefix, itemTitle)
}
