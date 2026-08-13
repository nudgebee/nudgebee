-- Adds two first-class, queryable columns to knowledge_graph_node:
--   specific_type       - the concrete cloud/native type (e.g. EC2Instance);
--                         node_type stays the ontology.
--   ontology_attributes - the cross-cloud-normalized field schema for the node's
--                         NodeType (e.g. every ComputeInstance exposes name/region/
--                         type/state/...), mapped from source-specific properties.
-- Both are additive and not part of the unique key, so they self-heal on the next
-- KG build (onConflictUpdate re-stamps them) -- no backfill required.

alter table "public"."knowledge_graph_node"
    add column if not exists "specific_type" text;

alter table "public"."knowledge_graph_node"
    add column if not exists "ontology_attributes" jsonb not null default jsonb_build_object();

-- Filter by concrete type (mirrors node_type usage).
create index if not exists idx_kg_node_specific_type
    on knowledge_graph_node (specific_type)
    where is_active = true;

-- Containment queries on normalized ontology fields (ontology_attributes @> '{...}').
create index if not exists idx_kg_node_ontology_gin
    on knowledge_graph_node using gin (ontology_attributes)
    where is_active = true;
