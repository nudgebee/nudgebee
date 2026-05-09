
CREATE TABLE "public"."knowledge_graph_node" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "properties" jsonb NOT NULL, "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "unique_key" text NOT NULL, PRIMARY KEY ("id") );
CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
RETURNS TRIGGER AS $$
DECLARE
  _new record;
BEGIN
  _new := NEW;
  _new."updated_at" = NOW();
  RETURN _new;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "set_public_knowledge_graph_node_updated_at"
BEFORE UPDATE ON "public"."knowledge_graph_node"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_knowledge_graph_node_updated_at" ON "public"."knowledge_graph_node"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."knowledge_graph_edge" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "source_node_id" uuid NOT NULL, "destination_node_id" uuid NOT NULL, "relationship_type" text NOT NULL, "properties" jsonb NOT NULL, "cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, PRIMARY KEY ("id") );
CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
RETURNS TRIGGER AS $$
DECLARE
  _new record;
BEGIN
  _new := NEW;
  _new."updated_at" = NOW();
  RETURN _new;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "set_public_knowledge_graph_edge_updated_at"
BEFORE UPDATE ON "public"."knowledge_graph_edge"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_knowledge_graph_edge_updated_at" ON "public"."knowledge_graph_edge"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."knowledge_graph_relationship_types" ("name" text NOT NULL, "value" text NOT NULL, PRIMARY KEY ("name") );

INSERT INTO "public"."knowledge_graph_relationship_types"("name", "value") VALUES (E'CALLS', E'CALLS');

INSERT INTO "public"."knowledge_graph_relationship_types"("name", "value") VALUES (E'PUBLISHES_TO', E'PUBLISHES_TO');

INSERT INTO "public"."knowledge_graph_relationship_types"("name", "value") VALUES (E'SUBSCRIBES_TO', E'SUBSCRIBES_TO');

INSERT INTO "public"."knowledge_graph_relationship_types"("name", "value") VALUES (E'RUNS_ON', E'RUNS_ON');

alter table "public"."knowledge_graph_edge"
  add constraint "knowledge_graph_edge_relationship_type_fkey"
  foreign key ("relationship_type")
  references "public"."knowledge_graph_relationship_types"
  ("name") on update restrict on delete restrict;
