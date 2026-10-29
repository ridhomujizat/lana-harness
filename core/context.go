package core

import "sync"

// ServiceKey is the name of a service in the Builder registry.
type ServiceKey string

const (
	ServiceLLM        ServiceKey = "llm"
	ServiceTools      ServiceKey = "tools"
	ServiceSessions   ServiceKey = "sessions"
	ServiceAgents     ServiceKey = "agents"
	ServiceApproval   ServiceKey = "approval"
	ServiceCompaction ServiceKey = "compaction"
)

// Context is the wiring produced by boot.
type Context struct {
	LLM      LLMRegistry
	Tools    ToolRegistry
	Sessions SessionStore
	Agents   AgentRegistry
	Approval ApprovalService
	Hooks    *Hooks
	Config   Config
	Secrets  Secrets
	Profile  string

	services     map[ServiceKey]any
	installed    []*installedModule
	shutdownOnce sync.Once
	shutdownErr  error
}

// Lookup returns any service.
func (c *Context) Lookup(key ServiceKey) (any, bool) {
	if c == nil {
		return nil, false
	}
	if c.services != nil {
		if v, ok := c.services[key]; ok {
			return v, true
		}
	}
	switch key {
	case ServiceLLM:
		if c.LLM == nil {
			return nil, false
		}
		return c.LLM, true
	case ServiceTools:
		if c.Tools == nil {
			return nil, false
		}
		return c.Tools, true
	case ServiceSessions:
		if c.Sessions == nil {
			return nil, false
		}
		return c.Sessions, true
	case ServiceAgents:
		if c.Agents == nil {
			return nil, false
		}
		return c.Agents, true
	case ServiceApproval:
		if c.Approval == nil {
			return nil, false
		}
		return c.Approval, true
	}
	return nil, false
}
