ALTER TABLE IF EXISTS public.knowledge_graph_edge
  ADD COLUMN IF NOT EXISTS source text,
  ADD COLUMN IF NOT EXISTS contributing_sources jsonb;

UPDATE public.knowledge_graph_edge
   SET source = properties->>'created_by_flow_source'
 WHERE source IS NULL AND properties ? 'created_by_flow_source';

UPDATE public.knowledge_graph_edge
   SET contributing_sources = COALESCE(
         CASE
           WHEN properties->'contributing_sources' IS NOT NULL
                AND jsonb_typeof(properties->'contributing_sources') = 'array'
             THEN (
               SELECT jsonb_agg(jsonb_build_object('source', elem, 'last_seen_at', to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
                 FROM jsonb_array_elements_text(properties->'contributing_sources') AS elem
             )
           WHEN properties ? 'created_by_flow_source'
             THEN jsonb_build_array(jsonb_build_object('source', properties->>'created_by_flow_source', 'last_seen_at', to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))
         END,
         '[]'::jsonb
       )
 WHERE contributing_sources IS NULL;

CREATE INDEX IF NOT EXISTS idx_kg_edge_tenant_source
  ON public.knowledge_graph_edge (tenant_id, source);

CREATE INDEX IF NOT EXISTS idx_kg_edge_contributing_sources_gin
  ON public.knowledge_graph_edge USING gin (contributing_sources);
