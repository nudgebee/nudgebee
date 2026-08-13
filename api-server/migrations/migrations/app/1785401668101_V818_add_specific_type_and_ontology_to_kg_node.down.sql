drop index if exists idx_kg_node_ontology_gin;
drop index if exists idx_kg_node_specific_type;

alter table "public"."knowledge_graph_node" drop column if exists "ontology_attributes";
alter table "public"."knowledge_graph_node" drop column if exists "specific_type";
