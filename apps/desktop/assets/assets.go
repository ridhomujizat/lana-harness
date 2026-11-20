// Package assets holds the pictures and fonts the app draws with, embedded
// in the binary (spec 58, REQ-DW-036): the coding agents' logos and the
// Geist and Geist Mono fonts (OFL, see fonts/README.md).
package assets

import "embed"

// Agents are the coding agents' logos, one SVG per agent, named as the web
// Workbench's src/assets/agents (see the web UI's agent-logo.tsx for the
// agent-label to file mapping).
//
//go:embed agents/*.svg
var Agents embed.FS

// Fonts are Geist and Geist Mono as TrueType files: Geist-Regular,
// -Medium, -SemiBold and -Bold for the interface, GeistMono-Regular and
// -Medium for the terminals.
//
//go:embed fonts/*.ttf
var Fonts embed.FS
