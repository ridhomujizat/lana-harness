# API contract: Knowledge vault

- **Status**: Approved (on behalf of the owner, gate lifted 2026-11-01)
- **Revision**: 1

## API-KNW-001: config section `[knowledge]`

Top-level, optional. Unknown keys are rejected like other sections.

| Key | Type | Default | Rule |
|---|---|---|---|
| `path` | string | `$LANA_HOME/knowledge` | `~` expands; relative paths resolve against `$LANA_HOME` |
| `auto_recall` | bool | `true` | REQ-KNW-050 |
| `embedding_provider` | string | `""` | must name a `[providers.<name>]` with `kind = "openai_compat"`; `""` = keyword search only |
| `embedding_model` | string | `""` | required when `embedding_provider` is set |

Spec 31 adds more keys to this section.

Example:

```toml
[knowledge]
path = "~/Documents/Knowledge"
embedding_provider = "openai"
embedding_model = "text-embedding-3-small"
```

Validation errors use the existing config error format, for example
`knowledge.embedding_provider: unknown provider "foo"` and
`knowledge.embedding_model: required when embedding_provider is set`.

## API-KNW-002: event kind `knowledge/compiled`

Appended to the session that ingested the source, after its compile pass ends (REQ-KNW-024). Not
part of the model history.

```json
{
  "v": 1,
  "source": "sources/2026-11-01 Rapat Q4.pdf",
  "ops": [
    {"op": "create", "title": "Rapat Q4"},
    {"op": "append", "title": "Proyek X"}
  ],
  "usage": {"input_tokens": 12000, "cached_input_tokens": 0, "output_tokens": 900},
  "error": ""
}
```

`op` is one of `create`, `append`, `replace`, `meta`. `error` is empty on success; on failure `ops`
holds the operations applied before the failure.

## API-KNW-003: tool catalog group

`GET` tool catalog (API-WEB-026) gains group `{ "id": "knowledge", "label": "Knowledge" }` with
`knowledge_search`, `knowledge_read`, `knowledge_write`, `knowledge_ingest`, placed after "Memory".

## API-KNW-004: embeddings request

For `embedding_provider`, lana sends `POST <base_url>/embeddings` with
`{"model": "<embedding_model>", "input": ["...", "..."]}` and the provider's API key and headers,
and reads `data[i].embedding` (float arrays) in `index` order. Batches hold at most 64 inputs.
