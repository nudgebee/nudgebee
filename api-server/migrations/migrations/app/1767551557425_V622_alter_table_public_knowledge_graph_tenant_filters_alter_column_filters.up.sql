
alter table "public"."knowledge_graph_tenant_filters" alter column "filters" set default jsonb_build_object();
