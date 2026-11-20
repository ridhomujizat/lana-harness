# Agent logos

Logos of the coding agents the Workbench recognizes (spec 55).

- Source: the LobeHub icon set, npm package `@lobehub/icons-static-svg`
  version 1.102.0 (https://github.com/lobehub/lobe-icons).
- Files: `https://unpkg.com/@lobehub/icons-static-svg@1.102.0/icons/<name>.svg`,
  copied unchanged.
- License: MIT, see `LICENSE` in this folder (copied from the lobe-icons
  repository).

The logos are trademarks of their owners and are used here only to name the
agent. Agents without a logo in the set (droid, letta, maki) get a monogram
tile; pi and omp show a white "π" glyph instead.

Exception: Claude Code (`claude`) shows `claude.svg`, a monochrome copy of
the LobeHub Claude mark: `claude-color.svg` (copied from
`web/ui/src/assets/providers/claude-color.svg`) with its `#D97757` fill set
to `currentColor`. The Workbench draws it white on a coral (`#D97757`) app
tile. `claude-color.svg` keeps the original and `claudecode-color.svg` is
kept but unused.

Exception: Codex (`codex`) shows `codex.svg`, a monochrome copy of the
LobeHub Codex mark: `codex-color.svg` with its white background square and
its gradient defs dropped and the mark's fill set to `currentColor`. The
Workbench draws it dark on a near-white (`#F7F7F7`) app tile.
`codex-color.svg` keeps the original.
