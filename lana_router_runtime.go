package lana

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
	"github.com/ridhomujizat/lana-harness/router"
)

// Claude Code and Antigravity models through the router (spec 78, ADR 0023).
// routerRuntimeStreamer is the router's RuntimeStreamer: a router call on a
// Claude Code or Antigravity account (the proxy endpoint, avatars, and
// background passes) runs one headless CLI process of that account through
// its external runtime's RunPrompt, with the environment and concurrency
// slot its agent turns use (REQ-RPR-002, REQ-RPR-006). lana never reads the
// CLI's tokens and never calls Anthropic or Google itself.
type routerRuntimeStreamer struct {
	rt *Runtime
}

var _ router.RuntimeStreamer = routerRuntimeStreamer{}

// proxyWorkDir is the folder under LANA_HOME that holds the empty working
// folder of each runtime call (REQ-RPR-002).
const proxyWorkDir = "router/proxy-work"

// wireRuntimeStreamer gives the router the runtime streamer (spec 78).
func (rt *Runtime) wireRuntimeStreamer() {
	if rt.router == nil || rt.registry == nil {
		return
	}
	rt.router.SetRuntimeStreamer(routerRuntimeStreamer{rt: rt})
}

// RuntimeInstalled reports whether the account's CLI is found: the
// account's binary setting (for the built-in Claude Code account, the one
// its agent turns use), else the CLI on PATH. It runs no process.
func (s routerRuntimeStreamer) RuntimeInstalled(kind, account string, cfg core.ProviderConfig) bool {
	switch kind {
	case core.ClaudeCodeKind:
		return binaryFound(s.rt.claudeAccountSpec(account, cfg).Binary, "claude")
	case core.AntigravityKind:
		return antigravityBinaryFound(cfg.Binary)
	}
	return false
}

// binaryFound reports whether bin (a leading ~/ expanded) is a file, or name
// is on PATH when bin is empty.
func binaryFound(bin, name string) bool {
	if bin == "" {
		_, err := exec.LookPath(name)
		return err == nil
	}
	if strings.HasPrefix(bin, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			bin = home + bin[1:]
		}
	}
	info, err := os.Stat(bin)
	return err == nil && !info.IsDir()
}

// StreamRuntime runs one router call on a Claude Code or Antigravity account
// (REQ-RPR-002): RunPrompt of the account's runtime in a new empty folder
// under <LANA_HOME>/router/proxy-work/, removed afterwards. The stream ends
// when the CLI does; a consumer that stops reading, or ctx ending, kills the
// process group. Failures are router-class provider errors (REQ-RPR-005).
func (s routerRuntimeStreamer) StreamRuntime(ctx context.Context, call router.RuntimeCall) (core.ChatStream, error) {
	name := ClaudeCodeRuntime
	if call.Kind == core.AntigravityKind {
		name = AntigravityRuntime
	}
	if s.rt.closed.Load() {
		return nil, &core.ProviderError{Class: "server", Message: "lana is shutting down"}
	}
	ext, err := s.rt.registry.Runtime(name)
	if err != nil {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: err.Error()}
	}
	prompter, ok := ext.(core.RuntimePrompter)
	if !ok {
		return nil, &core.ProviderError{Class: core.ErrorClassNotFound, Message: fmt.Sprintf("the %s runtime cannot serve router calls", name)}
	}
	cfg := s.runtimeConfig(call)
	return func(yield func(core.ChatEvent, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		dir, err := s.workDir()
		if err != nil {
			yield(core.ChatEvent{}, &core.ProviderError{Class: "server", Message: "router work folder: " + err.Error()})
			return
		}
		defer func() { _ = os.RemoveAll(dir) }()
		events := make(chan core.RuntimeEvent, 64)
		done := make(chan error, 1)
		go func() {
			defer close(events)
			done <- prompter.RunPrompt(ctx, core.RuntimePrompt{
				Config: cfg, System: call.System, Prompt: call.Prompt, Dir: dir,
				Emit: func(ev core.RuntimeEvent) {
					select {
					case events <- ev:
					case <-ctx.Done():
					}
				},
			})
		}()
		if call.Kind == core.AntigravityKind {
			// REQ-RPR-006: the account's usage windows are refreshed after
			// the call, as after an agent turn (throttled to once a minute).
			defer func() {
				if !s.rt.closed.Load() {
					go s.rt.refreshAntigravityAccountUsage(call.Account)
				}
			}()
		}
		sawText := false
		stopped := false
		for ev := range events {
			if stopped {
				continue
			}
			var out core.ChatEvent
			switch ev.Kind {
			case core.RuntimeTextDelta:
				if ev.Text == "" {
					continue
				}
				sawText = true
				out = core.ChatEvent{Kind: core.ChatTextDelta, Text: ev.Text}
			case core.RuntimeAssistantMessage:
				// A whole message is used only when no delta streamed it.
				if sawText || ev.Text == "" {
					continue
				}
				sawText = true
				out = core.ChatEvent{Kind: core.ChatTextDelta, Text: ev.Text}
			case core.RuntimeUsage:
				out = core.ChatEvent{Kind: core.ChatUsage, Usage: ev.Usage}
			case core.RuntimeLimits:
				// REQ-RPR-006: Claude Code's plan windows reach the account's
				// limit snapshot as they do from an agent turn.
				s.rt.updateExternalLimits(call.Account, name, ev.Windows)
				continue
			default:
				// Thinking is not sent (REQ-RPR-004); meta events are not
				// part of the answer.
				continue
			}
			if !yield(out, nil) {
				stopped = true
				cancel()
			}
		}
		runErr := <-done
		if stopped {
			return
		}
		if runErr != nil {
			if ctx.Err() != nil {
				yield(core.ChatEvent{}, ctx.Err())
				return
			}
			yield(core.ChatEvent{}, runtimeProviderError(runErr, time.Now()))
			return
		}
		yield(core.ChatEvent{Kind: core.ChatStop, StopReason: "end_turn"}, nil)
	}, nil
}

