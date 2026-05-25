
alter table "public"."knowledge_graph_edge" drop constraint "knowledge_graph_edge_relationship_type_fkey";

DELETE FROM "public"."knowledge_graph_relationship_types" WHERE "name" = 'RUNS_ON';

DELETE FROM "public"."knowledge_graph_relationship_types" WHERE "name" = 'SUBSCRIBES_TO';

DELETE FROM "public"."knowledge_graph_relationship_types" WHERE "name" = 'PUBLISHES_TO';

DELETE FROM "public"."knowledge_graph_relationship_types" WHERE "name" = 'CALLS';

DROP TABLE "public"."knowledge_graph_relationship_types";

DROP TABLE "public"."knowledge_graph_edge";

DROP TABLE "public"."knowledge_graph_node";
