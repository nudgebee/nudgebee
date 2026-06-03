
CREATE TABLE "public"."auto_pilot_category" ("value" text NOT NULL, "description" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));COMMENT ON TABLE "public"."auto_pilot_category" IS E'enum to store autopilot category';

INSERT INTO "public"."auto_pilot_category"("value", "description") VALUES (E'vertical_rightsize', E'auto pilot changing cpu and memory for workload');

INSERT INTO "public"."auto_pilot_category"("value", "description") VALUES (E'horizontal_rightsize', E'autopilot will change replicas number for workload');
