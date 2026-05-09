
alter table "public"."knowledge_graph_node" add column "level" text
 not null default 'Account';

alter table "public"."knowledge_graph_edge" add column "level" text
 not null default 'Account';
