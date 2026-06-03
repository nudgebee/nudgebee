
alter table "public"."knowledge_graph_node" add column "last_sync_version" numeric
 not null default '0';

alter table "public"."knowledge_graph_tenant_filters" add column "last_sync_time" timestamp
 null;

alter table "public"."knowledge_graph_tenant_filters" add column "last_sync_version" numeric
 not null default '0';
