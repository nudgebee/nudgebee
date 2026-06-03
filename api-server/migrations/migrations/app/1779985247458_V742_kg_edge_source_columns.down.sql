DROP INDEX IF EXISTS public.idx_kg_edge_contributing_sources_gin;
DROP INDEX IF EXISTS public.idx_kg_edge_tenant_source;

ALTER TABLE IF EXISTS public.knowledge_graph_edge
  DROP COLUMN IF EXISTS contributing_sources,
  DROP COLUMN IF EXISTS source;