// runtimeConfig is the runtime config of a call: the account keys its agent
// turns use (applyAccount for agents), the model, and the effort.
func (s routerRuntimeStreamer) runtimeConfig(call router.RuntimeCall) core.RuntimeConfig {
	effort := strings.TrimSpace(call.Effort)
	if effort == "off" {
		effort = ""
	}
	if call.Kind == core.AntigravityKind {
		spec := s.rt.antigravityAccountSpec(call.Account, call.Config)
		return core.RuntimeConfig{Account: call.Account, Binary: spec.Binary, Home: spec.Home, Model: call.Model, Effort: effort}
	}
	spec := s.rt.claudeAccountSpec(call.Account, call.Config)
	return core.RuntimeConfig{
		Account: call.Account, Binary: spec.Binary, Auth: spec.Auth, APIKeyEnv: spec.APIKeyEnv,
		ConfigDir: spec.ConfigDir, Model: call.Model,
		// Spec 76 REQ-MES-003: --effort only for a level Claude Code lists.
		Effort: models.PickEffort(effort, models.ClaudeCodeEfforts(), ""),
	}
}

// workDir makes the call's empty working folder under
// <LANA_HOME>/router/proxy-work/ (REQ-RPR-002).
func (s routerRuntimeStreamer) workDir() (string, error) {
	home := s.rt.home
	if home == "" {
		var err error
		if home, err = config.HomeDir(nil); err != nil {
			return "", err
		}
	}
	base := filepath.Join(home, filepath.FromSlash(proxyWorkDir))
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "call-")
}

// runtimeProviderError maps a RunPrompt failure to a router error
// (REQ-RPR-005): quota is rate_limit, a runtime failure server, and a
// rate_limit with a reported reset gets the time to it as RetryAfter, so the
// account cools down until then. A failure after the first text is server.
func runtimeProviderError(err error, now time.Time) *core.ProviderError {
	var early *core.RuntimeEarlyError
	if !errors.As(err, &early) {
		return &core.ProviderError{Class: "server", Message: err.Error()}
	}
	class := early.Class
	switch class {
	case core.ErrorClassQuota:
		class = "rate_limit"
	case "", "runtime":
		class = "server"
	}
	pe := &core.ProviderError{Class: class, Message: early.Message}
	if class == "rate_limit" && early.ResetAt.After(now) {
		pe.RetryAfter = early.ResetAt.Sub(now)
	}
	return pe
}
