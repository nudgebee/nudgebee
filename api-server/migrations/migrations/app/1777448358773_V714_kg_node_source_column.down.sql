DROP INDEX IF EXISTS public.idx_knowledge_graph_node_source;
ALTER TABLE public.knowledge_graph_node DROP COLUMN IF EXISTS source;
