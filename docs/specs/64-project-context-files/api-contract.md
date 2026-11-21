# Contract: Project context files

- **Requirements**: [requirements.md](requirements.md) (revision 1, Approved)
- **Contract revision**: 1
- **Applies to**: config
- **Status**: Draft

## API-PCX-001: `[agents.<name>] context_files`

- **Requirements**: REQ-PCX-001
- **Acceptance criteria**: AC-PCX-001, AC-PCX-002, AC-PCX-003
- **Stability**: New, additive

| Location | Name | Type | Required | Validation and meaning |
|---|---|---|---:|---|
| `[agents.<name>]` in `config.toml` | `context_files` | array of strings | No | Absent: `["AGENTS.md", "CLAUDE.md"]`. Empty array: feature off. Each item is a non-empty file name without `/` or `\`, and not `.` or `..`. |

Validation errors use the existing `config/validate.go` messages, for example
`agents.lana.context_files[0]: must be a file name without a path separator`.

## Prompt and tool-result format

Not a contract operation, but stable text the tests pin:

```text
# Project instructions
These are project instructions written for coding agents. Follow the ones that apply to your tools.

<project-context path="/Users/me/lana/AGENTS.md">
…file text…
</project-context>
```

A tool result gets the same blocks after its normal output, separated by one blank line, without the
heading lines.
