
CREATE TABLE "public"."knowledge_graph_metadata" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "attribute_key" text NOT NULL, "attribute_value" jsonb NOT NULL, "attribute_type" text NOT NULL, "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, PRIMARY KEY ("id") );
CREATE EXTENSION IF NOT EXISTS pgcrypto;

alter table "public"."knowledge_graph_node" add constraint "knowledge_graph_node_unique_key_cloud_account_id_tenant_id_key" unique ("unique_key", "cloud_account_id", "tenant_id");

alter table "public"."knowledge_graph_edge" add constraint "knowledge_graph_edge_source_node_id_destination_node_id_relationship_type_cloud_account_id_tenant_id_key" unique ("source_node_id", "destination_node_id", "relationship_type", "cloud_account_id", "tenant_id");
