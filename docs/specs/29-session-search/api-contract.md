# API contract: Session search

- **Status**: Not required
- **Revision**: 1

No CLI, config, HTTP, JSON-RPC, or event change. The new tool `session_search` is a model-facing
tool; its input schema lives in [design.md](design.md) §3. The SQLite schema version moves from 3
to 4, which the existing `ErrSchemaTooNew` check already covers for older binaries.
