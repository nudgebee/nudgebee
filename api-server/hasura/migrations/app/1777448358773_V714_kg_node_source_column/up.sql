ALTER TABLE IF EXISTS public.knowledge_graph_node
  ADD COLUMN IF NOT EXISTS source text;

UPDATE public.knowledge_graph_node
   SET source = properties->>'source'
 WHERE source IS NULL AND properties ? 'source';
