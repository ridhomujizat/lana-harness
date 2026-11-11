package lana

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// AssistEnv tells the workflow guardian assistant what the workflow can use
// (REQ-WF-010 revision): the agents, the tools of every configured MCP server
// (started if needed, so a tool node can call them), and the names of the
// owner's secrets. Secret values never leave this function.
func (d workflowDeps) AssistEnv(ctx context.Context, wf workflow.Workflow) workflow.AssistEnv {
	rt := d.rt
	var env workflow.AssistEnv

	agents := rt.agents()
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cfg := agents[name]
		env.Agents = append(env.Agents, workflow.EnvAgent{
			Name:        name,
			Description: cfg.Description,
			Model:       rt.AgentModel(name),
			MCP:         append([]string(nil), cfg.MCP...),
		})
	}

	servers := make([]string, 0, len(rt.mcpConfig()))
	for name := range rt.mcpConfig() {
		servers = append(servers, name)
	}
	sort.Strings(servers)
	if len(servers) > 0 {
		if service, ok := rt.coreCtx.Lookup(core.ServiceMCP); ok {
			if manager, ok := service.(core.MCPManager); ok {
				toolNames, unavailable := manager.Ensure(ctx, servers)
				sort.Strings(toolNames)
				for _, name := range toolNames {
					tool, ok := rt.coreCtx.Tools.Get(name)
					if !ok {
						continue
					}
					spec := tool.Spec()
					input := string(spec.InputSchema)
					var compact bytes.Buffer
					if json.Compact(&compact, spec.InputSchema) == nil {
						input = compact.String()
					}
					env.MCPTools = append(env.MCPTools, workflow.EnvTool{
						Name:        name,
						Description: spec.Description,
						Input:       input,
					})
				}
				if len(unavailable) > 0 {
					env.MCPUnavailable = unavailable
				}
			}
		}
	}

	env.Secrets = secretNames(rt.home)
	return env
}

// AssistSecrets returns the names in secrets.env, so a guardian chat's later
// note names a secret the owner added meanwhile, for example through
// secret_request (spec 53). Values never leave this function.
func (d workflowDeps) AssistSecrets(context.Context) []string {
	return secretNames(d.rt.home)
}
