
alter table "public"."knowledge_graph_edge" drop constraint "knowledge_graph_edge_source_node_id_destination_node_id_relationship_type_cloud_account_id_tenant_id_key";

alter table "public"."knowledge_graph_node" drop constraint "knowledge_graph_node_unique_key_cloud_account_id_tenant_id_key";

DROP TABLE "public"."knowledge_graph_metadata";
