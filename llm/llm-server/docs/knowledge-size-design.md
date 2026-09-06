# Large knowledge documents: workspace-backed selective reading

Part of #36421 and draft PR #37785. Status: implemented behind
`LLM_SERVER_KNOWLEDGE_WORKSPACE_ENABLED=false` (default). Deploy compatible
RAG and workspace images before enabling the LLM-server flag.

## Retrieval contract

Discovery retains at most 4 KiB of text plus a collection, exact indexed point
ID, content version and source citation. Candidate IDs include collection,
point and version, so identical excerpts cannot overwrite distinct documents.
Manual discovery reads a bounded SQL substring and retains the canonical KB ID.
Neither path provisions a workspace during discovery.

With the flag enabled, `load_skills` selects one candidate or exact account KB
name. Integration names return scoped document candidates; the next selection
fetches that exact indexed point, without another semantic search. Manual
selection reads the active account row in version-checked SQL chunks.

Documents up to 4 KiB remain inline. Larger documents stream through a temporary
file into the conversation workspace using a raw upload, with SHA256 verification
and atomic publication. The temporary file is deleted on success and failure.
The candidate cache retains the workspace handle, not the document body.
Identical workspace uploads reuse an intact file; changed files are rejected.

`load_skills(skill_name=<handle>, keyword=<literal>)` performs a case-insensitive
search. `start_line` selects a 1-based line range; it can also narrow keyword
matches. Output is limited to 8 KiB / 100 fragments, including for very long
lines. A section heading can be used as a keyword, followed by a line read.
These operations require no shell capability. Original source citations and
Type:file references survive tool persistence and observation compression;
knowledge-specific recall instructions label them reference documentation.

## Limits and lifecycle

- Discovery text and retained small content: 4 KiB per candidate.
- Serialized candidate: 32 KiB, 30-minute TTL, account/conversation/turn scope.
- Knowledge discovery HTTP response: 1 MiB maximum, including old servers.
- Indexed/manual document transfer and workspace read: 32 MiB maximum.
- Workspace knowledge storage: 128 MiB per conversation; uploads serialize quota
  checks and atomic publication. Files follow existing workspace cleanup.
- Selection/read deadline: 90 seconds, including lazy workspace provisioning.
- Read results: 8 KiB / 100 fragments, plus bounded title/handle instructions.

Manual access and version are rechecked for every selection/read. RAG exact
reads recheck current collection metadata, tenant visibility, live KB/integration
state and content version; unavailable access checks fail closed. Workspace
reads verify the full file hash before releasing selected text. Parent/delegate
calls share a handle within the same account, conversation and turn. Expired
handles require discovery again; no filesystem path supplied by the model is
accepted as a knowledge handle.

The current Qdrant SDK retrieves one indexed payload before streaming it; this
is not constant-memory upstream storage access. RAG validates its size and
encodes transfer chunks without a second whole-body bytes copy. Version checks
currently reread that indexed payload. Workspace selective reads scan at most
32 MiB to verify integrity, while memory and returned text stay bounded. A
future indexed version field / server-side range API can reduce that I/O.

The stored unit is an **indexed document**, not necessarily the whole Confluence
page. Content omitted at ingestion is not recoverable through this API. Missing
files, old servers, quota failures and changed documents return bounded errors;
they never fall back to full-document prompt injection.

With the flag disabled, legacy loads return explicitly labelled bounded excerpts
when the source is larger. Exact names recheck live account rows; query-dependent integration content is
not cached. The earlier draft's byte pagination and query snapshots are removed.

## Adversarial verdict: PROCEED

A bare path would strand knowledge-only agents, so the loader itself reads the
file. Search excerpts cannot reconstruct unseen source content, so lazy fetching
uses exact indexed identities and reports completeness honestly. Whole-body
JSON writes would duplicate memory pressure, so uploads stream with separate
transfer/output/storage limits. Fresh review additionally covered repeated
uploads, identical excerpts, long lines and stale small-manual handles.

## Validation evidence

Local HTTP flow through the actual RAG controller, Go loader and workspace file
handler: 3,600,049 indexed bytes → 4,096 discovery bytes → 367 response bytes,
with 0 cached body bytes. A final-section canary and subsequent line-range read
both passed. Source storage was a fixture; no deployed behavior is claimed.

Tests cover exact access/version validation, multi-megabyte tails, long lines,
content tampering, cross-account handles, idempotent uploads, transfer/quota
rejection and temporary-file cleanup. Run `TestKnowledgeLocalServices` with
`KNOWLEDGE_E2E_RAG_URL` and `KNOWLEDGE_E2E_WORKSPACE_URL` to exercise local services.
