# Manual KB context tags

Context tags are relevance hints, not access restrictions or execution targets.
The manual KB editor supports discovered service/namespace suggestions and
custom values. Saved tags remain visible even when their resource disappears.
The existing Select has opt-in custom creation; other consumers and file readers retain their behavior.

The backend trims tags, removes case-insensitive duplicates, and accepts up to
32 tags of 128 characters each without control characters. An omitted or null
update preserves stored tags; an explicit empty array clears them. No schema
migration is required: the existing context_tags array is reused.

Tags participate in manual name/description lookup and mapped skill scoring.
For semantic retrieval, llm-server sends them separately from source content to
RAG. After format parsing, RAG includes a context-tag header in each searchable
document and records tag metadata. The database source body is unchanged;
manual exact loading continues to read that canonical body. Untagged documents
and existing source/account/enablement gates remain supported.

Content changes and tag changes use the existing asynchronous reindex path.
The KB becomes processing until indexing completes, or reports an indexing
error. This means tag edits temporarily interrupt availability just like content
edits. Existing tagged KBs need a resync to update their semantic representation;
there is no automatic deployment-wide backfill. Deploy the RAG update with the
llm-server update before editing/resyncing tags; an older RAG server ignores the
new payload field. Integration-source tagging remains outside this manual-editor
change.

Design verdict: PROCEED. Avoided changing saved source text, treating tags as
hard scope filters, and changing default selection behavior. The tradeoff is
reindexing cost for metadata-only edits; live recall and latency remain to be
measured before claiming a quality improvement.

## Validation

- Focused Go tests: normalization, omitted-update persistence, explicit clearing
  and tag-only processing transition with original data retained, RAG HTTP
  payload, tagged and untagged mapped selection, existing knowledge regressions.
- Focused UI tests: custom creation with Enter without resource suggestions,
  saved tags, duplicate rejection. TypeScript check passes.
- Python unit tests: each parsed document gets search labels, unchanged source,
  no-tag behavior, metadata retention and validation. Syntax compilation passes.
- Fresh-context review and narrow follow-up review found no blockers.
- Live create/edit/resync/search and background completion are not yet verified.
  Focused Black formatting checks pass.
