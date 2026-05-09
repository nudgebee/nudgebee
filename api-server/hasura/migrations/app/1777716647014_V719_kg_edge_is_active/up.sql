alter table "public"."knowledge_graph_edge" add column "is_active" boolean
 not null default 'true';

alter table "public"."knowledge_graph_edge" add column "last_sync_version" bigint
 not null default 0;
