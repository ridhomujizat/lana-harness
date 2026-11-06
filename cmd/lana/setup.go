package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ridhomujizat/lana-harness/config"
	"github.com/ridhomujizat/lana-harness/core"
)

// setupTerminalLogging configures the logger for non-chat subcommands.
func setupTerminalLogging() {
	level := slog.LevelWarn
	switch strings.ToLower(os.Getenv("LANA_LOG")) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

// setupChatLogging redirects chat logs to a file to keep the terminal clean.
// The returned function restores the previous logger.
func setupChatLogging(home string) func() {
	previous := slog.Default()
	path := filepath.Join(home, "logs", "lana.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			slog.SetDefault(slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelInfo})))
			return func() {
				slog.SetDefault(previous)
				_ = file.Close()
			}
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func() { slog.SetDefault(previous) }
}

// loadAgent reads the config and picks the agent to use.
func loadAgent(ctx context.Context, home, requested string) (string, error) {
	result, err := config.Load(ctx, config.Options{Home: home})
	if err != nil {
		return "", err
	}
	return resolveAgent(result.Config, requested)
}

// resolveAgent picks the agent: the flag, a cli route, "default", or the only agent.
func resolveAgent(cfg core.Config, requested string) (string, error) {
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	if requested != "" {
		if _, ok := cfg.Agents[requested]; !ok {
			if len(names) == 0 {
				return "", errNoAgent()
			}
			return "", fmt.Errorf("unknown agent %q (available: %s)", requested, strings.Join(names, ", "))
		}
		return requested, nil
	}
	for _, route := range cfg.Routes {
		if route.Channel != "cli" {
			continue
		}
		if _, ok := cfg.Agents[route.Agent]; ok {
			return route.Agent, nil
		}
	}
	if _, ok := cfg.Agents["default"]; ok {
		return "default", nil
	}
	switch len(names) {
	case 0:
		return "", errNoAgent()
	case 1:
		return names[0], nil
	}
	return "", fmt.Errorf("no cli route; choose an agent with --agent (available: %s)", strings.Join(names, ", "))
}

func errNoAgent() error {
	return fmt.Errorf("no agent in the config yet; run 'lana init --yes' first")
}
