-- Memory attribution audit trail — point 7 of the memory data-audit baseline.
-- Each tool call the LLM makes gets an optional list of memory-row citations
-- (which memory items shaped THIS action). The LLM emits <memory_used><ref n=N/>
-- inside <action>; the executor stores each cited 1-based [mN] index directly
-- (no resolution to a persistent id at write time). Reference-row "used=true"
-- is then derived at UI query time by matching that stored position against
-- llm_conversation_references.metadata->'position' via:
-- EXISTS(... memory_refs @> [{"position": r.metadata->'position'}]).
--
-- Shape per row (jsonb array): [{"position": 1, "note": "..."}, ...]
-- Empty array = no memory shaped this action (default, most tool calls).

ALTER TABLE llm_conversation_tool_calls
    ADD COLUMN IF NOT EXISTS memory_refs jsonb NOT NULL DEFAULT '[]'::jsonb;

-- No GIN index. Every hydration query pre-filters by (conversation_id,
-- message_id) via the existing b-tree, which narrows to at most a handful
-- of tool_call rows per message — the jsonb @> check then runs against
-- that tiny row set. A GIN on memory_refs would never be picked by the
-- planner and would add insert-time cost on every tool_call write.
