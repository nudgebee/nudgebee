-- public.llm_kb_document_categories was never touched by the up migration (see
-- its header comment), so there is nothing to restore there: the Fact/SOP
-- marks it holds are exactly as they were before this migration ran. Dropping
-- this table only discards the point-in-time copy and the platform_capability
-- / kb_collection rows, which are both rebuildable (llm-server resyncs the
-- catalog at startup; rag-server rebuilds kb_collection rows from the vector
-- payload).
DROP TABLE IF EXISTS public.llm_kb_capability_entries;
