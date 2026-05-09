

CREATE TABLE "public"."auto_pilot_execution_status" ("value" text NOT NULL default 'Idle', "description" text NOT NULL, PRIMARY KEY ("value") );COMMENT ON TABLE "public"."auto_pilot_execution_status" IS E'execution status enum for auto pilot';

alter table "public"."auto_pilot" add column execution_status varchar(255);

alter table "public"."auto_pilot"
  add constraint "auto_pilot_execution_status_fkey"
  foreign key ("execution_status")
  references "public"."auto_pilot_execution_status"
  ("value") on update restrict on delete restrict;


INSERT INTO "public"."auto_pilot_execution_status"("value", "description") VALUES (E'InProgress', E'the auto pilot is in execution state');

INSERT INTO "public"."auto_pilot_execution_status"("value", "description") VALUES (E'Idle', E'the auto pilot is ready for execution.');


