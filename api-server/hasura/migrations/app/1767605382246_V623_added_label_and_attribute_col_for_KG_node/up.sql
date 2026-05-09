
alter table "public"."knowledge_graph_node" add column "labels" jsonb
 not null default jsonb_build_object();

alter table "public"."knowledge_graph_node" add column "query_attributes" jsonb
 not null default jsonb_build_object();
